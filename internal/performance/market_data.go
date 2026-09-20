package performance

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/nite/traio/internal/market"
)

// MarketValuationProvider uses the product-level market-data policy instead
// of a broker-specific CandleProvider. Historical security prices therefore
// use Tiingo first and Yahoo only as fallback. The UTC current day may use the
// exact account-owning broker connection before the public providers.
type MarketValuationProvider struct {
	market market.PolicyProvider
}

func NewMarketValuationProvider(provider market.PolicyProvider) *MarketValuationProvider {
	return &MarketValuationProvider{market: provider}
}

func (p *MarketValuationProvider) DailyPrices(ctx context.Context, instruments []Instrument, from, to time.Time) (map[int64][]Price, error) {
	out := map[int64][]Price{}
	if p == nil || p.market == nil {
		return out, nil
	}
	period := periodFor(from, to)
	today := time.Now().UTC().Format(time.DateOnly)
	for _, instrument := range instruments {
		symbol := strings.TrimSpace(instrument.Symbol)
		if symbol == "" {
			continue
		}
		history, err := p.market.GetHistoryFor(ctx, symbol, period, "1d", false, market.Request{Usage: market.UsagePortfolioValuation, Source: market.SourceAuto})
		if err == nil {
			for _, row := range history.Candles {
				date := time.Unix(row.Time, 0).UTC()
				if date.Format(time.DateOnly) >= today || date.Before(from) || date.After(to.AddDate(0, 0, 1)) || row.Close <= 0 {
					continue
				}
				out[instrument.ID] = append(out[instrument.ID], Price{Date: date.Format(time.DateOnly), Value: strconv.FormatFloat(row.Close, 'f', -1, 64), Source: history.Source})
			}
		}
		if to.Format(time.DateOnly) >= today {
			quote, quoteErr := p.market.GetQuoteFor(ctx, symbol, market.Request{Usage: market.UsagePortfolioValuation, Source: market.SourceAuto, ConnectionID: instrument.ConnectionID})
			if quoteErr == nil && quote != nil && quote.Last > 0 {
				out[instrument.ID] = upsertPrice(out[instrument.ID], Price{Date: today, Value: strconv.FormatFloat(quote.Last, 'f', -1, 64), Source: quote.Source})
			} else {
				// An explicit missing point prevents yesterday's close or a
				// Tiingo daily candle from masquerading as today's quote.
				out[instrument.ID] = upsertPrice(out[instrument.ID], Price{Date: today, Missing: true})
			}
		}
	}
	return out, nil
}

func (p *MarketValuationProvider) DailyFX(ctx context.Context, pairs []FXPair, from, to time.Time) (map[FXPair][]FXRate, error) {
	out := map[FXPair][]FXRate{}
	if p == nil || p.market == nil {
		return out, nil
	}
	period := periodFor(from, to)
	for _, pair := range pairs {
		// The current Tiingo adapter covers equities but not Tiingo FX. Yahoo's
		// canonical FX symbols provide an explicit historical adapter; failures
		// remain missing_fx rather than being replaced with a fabricated rate.
		symbol := strings.ToUpper(pair.From) + strings.ToUpper(pair.To) + "=X"
		history, err := p.market.GetHistoryFor(ctx, symbol, period, "1d", false, market.Request{Usage: market.UsagePortfolioValuation, Source: market.SourceYahoo})
		if err != nil {
			continue
		}
		for _, row := range history.Candles {
			date := time.Unix(row.Time, 0).UTC()
			if date.Before(from) || date.After(to.AddDate(0, 0, 1)) || row.Close <= 0 {
				continue
			}
			out[pair] = append(out[pair], FXRate{Date: date.Format(time.DateOnly), From: pair.From, To: pair.To, Value: strconv.FormatFloat(row.Close, 'f', -1, 64), Source: history.Source})
		}
	}
	return out, nil
}

func upsertPrice(rows []Price, value Price) []Price {
	for i := range rows {
		if rows[i].Date == value.Date {
			rows[i] = value
			return rows
		}
	}
	return append(rows, value)
}

func periodFor(from, to time.Time) string {
	days := int(to.Sub(from).Hours()/24) + 1
	switch {
	case days <= 31:
		return "1m"
	case days <= 93:
		return "3m"
	case days <= 186:
		return "6m"
	case days <= 366:
		return "1y"
	case days <= 730:
		return "2y"
	case days <= 1826:
		return "5y"
	default:
		return "max"
	}
}
