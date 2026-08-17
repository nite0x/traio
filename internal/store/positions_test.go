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

func TestReplaceBrokerAccountPositionsWritesAssetProjection(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	connection := createTestConnection(t, st, "default")
	if err := st.ReplaceBrokerConnectionAccounts(ctx, connection.ID, []broker.Account{{ID: "U1", BaseCurrency: "USD"}}); err != nil {
		t.Fatalf("replace accounts: %v", err)
	}

	dayPnL := 4.5
	dayPnLPct := 1.25
	err := st.ReplaceBrokerConnectionAccountPositions(ctx, connection.ID, "U1", []broker.Position{{
		Symbol:      "aapl",
		Name:        "Apple Inc.",
		ConID:       265598,
		Quantity:    2,
		AvgCost:     150,
		MarketPrice: 180,
		MarketValue: 360,
		Unrealized:  60,
		DailyPnL:    &dayPnL,
		DailyPnLPct: &dayPnLPct,
		Currency:    "usd",
	}})
	if err != nil {
		t.Fatalf("replace positions: %v", err)
	}

	var assetType, assetKey, symbol, name, currency string
	var avgCost, unrealized, storedDayPnL, storedDayPnLPct float64
	if err := st.db.QueryRow(`
		SELECT x.asset_type, x.asset_key, x.symbol, x.name, x.currency, x.avg_cost,
			x.unrealized_pnl, x.day_pnl, x.day_pnl_pct
		FROM broker_asset_positions x
		JOIN broker_accounts a ON a.id = x.account_id
		WHERE a.provider_code = 'IBKR' AND a.provider_account_id = 'U1'`).Scan(
		&assetType, &assetKey, &symbol, &name, &currency, &avgCost, &unrealized,
		&storedDayPnL, &storedDayPnLPct,
	); err != nil {
		t.Fatalf("read asset projection: %v", err)
	}
	if assetType != "stock" || assetKey != "stock:conid:265598" {
		t.Fatalf("unexpected asset identity: %s %s", assetType, assetKey)
	}
	if symbol != "AAPL" || name != "Apple Inc." || currency != "USD" || avgCost != 150 || unrealized != 60 || storedDayPnL != dayPnL || storedDayPnLPct != dayPnLPct {
		t.Fatalf("unexpected asset row: symbol=%s name=%s currency=%s avgCost=%v unrealized=%v dayPnL=%v dayPnLPct=%v", symbol, name, currency, avgCost, unrealized, storedDayPnL, storedDayPnLPct)
	}
	positions, err := st.ListBrokerPositions(ctx)
	if err != nil || len(positions) != 1 {
		t.Fatalf("list stored positions: positions=%#v err=%v", positions, err)
	}
	if positions[0].Name != "Apple Inc." || positions[0].DailyPnL == nil || *positions[0].DailyPnL != dayPnL || positions[0].DailyPnLPct == nil || *positions[0].DailyPnLPct != dayPnLPct {
		t.Fatalf("stored position lost frontend fields: %#v", positions[0])
	}
}

func TestBrokerAccountBalancesBelongToAccount(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	connection := createTestConnection(t, st, "default")
	if err := st.ReplaceBrokerConnectionAccounts(ctx, connection.ID, []broker.Account{{ID: "U1", BaseCurrency: "USD"}}); err != nil {
		t.Fatalf("seed broker account: %v", err)
	}
	account, err := st.ListBrokerAccounts(ctx)
	if err != nil || len(account) != 1 {
		t.Fatalf("list broker account: accounts=%#v err=%v", account, err)
	}
	if _, err := st.db.Exec(`
		INSERT INTO broker_account_balances (
			account_id, currency, net_liquidation, total_cash_value,
			gross_position_value, buying_power, unrealized_pnl, realized_pnl, synced_at
		) VALUES (
			?, 'USD', 1250, 500, 750, 1000, 150, 25, '2026-01-01T00:00:00Z'
		);
	`, account[0].ID); err != nil {
		t.Fatalf("seed account balance: %v", err)
	}

	var netLiquidation, cashValue float64
	if err := st.db.QueryRow(`
		SELECT net_liquidation, total_cash_value
		FROM broker_account_balances
		WHERE account_id = ? AND currency = 'USD'
	`, account[0].ID).Scan(&netLiquidation, &cashValue); err != nil {
		t.Fatalf("read account balance: %v", err)
	}
	if netLiquidation != 1250 || cashValue != 500 {
		t.Fatalf("unexpected account balance: net_liquidation=%v total_cash_value=%v", netLiquidation, cashValue)
	}

	if _, err := st.db.Exec(`DELETE FROM broker_accounts WHERE id = ?`, account[0].ID); err != nil {
		t.Fatalf("delete broker account: %v", err)
	}
	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM broker_account_balances`).Scan(&count); err != nil {
		t.Fatalf("count account balances: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected account balance to cascade-delete, got %d rows", count)
	}
}

func TestListBrokerPositionsReadsAssetProjection(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	connection := createTestConnection(t, st, "default")
	if err := st.ReplaceBrokerConnectionAccounts(ctx, connection.ID, []broker.Account{{ID: "U1", BaseCurrency: "USD"}}); err != nil {
		t.Fatalf("seed broker account: %v", err)
	}
	accounts, err := st.ListBrokerAccounts(ctx)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("list broker account: accounts=%#v err=%v", accounts, err)
	}
	instrument, err := st.ResolveInstrument(ctx, InstrumentIdentity{
		ProviderCode: "IBKR", AssetType: "stock", Market: "US", Symbol: "MSFT", Currency: "USD",
	})
	if err != nil {
		t.Fatalf("resolve instrument: %v", err)
	}
	if _, err := st.db.Exec(`
		INSERT INTO broker_asset_positions (
			account_id, instrument_id, external_id, asset_type, asset_key, symbol, conid, currency,
			quantity, avg_cost, market_price, market_value, unrealized_pnl,
			realized_pnl, synced_at
		) VALUES (?, ?, '', 'stock', 'stock:symbol:MSFT', 'MSFT', NULL, 'USD', 3, 200, 250, 750, 150, 0, '2026-01-01T00:00:00Z');
	`, accounts[0].ID, instrument.ID); err != nil {
		t.Fatalf("seed asset projection: %v", err)
	}

	positions, err := st.ListBrokerPositions(context.Background())
	if err != nil {
		t.Fatalf("list positions: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("expected one canonical position, got %#v", positions)
	}
	if positions[0].Symbol != "MSFT" || positions[0].Quantity != 3 || positions[0].Unrealized != 150 {
		t.Fatalf("unexpected position: %#v", positions[0])
	}
}
