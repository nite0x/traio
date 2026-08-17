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
	rows, err := s.queryContext(ctx, `
		SELECT x.account_id, COALESCE(ac.connection_id, 0), x.symbol, x.name, COALESCE(x.conid, 0), x.quantity, COALESCE(x.avg_cost, 0),
			COALESCE(x.market_price, 0), x.market_value,
			x.unrealized_pnl, x.realized_pnl, x.day_pnl, x.day_pnl_pct, x.currency,
			a.provider_account_id, a.provider_code, x.synced_at
		FROM broker_asset_positions x
		JOIN broker_accounts a ON a.id = x.account_id
		LEFT JOIN broker_account_connections ac ON ac.account_id = a.id AND ac.is_primary = 1
		WHERE x.asset_type <> 'cash'
		ORDER BY a.provider_code, a.provider_account_id, x.market_value DESC, x.symbol`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []broker.Position{}
	for rows.Next() {
		var position broker.Position
		var unrealized, realized, dailyPnL, dailyPnLPct sql.NullFloat64
		if err := rows.Scan(
			&position.BrokerAccountID, &position.ConnectionID,
			&position.Symbol, &position.Name, &position.ConID, &position.Quantity, &position.AvgCost,
			&position.MarketPrice, &position.MarketValue, &unrealized, &realized,
			&dailyPnL, &dailyPnLPct, &position.Currency, &position.Account, &position.Broker, &position.SyncedAt,
		); err != nil {
			return nil, err
		}
		if unrealized.Valid {
			position.Unrealized = unrealized.Float64
		}
		if realized.Valid {
			position.Realized = realized.Float64
		}
		if dailyPnL.Valid {
			value := dailyPnL.Float64
			position.DailyPnL = &value
		}
		if dailyPnLPct.Valid {
			value := dailyPnLPct.Float64
			position.DailyPnLPct = &value
		}
		out = append(out, position)
	}
	return out, rows.Err()
}
