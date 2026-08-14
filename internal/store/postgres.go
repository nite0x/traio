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
	return s.initializeBrokerModelPostgres()
}
