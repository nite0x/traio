package runtime

import (
	"context"
	"fmt"
	"reflect"
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
}

type ConnectionRuntimeStore interface {
	store.BrokerCatalogRepository
	store.BrokerRuntimeConfigRepository
}

func BuildConnectionManager(cfg config.Config, st ConnectionRuntimeStore, runtimeDir string) (*ConnectionManager, error) {
	providers, err := newRuntimeProviderRegistry(st)
	if err != nil {
		return nil, err
	}
	return buildConnectionManagerWithProviderRegistry(cfg, st, runtimeDir, providers)
}

func buildConnectionManagerWithProviderRegistry(cfg config.Config, st ConnectionRuntimeStore, _ string, providers *broker.ProviderRegistry) (*ConnectionManager, error) {
	marketData := broker.NewMarketDataService()
	manager := &ConnectionManager{
		Trading:        broker.NewTradingService(),
		MarketData:     marketData,
		snap:           snaptrade.New(cfg.SnapTrade),
		store:          st,
		providers:      providers,
		sessions:       map[int64]broker.BrokerSession{},
		sessionConfigs: map[int64]broker.ConnectionConfig{},
	}
	if err := manager.Reload(context.Background()); err != nil {
		return nil, err
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

	b.mu.Lock()
	previousSessions := b.sessions
	b.sessions = sessions
	b.sessionConfigs = sessionConfigs
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
	return nil
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
