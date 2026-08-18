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

// Brokers is the live adapter registry. Each enabled database connection owns
// its own client and authentication state.
type Brokers struct {
	mu sync.RWMutex

	Schwab      *schwab.Client
	Alpaca      *alpaca.Client
	IBKR        broker.Broker
	Gateway     broker.GatewayController
	Instruments broker.InstrumentProvider
	Quotes      broker.BatchMarketDataProvider
	Candles     broker.CandleProvider
	Trading     *broker.TradingService

	snap  *snaptrade.Client
	store BrokerRuntimeStore

	ibkrConnections   map[int64]*ibkr.Broker
	ibkrGateways      map[int64]*ibkr.GatewayManager
	ibkrGatewayConfig map[int64]config.IBKRConfig
	schwabConnections map[int64]*schwab.Client
	alpacaConnections map[int64]*alpaca.Client
}

type BrokerRuntimeStore interface {
	store.BrokerCatalogRepository
	store.BrokerRuntimeConfigRepository
	store.IBKRGatewayRepository
}

func BuildBrokers(cfg config.Config, st BrokerRuntimeStore, runtimeDir string) (*Brokers, error) {
	registry := &Brokers{
		Trading:           broker.NewTradingService(),
		snap:              snaptrade.New(cfg.SnapTrade),
		store:             st,
		ibkrConnections:   map[int64]*ibkr.Broker{},
		ibkrGateways:      map[int64]*ibkr.GatewayManager{},
		ibkrGatewayConfig: map[int64]config.IBKRConfig{},
		schwabConnections: map[int64]*schwab.Client{},
		alpacaConnections: map[int64]*alpaca.Client{},
	}
	registry.Gateway = gatewayRegistryAdapter{registry: registry}
	if err := registry.Reload(context.Background()); err != nil {
		return nil, err
	}
	migrated, err := migrateLegacyIBKRGatewayDirectories(context.Background(), runtimeDir, st)
	if err != nil {
		return nil, err
	}
	if migrated {
		if err := registry.Reload(context.Background()); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// Reload rebuilds adapters from provider- and connection-scoped SQLite data.
func (b *Brokers) Reload(ctx context.Context) error {
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

	ibkrConnections := map[int64]*ibkr.Broker{}
	schwabConnections := map[int64]*schwab.Client{}
	alpacaConnections := map[int64]*alpaca.Client{}
	for _, publicConnection := range connections {
		if !publicConnection.Enabled {
			continue
		}
		connection, err := b.store.GetBrokerConnectionRuntimeConfig(ctx, publicConnection.ID)
		if err != nil {
			return fmt.Errorf("load broker connection %d: %w", publicConnection.ID, err)
		}
		switch connection.ProviderCode {
		case "IBKR":
			cfg, err := ibkrConnectionConfig(connection)
			if err != nil {
				return fmt.Errorf("configure IBKR connection %d: %w", connection.ID, err)
			}
			ibkrConnections[connection.ID] = ibkr.NewBroker(cfg)
		case "SCHWAB":
			providerConfig, err := loadProvider(connection.ProviderCode)
			if err != nil {
				return fmt.Errorf("load broker provider %s: %w", connection.ProviderCode, err)
			}
			schwabConnections[connection.ID] = b.newSchwabConnectionClient(providerConfig, connection)
		case "ALPACA":
			providerConfig, err := loadProvider(connection.ProviderCode)
			if err != nil {
				return fmt.Errorf("load broker provider %s: %w", connection.ProviderCode, err)
			}
			alpacaConnections[connection.ID] = alpaca.New(alpacaConfig(providerConfig, connection))
		}
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
	b.ibkrConnections = ibkrConnections
	b.ibkrGateways = ibkrGateways
	b.ibkrGatewayConfig = ibkrGatewayConfig
	b.schwabConnections = schwabConnections
	b.alpacaConnections = alpacaConnections
	b.selectLegacyDefaultsLocked()
	b.mu.Unlock()
	traders := make(map[int64]broker.TradingProvider, len(ibkrConnections)+len(schwabConnections)+len(alpacaConnections))
	for id, client := range ibkrConnections {
		traders[id] = client
	}
	for id, client := range schwabConnections {
		traders[id] = client
	}
	for id, client := range alpacaConnections {
		traders[id] = client
	}
	b.Trading.Replace(traders)
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
func (b *Brokers) importLegacyIBKRGateways(ctx context.Context, connections []store.BrokerConnection, existing []store.IBKRGateway) ([]store.IBKRGateway, error) {
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

func (b *Brokers) clearLegacyIBKRGatewayFields(ctx context.Context, connectionID int64) error {
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

func (b *Brokers) selectLegacyDefaultsLocked() {
	b.Schwab = nil
	b.Alpaca = nil
	b.IBKR = nil
	b.Instruments = nil
	b.Quotes = nil
	b.Candles = nil
	for _, id := range sortedMapKeys(b.ibkrConnections) {
		adapter := b.ibkrConnections[id]
		b.IBKR = adapter
		b.Instruments = adapter.Client()
		b.Quotes = adapter.Client()
		b.Candles = adapter.Client()
		break
	}
	for _, id := range sortedMapKeys(b.schwabConnections) {
		b.Schwab = b.schwabConnections[id]
		break
	}
	for _, id := range sortedMapKeys(b.alpacaConnections) {
		b.Alpaca = b.alpacaConnections[id]
		break
	}
}

func (b *Brokers) SyncSources() []portfolio.Source {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sources := make([]portfolio.Source, 0, len(b.ibkrConnections)+len(b.schwabConnections)+len(b.alpacaConnections))
	for _, connectionID := range sortedMapKeys(b.ibkrConnections) {
		sources = append(sources, portfolio.Source{
			Name: "IBKR", ConnectionID: connectionID, Broker: b.ibkrConnections[connectionID],
		})
	}
	for _, connectionID := range sortedMapKeys(b.schwabConnections) {
		sources = append(sources, portfolio.Source{
			Name: "SCHWAB", ConnectionID: connectionID, Broker: b.schwabConnections[connectionID],
		})
	}
	for _, connectionID := range sortedMapKeys(b.alpacaConnections) {
		sources = append(sources, portfolio.Source{
			Name: "ALPACA", ConnectionID: connectionID, Broker: b.alpacaConnections[connectionID],
		})
	}
	return sources
}

func (b *Brokers) AccountSources() []account.Source {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sources := make([]account.Source, 0, len(b.ibkrConnections)+len(b.schwabConnections)+len(b.alpacaConnections))
	for _, id := range sortedMapKeys(b.ibkrConnections) {
		sources = append(sources, account.Source{Name: "IBKR", Provider: b.ibkrConnections[id].Client()})
	}
	for _, id := range sortedMapKeys(b.schwabConnections) {
		sources = append(sources, account.Source{Name: "SCHWAB", Provider: b.schwabConnections[id]})
	}
	for _, id := range sortedMapKeys(b.alpacaConnections) {
		sources = append(sources, account.Source{Name: "ALPACA", Provider: b.alpacaConnections[id]})
	}
	return sources
}

func (b *Brokers) BeginConnectionLogin(ctx context.Context, connectionID int64, state string) (broker.LoginAction, error) {
	connection, err := b.store.GetBrokerConnection(ctx, connectionID)
	if err != nil {
		return broker.LoginAction{}, err
	}
	if !connection.Enabled {
		return broker.LoginAction{}, fmt.Errorf("broker connection %d is disabled", connectionID)
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	switch connection.ProviderCode {
	case "IBKR":
		adapter := b.ibkrConnections[connectionID]
		if adapter == nil {
			return broker.LoginAction{}, fmt.Errorf("IBKR connection %d is not loaded", connectionID)
		}
		return adapter.BeginLogin(ctx)
	case "SCHWAB":
		client := b.schwabConnections[connectionID]
		if client == nil {
			return broker.LoginAction{}, fmt.Errorf("Schwab connection %d is not loaded", connectionID)
		}
		_, authenticated := client.Token()
		return broker.LoginAction{URL: client.AuthURL(state), Authenticated: authenticated}, nil
	case "ALPACA":
		client := b.alpacaConnections[connectionID]
		if client == nil {
			return broker.LoginAction{}, fmt.Errorf("Alpaca connection %d is not loaded", connectionID)
		}
		return client.BeginLogin(ctx)
	default:
		return broker.LoginAction{}, fmt.Errorf("unsupported broker provider %s", connection.ProviderCode)
	}
}

// ConnectionLoginStatus reports authentication without starting a Gateway or
// issuing another browser login URL.
func (b *Brokers) ConnectionLoginStatus(ctx context.Context, connectionID int64) (broker.LoginAction, error) {
	connection, err := b.store.GetBrokerConnection(ctx, connectionID)
	if err != nil {
		return broker.LoginAction{}, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	switch connection.ProviderCode {
	case "IBKR":
		adapter := b.ibkrConnections[connectionID]
		if adapter == nil {
			return broker.LoginAction{}, fmt.Errorf("IBKR connection %d is not loaded", connectionID)
		}
		return adapter.LoginStatus(ctx)
	case "SCHWAB":
		client := b.schwabConnections[connectionID]
		if client == nil {
			return broker.LoginAction{}, fmt.Errorf("Schwab connection %d is not loaded", connectionID)
		}
		_, authenticated := client.Token()
		return broker.LoginAction{Authenticated: authenticated}, nil
	case "ALPACA":
		client := b.alpacaConnections[connectionID]
		if client == nil {
			return broker.LoginAction{}, fmt.Errorf("Alpaca connection %d is not loaded", connectionID)
		}
		return client.LoginStatus(ctx)
	default:
		return broker.LoginAction{}, fmt.Errorf("unsupported broker provider %s", connection.ProviderCode)
	}
}

// IBKRGatewayTarget resolves a connection to its private Gateway origin. The
// API proxy performs the final loopback validation before forwarding traffic.
func (b *Brokers) IBKRGatewayTarget(ctx context.Context, connectionID int64) (*url.URL, bool, error) {
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
	adapter := b.ibkrConnections[connectionID]
	b.mu.RUnlock()
	if adapter == nil {
		return nil, true, fmt.Errorf("IBKR connection %d is not loaded", connectionID)
	}
	target, err := url.Parse(adapter.BaseURL())
	if err != nil {
		return nil, true, fmt.Errorf("parse IBKR connection %d Gateway URL: %w", connectionID, err)
	}
	return target, true, nil
}

func (b *Brokers) ExchangeConnectionOAuthCode(ctx context.Context, connectionID int64, code string) error {
	connection, err := b.store.GetBrokerConnection(ctx, connectionID)
	if err != nil {
		return err
	}
	if connection.ProviderCode != "SCHWAB" {
		return fmt.Errorf("broker connection %d does not use Schwab OAuth", connectionID)
	}
	b.mu.RLock()
	client := b.schwabConnections[connectionID]
	b.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("Schwab connection %d is not loaded", connectionID)
	}
	_, err = client.ExchangeCodeForToken(ctx, strings.TrimSpace(code))
	return err
}

func (b *Brokers) ApplyConfig(_ context.Context, updated config.Config) error {
	b.snap.SetConfig(updated.SnapTrade)
	return nil
}

func (b *Brokers) StartGateway(ctx context.Context) error {
	b.mu.RLock()
	gateways := make([]*ibkr.GatewayManager, 0, len(b.ibkrGateways))
	for _, id := range sortedMapKeys(b.ibkrGateways) {
		gateways = append(gateways, b.ibkrGateways[id])
	}
	b.mu.RUnlock()
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
func (b *Brokers) ShutdownGateways() error {
	b.mu.RLock()
	gateways := make([]*ibkr.GatewayManager, 0, len(b.ibkrGateways))
	for _, id := range sortedMapKeys(b.ibkrGateways) {
		gateways = append(gateways, b.ibkrGateways[id])
	}
	b.mu.RUnlock()
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

func (b *Brokers) IBKRGatewayStatus(gatewayID int64) (any, error) {
	manager, err := b.ibkrGatewayManager(gatewayID)
	if err != nil {
		return nil, err
	}
	return manager.Status(), nil
}

func (b *Brokers) IBKRGatewayLoginURL(gatewayID int64) (string, error) {
	manager, err := b.ibkrGatewayManager(gatewayID)
	if err != nil {
		return "", err
	}
	return manager.LoginURL(), nil
}

func (b *Brokers) StartIBKRGateway(ctx context.Context, gatewayID int64) error {
	manager, err := b.ibkrGatewayManager(gatewayID)
	if err != nil {
		return err
	}
	return manager.StartGateway(ctx)
}

func (b *Brokers) StopIBKRGateway(gatewayID int64, keepSession bool) error {
	manager, err := b.ibkrGatewayManager(gatewayID)
	if err != nil {
		return err
	}
	return manager.StopGateway(keepSession)
}

func (b *Brokers) ReconnectIBKRGateway(gatewayID int64) error {
	manager, err := b.ibkrGatewayManager(gatewayID)
	if err != nil {
		return err
	}
	return manager.Reconnect()
}

func (b *Brokers) UpgradeIBKRGateway(ctx context.Context, gatewayID int64) error {
	manager, err := b.ibkrGatewayManager(gatewayID)
	if err != nil {
		return err
	}
	return manager.Upgrade(ctx)
}

func (b *Brokers) RollbackIBKRGateway(ctx context.Context, gatewayID int64) error {
	manager, err := b.ibkrGatewayManager(gatewayID)
	if err != nil {
		return err
	}
	return manager.Rollback(ctx)
}

func (b *Brokers) ibkrGatewayManager(gatewayID int64) (*ibkr.GatewayManager, error) {
	b.mu.RLock()
	manager := b.ibkrGateways[gatewayID]
	b.mu.RUnlock()
	if manager == nil {
		return nil, fmt.Errorf("IBKR gateway %d is not loaded", gatewayID)
	}
	return manager, nil
}

func (b *Brokers) newSchwabConnectionClient(provider store.BrokerProviderRuntimeConfig, connection store.BrokerConnection) *schwab.Client {
	cfg := config.SchwabConfig{
		ClientID:     provider.Secrets["client_id"],
		ClientSecret: provider.Secrets["client_secret"],
		RedirectURI:  stringValue(provider.Config, "redirect_uri"),
	}
	client := schwab.New(cfg, schwab.WithTokenHandler(func(token schwab.Token) {
		current, err := b.store.GetBrokerConnectionRuntimeConfig(context.Background(), connection.ID)
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
		_, _ = b.store.UpsertBrokerConnection(context.Background(), current)
	}))
	if accessToken := connection.Secrets["access_token"]; accessToken != "" {
		expiresAt, _ := time.Parse(time.RFC3339Nano, stringValue(connection.Config, "expires_at"))
		client.SetToken(schwab.Token{
			AccessToken: accessToken, RefreshToken: connection.Secrets["refresh_token"], ExpiresAt: expiresAt,
		})
	}
	return client
}

func ibkrConnectionConfig(connection store.BrokerConnection) (config.IBKRConfig, error) {
	gatewayURL := strings.TrimSuffix(strings.TrimRight(stringValue(connection.Config, "gateway_url"), "/"), "/v1/api")
	if gatewayURL == "" {
		return config.IBKRConfig{}, errors.New("gateway_url is required")
	}
	parsed, err := url.Parse(gatewayURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return config.IBKRConfig{}, errors.New("gateway_url must be an HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return config.IBKRConfig{}, errors.New("gateway_url must be an origin without credentials, path, query, or fragment")
	}
	flexBaseURL := stringValue(connection.Config, "flex_base_url")
	if flexBaseURL == "" {
		flexBaseURL = "https://ndcdyn.interactivebrokers.com/AccountManagement/FlexWebService"
	}
	return config.IBKRConfig{
		FlexToken:   connection.Secrets["flex_token"],
		FlexQueryID: stringValue(connection.Config, "flex_query_id"),
		FlexBaseURL: flexBaseURL,
		GatewayURL:  gatewayURL,
	}, nil
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

func alpacaConfig(provider store.BrokerProviderRuntimeConfig, connection store.BrokerConnection) config.AlpacaConfig {
	baseURL := stringValue(connection.Config, "base_url")
	if baseURL == "" {
		if strings.EqualFold(connection.Environment, "live") {
			baseURL = stringValue(provider.Config, "live_base_url")
		} else {
			baseURL = stringValue(provider.Config, "paper_base_url")
		}
	}
	return config.AlpacaConfig{
		APIKey: connection.Secrets["api_key"], APISecret: connection.Secrets["api_secret"], BaseURL: baseURL,
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

type gatewayRegistryAdapter struct{ registry *Brokers }

func (g gatewayRegistryAdapter) manager() (*ibkr.GatewayManager, error) {
	g.registry.mu.RLock()
	defer g.registry.mu.RUnlock()
	for _, id := range sortedMapKeys(g.registry.ibkrGateways) {
		return g.registry.ibkrGateways[id], nil
	}
	return nil, errors.New("IBKR gateway is not configured")
}

func (g gatewayRegistryAdapter) Status() any {
	manager, err := g.manager()
	if err != nil {
		return map[string]any{"error": err.Error(), "running": false}
	}
	return manager.Status()
}

func (g gatewayRegistryAdapter) LoginURL() string {
	manager, err := g.manager()
	if err != nil {
		return ""
	}
	return manager.LoginURL()
}

func (g gatewayRegistryAdapter) StartGateway(ctx context.Context) error {
	manager, err := g.manager()
	if err != nil {
		return err
	}
	return manager.StartGateway(ctx)
}

func (g gatewayRegistryAdapter) StopGateway(keepSession bool) error {
	manager, err := g.manager()
	if err != nil {
		return err
	}
	return manager.StopGateway(keepSession)
}

func (g gatewayRegistryAdapter) Reconnect() error {
	manager, err := g.manager()
	if err != nil {
		return err
	}
	return manager.Reconnect()
}

func (g gatewayRegistryAdapter) Upgrade(ctx context.Context) error {
	manager, err := g.manager()
	if err != nil {
		return err
	}
	return manager.Upgrade(ctx)
}

func (g gatewayRegistryAdapter) Rollback(ctx context.Context) error {
	manager, err := g.manager()
	if err != nil {
		return err
	}
	return manager.Rollback(ctx)
}
