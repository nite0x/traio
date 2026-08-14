package runtime

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

func TestBuildBrokersLoadsEnabledConnectionsWithoutInventingDefaults(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := config.Default(baseDir)
	registry, err := BuildBrokers(cfg, st, baseDir)
	if err != nil {
		t.Fatalf("build brokers: %v", err)
	}
	if len(registry.SyncSources()) != 0 {
		t.Fatalf("empty database invented broker sources: %#v", registry.SyncSources())
	}
	connections, err := st.ListBrokerConnections(t.Context())
	if err != nil || len(connections) != 0 {
		t.Fatalf("build must not invent connections: connections=%#v err=%v", connections, err)
	}

	first, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "first", Name: "First", Enabled: true,
		Config: map[string]any{
			"gateway_url": "https://gateway-one.example.test",
		},
	})
	if err != nil {
		t.Fatalf("create first connection: %v", err)
	}
	second, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "second", Name: "Second", Enabled: true,
		Config: map[string]any{
			"gateway_url": "https://gateway-two.example.test",
		},
	})
	if err != nil {
		t.Fatalf("create second connection: %v", err)
	}
	if err := registry.Reload(t.Context()); err != nil {
		t.Fatalf("reload brokers: %v", err)
	}
	target, isIBKR, err := registry.IBKRGatewayTarget(t.Context(), second.ID)
	if err != nil || !isIBKR || target.String() != "https://gateway-two.example.test" {
		t.Fatalf("connection-scoped Gateway target: target=%v isIBKR=%v err=%v", target, isIBKR, err)
	}
	sources := registry.SyncSources()
	if len(sources) != 2 || sources[0].ConnectionID != first.ID || sources[1].ConnectionID != second.ID {
		t.Fatalf("unexpected connection-scoped sources: %#v", sources)
	}
	if err := st.SetBrokerConnectionEnabled(t.Context(), first.ID, false); err != nil {
		t.Fatalf("disable first connection: %v", err)
	}
	if err := registry.Reload(t.Context()); err != nil {
		t.Fatalf("reload disabled brokers: %v", err)
	}
	sources = registry.SyncSources()
	if len(sources) != 1 || sources[0].ConnectionID != second.ID {
		t.Fatalf("disabled connection remained active: %#v", sources)
	}
	if len(registry.ibkrGateways) != 0 {
		t.Fatalf("connections must not create managed gateways: %#v", registry.ibkrGateways)
	}
}

func TestBuildBrokersImportsCompleteLegacyIBKRGateway(t *testing.T) {
	t.Setenv("TRAIO_IBKR_GATEWAY_LIFECYCLE", "managed")
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "primary", Name: "My IBKR", Enabled: true,
		Config: map[string]any{
			"gateway_url":  "https://localhost:5680",
			"gateway_dir":  filepath.Join(baseDir, "ibkr-gateway"),
			"gateway_port": 5680,
		},
	})
	if err != nil {
		t.Fatalf("create legacy connection: %v", err)
	}

	registry, err := BuildBrokers(config.Default(baseDir), st, baseDir)
	if err != nil {
		t.Fatalf("build brokers: %v", err)
	}
	gateways, err := st.ListIBKRGateways(t.Context())
	if err != nil || len(gateways) != 1 {
		t.Fatalf("legacy Gateway import: gateways=%#v err=%v", gateways, err)
	}
	got := gateways[0]
	if got.GatewayKey != "primary" || got.GatewayURL != "https://localhost:5680" || got.GatewayPort != 5680 || got.Lifecycle != "managed" {
		t.Fatalf("unexpected imported Gateway: %#v", got)
	}
	if want := config.DefaultIBKRGatewayDir(baseDir, "primary"); got.GatewayDir != want {
		t.Fatalf("imported Gateway directory: got %q, want %q", got.GatewayDir, want)
	}
	if registry.ibkrGateways[got.ID] == nil {
		t.Fatalf("imported Gateway was not loaded into runtime")
	}
	cleaned, err := st.GetBrokerConnectionRuntimeConfig(t.Context(), connection.ID)
	if err != nil {
		t.Fatalf("load cleaned connection: %v", err)
	}
	if cleaned.Config["gateway_url"] != "https://localhost:5680" || cleaned.Config["gateway_dir"] != nil || cleaned.Config["gateway_port"] != nil {
		t.Fatalf("legacy process fields were not centralized: %#v", cleaned.Config)
	}
	if err := registry.Reload(t.Context()); err != nil {
		t.Fatalf("reload imported Gateway: %v", err)
	}
	gateways, err = st.ListIBKRGateways(t.Context())
	if err != nil || len(gateways) != 1 {
		t.Fatalf("legacy import was not idempotent: gateways=%#v err=%v", gateways, err)
	}
	if err := st.DeleteIBKRGateway(t.Context(), got.ID); err != nil {
		t.Fatalf("delete imported Gateway: %v", err)
	}
	if err := registry.Reload(t.Context()); err != nil {
		t.Fatalf("reload deleted Gateway: %v", err)
	}
	gateways, err = st.ListIBKRGateways(t.Context())
	if err != nil || len(gateways) != 0 {
		t.Fatalf("deleted Gateway was reimported: gateways=%#v err=%v", gateways, err)
	}
}

func TestSyncSourcesIncludeEverySupportedConnection(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	connections := []store.BrokerConnection{
		{ProviderCode: "IBKR", ConnectionKey: "ibkr", Name: "IBKR", Enabled: true, Config: map[string]any{"gateway_url": "https://ibkr.example.test"}},
		{ProviderCode: "SCHWAB", ConnectionKey: "schwab", Name: "Schwab", Enabled: true},
		{ProviderCode: "ALPACA", ConnectionKey: "alpaca", Name: "Alpaca", Enabled: true},
	}
	wantIDs := make([]int64, 0, len(connections))
	for _, connection := range connections {
		created, err := st.UpsertBrokerConnection(t.Context(), connection)
		if err != nil {
			t.Fatalf("create %s connection: %v", connection.ProviderCode, err)
		}
		wantIDs = append(wantIDs, created.ID)
	}

	registry, err := BuildBrokers(config.Default(baseDir), st, baseDir)
	if err != nil {
		t.Fatalf("build brokers: %v", err)
	}
	sources := registry.SyncSources()
	if len(sources) != 3 {
		t.Fatalf("got %d sync sources, want 3: %#v", len(sources), sources)
	}
	gotNames := make([]string, 0, len(sources))
	gotIDs := make([]int64, 0, len(sources))
	for _, source := range sources {
		gotNames = append(gotNames, source.Name)
		gotIDs = append(gotIDs, source.ConnectionID)
	}
	if !slices.Equal(gotNames, []string{"IBKR", "SCHWAB", "ALPACA"}) {
		t.Fatalf("unexpected sync source names: %v", gotNames)
	}
	slices.Sort(gotIDs)
	slices.Sort(wantIDs)
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("unexpected sync source IDs: got %v want %v", gotIDs, wantIDs)
	}
}

func TestIBKRGatewayLifecycleIsIndependentFromConnection(t *testing.T) {
	t.Setenv("TRAIO_IBKR_GATEWAY_LIFECYCLE", "managed")
	provider := store.BrokerProviderRuntimeConfig{Config: map[string]any{}}
	gateway := store.IBKRGateway{
		GatewayURL: "https://localhost:5680", GatewayDir: t.TempDir(),
		GatewayPort: 5680, Lifecycle: "persistent",
	}
	if got := ibkrGatewayConfigFor(provider, gateway).GatewayLifecycle; got != config.IBKRGatewayLifecyclePersistent {
		t.Fatalf("got %q", got)
	}
	connection := store.BrokerConnection{Config: map[string]any{
		"gateway_url":       "https://remote.example.test",
		"gateway_lifecycle": "persistent",
	}}
	cfg, err := ibkrConnectionConfig(connection)
	if err != nil {
		t.Fatalf("connection config: %v", err)
	}
	if cfg.GatewayURL != "https://remote.example.test" || cfg.GatewayDir != "" || cfg.GatewayLifecycle != "" {
		t.Fatalf("connection retained lifecycle configuration: %#v", cfg)
	}
}

func TestBuildBrokersLoadsManagedGatewayWithoutConnection(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	gateway, err := st.UpsertIBKRGateway(t.Context(), store.IBKRGateway{
		GatewayKey: "local-paper", Name: "Local paper",
		GatewayURL: "https://localhost:5688", GatewayDir: filepath.Join(baseDir, "gateway"),
		GatewayPort: 5688, Lifecycle: "persistent", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	registry, err := BuildBrokers(config.Default(baseDir), st, baseDir)
	if err != nil {
		t.Fatalf("build brokers: %v", err)
	}
	if len(registry.ibkrConnections) != 0 || registry.ibkrGateways[gateway.ID] == nil {
		t.Fatalf("gateway was not independently loaded: connections=%d gateways=%#v", len(registry.ibkrConnections), registry.ibkrGateways)
	}
}
