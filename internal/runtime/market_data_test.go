package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

type marketDataProviderFactory struct {
	sessions []*runtimeMarketDataSession
}

func (*marketDataProviderFactory) Definition() broker.ProviderDefinition {
	return broker.ProviderDefinition{Code: "IBKR", Name: "Generic market data test"}
}

func (f *marketDataProviderFactory) Open(_ context.Context, cfg broker.ConnectionConfig) (broker.BrokerSession, error) {
	label, _ := cfg.Config["gateway_url"].(string)
	session := &runtimeMarketDataSession{id: cfg.ID, label: label}
	f.sessions = append(f.sessions, session)
	return session, nil
}

type runtimeMarketDataSession struct {
	id     int64
	label  string
	closed atomic.Int32
}

func (s *runtimeMarketDataSession) ConnectionID() int64 { return s.id }
func (*runtimeMarketDataSession) ProviderCode() string  { return "IBKR" }
func (*runtimeMarketDataSession) Health(context.Context) (broker.ConnectionHealth, error) {
	return broker.ConnectionHealth{State: broker.ConnectionStateConnected}, nil
}
func (s *runtimeMarketDataSession) Close(context.Context) error {
	s.closed.Add(1)
	return nil
}
func (s *runtimeMarketDataSession) SearchInstruments(context.Context, string) ([]broker.Instrument, error) {
	return []broker.Instrument{{ConID: s.id, Symbol: s.label}}, nil
}
func (s *runtimeMarketDataSession) GetQuotesByConID(context.Context, []int64) ([]broker.Quote, error) {
	return []broker.Quote{{ConID: s.id, Symbol: s.label}}, nil
}
func (s *runtimeMarketDataSession) GetCandles(context.Context, int64, string, string) ([]broker.Candle, error) {
	return []broker.Candle{{Time: s.id, Close: float64(s.id)}}, nil
}

func TestReloadBuildsMarketDataRoutesFromGenericSessions(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "generic", Name: "Generic", Enabled: true,
		Config: map[string]any{"gateway_url": "https://one.example.test"},
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	factory := &marketDataProviderFactory{}
	providers := broker.NewProviderRegistry()
	if err := providers.Register(factory); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	runtime, err := buildConnectionManagerWithProviderRegistry(config.Default(baseDir), st, baseDir, providers)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	results, err := runtime.MarketData.SearchInstrumentsForConnection(t.Context(), connection.ID, "AAPL")
	if err != nil || len(results) != 1 || results[0].Symbol != "https://one.example.test" {
		t.Fatalf("generic session route: results=%#v err=%v", results, err)
	}
	quotes, err := runtime.MarketData.GetQuotesByConID(t.Context(), []int64{connection.ID})
	if err != nil || len(quotes) != 1 || quotes[0].ConID != connection.ID {
		t.Fatalf("legacy quote facade: quotes=%#v err=%v", quotes, err)
	}

	first := factory.sessions[0]
	connection.Config["gateway_url"] = "https://two.example.test"
	if _, err := st.UpsertBrokerConnection(t.Context(), connection); err != nil {
		t.Fatalf("replace connection config: %v", err)
	}
	if err := runtime.Reload(t.Context()); err != nil {
		t.Fatalf("reload replacement: %v", err)
	}
	results, err = runtime.MarketData.SearchInstruments(t.Context(), "AAPL")
	if err != nil || len(results) != 1 || results[0].Symbol != "https://two.example.test" {
		t.Fatalf("replacement route: results=%#v err=%v", results, err)
	}
	if first.closed.Load() != 1 {
		t.Fatalf("replaced session close count = %d", first.closed.Load())
	}

	if err := st.SetBrokerConnectionEnabled(t.Context(), connection.ID, false); err != nil {
		t.Fatalf("disable connection: %v", err)
	}
	if err := runtime.Reload(t.Context()); err != nil {
		t.Fatalf("reload disabled connection: %v", err)
	}
	if runtime.MarketData.Supports(broker.MarketDataCapabilityInstruments) {
		t.Fatal("disabled connection remained in market data routes")
	}
	if _, err := runtime.MarketData.SearchInstruments(t.Context(), "AAPL"); !errors.Is(err, broker.ErrCapabilityUnavailable) {
		t.Fatalf("disabled route error = %v", err)
	}
}

func TestRuntimeKeepsSchwabSingleQuotesSeparateFromIBKRContractData(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ibkrConnection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "ibkr-market", Name: "IBKR market", Enabled: true,
		Config: map[string]any{"gateway_id": "primary", "gateway_url": "https://ibkr.example.test"},
	})
	if err != nil {
		t.Fatalf("create IBKR connection: %v", err)
	}
	schwabConnection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "SCHWAB", ConnectionKey: "schwab-market", Name: "Schwab market", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create Schwab connection: %v", err)
	}
	runtime, err := BuildConnectionManager(config.Default(baseDir), st, baseDir)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	for _, capability := range []broker.MarketDataCapability{
		broker.MarketDataCapabilityInstruments,
		broker.MarketDataCapabilityBatchQuotes,
		broker.MarketDataCapabilityCandles,
	} {
		connectionID, err := runtime.MarketData.DefaultConnectionID(capability)
		if err != nil || connectionID != ibkrConnection.ID {
			t.Fatalf("%s default: id=%d err=%v; want IBKR %d", capability, connectionID, err, ibkrConnection.ID)
		}
	}
	singleQuoteID, err := runtime.MarketData.DefaultConnectionID(broker.MarketDataCapabilityQuotes)
	if err != nil || singleQuoteID != schwabConnection.ID {
		t.Fatalf("single quote default: id=%d err=%v; want Schwab %d", singleQuoteID, err, schwabConnection.ID)
	}
}
