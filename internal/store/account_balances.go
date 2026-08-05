package store

import (
	"context"
)

// BrokerAccountBalance is the latest base-currency balance projection for one account.
type BrokerAccountBalance struct {
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT broker, account, currency, net_liquidation, total_cash_value,
			gross_position_value, buying_power, unrealized_pnl, realized_pnl,
			settled_cash, exchange_rate, is_base_currency, synced_at
		FROM broker_account_balances
		ORDER BY broker, account, currency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BrokerAccountBalance{}
	for rows.Next() {
		var balance BrokerAccountBalance
		if err := rows.Scan(
			&balance.Broker, &balance.Account, &balance.Currency,
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
