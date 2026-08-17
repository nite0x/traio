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
	if err := s.requireCurrentBrokerSchemaPostgres(); err != nil {
		return err
	}
	return s.initializeBrokerModelPostgres()
}

// requireCurrentBrokerSchemaPostgres follows the same explicit cutover policy
// as SQLite. Existing numeric-provider broker tables must be migrated or
// recreated instead of being partially modified by CREATE TABLE IF NOT EXISTS.
func (s *Store) requireCurrentBrokerSchemaPostgres() error {
	var exists int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'broker_accounts'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	rows, err := s.db.Query(`
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'broker_accounts'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	hasProviderAccountID := false
	hasProviderCode := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		hasProviderAccountID = hasProviderAccountID || name == "provider_account_id"
		hasProviderCode = hasProviderCode || name == "provider_code"
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !hasProviderAccountID || !hasProviderCode {
		return fmt.Errorf("legacy PostgreSQL broker schema detected: back up and recreate or migrate the database; automatic migration is intentionally disabled")
	}
	var positionColumns int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'broker_asset_positions'
			AND column_name IN ('instrument_id', 'external_id') AND is_nullable = 'NO'`).Scan(&positionColumns); err != nil {
		return err
	}
	if positionColumns != 2 {
		return fmt.Errorf("pre-instrument PostgreSQL broker schema detected: back up and recreate or migrate the database; automatic migration is intentionally disabled")
	}
	var instrumentTables int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name IN ('instruments', 'broker_instruments')`).Scan(&instrumentTables); err != nil {
		return err
	}
	if instrumentTables != 2 {
		return fmt.Errorf("incomplete instrument schema detected: back up and recreate or migrate the database")
	}
	return nil
}
