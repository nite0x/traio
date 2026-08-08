package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func openPostgres(dataSource string) (*Store, error) {
	db, err := sql.Open("pgx", dataSource)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	s := &Store{db: db, dialect: dialectPostgres}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate postgres: %w", err)
	}
	return s, nil
}

func (s *Store) migratePostgres() error {
	const schema = `
CREATE TABLE IF NOT EXISTS watchlist_groups (
	id BIGSERIAL PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	sort_order INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS watchlist_items (
	id BIGSERIAL PRIMARY KEY,
	group_id BIGINT NOT NULL REFERENCES watchlist_groups(id) ON DELETE CASCADE,
	symbol TEXT NOT NULL,
	conid BIGINT NOT NULL DEFAULT 0,
	name TEXT NOT NULL DEFAULT '',
	sec_type TEXT NOT NULL DEFAULT '',
	exchange TEXT NOT NULL DEFAULT '',
	currency TEXT NOT NULL DEFAULT '',
	tags TEXT NOT NULL DEFAULT '[]',
	notes TEXT NOT NULL DEFAULT '',
	custom_fields TEXT NOT NULL DEFAULT '{}',
	sort_order INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(group_id, symbol)
);
CREATE TABLE IF NOT EXISTS oauth_tokens (
	provider TEXT PRIMARY KEY,
	access_token TEXT NOT NULL,
	refresh_token TEXT,
	expires_at TEXT,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS app_settings (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	data TEXT NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS broker_accounts (
	broker TEXT NOT NULL,
	account TEXT NOT NULL DEFAULT '',
	display_name TEXT NOT NULL DEFAULT '',
	account_type TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	currency TEXT NOT NULL DEFAULT '',
	synced_at TEXT NOT NULL,
	PRIMARY KEY (broker, account)
);
CREATE TABLE IF NOT EXISTS broker_account_balances (
	broker TEXT NOT NULL,
	account TEXT NOT NULL DEFAULT '',
	currency TEXT NOT NULL DEFAULT '',
	net_liquidation DOUBLE PRECISION NOT NULL DEFAULT 0,
	total_cash_value DOUBLE PRECISION NOT NULL DEFAULT 0,
	gross_position_value DOUBLE PRECISION NOT NULL DEFAULT 0,
	buying_power DOUBLE PRECISION NOT NULL DEFAULT 0,
	unrealized_pnl DOUBLE PRECISION NOT NULL DEFAULT 0,
	realized_pnl DOUBLE PRECISION NOT NULL DEFAULT 0,
	settled_cash DOUBLE PRECISION NOT NULL DEFAULT 0,
	exchange_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
	is_base_currency BOOLEAN NOT NULL DEFAULT FALSE,
	synced_at TEXT NOT NULL,
	PRIMARY KEY (broker, account, currency),
	FOREIGN KEY (broker, account) REFERENCES broker_accounts(broker, account) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS broker_asset_positions (
	broker TEXT NOT NULL,
	account TEXT NOT NULL DEFAULT '',
	asset_type TEXT NOT NULL,
	asset_key TEXT NOT NULL,
	symbol TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL DEFAULT '',
	conid BIGINT,
	currency TEXT NOT NULL DEFAULT '',
	quantity DOUBLE PRECISION NOT NULL,
	avg_cost DOUBLE PRECISION,
	market_price DOUBLE PRECISION,
	market_value DOUBLE PRECISION NOT NULL DEFAULT 0,
	unrealized_pnl DOUBLE PRECISION,
	realized_pnl DOUBLE PRECISION,
	cost_basis DOUBLE PRECISION,
	day_pnl DOUBLE PRECISION,
	day_pnl_pct DOUBLE PRECISION,
	raw_payload TEXT,
	synced_at TEXT NOT NULL,
	PRIMARY KEY (broker, account, asset_key),
	FOREIGN KEY (broker, account) REFERENCES broker_accounts(broker, account) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_broker_asset_positions_symbol ON broker_asset_positions(symbol);
CREATE INDEX IF NOT EXISTS idx_broker_asset_positions_asset_key ON broker_asset_positions(asset_key);
CREATE TABLE IF NOT EXISTS broker_account_performance (
	broker TEXT NOT NULL,
	account TEXT NOT NULL DEFAULT '',
	daily_pnl DOUBLE PRECISION NOT NULL DEFAULT 0,
	net_liquidation DOUBLE PRECISION NOT NULL DEFAULT 0,
	unrealized_pnl DOUBLE PRECISION NOT NULL DEFAULT 0,
	excess_liquidity DOUBLE PRECISION NOT NULL DEFAULT 0,
	market_value DOUBLE PRECISION NOT NULL DEFAULT 0,
	synced_at TEXT NOT NULL,
	PRIMARY KEY (broker, account),
	FOREIGN KEY (broker, account) REFERENCES broker_accounts(broker, account) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS broker_sync_status (
	broker TEXT NOT NULL,
	account TEXT NOT NULL DEFAULT '',
	data_type TEXT NOT NULL,
	synced_at TEXT NOT NULL DEFAULT '',
	last_attempt_at TEXT NOT NULL,
	last_error TEXT NOT NULL DEFAULT '',
	item_count INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (broker, account, data_type)
);
CREATE TABLE IF NOT EXISTS candle_cache (
	symbol TEXT NOT NULL,
	conid BIGINT NOT NULL,
	period TEXT NOT NULL,
	bar TEXT NOT NULL,
	candles TEXT NOT NULL,
	cached_at BIGINT NOT NULL,
	PRIMARY KEY (symbol, period, bar)
);
INSERT INTO watchlist_groups (id, name, sort_order) VALUES (1, '默认', 0)
	ON CONFLICT(id) DO NOTHING;
`

	for _, statement := range strings.Split(schema, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}
