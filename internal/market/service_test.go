package market

import (
	"context"
	"errors"
	"testing"

	"github.com/nite/traio/internal/broker"
)

type policySymbolStub struct {
	quote   broker.Quote
	candles []broker.Candle
	err     error
	calls   int
}

func (s *policySymbolStub) GetQuote(context.Context, string) (*broker.Quote, error) {
	s.calls++
	quote := s.quote
	return &quote, s.err
}
func (s *policySymbolStub) GetQuotes(context.Context, []string) ([]broker.Quote, error) {
	s.calls++
	return []broker.Quote{s.quote}, s.err
}
func (s *policySymbolStub) GetHistory(context.Context, string, string, string, bool) ([]broker.Candle, error) {
	s.calls++
	return s.candles, s.err
}

type policyBrokerStub struct {
	codes        map[int64]string
	directQuotes map[int64]broker.Quote
	requested    []int64
}

func (s *policyBrokerStub) ProviderCode(id int64) (string, bool) {
	value, ok := s.codes[id]
	return value, ok
}
func (s *policyBrokerStub) GetQuoteForConnection(_ context.Context, id int64, _ string) (*broker.Quote, error) {
	s.requested = append(s.requested, id)
	if quote, ok := s.directQuotes[id]; ok {
		return &quote, nil
	}
	return nil, &broker.CapabilityUnavailableError{ConnectionID: id, Capability: broker.MarketDataCapabilityQuotes}
}
func (s *policyBrokerStub) SearchInstrumentsForConnection(_ context.Context, id int64, symbol string) ([]broker.Instrument, error) {
	s.requested = append(s.requested, id)
	return []broker.Instrument{{ConID: id * 10, Symbol: symbol, Currency: "USD"}}, nil
}
func (s *policyBrokerStub) GetQuotesByConIDForConnection(_ context.Context, id int64, _ []int64) ([]broker.Quote, error) {
	s.requested = append(s.requested, id)
	return []broker.Quote{{Symbol: "AAPL", Last: float64(id)}}, nil
}

func TestQuotePolicyUsesExactOwningBrokerConnection(t *testing.T) {
	brokerRouter := &policyBrokerStub{
		codes: map[int64]string{11: "IBKR", 22: "SCHWAB", 33: "ALPACA"},
		directQuotes: map[int64]broker.Quote{
			22: {Symbol: "AAPL", Last: 220},
			33: {Symbol: "AAPL", Last: 330},
		},
	}
	tiingo := &policySymbolStub{quote: broker.Quote{Symbol: "AAPL", Last: 999}}
	service := NewService(brokerRouter, tiingo, nil)
	for _, test := range []struct {
		id     int64
		source string
		last   float64
	}{{11, "ibkr", 11}, {22, "schwab", 220}, {33, "alpaca", 330}} {
		quote, err := service.GetQuoteFor(t.Context(), "AAPL", Request{Usage: UsagePortfolioValuation, ConnectionID: test.id})
		if err != nil {
			t.Fatalf("connection %d: %v", test.id, err)
		}
		if quote.Source != test.source || quote.Status != StatusOfficial || quote.Last != test.last {
			t.Fatalf("connection %d quote=%+v", test.id, quote)
		}
	}
	if tiingo.calls != 0 {
		t.Fatalf("broker success unexpectedly fell back to Tiingo: %d", tiingo.calls)
	}
	for _, id := range brokerRouter.requested {
		if id != 11 && id != 22 && id != 33 {
			t.Fatalf("unexpected connection selected: %d", id)
		}
	}
}

func TestHistoryPolicyPrefersTiingoAndMarksYahooFallbackProvisional(t *testing.T) {
	tiingo := &policySymbolStub{candles: []broker.Candle{{Time: 200, Close: 20}}}
	yahoo := &policySymbolStub{candles: []broker.Candle{{Time: 100, Close: 10}}}
	service := NewService(nil, tiingo, yahoo)

	history, err := service.GetHistoryFor(t.Context(), "AAPL", "1y", "1d", false, Request{Usage: UsageHistoricalChart})
	if err != nil {
		t.Fatal(err)
	}
	if history.Source != "tiingo" || history.Status != StatusOfficial || history.AdjustmentType != AdjustmentSplitDividendAdjusted || yahoo.calls != 0 {
		t.Fatalf("primary history=%+v yahoo_calls=%d", history, yahoo.calls)
	}
	if history.Attribution != "Data sourced by Tiingo" {
		t.Fatalf("Tiingo attribution=%q", history.Attribution)
	}
	valuation, err := service.GetHistoryFor(t.Context(), "AAPL", "1y", "1d", false, Request{Usage: UsagePortfolioValuation})
	if err != nil || valuation.AdjustmentType != AdjustmentUnadjusted {
		t.Fatalf("valuation history=%+v err=%v", valuation, err)
	}

	tiingo.err = NewProviderError(ErrorRateLimited, "tiingo", "history", "rate limited", nil)
	history, err = service.GetHistoryFor(t.Context(), "AAPL", "1y", "1d", false, Request{Usage: UsageHistoricalChart})
	if err != nil {
		t.Fatal(err)
	}
	if history.Source != "yahoo" || history.Status != StatusProvisional || history.AdjustmentType != AdjustmentUnadjusted {
		t.Fatalf("fallback history=%+v", history)
	}
	if history.Attribution != "Data sourced by Yahoo Finance" {
		t.Fatalf("Yahoo attribution=%q", history.Attribution)
	}
}

func TestQuotePolicyFallsBackFromOwningBrokerDirectlyToYahoo(t *testing.T) {
	tiingo := &policySymbolStub{quote: broker.Quote{Symbol: "AAPL", Last: 999}}
	yahoo := &policySymbolStub{quote: broker.Quote{Symbol: "AAPL", Last: 123}}
	service := NewService(nil, tiingo, yahoo)
	quote, err := service.GetQuoteFor(t.Context(), "AAPL", Request{Usage: UsagePortfolioValuation, ConnectionID: 99})
	if err != nil || quote.Source != "yahoo" || quote.Last != 123 {
		t.Fatalf("quote=%+v err=%v", quote, err)
	}
	if tiingo.calls != 0 || yahoo.calls != 1 {
		t.Fatalf("quote calls tiingo=%d yahoo=%d", tiingo.calls, yahoo.calls)
	}
}

func TestTiingoQuoteIsRejectedBecauseTiingoIsHistoricalOnly(t *testing.T) {
	service := NewService(nil, &policySymbolStub{}, nil)
	_, err := service.GetQuoteFor(t.Context(), "AAPL", Request{Source: SourceTiingo})
	code, _ := ErrorDetails(err)
	if code != ErrorInvalidRequest || !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("classification code=%s err=%v", code, err)
	}
}
