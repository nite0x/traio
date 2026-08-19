package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nite/traio/internal/account"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/alpaca"
	"github.com/nite/traio/internal/broker/ibkr"
	"github.com/nite/traio/internal/broker/schwab"
	"github.com/nite/traio/internal/broker/snaptrade"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/portfolio"
	"github.com/nite/traio/internal/store"
)

// ConnectionManager owns the live session for every enabled broker connection.
// Provider-specific behavior is discovered through small capability interfaces.
type ConnectionManager struct {
	mu       sync.RWMutex
	reloadMu sync.Mutex

	MarketData *broker.MarketDataService
	Trading    *broker.TradingService

	snap           *snaptrade.Client
	store          ConnectionRuntimeStore
	providers      *broker.ProviderRegistry
	sessions       map[int64]broker.BrokerSession
	sessionConfigs map[int64]broker.ConnectionConfig

	ibkrGateways      map[int64]*ibkr.GatewayManager
	ibkrGatewayConfig map[int64]config.IBKRConfig
}

type ConnectionRuntimeStore interface {
	store.BrokerCatalogRepository
	store.BrokerRuntimeConfigRepository
	store.IBKRGatewayRepository
}

func BuildConnectionManager(cfg config.Config, st ConnectionRuntimeStore, runtimeDir string) (*ConnectionManager, error) {
	providers, err := newRuntimeProviderRegistry(st)
	if err != nil {
		return nil, err
	}
	return buildConnectionManagerWithProviderRegistry(cfg, st, runtimeDir, providers)
}

func buildConnectionManagerWithProviderRegistry(cfg config.Config, st ConnectionRuntimeStore, runtimeDir string, providers *broker.ProviderRegistry) (*ConnectionManager, error) {
	marketData := broker.NewMarketDataService()
	manager := &ConnectionManager{
		Trading:           broker.NewTradingService(),
		MarketData:        marketData,
		snap:              snaptrade.New(cfg.SnapTrade),
		store:             st,
		providers:         providers,
		sessions:          map[int64]broker.BrokerSession{},
		sessionConfigs:    map[int64]broker.ConnectionConfig{},
		ibkrGateways:      map[int64]*ibkr.GatewayManager{},
		ibkrGatewayConfig: map[int64]config.IBKRConfig{},
	}
	if err := manager.Reload(context.Background()); err != nil {
		return nil, err
	}
	migrated, err := migrateLegacyIBKRGatewayDirectories(context.Background(), runtimeDir, st)
	if err != nil {
		return nil, err
	}
	if migrated {
		if err := manager.Reload(context.Background()); err != nil {
			return nil, err
		}
	}
	return manager, nil
}

func newRuntimeProviderRegistry(st ConnectionRuntimeStore) (*broker.ProviderRegistry, error) {
	providers := broker.NewProviderRegistry()
	factories := []broker.ProviderFactory{
		ibkr.NewFactory(),
		schwab.NewFactory(schwab.WithFactoryTokenHandler(func(connectionID int64, token schwab.Token) {
			persistSchwabToken(st, connectionID, token)
		})),
		alpaca.NewFactory(),
	}
	for _, factory := range factories {
		if err := providers.Register(factory); err != nil {
			return nil, fmt.Errorf("register broker provider: %w", err)
		}
	}
	return providers, nil
}

// Reload rebuilds adapters from provider- and connection-scoped SQLite data.
func (b *ConnectionManager) Reload(ctx context.Context) error {
	b.reloadMu.Lock()
	defer b.reloadMu.Unlock()

	connections, err := b.store.ListBrokerConnections(ctx)
	if err != nil {
		return err
	}
	providerConfigs := map[string]store.BrokerProviderRuntimeConfig{}
	loadProvider := func(code string) (store.BrokerProviderRuntimeConfig, error) {
		if cfg, ok := providerConfigs[code]; ok {
			return cfg, nil
		}
		cfg, err := b.store.GetBrokerProviderRuntimeConfig(ctx, code)
		if err != nil {
			return store.BrokerProviderRuntimeConfig{}, err
		}
		providerConfigs[code] = cfg
		return cfg, nil
	}

	b.mu.RLock()
	oldSessions := b.sessions
	oldSessionConfigs := b.sessionConfigs
	b.mu.RUnlock()

	sessions := map[int64]broker.BrokerSession{}
	sessionConfigs := map[int64]broker.ConnectionConfig{}
	reusedSessions := map[int64]bool{}
	committed := false
	defer func() {
		if committed {
			return
		}
		for id, session := range sessions {
			if !reusedSessions[id] {
				_ = session.Close(context.Background())
			}
		}
	}()

	for _, publicConnection := range connections {
		if !publicConnection.Enabled {
			continue
		}
		connection, err := b.store.GetBrokerConnectionRuntimeConfig(ctx, publicConnection.ID)
		if err != nil {
			return fmt.Errorf("load broker connection %d: %w", publicConnection.ID, err)
		}
		providerConfig, err := loadProvider(connection.ProviderCode)
		if err != nil {
			return fmt.Errorf("load broker provider %s: %w", connection.ProviderCode, err)
		}
		factory, err := b.providers.Factory(connection.ProviderCode)
		if err != nil {
			return fmt.Errorf("configure broker connection %d: %w", connection.ID, err)
		}
		runtimeConfig := brokerConnectionConfig(providerConfig, connection)
		session := oldSessions[connection.ID]
		if session != nil && reflect.DeepEqual(oldSessionConfigs[connection.ID], runtimeConfig) {
			reusedSessions[connection.ID] = true
		} else {
			session, err = factory.Open(ctx, runtimeConfig)
			if err != nil {
				return fmt.Errorf("configure %s connection %d: %w", connection.ProviderCode, connection.ID, err)
			}
		}
		if session == nil {
			return fmt.Errorf("configure %s connection %d: provider returned a nil session", connection.ProviderCode, connection.ID)
		}
		if session.ConnectionID() != connection.ID || !strings.EqualFold(session.ProviderCode(), connection.ProviderCode) {
			if !reusedSessions[connection.ID] {
				_ = session.Close(context.Background())
			}
			return fmt.Errorf("configure %s connection %d: provider returned mismatched session identity", connection.ProviderCode, connection.ID)
		}
		sessions[connection.ID] = session
		sessionConfigs[connection.ID] = runtimeConfig
	}

	gatewayRecords, err := b.store.ListIBKRGateways(ctx)
	if err != nil {
		return fmt.Errorf("list IBKR gateways: %w", err)
	}
	gatewayRecords, err = b.importLegacyIBKRGateways(ctx, connections, gatewayRecords)
	if err != nil {
		return err
	}
	ibkrProvider, err := loadProvider("IBKR")
	if err != nil {
		return fmt.Errorf("load IBKR provider: %w", err)
	}
	b.mu.RLock()
	oldGateways := b.ibkrGateways
	oldConfigs := b.ibkrGatewayConfig
	b.mu.RUnlock()
	ibkrGateways := map[int64]*ibkr.GatewayManager{}
	ibkrGatewayConfig := map[int64]config.IBKRConfig{}
	for _, record := range gatewayRecords {
		if !record.Enabled {
			continue
		}
		cfg := ibkrGatewayConfigFor(ibkrProvider, record)
		ibkrGatewayConfig[record.ID] = cfg
		if current := oldGateways[record.ID]; current != nil && reflect.DeepEqual(oldConfigs[record.ID], cfg) {
			ibkrGateways[record.ID] = current
			continue
		}
		ibkrGateways[record.ID] = ibkr.NewGatewayManager(cfg)
	}

	b.mu.Lock()
	previousGateways := b.ibkrGateways
	previousSessions := b.sessions
	b.sessions = sessions
	b.sessionConfigs = sessionConfigs
	b.ibkrGateways = ibkrGateways
	b.ibkrGatewayConfig = ibkrGatewayConfig
	b.MarketData.Replace(sessions)
	b.mu.Unlock()
	committed = true
	traders := make(map[int64]broker.TradingProvider, len(sessions))
	for id, session := range sessions {
		if trader, ok := session.(broker.TradingProvider); ok {
			traders[id] = trader
		}
	}
	b.Trading.Replace(traders)
	for id, session := range previousSessions {
		if reusedSessions[id] {
			continue
		}
		if err := session.Close(ctx); err != nil {
			return fmt.Errorf("close replaced broker session %d: %w", id, err)
		}
	}
	for id, manager := range previousGateways {
		if manager != ibkrGateways[id] {
			if err := manager.Shutdown(); err != nil {
				return fmt.Errorf("shutdown replaced IBKR gateway %d: %w", id, err)
			}
		}
	}
	return nil
}

// importLegacyIBKRGateways moves complete, local Gateway process settings out
// of old IBKR connection config into the dedicated ibkr_gateways registry.
// Connections with only gateway_url remain ordinary local or remote clients.
func (b *ConnectionManager) importLegacyIBKRGateways(ctx context.Context, connections []store.BrokerConnection, existing []store.IBKRGateway) ([]store.IBKRGateway, error) {
	knownKeys := map[string]bool{}
	knownURLs := map[string]bool{}
	knownDirs := map[string]bool{}
	knownPorts := map[int]bool{}
	for _, gateway := range existing {
		knownKeys[gateway.GatewayKey] = true
		knownURLs[gateway.GatewayURL] = true
		knownDirs[gateway.GatewayDir] = true
		knownPorts[gateway.GatewayPort] = true
	}

	imported := false
	for _, connection := range connections {
		gateway, ok := legacyIBKRGateway(connection)
		if !ok {
			continue
		}
		managed := knownURLs[gateway.GatewayURL]
		if !managed && (knownDirs[gateway.GatewayDir] || knownPorts[gateway.GatewayPort]) {
			continue
		}
		if !managed {
			if knownKeys[gateway.GatewayKey] {
				gateway.GatewayKey = fmt.Sprintf("%s-%d", gateway.GatewayKey, connection.ID)
			}
			created, err := b.store.UpsertIBKRGateway(ctx, gateway)
			if err != nil {
				return nil, fmt.Errorf("import legacy IBKR gateway from connection %d: %w", connection.ID, err)
			}
			knownKeys[created.GatewayKey] = true
			knownURLs[created.GatewayURL] = true
			knownDirs[created.GatewayDir] = true
			knownPorts[created.GatewayPort] = true
			imported = true
		}
		if err := b.clearLegacyIBKRGatewayFields(ctx, connection.ID); err != nil {
			return nil, err
		}
	}
	if !imported {
		return existing, nil
	}
	gateways, err := b.store.ListIBKRGateways(ctx)
	if err != nil {
		return nil, fmt.Errorf("reload imported IBKR gateways: %w", err)
	}
	return gateways, nil
}

func (b *ConnectionManager) clearLegacyIBKRGatewayFields(ctx context.Context, connectionID int64) error {
	connection, err := b.store.GetBrokerConnectionRuntimeConfig(ctx, connectionID)
	if err != nil {
		return fmt.Errorf("load imported IBKR connection %d: %w", connectionID, err)
	}
	delete(connection.Config, "gateway_dir")
	delete(connection.Config, "gateway_port")
	delete(connection.Config, "gateway_lifecycle")
	if _, err := b.store.UpsertBrokerConnection(ctx, connection); err != nil {
		return fmt.Errorf("clear legacy IBKR gateway fields from connection %d: %w", connectionID, err)
	}
	return nil
}

func legacyIBKRGateway(connection store.BrokerConnection) (store.IBKRGateway, bool) {
	if connection.ProviderCode != "IBKR" {
		return store.IBKRGateway{}, false
	}
	gatewayURL := strings.TrimRight(stringValue(connection.Config, "gateway_url"), "/")
	gatewayDir := strings.TrimSpace(stringValue(connection.Config, "gateway_dir"))
	gatewayPort, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(connection.Config["gateway_port"])))
	if gatewayURL == "" || gatewayDir == "" || err != nil || gatewayPort <= 0 || gatewayPort > 65535 {
		return store.IBKRGateway{}, false
	}
	parsed, err := url.Parse(gatewayURL)
	if err != nil || parsed.Port() != strconv.Itoa(gatewayPort) {
		return store.IBKRGateway{}, false
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return store.IBKRGateway{}, false
	}
	key := strings.TrimSpace(connection.ConnectionKey)
	if config.DefaultIBKRGatewayDir(".", key) == "" {
		key = fmt.Sprintf("legacy-%d", connection.ID)
	}
	name := strings.TrimSpace(connection.Name)
	if name == "" {
		name = key
	}
	return store.IBKRGateway{
		GatewayKey:  key,
		Name:        name + " Gateway",
		GatewayURL:  gatewayURL,
		GatewayDir:  gatewayDir,
		GatewayPort: gatewayPort,
		Lifecycle:   config.ResolveIBKRGatewayLifecycle(),
		Enabled:     connection.Enabled,
	}, true
}

// AcquireDefaultSession leases the first enabled session for a provider. The
// caller must invoke release after finishing the operation so Reload cannot
// close or replace the session while it is in use.
func (b *ConnectionManager) AcquireDefaultSession(providerCode string) (session broker.BrokerSession, release func()) {
	b.mu.RLock()
	for _, connectionID := range sortedMapKeys(b.sessions) {
		candidate := b.sessions[connectionID]
		if strings.EqualFold(candidate.ProviderCode(), providerCode) {
			var once sync.Once
			return candidate, func() { once.Do(b.mu.RUnlock) }
		}
	}
	b.mu.RUnlock()
	return nil, func() {}
}

func (b *ConnectionManager) SyncSources() []portfolio.Source {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sources := make([]portfolio.Source, 0, len(b.sessions))
	for _, connectionID := range sortedMapKeys(b.sessions) {
		session := b.sessions[connectionID]
		_, ok := broker.AsPortfolioProvider(session)
		if !ok {
			continue
		}
		id := connectionID
		sources = append(sources, portfolio.Source{
			Name: session.ProviderCode(), ConnectionID: id,
			Acquire: func() (broker.PortfolioProvider, func()) {
				return b.acquirePortfolioProvider(id)
			},
		})
	}
	return sources
}

func (b *ConnectionManager) acquirePortfolioProvider(connectionID int64) (broker.PortfolioProvider, func()) {
	b.mu.RLock()
	provider, ok := broker.AsPortfolioProvider(b.sessions[connectionID])
	if !ok {
		b.mu.RUnlock()
		return nil, func() {}
	}
	var once sync.Once
	return provider, func() { once.Do(b.mu.RUnlock) }
}

func (b *ConnectionManager) AccountSources() []account.Source {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sources := make([]account.Source, 0, len(b.sessions))
	for _, id := range sortedMapKeys(b.sessions) {
		session := b.sessions[id]
		_, ok := session.(broker.AccountEquityProvider)
		if !ok {
			continue
		}
		sources = append(sources, account.Source{
			Name: session.ProviderCode(),
			Provider: leasedAccountEquityProvider{
				manager: b, connectionID: id,
			},
		})
	}
	return sources
}

type leasedAccountEquityProvider struct {
	manager      *ConnectionManager
	connectionID int64
}

func (p leasedAccountEquityProvider) AccountSummary(ctx context.Context) (broker.AccountSummary, error) {
	provider, release := p.manager.acquireAccountEquityProvider(p.connectionID)
	defer release()
	if provider == nil {
		return broker.AccountSummary{}, fmt.Errorf("%w for broker connection %d", broker.ErrCapabilityUnavailable, p.connectionID)
	}
	return provider.AccountSummary(ctx)
}

func (p leasedAccountEquityProvider) HistoricalEquity(ctx context.Context) ([]broker.AccountEquityPoint, error) {
	provider, release := p.manager.acquireAccountEquityProvider(p.connectionID)
	defer release()
	if provider == nil {
		return nil, fmt.Errorf("%w for broker connection %d", broker.ErrCapabilityUnavailable, p.connectionID)
	}
	return provider.HistoricalEquity(ctx)
}

func (b *ConnectionManager) acquireAccountEquityProvider(connectionID int64) (broker.AccountEquityProvider, func()) {
	b.mu.RLock()
	provider, ok := b.sessions[connectionID].(broker.AccountEquityProvider)
	if !ok {
		b.mu.RUnlock()
		return nil, func() {}
	}
	var once sync.Once
	return provider, func() { once.Do(b.mu.RUnlock) }
}

func (b *ConnectionManager) BeginConnectionLogin(ctx context.Context, connectionID int64, state string) (broker.LoginAction, error) {
	connection, err := b.store.GetBrokerConnection(ctx, connectionID)
	if err != nil {
		return broker.LoginAction{}, err
	}
	if !connection.Enabled {
		return broker.LoginAction{}, fmt.Errorf("broker connection %d is disabled", connectionID)
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	provider, err := b.connectionAuthenticationProviderLocked(connectionID)
	if err != nil {
		return broker.LoginAction{}, err
	}
	action, err := provider.BeginAuthentication(ctx, broker.AuthenticationRequest{State: state})
	return action, broker.AuthenticationOperationError("begin login", err)
}

// ConnectionLoginStatus reports authentication without starting a Gateway or
// issuing another browser login URL.
func (b *ConnectionManager) ConnectionLoginStatus(ctx context.Context, connectionID int64) (broker.LoginAction, error) {
	if _, err := b.store.GetBrokerConnection(ctx, connectionID); err != nil {
		return broker.LoginAction{}, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	provider, err := b.connectionAuthenticationProviderLocked(connectionID)
	if err != nil {
		return broker.LoginAction{}, err
	}
	action, err := provider.AuthenticationStatus(ctx)
	return action, broker.AuthenticationOperationError("check status", err)
}

// connectionAuthenticationProviderLocked resolves a capability while the
// caller holds mu for the entire operation. This prevents Reload from closing
// or replacing a session during an in-flight login/status request.
func (b *ConnectionManager) connectionAuthenticationProviderLocked(connectionID int64) (broker.AuthenticationProvider, error) {
	session := b.sessions[connectionID]
	if session == nil {
		return nil, fmt.Errorf("%w for broker connection %d", broker.ErrAuthenticationUnavailable, connectionID)
	}
	provider, ok := session.(broker.AuthenticationProvider)
	if !ok {
		return nil, fmt.Errorf("%w for broker connection %d", broker.ErrAuthenticationUnavailable, connectionID)
	}
	return provider, nil
}

// IBKRGatewayTarget resolves a connection to its private Gateway origin. The
// API proxy performs the final loopback validation before forwarding traffic.
func (b *ConnectionManager) IBKRGatewayTarget(ctx context.Context, connectionID int64) (*url.URL, bool, error) {
	connection, err := b.store.GetBrokerConnection(ctx, connectionID)
	if err != nil {
		return nil, false, err
	}
	if connection.ProviderCode != "IBKR" {
		return nil, false, nil
	}
	if !connection.Enabled {
		return nil, true, fmt.Errorf("broker connection %d is disabled", connectionID)
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	session := b.sessions[connectionID]
	targetProvider, ok := session.(interface{ GatewayTarget() (*url.URL, error) })
	if !ok {
		return nil, false, nil
	}
	target, err := targetProvider.GatewayTarget()
	if err != nil {
		return nil, true, fmt.Errorf("parse IBKR connection %d Gateway URL: %w", connectionID, err)
	}
	return target, true, nil
}

func (b *ConnectionManager) ExchangeConnectionOAuthCode(ctx context.Context, connectionID int64, code string) error {
	if _, err := b.store.GetBrokerConnection(ctx, connectionID); err != nil {
		return err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	session := b.sessions[connectionID]
	if session == nil {
		return fmt.Errorf("%w for broker connection %d", broker.ErrAuthenticationCallbackUnavailable, connectionID)
	}
	handler, ok := session.(broker.AuthenticationCallbackHandler)
	if !ok {
		return fmt.Errorf("%w for broker connection %d", broker.ErrAuthenticationCallbackUnavailable, connectionID)
	}
	err := handler.CompleteAuthentication(ctx, broker.AuthenticationCallback{Code: strings.TrimSpace(code)})
	return broker.AuthenticationOperationError(
		"complete authentication",
		err,
	)
}

// RefreshConnectionAuthentication invokes the optional renewable-credential
// capability without coupling runtime to a concrete provider.
func (b *ConnectionManager) RefreshConnectionAuthentication(ctx context.Context, connectionID int64) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	session := b.sessions[connectionID]
	refresher, ok := session.(broker.AuthenticationRefresher)
	if !ok {
		return fmt.Errorf("%w for broker connection %d", broker.ErrAuthenticationRefreshUnavailable, connectionID)
	}
	err := refresher.RefreshAuthentication(ctx)
	return broker.AuthenticationOperationError("refresh authentication", err)
}

// RevokeConnectionAuthentication invokes provider revocation when a session
// explicitly supports it. No current broker implements remote revocation.
func (b *ConnectionManager) RevokeConnectionAuthentication(ctx context.Context, connectionID int64) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	session := b.sessions[connectionID]
	revoker, ok := session.(broker.AuthenticationRevoker)
	if !ok {
		return fmt.Errorf("%w for broker connection %d", broker.ErrAuthenticationRevokeUnavailable, connectionID)
	}
	err := revoker.RevokeAuthentication(ctx)
	return broker.AuthenticationOperationError("revoke authentication", err)
}

func (b *ConnectionManager) ApplyConfig(_ context.Context, updated config.Config) error {
	b.snap.SetConfig(updated.SnapTrade)
	return nil
}

func (b *ConnectionManager) StartGateway(ctx context.Context) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	gateways := make([]*ibkr.GatewayManager, 0, len(b.ibkrGateways))
	for _, id := range sortedMapKeys(b.ibkrGateways) {
		gateways = append(gateways, b.ibkrGateways[id])
	}
	var failures []string
	for _, gateway := range gateways {
		if err := gateway.Start(ctx); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// ShutdownGateways applies each independently managed Gateway's configured lifecycle.
// Managed gateways are stopped; persistent desktop gateways are detached and
// left available for the next Traio process to validate and reattach.
func (b *ConnectionManager) ShutdownGateways() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	gateways := make([]*ibkr.GatewayManager, 0, len(b.ibkrGateways))
	for _, id := range sortedMapKeys(b.ibkrGateways) {
		gateways = append(gateways, b.ibkrGateways[id])
	}
	var failures []string
	for _, gateway := range gateways {
		if err := gateway.Shutdown(); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func (b *ConnectionManager) IBKRGatewayStatus(gatewayID int64) (any, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	manager, err := b.ibkrGatewayManagerLocked(gatewayID)
	if err != nil {
		return nil, err
	}
	return manager.Status(), nil
}

func (b *ConnectionManager) IBKRGatewayLoginURL(gatewayID int64) (string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	manager, err := b.ibkrGatewayManagerLocked(gatewayID)
	if err != nil {
		return "", err
	}
	return manager.LoginURL(), nil
}

func (b *ConnectionManager) StartIBKRGateway(ctx context.Context, gatewayID int64) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	manager, err := b.ibkrGatewayManagerLocked(gatewayID)
	if err != nil {
		return err
	}
	return manager.StartGateway(ctx)
}

func (b *ConnectionManager) StopIBKRGateway(gatewayID int64, keepSession bool) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	manager, err := b.ibkrGatewayManagerLocked(gatewayID)
	if err != nil {
		return err
	}
	return manager.StopGateway(keepSession)
}

func (b *ConnectionManager) ReconnectIBKRGateway(gatewayID int64) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	manager, err := b.ibkrGatewayManagerLocked(gatewayID)
	if err != nil {
		return err
	}
	return manager.Reconnect()
}

func (b *ConnectionManager) UpgradeIBKRGateway(ctx context.Context, gatewayID int64) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	manager, err := b.ibkrGatewayManagerLocked(gatewayID)
	if err != nil {
		return err
	}
	return manager.Upgrade(ctx)
}

func (b *ConnectionManager) RollbackIBKRGateway(ctx context.Context, gatewayID int64) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	manager, err := b.ibkrGatewayManagerLocked(gatewayID)
	if err != nil {
		return err
	}
	return manager.Rollback(ctx)
}

// ibkrGatewayManagerLocked requires b.mu to be held for reading. Callers keep
// the lock through their operation so Reload cannot shut down the manager.
func (b *ConnectionManager) ibkrGatewayManagerLocked(gatewayID int64) (*ibkr.GatewayManager, error) {
	manager := b.ibkrGateways[gatewayID]
	if manager == nil {
		return nil, fmt.Errorf("IBKR gateway %d is not loaded", gatewayID)
	}
	return manager, nil
}

func brokerConnectionConfig(provider store.BrokerProviderRuntimeConfig, connection store.BrokerConnection) broker.ConnectionConfig {
	return broker.ConnectionConfig{
		ID: connection.ID, ProviderCode: connection.ProviderCode,
		ConnectionKey: connection.ConnectionKey, Name: connection.Name,
		ProviderUserID: connection.ProviderUserID, Username: connection.Username,
		Environment: connection.Environment, AuthMode: broker.AuthMode(connection.AuthType),
		ProviderConfig: provider.Config, ProviderSecrets: provider.Secrets,
		Config: connection.Config, Secrets: connection.Secrets,
	}
}

func persistSchwabToken(st ConnectionRuntimeStore, connectionID int64, token schwab.Token) {
	current, err := st.GetBrokerConnectionRuntimeConfig(context.Background(), connectionID)
	if err != nil {
		return
	}
	if current.Secrets == nil {
		current.Secrets = map[string]string{}
	}
	if current.Config == nil {
		current.Config = map[string]any{}
	}
	current.Secrets["access_token"] = token.AccessToken
	current.Secrets["refresh_token"] = token.RefreshToken
	current.Config["expires_at"] = token.ExpiresAt.UTC().Format(time.RFC3339Nano)
	current.Status = store.BrokerConnectionStatusConnected
	_, _ = st.UpsertBrokerConnection(context.Background(), current)
}

func ibkrGatewayConfigFor(provider store.BrokerProviderRuntimeConfig, gateway store.IBKRGateway) config.IBKRConfig {
	proxyHost := stringValue(provider.Config, "gateway_proxy_host")
	if proxyHost == "" {
		proxyHost = "https://api.ibkr.com"
	}
	allowIPs := stringSliceValue(provider.Config, "gateway_allow_ips")
	if len(allowIPs) == 0 {
		allowIPs = []string{"127.0.0.1"}
	}
	return config.IBKRConfig{
		GatewayDir:        gateway.GatewayDir,
		BundledGatewayDir: stringValue(provider.Config, "bundled_gateway_dir"),
		GatewayPort:       gateway.GatewayPort,
		GatewayURL:        strings.TrimRight(gateway.GatewayURL, "/"),
		GatewayLifecycle:  config.NormalizeIBKRGatewayLifecycle(gateway.Lifecycle),
		DownloadProxy:     stringValue(provider.Config, "download_proxy"),
		GatewayProxyHost:  proxyHost,
		GatewayAllowIPs:   allowIPs,
	}
}

func stringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func stringSliceValue(values map[string]any, key string) []string {
	switch value := values[key].(type) {
	case []string:
		return value
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}

func sortedMapKeys[T any](values map[int64]T) []int64 {
	keys := make([]int64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// DefaultGateway returns a stable controller for the legacy single-Gateway
// HTTP endpoints. The dedicated gateway registry remains authoritative.
func (b *ConnectionManager) DefaultGateway() broker.GatewayController {
	return defaultGatewayAdapter{manager: b}
}

type defaultGatewayAdapter struct{ manager *ConnectionManager }

// resolveLocked requires manager.mu to be held for reading. Callers retain the
// lock through the Gateway operation so Reload cannot concurrently replace and
// shut down the manager.
func (g defaultGatewayAdapter) resolveLocked() (*ibkr.GatewayManager, error) {
	for _, id := range sortedMapKeys(g.manager.ibkrGateways) {
		return g.manager.ibkrGateways[id], nil
	}
	return nil, errors.New("IBKR gateway is not configured")
}

func (g defaultGatewayAdapter) Status() any {
	g.manager.mu.RLock()
	defer g.manager.mu.RUnlock()
	manager, err := g.resolveLocked()
	if err != nil {
		return map[string]any{"error": err.Error(), "running": false}
	}
	return manager.Status()
}

func (g defaultGatewayAdapter) LoginURL() string {
	g.manager.mu.RLock()
	defer g.manager.mu.RUnlock()
	manager, err := g.resolveLocked()
	if err != nil {
		return ""
	}
	return manager.LoginURL()
}

func (g defaultGatewayAdapter) StartGateway(ctx context.Context) error {
	g.manager.mu.RLock()
	defer g.manager.mu.RUnlock()
	manager, err := g.resolveLocked()
	if err != nil {
		return err
	}
	return manager.StartGateway(ctx)
}

func (g defaultGatewayAdapter) StopGateway(keepSession bool) error {
	g.manager.mu.RLock()
	defer g.manager.mu.RUnlock()
	manager, err := g.resolveLocked()
	if err != nil {
		return err
	}
	return manager.StopGateway(keepSession)
}

func (g defaultGatewayAdapter) Reconnect() error {
	g.manager.mu.RLock()
	defer g.manager.mu.RUnlock()
	manager, err := g.resolveLocked()
	if err != nil {
		return err
	}
	return manager.Reconnect()
}

func (g defaultGatewayAdapter) Upgrade(ctx context.Context) error {
	g.manager.mu.RLock()
	defer g.manager.mu.RUnlock()
	manager, err := g.resolveLocked()
	if err != nil {
		return err
	}
	return manager.Upgrade(ctx)
}

func (g defaultGatewayAdapter) Rollback(ctx context.Context) error {
	g.manager.mu.RLock()
	defer g.manager.mu.RUnlock()
	manager, err := g.resolveLocked()
	if err != nil {
		return err
	}
	return manager.Rollback(ctx)
}
