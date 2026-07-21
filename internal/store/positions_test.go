package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nite/traio/internal/broker"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestReplaceBrokerPositionsWritesAssetProjection(t *testing.T) {
	st := newTestStore(t)

	err := st.ReplaceBrokerPositions(context.Background(), "ibkr", []broker.Position{{
		Symbol:      "aapl",
		ConID:       265598,
		Quantity:    2,
		AvgCost:     150,
		MarketPrice: 180,
		MarketValue: 360,
		Unrealized:  60,
		Currency:    "usd",
		Account:     "U1",
	}})
	if err != nil {
		t.Fatalf("replace positions: %v", err)
	}

	var assetType, assetKey, symbol, currency string
	var avgCost, unrealized float64
	if err := st.db.QueryRow(`
		SELECT asset_type, asset_key, symbol, currency, avg_cost, unrealized_pnl
		FROM broker_asset_positions
		WHERE broker = 'IBKR' AND account = 'U1'`).Scan(
		&assetType, &assetKey, &symbol, &currency, &avgCost, &unrealized,
	); err != nil {
		t.Fatalf("read asset projection: %v", err)
	}
	if assetType != "security" || assetKey != "security:conid:265598" {
		t.Fatalf("unexpected asset identity: %s %s", assetType, assetKey)
	}
	if symbol != "AAPL" || currency != "USD" || avgCost != 150 || unrealized != 60 {
		t.Fatalf("unexpected asset row: symbol=%s currency=%s avgCost=%v unrealized=%v", symbol, currency, avgCost, unrealized)
	}
}

func TestListBrokerPositionsReadsAssetProjection(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.db.Exec(`
		INSERT INTO broker_accounts (broker, account, currency, synced_at)
		VALUES ('IBKR', 'U1', 'USD', '2026-01-01T00:00:00Z');
		INSERT INTO broker_asset_positions (
			broker, account, asset_type, asset_key, symbol, conid, currency,
			quantity, avg_cost, market_price, market_value, unrealized_pnl,
			realized_pnl, synced_at
		) VALUES
			('IBKR', 'U1', 'cash', 'cash:USD', 'USD', NULL, 'USD', 1000, NULL, 1, 1000, NULL, NULL, '2026-01-01T00:00:00Z'),
			('IBKR', 'U1', 'security', 'security:symbol:MSFT', 'MSFT', NULL, 'USD', 3, 200, 250, 750, 150, 0, '2026-01-01T00:00:00Z');
	`); err != nil {
		t.Fatalf("seed asset projection: %v", err)
	}

	positions, err := st.ListBrokerPositions(context.Background())
	if err != nil {
		t.Fatalf("list positions: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("expected only non-cash positions, got %#v", positions)
	}
	if positions[0].Symbol != "MSFT" || positions[0].Quantity != 3 || positions[0].Unrealized != 150 {
		t.Fatalf("unexpected position: %#v", positions[0])
	}
}
