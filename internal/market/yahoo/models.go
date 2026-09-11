package yahoo

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/market"
)

func parseQuotes(body []byte) ([]broker.Quote, error) {
	var raw struct {
		QuoteResponse *struct {
			Error  json.RawMessage `json:"error"`
			Result []struct {
				Symbol        string   `json:"symbol"`
				Price         *float64 `json:"regularMarketPrice"`
				Change        *float64 `json:"regularMarketChange"`
				ChangePct     *float64 `json:"regularMarketChangePercent"`
				PreviousClose *float64 `json:"regularMarketPreviousClose"`
				Bid           float64  `json:"bid"`
				Ask           float64  `json:"ask"`
				Volume        int64    `json:"regularMarketVolume"`
				High          float64  `json:"regularMarketDayHigh"`
				Low           float64  `json:"regularMarketDayLow"`
				High52        float64  `json:"fiftyTwoWeekHigh"`
				Low52         float64  `json:"fiftyTwoWeekLow"`
				Delay         *int     `json:"exchangeDataDelayedBy"`
				Currency      string   `json:"currency"`
				Time          int64    `json:"regularMarketTime"`
				State         string   `json:"marketState"`
			} `json:"result"`
		} `json:"quoteResponse"`
	}
	if err := decode(body, &raw); err != nil {
		return nil, err
	}
	if raw.QuoteResponse == nil || hasError(raw.QuoteResponse.Error) || raw.QuoteResponse.Result == nil {
		return nil, errors.New("yahoo: invalid quote response")
	}
	out := []broker.Quote{}
	for _, q := range raw.QuoteResponse.Result {
		if q.Price == nil || *q.Price <= 0 || strings.TrimSpace(q.Symbol) == "" {
			continue
		}
		change, pct := 0.0, 0.0
		if q.PreviousClose != nil && *q.PreviousClose > 0 {
			change = *q.Price - *q.PreviousClose
			pct = change / *q.PreviousClose * 100
		}
		if q.Change != nil {
			change = *q.Change
		}
		if q.ChangePct != nil {
			pct = *q.ChangePct
		}
		out = append(out, broker.Quote{Symbol: strings.ToUpper(q.Symbol), Last: *q.Price, Change: change, ChangePct: pct,
			Bid: q.Bid, Ask: q.Ask, Volume: q.Volume, High: q.High, Low: q.Low,
			Delayed: q.Delay == nil || *q.Delay > 0, Source: "yahoo", Currency: q.Currency, AsOf: q.Time,
			MarketState: q.State, Week52High: q.High52, Week52Low: q.Low52})
	}
	return out, nil
}

func hasError(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }

func parseChart(body []byte) ([]broker.Candle, error) {
	var raw struct {
		Chart *struct {
			Error  json.RawMessage `json:"error"`
			Result []struct {
				Timestamp  []*int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Open   []*float64 `json:"open"`
						High   []*float64 `json:"high"`
						Low    []*float64 `json:"low"`
						Close  []*float64 `json:"close"`
						Volume []*int64   `json:"volume"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
		} `json:"chart"`
	}
	if err := decode(body, &raw); err != nil {
		return nil, err
	}
	if raw.Chart == nil || hasError(raw.Chart.Error) {
		return nil, errors.New("yahoo: invalid chart response")
	}
	if len(raw.Chart.Result) == 0 {
		return nil, market.ErrNotFound
	}
	r := raw.Chart.Result[0]
	if len(r.Indicators.Quote) == 0 {
		return nil, errors.New("yahoo: chart indicators missing")
	}
	q := r.Indicators.Quote[0]
	out := []broker.Candle{}
	for i, ts := range r.Timestamp {
		// Missing OHLC values are gaps, not zero-price candles.
		if ts == nil || *ts <= 0 || !present(q.Open, i) || !present(q.High, i) || !present(q.Low, i) || !present(q.Close, i) {
			continue
		}
		if len(out) > 0 && *ts <= out[len(out)-1].Time {
			continue
		}
		volume := int64(0)
		if present(q.Volume, i) {
			volume = *q.Volume[i]
		}
		out = append(out, broker.Candle{Time: *ts, Open: *q.Open[i], High: *q.High[i], Low: *q.Low[i], Close: *q.Close[i], Volume: volume})
	}
	return out, nil
}

func present[T any](values []*T, i int) bool { return i < len(values) && values[i] != nil }
