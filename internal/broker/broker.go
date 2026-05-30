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
