package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nite/traio/internal/broker"
)

func TestBrokerProvidersAndConnectionsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	providers, err := st.ListBrokerProviders(t.Context())
	if err != nil {
		t.Fatalf("list providers: %v", err)
	}
	if len(providers) != 3 || providers[0].Code != "ALPACA" || providers[1].Code != "IBKR" || providers[2].Code != "SCHWAB" {
		t.Fatalf("unexpected initialized providers: %#v", providers)
	}
	if len(providers[1].Capabilities) == 0 || providers[1].DisplayInfo["short_name"] != "IBKR" {
		t.Fatalf("missing IBKR metadata: %#v", providers[1])
	}

	first, err := st.UpsertBrokerConnection(t.Context(), BrokerConnection{
		ProviderCode: "ibkr", ConnectionKey: "primary", Name: "Primary", Environment: "live", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if first.Status != BrokerConnectionStatusDisconnected {
		t.Fatalf("new connection status: got %q", first.Status)
	}
	if err := st.ReplaceBrokerConnectionAccounts(t.Context(), first.ID, []broker.Account{{ID: "U1"}}); err != nil {
		t.Fatalf("authenticate connection: %v", err)
	}
	updated, err := st.UpsertBrokerConnection(t.Context(), BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "primary", Name: "Primary renamed", Environment: "paper", Enabled: true,
	})
	if err != nil {
		t.Fatalf("update connection: %v", err)
	}
	if updated.ID != first.ID || updated.Status != BrokerConnectionStatusConnected || updated.LastAuthenticatedAt == "" {
		t.Fatalf("upsert did not preserve connection identity/authentication: %#v", updated)
	}
	second, err := st.UpsertBrokerConnection(t.Context(), BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "secondary", Name: "Secondary", Environment: "paper", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create second connection: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("provider connections must have distinct identities")
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	st, err = Open(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	providers, err = st.ListBrokerProviders(t.Context())
	if err != nil || len(providers) != 3 {
		t.Fatalf("providers after repeated migration: providers=%#v err=%v", providers, err)
	}
	connections, err := st.ListBrokerConnections(t.Context())
	if err != nil || len(connections) != 2 {
		t.Fatalf("connections after repeated migration: connections=%#v err=%v", connections, err)
	}
}

func TestOpenRejectsLegacyBrokerSchemaWithoutMigrating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE broker_accounts (
		broker TEXT NOT NULL,
		account TEXT NOT NULL,
		PRIMARY KEY (broker, account)
	)`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}
	_, err = Open(path)
	if err == nil || !strings.Contains(err.Error(), "automatic migration is intentionally disabled") {
		t.Fatalf("expected explicit schema reset error, got %v", err)
	}
}

func TestOpenRejectsNumericProviderBrokerSchemaWithoutMigrating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "numeric-provider.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open numeric provider database: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE broker_accounts (
		id INTEGER PRIMARY KEY,
		provider_id INTEGER NOT NULL,
		provider_account_id TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create numeric provider schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close numeric provider database: %v", err)
	}
	_, err = Open(path)
	if err == nil || !strings.Contains(err.Error(), "automatic migration is intentionally disabled") {
		t.Fatalf("expected explicit schema reset error, got %v", err)
	}
}

func TestBrokerSchemasUseCanonicalProviderAccountModel(t *testing.T) {
	wantColumns := map[string][]string{
		"broker_providers":           {"code", "provider_fields", "connection_fields", "config_json", "secrets_json"},
		"broker_connections":         {"id", "provider_code", "provider_user_id", "username", "auth_type", "config_json", "secrets_json"},
		"broker_accounts":            {"id", "provider_code", "provider_account_id", "first_discovered_connection_id"},
		"broker_account_connections": {"account_id", "connection_id", "is_primary", "first_seen_at", "last_seen_at"},
		"broker_account_balances":    {"account_id"},
		"broker_asset_positions":     {"account_id"},
		"broker_account_performance": {"account_id"},
	}
	forbiddenColumns := map[string][]string{
		"broker_providers":   {"id"},
		"broker_connections": {"provider_id"},
		"broker_accounts":    {"provider_id"},
	}
	for name, schema := range map[string]string{
		"sqlite":   brokerModelSQLiteSchema,
		"postgres": brokerModelPostgresSchema,
	} {
		t.Run(name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatalf("open schema database: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			db.SetMaxOpenConns(1)
			if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
				t.Fatalf("enable foreign keys: %v", err)
			}
			if _, err := db.Exec(schema); err != nil {
				t.Fatalf("initialize %s broker schema: %v", name, err)
			}
			for table, wanted := range wantColumns {
				columns := sqliteTableColumns(t, db, table)
				for _, column := range wanted {
					if !columns[column] {
						t.Errorf("%s schema table %s is missing column %s", name, table, column)
					}
				}
			}
			for table, forbidden := range forbiddenColumns {
				columns := sqliteTableColumns(t, db, table)
				for _, column := range forbidden {
					if columns[column] {
						t.Errorf("%s schema table %s still contains column %s", name, table, column)
					}
				}
			}
		})
	}
}

func sqliteTableColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("inspect table %s: %v", table, err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, typ string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table %s: %v", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("inspect table %s: %v", table, err)
	}
	return columns
}

func TestProviderAndConnectionSecretsAreWriteOnly(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	provider, err := st.UpdateBrokerProviderConfig(ctx, "SCHWAB",
		map[string]any{"redirect_uri": "https://127.0.0.1/callback"},
		map[string]string{"client_id": "client", "client_secret": "secret"},
	)
	if err != nil {
		t.Fatalf("update provider config: %v", err)
	}
	if provider.Secrets != nil || !reflect.DeepEqual(provider.ConfiguredSecretKeys, []string{"client_id", "client_secret"}) {
		t.Fatalf("provider leaked or lost secret metadata: %#v", provider)
	}
	connection, err := st.UpsertBrokerConnection(ctx, BrokerConnection{
		ProviderCode: "SCHWAB", ConnectionKey: "user-1", Name: "User 1",
		AuthType: "oauth", Enabled: true,
		Config:  map[string]any{"scope": "readonly"},
		Secrets: map[string]string{"access_token": "access", "refresh_token": "refresh"},
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if connection.Secrets != nil || !reflect.DeepEqual(connection.ConfiguredSecretKeys, []string{"access_token", "refresh_token"}) {
		t.Fatalf("connection leaked or lost secret metadata: %#v", connection)
	}
	connection.Name = "Renamed"
	connection.Config = nil
	connection.Secrets = nil
	updated, err := st.UpsertBrokerConnection(ctx, connection)
	if err != nil {
		t.Fatalf("rename connection: %v", err)
	}
	if !reflect.DeepEqual(updated.ConfiguredSecretKeys, []string{"access_token", "refresh_token"}) || updated.Config["scope"] != "readonly" {
		t.Fatalf("partial update cleared connection configuration: %#v", updated)
	}
}

func TestSharedAccountUsesOneProjectionAndPromotesRemainingConnection(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	primary := createTestConnection(t, st, "primary")
	secondary := createTestConnection(t, st, "secondary")

	for _, connection := range []BrokerConnection{primary, secondary} {
		if err := st.ReplaceBrokerConnectionAccounts(ctx, connection.ID, []broker.Account{{ID: "U1", BaseCurrency: "USD"}}); err != nil {
			t.Fatalf("store account for %s: %v", connection.ConnectionKey, err)
		}
	}
	accounts, err := st.ListBrokerAccounts(ctx)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("shared provider account must be stored once: accounts=%#v err=%v", accounts, err)
	}
	if accounts[0].ProviderAccountID != "U1" || len(accounts[0].ConnectionIDs) != 2 {
		t.Fatalf("missing shared connection relationships: %#v", accounts[0])
	}
	for _, connectionID := range []int64{primary.ID, secondary.ID} {
		visible, err := st.ListBrokerAccountsByConnection(ctx, connectionID)
		if err != nil || len(visible) != 1 || visible[0].ProviderAccountID != "U1" {
			t.Fatalf("connection account filter failed for %d: accounts=%#v err=%v", connectionID, visible, err)
		}
	}
	if accounts[0].PrimaryConnectionID == nil || *accounts[0].PrimaryConnectionID != primary.ID {
		t.Fatalf("first connection must be primary: %#v", accounts[0])
	}

	if err := st.ReplaceBrokerConnectionCashBalances(ctx, primary.ID, "U1", []broker.CashBalance{{Currency: "USD", Total: 111}}); err != nil {
		t.Fatalf("store primary balance: %v", err)
	}
	if err := st.ReplaceBrokerConnectionAccountPositions(ctx, primary.ID, "U1", []broker.Position{{Symbol: "AAA", Quantity: 1, MarketValue: 10}}); err != nil {
		t.Fatalf("store primary position: %v", err)
	}
	if err := st.ReplaceBrokerConnectionAccountPerformance(ctx, primary.ID, broker.DailyPerformance{AccountID: "U1", DailyPnL: 1}); err != nil {
		t.Fatalf("store primary performance: %v", err)
	}

	balances, err := st.ListBrokerAccountBalances(ctx)
	if err != nil || len(balances) != 1 || balances[0].TotalCashValue != 111 {
		t.Fatalf("shared account balance duplicated: balances=%#v err=%v", balances, err)
	}
	positions, err := st.ListBrokerPositions(ctx)
	if err != nil || len(positions) != 1 || positions[0].Symbol != "AAA" {
		t.Fatalf("shared account positions duplicated: positions=%#v err=%v", positions, err)
	}
	if err := st.SetBrokerConnectionEnabled(ctx, primary.ID, false); err != nil {
		t.Fatalf("disable primary connection: %v", err)
	}
	accounts, err = st.ListBrokerAccounts(ctx)
	if err != nil || accounts[0].PrimaryConnectionID == nil || *accounts[0].PrimaryConnectionID != secondary.ID {
		t.Fatalf("enabled fallback connection was not promoted: accounts=%#v err=%v", accounts, err)
	}

	impact, err := st.GetBrokerConnectionDeleteImpact(ctx, primary.ID)
	if err != nil || len(impact.Shared) != 1 || len(impact.Orphaned) != 0 {
		t.Fatalf("unexpected primary delete impact: impact=%#v err=%v", impact, err)
	}
	if err := st.DeleteBrokerConnection(ctx, primary.ID); err != nil {
		t.Fatalf("delete connection: %v", err)
	}
	accounts, err = st.ListBrokerAccounts(ctx)
	if err != nil || len(accounts) != 1 || accounts[0].PrimaryConnectionID == nil || *accounts[0].PrimaryConnectionID != secondary.ID {
		t.Fatalf("remaining connection was not promoted: accounts=%#v err=%v", accounts, err)
	}
	balances, _ = st.ListBrokerAccountBalances(ctx)
	positions, _ = st.ListBrokerPositions(ctx)
	performance, _ := st.ListBrokerAccountPerformance(ctx)
	if len(balances) != 1 || len(positions) != 1 || len(performance) != 1 {
		t.Fatalf("shared account projections were removed: balances=%#v positions=%#v performance=%#v", balances, positions, performance)
	}

	impact, err = st.GetBrokerConnectionDeleteImpact(ctx, secondary.ID)
	if err != nil || len(impact.Orphaned) != 1 {
		t.Fatalf("last connection must orphan account: impact=%#v err=%v", impact, err)
	}
	if err := st.DeleteBrokerConnection(ctx, secondary.ID); err != nil {
		t.Fatalf("delete final connection: %v", err)
	}
	accounts, _ = st.ListBrokerAccounts(ctx)
	balances, _ = st.ListBrokerAccountBalances(ctx)
	positions, _ = st.ListBrokerPositions(ctx)
	if len(accounts) != 0 || len(balances) != 0 || len(positions) != 0 {
		t.Fatalf("orphan account cascade failed: accounts=%#v balances=%#v positions=%#v", accounts, balances, positions)
	}
}

func createTestConnection(t *testing.T, st *Store, key string) BrokerConnection {
	t.Helper()
	connection, err := st.UpsertBrokerConnection(t.Context(), BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: key, Name: key, Environment: "test", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create %s connection: %v", key, err)
	}
	return connection
}
