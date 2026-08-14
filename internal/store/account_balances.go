package store

import (
	"context"
)

// BrokerAccountBalance is the latest base-currency balance projection for one account.
type BrokerAccountBalance struct {
	AccountID          int64   `json:"account_id"`
	ConnectionID       int64   `json:"connection_id"`
	Broker             string  `json:"broker"`
	Account            string  `json:"account"`
	Currency           string  `json:"currency"`
	NetLiquidation     float64 `json:"net_liquidation"`
	TotalCashValue     float64 `json:"total_cash_value"`
	GrossPositionValue float64 `json:"gross_position_value"`
	BuyingPower        float64 `json:"buying_power"`
	UnrealizedPnL      float64 `json:"unrealized_pnl"`
	RealizedPnL        float64 `json:"realized_pnl"`
	SettledCash        float64 `json:"settled_cash"`
	ExchangeRate       float64 `json:"exchange_rate"`
	IsBaseCurrency     bool    `json:"is_base_currency"`
	SyncedAt           string  `json:"synced_at"`
}

func (s *Store) ListBrokerAccountBalances(ctx context.Context) ([]BrokerAccountBalance, error) {
	rows, err := s.queryContext(ctx, `
		SELECT b.account_id, COALESCE(ac.connection_id, 0), p.code, a.provider_account_id,
			b.currency, b.net_liquidation, b.total_cash_value,
			b.gross_position_value, b.buying_power, b.unrealized_pnl, b.realized_pnl,
			b.settled_cash, b.exchange_rate, b.is_base_currency, b.synced_at
		FROM broker_account_balances b
		JOIN broker_accounts a ON a.id = b.account_id
		JOIN broker_providers p ON p.id = a.provider_id
		LEFT JOIN broker_account_connections ac ON ac.account_id = a.id AND ac.is_primary = 1
		ORDER BY p.code, a.provider_account_id, b.currency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BrokerAccountBalance{}
	for rows.Next() {
		var balance BrokerAccountBalance
		if err := rows.Scan(
			&balance.AccountID, &balance.ConnectionID, &balance.Broker,
			&balance.Account, &balance.Currency,
			&balance.NetLiquidation, &balance.TotalCashValue, &balance.GrossPositionValue,
			&balance.BuyingPower, &balance.UnrealizedPnL, &balance.RealizedPnL,
			&balance.SettledCash, &balance.ExchangeRate, &balance.IsBaseCurrency,
			&balance.SyncedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, balance)
	}
	return out, rows.Err()
}
