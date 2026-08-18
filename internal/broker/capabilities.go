package broker

import "context"

// Broker is the minimum capability set required from a first-class broker
// integration. Optional trading and market-data capabilities remain separate.
type Broker interface {
	LoginProvider
	AccountProvider
	PositionProvider
	PerformanceProvider
}

// LoginProvider starts or resumes interactive broker authentication.
// The application layer is responsible for opening LoginAction.URL.
type LoginProvider interface {
	BeginLogin(ctx context.Context) (LoginAction, error)
}

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

// AccountSnapshotProvider is an optional bulk capability for brokers that can
// return all projection data in one upstream request. SyncService prefers this
// over issuing one request per capability and account, so every stored resource
// in a synchronization cycle is derived from the same broker snapshot.
type AccountSnapshotProvider interface {
	ListAccountSnapshots(ctx context.Context) ([]AccountSnapshot, error)
}

// AccountSnapshot contains the complete set of resources persisted by the
// periodic broker synchronization loop for one account.
type AccountSnapshot struct {
	Account          Account          `json:"account"`
	CashBalances     []CashBalance    `json:"cash_balances"`
	Positions        []Position       `json:"positions"`
	DailyPerformance DailyPerformance `json:"daily_performance"`
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
