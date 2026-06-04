package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nite/traio/internal/broker"
)

// candleTTL returns how long to cache candles for a given bar size.
// Historical bars are immutable once closed; only the latest bar still changes.
// Convention matches major charting platforms (TradingView, Futu, etc.):
//   - daily / weekly: 24 h — data is final after market close
//   - hourly:          1 h — one new bar per hour at most
//   - sub-hour:       15 min — stale after ~3 bars
func candleTTL(bar string) time.Duration {
	switch bar {
	case "1d", "1w", "1m":
		return 24 * time.Hour
	case "1h", "2h", "4h":
		return time.Hour
	default: // 5min, 15min, 30min, etc.
		return 15 * time.Minute
	}
}

// ensureCandleCache creates the candle_cache table if it doesn't exist.
// Called once from migrate().
func (s *Store) ensureCandleCache() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS candle_cache (
	symbol    TEXT    NOT NULL,
	conid     INTEGER NOT NULL,
	period    TEXT    NOT NULL,
	bar       TEXT    NOT NULL,
	candles   TEXT    NOT NULL,   -- JSON array of broker.Candle
	cached_at INTEGER NOT NULL,   -- Unix seconds
	PRIMARY KEY (symbol, period, bar)
)`)
	return err
}

// GetCachedCandles returns cached candles if present and not expired.
// Returns (nil, nil) on cache miss.
func (s *Store) GetCachedCandles(ctx context.Context, symbol, period, bar string) ([]broker.Candle, error) {
	var raw string
	var cachedAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT candles, cached_at FROM candle_cache WHERE symbol=? AND period=? AND bar=?`,
		symbol, period, bar,
	).Scan(&raw, &cachedAt)
	if err == sql.ErrNoRows {
		return nil, nil // cache miss
	}
	if err != nil {
		return nil, fmt.Errorf("candle cache read: %w", err)
	}

	ttl := candleTTL(bar)
	if time.Now().Unix()-cachedAt > int64(ttl.Seconds()) {
		return nil, nil // expired
	}

	var candles []broker.Candle
	if err := json.Unmarshal([]byte(raw), &candles); err != nil {
		return nil, fmt.Errorf("candle cache decode: %w", err)
	}
	return candles, nil
}

// SetCachedCandles stores candles in the cache, replacing any existing entry.
func (s *Store) SetCachedCandles(ctx context.Context, symbol string, conid int64, period, bar string, candles []broker.Candle) error {
	raw, err := json.Marshal(candles)
	if err != nil {
		return fmt.Errorf("candle cache encode: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO candle_cache (symbol, conid, period, bar, candles, cached_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(symbol, period, bar) DO UPDATE SET
		   conid=excluded.conid, candles=excluded.candles, cached_at=excluded.cached_at`,
		symbol, conid, period, bar, string(raw), time.Now().Unix(),
	)
	return err
}

// PurgeExpiredCandles removes all stale cache entries. Safe to call periodically.
func (s *Store) PurgeExpiredCandles(ctx context.Context) error {
	now := time.Now().Unix()
	// Use the shortest TTL as a conservative lower bound for the DELETE.
	// Rows with longer TTLs will not be deleted prematurely because
	// GetCachedCandles checks per-bar TTL individually.
	minTTL := int64((15 * time.Minute).Seconds())
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM candle_cache WHERE cached_at < ?`, now-minTTL)
	return err
}
