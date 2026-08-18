package portfolio

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/store"
)

func TestSnapshotAggregatesSameInstrumentAcrossBrokers(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "aggregate.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	type seed struct {
		provider, key, account, external               string
		quantity, averageCost, marketValue, unrealized float64
	}
	for _, item := range []seed{
		{provider: "IBKR", key: "ibkr", account: "U1", external: "265598", quantity: 2, averageCost: 150, marketValue: 400, unrealized: 100},
		{provider: "SCHWAB", key: "schwab", account: "S1", external: "037833100", quantity: 3, averageCost: 160, marketValue: 600, unrealized: 120},
	} {
		connection, err := st.UpsertBrokerConnection(ctx, store.BrokerConnection{
			ProviderCode: item.provider, ConnectionKey: item.key, Name: item.provider, Enabled: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.ReplaceBrokerConnectionAccounts(ctx, connection.ID, []broker.Account{{ID: item.account, BaseCurrency: "USD"}}); err != nil {
			t.Fatal(err)
		}
		if err := st.ReplaceBrokerConnectionAccountPositions(ctx, connection.ID, item.account, []broker.Position{{
			ExternalID: item.external, AssetType: "stock", Market: "US", Symbol: "AAPL", Name: "Apple Inc.",
			Quantity: item.quantity, AvgCost: item.averageCost, MarketValue: item.marketValue,
			Unrealized: item.unrealized, Currency: "USD",
		}}); err != nil {
			t.Fatal(err)
		}
	}

	snapshot, err := NewSyncService(st).Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Positions) != 1 {
		t.Fatalf("got %#v", snapshot.Positions)
	}
	position := snapshot.Positions[0]
	if position.InstrumentID == 0 || position.PositionID == "" || len(position.Legs) != 2 {
		t.Fatalf("missing stable identity or legs: %#v", position)
	}
	if position.Quantity != 5 || position.MarketValue != 1000 || position.CostBasis != 780 || position.AverageCost != 156 || position.UnrealizedPnL != 220 {
		t.Fatalf("unexpected aggregation: %#v", position)
	}
}

func TestAggregatePositionsRejectsMissingInstrumentID(t *testing.T) {
	_, err := aggregatePositions([]broker.Position{{Symbol: "AAPL", Account: "U1", Quantity: 1}}, 100)
	if err == nil || !strings.Contains(err.Error(), "missing instrument_id") {
		t.Fatalf("expected missing instrument_id error, got %v", err)
	}
}

func TestSnapshotUsesPositionTotalsInsteadOfAccountPerformanceEstimates(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "position-totals.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	connection, err := st.UpsertBrokerConnection(ctx, store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "ibkr", Name: "IBKR", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceBrokerConnectionAccounts(ctx, connection.ID, []broker.Account{{ID: "U1", BaseCurrency: "USD"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceBrokerConnectionAccountPositions(ctx, connection.ID, "U1", []broker.Position{
		{ExternalID: "1", AssetType: "stock", Market: "US", Symbol: "AAPL", Quantity: 100, MarketValue: 30_705, Unrealized: -12, Currency: "USD"},
		{ExternalID: "2", AssetType: "etf", Market: "US", Symbol: "QQQ", Quantity: 100, MarketValue: 73_423, Unrealized: -62, Currency: "USD"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceBrokerConnectionAccountPerformance(ctx, connection.ID, broker.DailyPerformance{
		AccountID: "U1", NetLiquidation: 1_000_614.64, MarketValue: 104_200, UnrealizedPnL: -43, DailyPnL: -43,
	}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := NewSyncService(st).Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Summary.NetAssetValue != 1_000_614.64 || snapshot.Summary.HoldingsValue != 104_128 || snapshot.Summary.UnrealizedPnL != -74 || snapshot.Summary.DailyPnL != -43 {
		t.Fatalf("unexpected summary: %#v", snapshot.Summary)
	}
}
