package performance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nite/traio/internal/activity"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/market"
)

type valuationPolicyStub struct {
	historyRequests []market.Request
	quoteRequests   []market.Request
	historyToday    bool
	quoteErr        error
}

func (s *valuationPolicyStub) GetQuote(ctx context.Context, symbol string) (*broker.Quote, error) {
	return s.GetQuoteFor(ctx, symbol, market.Request{})
}
func (s *valuationPolicyStub) GetQuotes(context.Context, []string) ([]broker.Quote, error) {
	return nil, nil
}
func (s *valuationPolicyStub) GetHistory(ctx context.Context, symbol, period, bar string, refresh bool) ([]broker.Candle, error) {
	history, err := s.GetHistoryFor(ctx, symbol, period, bar, refresh, market.Request{})
	return history.Candles, err
}
func (s *valuationPolicyStub) GetQuoteFor(_ context.Context, _ string, request market.Request) (*broker.Quote, error) {
	s.quoteRequests = append(s.quoteRequests, request)
	if s.quoteErr != nil {
		return nil, s.quoteErr
	}
	source := "yahoo"
	if request.ConnectionID > 0 {
		source = "ibkr"
	}
	return &broker.Quote{Last: 125, Source: source}, nil
}
func (s *valuationPolicyStub) GetQuotesFor(context.Context, []string, market.Request) ([]broker.Quote, error) {
	return nil, nil
}
func (s *valuationPolicyStub) GetHistoryFor(_ context.Context, symbol, _ string, _ string, _ bool, request market.Request) (market.History, error) {
	s.historyRequests = append(s.historyRequests, request)
	now := time.Now().UTC()
	source := "tiingo"
	closeValue := 100.0
	if symbol == "EURUSD=X" {
		source, closeValue = "yahoo", 1.1
	}
	candles := []broker.Candle{{Time: now.AddDate(0, 0, -1).Unix(), Close: closeValue}}
	if s.historyToday {
		candles = append(candles, broker.Candle{Time: now.Unix(), Close: 999})
	}
	return market.History{Source: source, Candles: candles}, nil
}

func TestMarketValuationDropsTiingoTodayAndUsesYahooWithoutConnection(t *testing.T) {
	stub := &valuationPolicyStub{historyToday: true}
	provider := NewMarketValuationProvider(stub)
	now := time.Now().UTC()
	prices, err := provider.DailyPrices(t.Context(), []Instrument{{ID: 2, Symbol: "MSFT", Currency: "USD"}}, now.AddDate(0, 0, -2), now)
	if err != nil {
		t.Fatal(err)
	}
	today := now.Format(time.DateOnly)
	var todayRows []Price
	for _, price := range prices[2] {
		if price.Date == today {
			todayRows = append(todayRows, price)
		}
	}
	if len(stub.quoteRequests) != 1 || stub.quoteRequests[0].ConnectionID != 0 {
		t.Fatalf("quote requests=%#v", stub.quoteRequests)
	}
	if len(todayRows) != 1 || todayRows[0].Source != "yahoo" || todayRows[0].Value != "125" || todayRows[0].Missing {
		t.Fatalf("today rows=%#v all=%#v", todayRows, prices[2])
	}
}

func TestMarketValuationQuoteFailureMarksTodayMissingInsteadOfCarryingHistory(t *testing.T) {
	stub := &valuationPolicyStub{historyToday: true, quoteErr: errors.New("quote unavailable")}
	provider := NewMarketValuationProvider(stub)
	now := time.Now().UTC()
	rows, err := provider.DailyPrices(t.Context(), []Instrument{{ID: 3, Symbol: "AAPL", Currency: "USD"}}, now.AddDate(0, 0, -2), now)
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := indexPrices(rows)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := priceAt(now.Format(time.DateOnly), 3, indexed); err != nil || ok {
		t.Fatalf("today price ok=%v err=%v rows=%#v", ok, err, rows[3])
	}
	today := now.Format(time.DateOnly)
	repo := &testRepository{input: Input{
		AccountID: 3, BaseCurrency: "USD",
		Coverage:    []CoverageWindow{{Source: "flex", From: today, To: today, FetchStatus: "complete", NormalizeStatus: "complete", ReconcileStatus: "passed"}},
		Instruments: map[int64]Instrument{3: {ID: 3, Symbol: "AAPL", Currency: "USD"}},
		Activities: []activity.Activity{
			testActivity("security_transfer", today, activity.Leg{Kind: "position", InstrumentID: 3, QuantityDelta: "1"}),
		},
	}}
	service := New(repo, provider, RetentionTransient)
	service.now = func() time.Time { return now }
	series, err := service.Calculate(t.Context(), Query{AccountID: 3, From: today, To: today})
	if err != nil {
		t.Fatal(err)
	}
	point := series.Points[0]
	if point.Quality != "partial" || point.TotalValue != nil || len(point.MissingPrices) != 1 || point.MissingPrices[0] != "AAPL" {
		t.Fatalf("quote failure point=%#v", point)
	}
}

func TestMarketValuationUsesPublicHistoryBrokerTodayAndExplicitFX(t *testing.T) {
	stub := &valuationPolicyStub{}
	provider := NewMarketValuationProvider(stub)
	now := time.Now().UTC()
	prices, err := provider.DailyPrices(t.Context(), []Instrument{{ID: 1, ConnectionID: 17, Symbol: "AAPL", Currency: "USD"}}, now.AddDate(0, 0, -2), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.historyRequests) != 1 || stub.historyRequests[0].Usage != market.UsagePortfolioValuation || stub.historyRequests[0].Source != market.SourceAuto {
		t.Fatalf("history requests=%#v", stub.historyRequests)
	}
	if len(stub.quoteRequests) != 1 || stub.quoteRequests[0].ConnectionID != 17 {
		t.Fatalf("quote requests=%#v", stub.quoteRequests)
	}
	today := now.Format(time.DateOnly)
	foundBrokerToday := false
	for _, price := range prices[1] {
		if price.Date == today && price.Source == "ibkr" && price.Value == "125" {
			foundBrokerToday = true
		}
	}
	if !foundBrokerToday {
		t.Fatalf("prices=%#v", prices)
	}
	rates, err := provider.DailyFX(t.Context(), []FXPair{{From: "EUR", To: "USD"}}, now.AddDate(0, 0, -2), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.historyRequests) != 2 || stub.historyRequests[1].Source != market.SourceYahoo || len(rates[FXPair{From: "EUR", To: "USD"}]) != 1 {
		t.Fatalf("FX history requests=%#v rates=%#v", stub.historyRequests, rates)
	}
}

func TestDailyValuesDoNotCarryForwardIndefinitely(t *testing.T) {
	prices := map[int64]map[string]Price{1: {"2026-01-01": {Date: "2026-01-01", Value: "10", Source: "tiingo"}}}
	if _, _, ok, err := priceAt("2026-01-09", 1, prices); err != nil || ok {
		t.Fatalf("stale price ok=%v err=%v", ok, err)
	}
	rates := map[FXPair]map[string]FXRate{{From: "EUR", To: "USD"}: {"2026-01-01": {Date: "2026-01-01", Value: "1.1", Source: "yahoo"}}}
	if _, _, ok, err := fxAt("2026-01-09", "EUR", "USD", rates); err != nil || ok {
		t.Fatalf("stale FX ok=%v err=%v", ok, err)
	}
}
