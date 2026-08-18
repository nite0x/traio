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
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Delayed   bool    `json:"delayed,omitempty"`
}

// Position is a normalized holding.
type Position struct {
	BrokerAccountID int64    `json:"broker_account_id,omitempty"`
	ConnectionID    int64    `json:"connection_id,omitempty"`
	InstrumentID    int64    `json:"instrument_id,omitempty"`
	ExternalID      string   `json:"external_id,omitempty"`
	AssetType       string   `json:"asset_type,omitempty"`
	Market          string   `json:"market,omitempty"`
	Exchange        string   `json:"exchange,omitempty"`
	Symbol          string   `json:"symbol"`
	Name            string   `json:"name,omitempty"`
	ConID           int64    `json:"conid"`
	Quantity        float64  `json:"quantity"`
	AvgCost         float64  `json:"avg_cost"`
	MarketPrice     float64  `json:"market_price"`
	MarketValue     float64  `json:"market_value"`
	Unrealized      float64  `json:"unrealized_pnl"`
	Realized        float64  `json:"realized_pnl"`
	DailyPnL        *float64 `json:"daily_pnl,omitempty"`
	DailyPnLPct     *float64 `json:"daily_pnl_pct,omitempty"`
	Currency        string   `json:"currency"`
	Account         string   `json:"account"`
	Broker          string   `json:"broker"`
	SyncedAt        string   `json:"synced_at,omitempty"`
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
	AccountID      string  `json:"account_id"`
	Symbol         string  `json:"symbol"`
	InstrumentID   string  `json:"instrument_id,omitempty"`   // IBKR conid or broker asset id
	AssetClass     string  `json:"asset_class,omitempty"`     // equity | option | future | forex | crypto | fund | bond
	Side           string  `json:"side"`                      // buy | sell
	PositionEffect string  `json:"position_effect,omitempty"` // open | close (primarily options)
	OrderType      string  `json:"order_type"`                // market | limit | stop | stop_limit | trailing_stop
	Quantity       float64 `json:"quantity,omitempty"`
	Notional       float64 `json:"notional,omitempty"`
	LimitPrice     float64 `json:"limit_price,omitempty"`
	StopPrice      float64 `json:"stop_price,omitempty"`
	TrailPrice     float64 `json:"trail_price,omitempty"`
	TrailPercent   float64 `json:"trail_percent,omitempty"`
	TimeInForce    string  `json:"time_in_force"` // day | gtc | ioc | fok | opg | cls
	ExtendedHours  bool    `json:"extended_hours,omitempty"`
	ClientOrderID  string  `json:"client_order_id,omitempty"`
}

// Order is the normalized lifecycle view returned by every trading adapter.
type Order struct {
	ID               string  `json:"id"`
	ClientOrderID    string  `json:"client_order_id,omitempty"`
	AccountID        string  `json:"account_id"`
	Symbol           string  `json:"symbol,omitempty"`
	InstrumentID     string  `json:"instrument_id,omitempty"`
	AssetClass       string  `json:"asset_class,omitempty"`
	Side             string  `json:"side,omitempty"`
	OrderType        string  `json:"order_type,omitempty"`
	Quantity         float64 `json:"quantity,omitempty"`
	FilledQuantity   float64 `json:"filled_quantity,omitempty"`
	LimitPrice       float64 `json:"limit_price,omitempty"`
	StopPrice        float64 `json:"stop_price,omitempty"`
	AverageFillPrice float64 `json:"average_fill_price,omitempty"`
	TimeInForce      string  `json:"time_in_force,omitempty"`
	Status           string  `json:"status"`
	SubmittedAt      string  `json:"submitted_at,omitempty"`
	UpdatedAt        string  `json:"updated_at,omitempty"`
	RawStatus        string  `json:"raw_status,omitempty"`
}

// OrderQuery controls an order listing without leaking provider-specific query shapes.
type OrderQuery struct {
	AccountID string `json:"account_id"`
	Status    string `json:"status,omitempty"` // open | closed | all
	Limit     int    `json:"limit,omitempty"`
}

// TradingProvider is the single server-side contract implemented by all brokers.
type TradingProvider interface {
	PlaceOrder(context.Context, OrderRequest) (Order, error)
	GetOrder(context.Context, string, string) (Order, error)
	ListOrders(context.Context, OrderQuery) ([]Order, error)
	CancelOrder(context.Context, string, string) error
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

// AccountEquityProvider returns historical and real-time account equity data.
// It is kept separate from AccountProvider because timeline data is not part of
// the minimum IBKR account-access contract.
type AccountEquityProvider interface {
	AccountSummary(ctx context.Context) (AccountSummary, error)
	HistoricalEquity(ctx context.Context) ([]AccountEquityPoint, error)
}

// Candle is one OHLCV bar.
type Candle struct {
	Time   int64   `json:"time"` // Unix seconds (UTC)
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume int64   `json:"volume"`
}

// CandleProvider fetches historical OHLCV bars for a contract.
// period: "1d" "5d" "1m" "3m" "6m" "1y" "2y" "5y"
// bar:    "1min" "5min" "15min" "30min" "1h" "1d" "1w"
type CandleProvider interface {
	GetCandles(ctx context.Context, conID int64, period, bar string) ([]Candle, error)
}
