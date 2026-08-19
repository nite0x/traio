package broker

import "context"

// AccountProvider exposes account discovery, metadata, and currency balances.
type AccountProvider interface {
	ListAccounts(ctx context.Context) ([]Account, error)
	GetAccount(ctx context.Context, accountID string) (Account, error)
	GetCashBalances(ctx context.Context, accountID string) ([]CashBalance, error)
}

// PositionProvider returns positions for one explicit account.
type PositionProvider interface {
	ListAccountPositions(ctx context.Context, accountID string) ([]Position, error)
}

// PerformanceProvider returns the current trading day's account performance.
type PerformanceProvider interface {
	GetDailyPerformance(ctx context.Context, accountID string) (DailyPerformance, error)
}

// AccountSnapshot models the complete set of resources persisted by portfolio
// synchronization. Consumers should call Resolve before reading its resources;
// native bulk snapshots resolve without additional work.
type AccountSnapshot struct {
	Account          Account          `json:"account"`
	CashBalances     []CashBalance    `json:"cash_balances"`
	Positions        []Position       `json:"positions"`
	DailyPerformance DailyPerformance `json:"daily_performance"`

	resolve accountSnapshotResolver
}

// LoginAction describes the UI action needed to finish broker authentication.
type LoginAction struct {
	URL           string `json:"url,omitempty"`
	Authenticated bool   `json:"authenticated"`
	AccountID     string `json:"account_id,omitempty"`
}

// Account is normalized broker account metadata.
type Account struct {
	ID           string `json:"id"`
	Broker       string `json:"broker"`
	DisplayName  string `json:"display_name"`
	AccountType  string `json:"account_type"`
	Status       string `json:"status"`
	BaseCurrency string `json:"base_currency"`
}

// CashBalance is one account cash balance in a single currency.
// IBKR may also return a synthetic BASE currency entry.
type CashBalance struct {
	AccountID      string  `json:"account_id"`
	Currency       string  `json:"currency"`
	Total          float64 `json:"total"`
	Settled        float64 `json:"settled"`
	ExchangeRate   float64 `json:"exchange_rate"`
	IsBaseCurrency bool    `json:"is_base_currency"`
	AsOf           string  `json:"as_of"`
}

// DailyPerformance is the current day's account-level P&L snapshot.
type DailyPerformance struct {
	AccountID       string  `json:"account_id"`
	DailyPnL        float64 `json:"daily_pnl"`
	NetLiquidation  float64 `json:"net_liquidation"`
	UnrealizedPnL   float64 `json:"unrealized_pnl"`
	ExcessLiquidity float64 `json:"excess_liquidity"`
	MarketValue     float64 `json:"market_value"`
	AsOf            string  `json:"as_of"`
}
