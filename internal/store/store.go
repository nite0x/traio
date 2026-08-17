package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type Store struct {
	db      *sql.DB
	dialect dialect
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite PRAGMAs are connection-local. Keep one shared connection so every
	// store operation consistently uses foreign keys and WAL mode.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable sqlite wal: %w", err)
	}
	s := &Store{db: db, dialect: dialectSQLite}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrateSQLite() error {
	schema := `
CREATE TABLE IF NOT EXISTS watchlist_groups (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	sort_order INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS watchlist_items (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	group_id INTEGER NOT NULL REFERENCES watchlist_groups(id) ON DELETE CASCADE,
	symbol TEXT NOT NULL,
	tags TEXT NOT NULL DEFAULT '[]',
	notes TEXT NOT NULL DEFAULT '',
	custom_fields TEXT NOT NULL DEFAULT '{}',
	sort_order INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(group_id, symbol)
);

CREATE TABLE IF NOT EXISTS app_settings (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	data TEXT NOT NULL,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

	`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	if err := s.requireCurrentBrokerSchemaSQLite(); err != nil {
		return err
	}
	if err := s.initializeBrokerModelSQLite(); err != nil {
		return err
	}
	if err := s.ensureWatchlistItemColumns(); err != nil {
		return err
	}
	if err := s.ensureCandleCache(); err != nil {
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO watchlist_groups (id, name, sort_order) VALUES (1, '默认', 0)
		ON CONFLICT(id) DO NOTHING;
	`)
	return err
}

// requireCurrentBrokerSchemaSQLite deliberately refuses legacy broker tables.
// This release is a direct schema cutover: callers must back up and recreate
// the SQLite database instead of expecting an implicit data migration.
func (s *Store) requireCurrentBrokerSchemaSQLite() error {
	var exists int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'broker_accounts'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	rows, err := s.db.Query(`PRAGMA table_info(broker_accounts)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	hasProviderAccountID := false
	hasProviderCode := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		hasProviderAccountID = hasProviderAccountID || name == "provider_account_id"
		hasProviderCode = hasProviderCode || name == "provider_code"
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !hasProviderAccountID || !hasProviderCode {
		return fmt.Errorf("legacy SQLite broker schema detected: back up and recreate the database; automatic migration is intentionally disabled")
	}
	return nil
}

func (s *Store) ensureWatchlistItemColumns() error {
	rows, err := s.db.Query(`PRAGMA table_info(watchlist_items)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	add := map[string]string{
		"conid":      "ALTER TABLE watchlist_items ADD COLUMN conid INTEGER NOT NULL DEFAULT 0",
		"name":       "ALTER TABLE watchlist_items ADD COLUMN name TEXT NOT NULL DEFAULT ''",
		"sec_type":   "ALTER TABLE watchlist_items ADD COLUMN sec_type TEXT NOT NULL DEFAULT ''",
		"exchange":   "ALTER TABLE watchlist_items ADD COLUMN exchange TEXT NOT NULL DEFAULT ''",
		"currency":   "ALTER TABLE watchlist_items ADD COLUMN currency TEXT NOT NULL DEFAULT ''",
		"updated_at": "ALTER TABLE watchlist_items ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''",
	}
	for name, stmt := range add {
		if !columns[name] {
			if _, err := s.db.Exec(stmt); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) DB() *sql.DB {
	return s.db
}

// ListWatchlistGroups returns all watchlist groups ordered by sort_order.
func (s *Store) ListWatchlistGroups(ctx context.Context) ([]WatchlistGroup, error) {
	rows, err := s.queryContext(ctx, `
		SELECT id, name, sort_order FROM watchlist_groups ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WatchlistGroup{}
	for rows.Next() {
		var g WatchlistGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListWatchlistItems returns all items in a group ordered by sort_order.
func (s *Store) ListWatchlistItems(ctx context.Context, groupID int64) ([]WatchlistItem, error) {
	rows, err := s.queryContext(ctx, `
		SELECT id, group_id, symbol, conid, name, sec_type, exchange, currency, tags, notes, custom_fields, sort_order
		FROM watchlist_items
		WHERE group_id = ?
		ORDER BY sort_order, symbol`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []WatchlistItem{}
	for rows.Next() {
		var item WatchlistItem
		if err := rows.Scan(
			&item.ID,
			&item.GroupID,
			&item.Symbol,
			&item.ConID,
			&item.Name,
			&item.SecType,
			&item.Exchange,
			&item.Currency,
			&item.Tags,
			&item.Notes,
			&item.CustomFields,
			&item.SortOrder,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// UpsertWatchlistItem adds or refreshes a watchlist item in a group.
func (s *Store) UpsertWatchlistItem(ctx context.Context, item WatchlistItem) (WatchlistItem, error) {
	item.Symbol = strings.ToUpper(strings.TrimSpace(item.Symbol))
	if item.Symbol == "" {
		return WatchlistItem{}, fmt.Errorf("symbol is required")
	}
	if item.GroupID == 0 {
		item.GroupID = 1
	}
	_, err := s.execContext(ctx, `
		INSERT INTO watchlist_items (group_id, symbol, conid, name, sec_type, exchange, currency, tags, notes, custom_fields)
		VALUES (?, ?, ?, ?, ?, ?, ?, COALESCE(NULLIF(?, ''), '[]'), ?, COALESCE(NULLIF(?, ''), '{}'))
		ON CONFLICT(group_id, symbol) DO UPDATE SET
			conid = excluded.conid,
			name = excluded.name,
			sec_type = excluded.sec_type,
			exchange = excluded.exchange,
			currency = excluded.currency,
			updated_at = CURRENT_TIMESTAMP`,
		item.GroupID,
		item.Symbol,
		item.ConID,
		item.Name,
		item.SecType,
		item.Exchange,
		item.Currency,
		item.Tags,
		item.Notes,
		item.CustomFields,
	)
	if err != nil {
		return WatchlistItem{}, err
	}
	return s.GetWatchlistItem(ctx, item.GroupID, item.Symbol)
}

// GetWatchlistItem returns one item by group and symbol.
func (s *Store) GetWatchlistItem(ctx context.Context, groupID int64, symbol string) (WatchlistItem, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	var item WatchlistItem
	err := s.queryRowContext(ctx, `
		SELECT id, group_id, symbol, conid, name, sec_type, exchange, currency, tags, notes, custom_fields, sort_order
		FROM watchlist_items
		WHERE group_id = ? AND symbol = ?`, groupID, symbol).Scan(
		&item.ID,
		&item.GroupID,
		&item.Symbol,
		&item.ConID,
		&item.Name,
		&item.SecType,
		&item.Exchange,
		&item.Currency,
		&item.Tags,
		&item.Notes,
		&item.CustomFields,
		&item.SortOrder,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return WatchlistItem{}, ErrNotFound
	}
	return item, err
}

// DeleteWatchlistItem removes one item by group and symbol.
func (s *Store) DeleteWatchlistItem(ctx context.Context, groupID int64, symbol string) error {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	res, err := s.execContext(ctx, `
		DELETE FROM watchlist_items WHERE group_id = ? AND symbol = ?`, groupID, symbol)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

type WatchlistGroup struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
}

type WatchlistItem struct {
	ID           int64  `json:"id"`
	GroupID      int64  `json:"group_id"`
	Symbol       string `json:"symbol"`
	ConID        int64  `json:"conid"`
	Name         string `json:"name"`
	SecType      string `json:"sec_type"`
	Exchange     string `json:"exchange"`
	Currency     string `json:"currency"`
	Tags         string `json:"tags"`
	Notes        string `json:"notes"`
	CustomFields string `json:"custom_fields"`
	SortOrder    int    `json:"sort_order"`
}
