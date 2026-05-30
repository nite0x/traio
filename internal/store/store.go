package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS watchlist_groups (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	sort_order INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS watchlist_items (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	group_id INTEGER NOT NULL REFERENCES watchlist_groups(id) ON DELETE CASCADE,
	symbol TEXT NOT NULL,
	tags TEXT NOT NULL DEFAULT '[]',
	notes TEXT NOT NULL DEFAULT '',
	custom_fields TEXT NOT NULL DEFAULT '{}',
	sort_order INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	UNIQUE(group_id, symbol)
);

CREATE TABLE IF NOT EXISTS oauth_tokens (
	provider TEXT PRIMARY KEY,
	access_token TEXT NOT NULL,
	refresh_token TEXT,
	expires_at TEXT,
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS app_settings (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	data TEXT NOT NULL,
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	if err := s.ensureWatchlistItemColumns(); err != nil {
		return err
	}
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO watchlist_groups (id, name, sort_order) VALUES (1, '默认', 0);
	`)
	return err
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
	rows, err := s.db.QueryContext(ctx, `
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
	rows, err := s.db.QueryContext(ctx, `
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
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO watchlist_items (group_id, symbol, conid, name, sec_type, exchange, currency, tags, notes, custom_fields)
		VALUES (?, ?, ?, ?, ?, ?, ?, COALESCE(NULLIF(?, ''), '[]'), ?, COALESCE(NULLIF(?, ''), '{}'))
		ON CONFLICT(group_id, symbol) DO UPDATE SET
			conid = excluded.conid,
			name = excluded.name,
			sec_type = excluded.sec_type,
			exchange = excluded.exchange,
			currency = excluded.currency,
			updated_at = datetime('now')`,
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
	err := s.db.QueryRowContext(ctx, `
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
	return item, err
}

// DeleteWatchlistItem removes one item by group and symbol.
func (s *Store) DeleteWatchlistItem(ctx context.Context, groupID int64, symbol string) error {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM watchlist_items WHERE group_id = ? AND symbol = ?`, groupID, symbol)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
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
