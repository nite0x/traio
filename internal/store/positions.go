package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/nite/traio/internal/broker"
)

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
