package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/nite/traio/internal/broker"
)

// ReplaceBrokerPositions atomically replaces one broker's position projection.
// Failed broker syncs must not call this method, so previously synced data stays readable.
func (s *Store) ReplaceBrokerPositions(ctx context.Context, brokerName string, positions []broker.Position) error {
	brokerName = strings.ToUpper(strings.TrimSpace(brokerName))
	if brokerName == "" {
		return fmt.Errorf("broker is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM broker_asset_positions WHERE broker = ?`, brokerName); err != nil {
		return err
	}

	syncedAt := time.Now().UTC().Format(time.RFC3339)
	accounts := map[string]string{}
	for _, position := range positions {
		account := strings.TrimSpace(position.Account)
		currency := strings.ToUpper(strings.TrimSpace(position.Currency))
		if accounts[account] == "" {
			accounts[account] = currency
		}
	}
	for account, currency := range accounts {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO broker_accounts (broker, account, currency, synced_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(broker, account) DO UPDATE SET
				currency = CASE WHEN excluded.currency = '' THEN broker_accounts.currency ELSE excluded.currency END,
				synced_at = excluded.synced_at`,
			brokerName, account, currency, syncedAt,
		); err != nil {
			return err
		}
	}

	for _, position := range positions {
		symbol := strings.ToUpper(strings.TrimSpace(position.Symbol))
		if symbol == "" || position.Quantity == 0 {
			continue
		}
		account := strings.TrimSpace(position.Account)
		marketPrice := position.MarketPrice
		if marketPrice == 0 && position.Quantity != 0 {
			marketPrice = position.MarketValue / position.Quantity
		}
		assetType, assetKey := positionAssetIdentity(position)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO broker_asset_positions (
				broker, account, asset_type, asset_key, symbol, conid, quantity,
				avg_cost, market_price, market_value, unrealized_pnl, realized_pnl,
				currency, synced_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			brokerName, account, assetType, assetKey, symbol, nullableConID(position.ConID),
			position.Quantity, nullableFloat(position.AvgCost), nullableFloat(marketPrice),
			position.MarketValue, nullableFloat(position.Unrealized), nullableFloat(position.Realized),
			strings.ToUpper(strings.TrimSpace(position.Currency)), syncedAt,
		); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO broker_position_syncs (broker, synced_at, last_attempt_at, last_error)
		VALUES (?, ?, ?, '')
		ON CONFLICT(broker) DO UPDATE SET
			synced_at = excluded.synced_at,
			last_attempt_at = excluded.last_attempt_at,
			last_error = ''`,
		brokerName, syncedAt, syncedAt,
	); err != nil {
		return err
	}
	return tx.Commit()
}

type BrokerAssetPosition struct {
	Broker        string   `json:"broker"`
	Account       string   `json:"account"`
	AssetType     string   `json:"asset_type"`
	AssetKey      string   `json:"asset_key"`
	Symbol        string   `json:"symbol"`
	Name          string   `json:"name,omitempty"`
	ConID         *int64   `json:"conid,omitempty"`
	Currency      string   `json:"currency"`
	Quantity      float64  `json:"quantity"`
	AvgCost       *float64 `json:"avg_cost,omitempty"`
	MarketPrice   *float64 `json:"market_price,omitempty"`
	MarketValue   float64  `json:"market_value"`
	UnrealizedPnL *float64 `json:"unrealized_pnl,omitempty"`
	RealizedPnL   *float64 `json:"realized_pnl,omitempty"`
	CostBasis     *float64 `json:"cost_basis,omitempty"`
	DayPnL        *float64 `json:"day_pnl,omitempty"`
	DayPnLPct     *float64 `json:"day_pnl_pct,omitempty"`
	RawPayload    string   `json:"raw_payload,omitempty"`
	SyncedAt      string   `json:"synced_at"`
}

func positionAssetIdentity(position broker.Position) (string, string) {
	symbol := strings.ToUpper(strings.TrimSpace(position.Symbol))
	currency := strings.ToUpper(strings.TrimSpace(position.Currency))
	if symbol != "" {
		if position.ConID != 0 {
			return "security", fmt.Sprintf("security:conid:%d", position.ConID)
		}
		return "security", "security:symbol:" + symbol
	}
	if currency != "" {
		return "cash", "cash:" + currency
	}
	return "unknown", fmt.Sprintf("unknown:%d", position.ConID)
}

func nullableConID(conID int64) any {
	if conID == 0 {
		return nil
	}
	return conID
}

func nullableFloat(value float64) any {
	if value == 0 {
		return nil
	}
	return value
}

type BrokerPositionSync struct {
	Broker        string `json:"broker"`
	SyncedAt      string `json:"synced_at"`
	LastAttemptAt string `json:"last_attempt_at"`
	LastError     string `json:"last_error,omitempty"`
}

func (s *Store) ListBrokerPositionSyncs(ctx context.Context) ([]BrokerPositionSync, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT broker, synced_at, last_attempt_at, last_error
		FROM broker_position_syncs
		ORDER BY broker`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BrokerPositionSync{}
	for rows.Next() {
		var status BrokerPositionSync
		if err := rows.Scan(&status.Broker, &status.SyncedAt, &status.LastAttemptAt, &status.LastError); err != nil {
			return nil, err
		}
		out = append(out, status)
	}
	return out, rows.Err()
}

func (s *Store) RecordBrokerPositionSyncError(ctx context.Context, brokerName string, syncErr error) error {
	brokerName = strings.ToUpper(strings.TrimSpace(brokerName))
	if brokerName == "" {
		return fmt.Errorf("broker is required")
	}
	attemptedAt := time.Now().UTC().Format(time.RFC3339)
	message := ""
	if syncErr != nil {
		message = syncErr.Error()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO broker_position_syncs (broker, synced_at, last_attempt_at, last_error)
		VALUES (?, '', ?, ?)
		ON CONFLICT(broker) DO UPDATE SET
			last_attempt_at = excluded.last_attempt_at,
			last_error = excluded.last_error`,
		brokerName, attemptedAt, message,
	)
	return err
}

func (s *Store) ListBrokerPositions(ctx context.Context) ([]broker.Position, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol, COALESCE(conid, 0), quantity, COALESCE(avg_cost, 0),
			COALESCE(market_price, 0), market_value,
			unrealized_pnl, realized_pnl, currency, account, broker, synced_at
		FROM broker_asset_positions
		WHERE asset_type <> 'cash'
		ORDER BY broker, account, market_value DESC, symbol`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []broker.Position{}
	for rows.Next() {
		var position broker.Position
		var unrealized, realized sql.NullFloat64
		if err := rows.Scan(
			&position.Symbol, &position.ConID, &position.Quantity, &position.AvgCost,
			&position.MarketPrice, &position.MarketValue, &unrealized, &realized,
			&position.Currency, &position.Account, &position.Broker, &position.SyncedAt,
		); err != nil {
			return nil, err
		}
		if unrealized.Valid {
			position.Unrealized = unrealized.Float64
		}
		if realized.Valid {
			position.Realized = realized.Float64
		}
		out = append(out, position)
	}
	return out, rows.Err()
}
