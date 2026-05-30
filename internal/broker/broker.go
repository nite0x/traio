package broker

import "context"

// Quote is a normalized real-time quote across brokers.
type Quote struct {
	ConID     int64   `json:"conid,omitempty"`
	Symbol    string  `json:"symbol"`
	Last      float64 `json:"last"`
	Bid       float64 `json:"bid"`
	Ask       float64 `json:"ask"`
	Change    float64 `json:"change"`
	ChangePct float64 `json:"change_pct"`
	Volume    int64   `json:"volume"`
}

// Position is a normalized holding.
type Position struct {
	Symbol      string  `json:"symbol"`
	Quantity    float64 `json:"quantity"`
	AvgCost     float64 `json:"avg_cost"`
	MarketValue float64 `json:"market_value"`
	Unrealized  float64 `json:"unrealized_pnl"`
	Broker      string  `json:"broker"`
}

// AccountSummary is a normalized real-time account snapshot.
type AccountSummary struct {
	AccountID          string  `json:"account_id"`
	Currency           string  `json:"currency"`
	NetLiquidation     float64 `json:"net_liquidation"`
	TotalCashValue     float64 `json:"total_cash_value"`
	GrossPositionValue float64 `json:"gross_position_value"`
	UnrealizedPnL      float64 `json:"unrealized_pnl"`
	RealizedPnL        float64 `json:"realized_pnl"`
	BuyingPower        float64 `json:"buying_power"`
	Broker             string  `json:"broker"`
	AsOf               string  `json:"as_of"`
}

// AccountEquityPoint is one point in the account equity curve.
type AccountEquityPoint struct {
	Time     string  `json:"time"`
	Value    float64 `json:"value"`
	Currency string  `json:"currency,omitempty"`
	Source   string  `json:"source"`
}

// Instrument is a normalized contract/search result across brokers.
type Instrument struct {
	ConID    int64  `json:"conid"`
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	SecType  string `json:"sec_type"`
	Exchange string `json:"exchange"`
	Currency string `json:"currency"`
}

// OrderRequest is a normalized order payload.
type OrderRequest struct {
	Symbol      string  `json:"symbol"`
	Side        string  `json:"side"`       // buy | sell
	OrderType   string  `json:"order_type"` // market | limit | stop
	Quantity    float64 `json:"quantity"`
	LimitPrice  float64 `json:"limit_price,omitempty"`
	StopPrice   float64 `json:"stop_price,omitempty"`
	TimeInForce string  `json:"time_in_force"` // day | gtc
}

// MarketDataProvider streams quotes and historical bars.
type MarketDataProvider interface {
	GetQuote(ctx context.Context, symbol string) (*Quote, error)
}

// BatchMarketDataProvider returns market data for multiple contracts.
type BatchMarketDataProvider interface {
	GetQuotesByConID(ctx context.Context, conIDs []int64) ([]Quote, error)
}

// InstrumentProvider searches tradable instruments/contracts.
type InstrumentProvider interface {
	SearchInstruments(ctx context.Context, query string) ([]Instrument, error)
}

// PortfolioProvider returns positions and submits orders.
type PortfolioProvider interface {
	ListPositions(ctx context.Context) ([]Position, error)
	PlaceOrder(ctx context.Context, req OrderRequest) (string, error)
}

// AccountProvider returns historical and real-time account equity data.
type AccountProvider interface {
	AccountSummary(ctx context.Context) (AccountSummary, error)
	HistoricalEquity(ctx context.Context) ([]AccountEquityPoint, error)
}
