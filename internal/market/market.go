package market

import "time"

// Interval represents a candle timeframe.
type Interval string

const (
	Interval1m  Interval = "1m"
	Interval5m  Interval = "5m"
	Interval15m Interval = "15m"
	Interval1h  Interval = "1h"
	Interval1d  Interval = "1d"
	Interval1w  Interval = "1w"
)

// Bar is OHLCV candle data for charts and indicators.
type Bar struct {
	Symbol    string    `json:"symbol"`
	Interval  Interval  `json:"interval"`
	Open      float64   `json:"open"`
	High      float64   `json:"high"`
	Low       float64   `json:"low"`
	Close     float64   `json:"close"`
	Volume    int64     `json:"volume"`
	Timestamp time.Time `json:"timestamp"`
}

// Aggregator merges tick/stream data into bars (phase 3).
type Aggregator struct{}

func NewAggregator() *Aggregator {
	return &Aggregator{}
}
