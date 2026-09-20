package performance

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/nite/traio/internal/activity"
)

type testRepository struct {
	input      Input
	dirty      string
	anchor     Anchor
	saves      int
	savedFrom  string
	clearDirty bool
}

func (r *testRepository) LoadPerformanceInput(context.Context, int64, string) (Input, error) {
	return r.input, nil
}
func (r *testRepository) PerformanceDirtyFrom(context.Context, int64) (string, error) {
	return r.dirty, nil
}
func (r *testRepository) LoadPerformanceAnchor(context.Context, int64, string, string) (Anchor, error) {
	return r.anchor, nil
}
func (r *testRepository) ReplacePerformanceSeries(_ context.Context, _ Series, from string, clear bool) error {
	r.saves++
	r.savedFrom = from
	r.clearDirty = clear
	if clear {
		r.dirty = ""
	}
	return nil
}
func (r *testRepository) InvalidatePerformanceSource(context.Context, string) error { return nil }

type testValuation struct {
	prices map[int64][]Price
	fx     map[FXPair][]FXRate
}

func (v testValuation) DailyPrices(context.Context, []Instrument, time.Time, time.Time) (map[int64][]Price, error) {
	return v.prices, nil
}
func (v testValuation) DailyFX(context.Context, []FXPair, time.Time, time.Time) (map[FXPair][]FXRate, error) {
	return v.fx, nil
}

func TestDailyPerformanceExcludesExternalFlowsAndRebuildsActivityState(t *testing.T) {
	repo := &testRepository{input: Input{
		AccountID: 7, BaseCurrency: "USD", ActivityGeneration: 11,
		Coverage:    []CoverageWindow{{Source: "flex", From: "2026-09-01", To: "2026-09-05", FetchStatus: "complete", NormalizeStatus: "complete", ReconcileStatus: "passed"}},
		Instruments: map[int64]Instrument{1: {ID: 1, Symbol: "AAPL", Currency: "USD", ExternalID: "265598"}},
		Activities: []activity.Activity{
			testActivity("deposit", "2026-09-01", activity.Leg{Kind: "cash", Currency: "USD", CashDelta: "1000"}),
			testTrade("2026-09-02", "5", "-500", "100"),
			testActivity("deposit", "2026-09-03", activity.Leg{Kind: "cash", Currency: "USD", CashDelta: "200"}),
			testActivity("dividend", "2026-09-04", activity.Leg{Kind: "cash", Currency: "USD", CashDelta: "10"}),
			testActivity("fee", "2026-09-04", activity.Leg{Kind: "cash", Currency: "USD", CashDelta: "-5"}),
			testActivity("withdrawal", "2026-09-04", activity.Leg{Kind: "cash", Currency: "USD", CashDelta: "-100"}),
			testActivity("split", "2026-09-05", activity.Leg{Kind: "position", InstrumentID: 1, QuantityDelta: "5"}),
		},
	}}
	market := testValuation{prices: map[int64][]Price{1: {
		{Date: "2026-09-02", Value: "100", Source: "fixture"},
		{Date: "2026-09-03", Value: "110", Source: "fixture"},
		{Date: "2026-09-04", Value: "110", Source: "fixture"},
		{Date: "2026-09-05", Value: "55", Source: "fixture"},
	}}}
	service := New(repo, market, RetentionTransient)
	service.now = func() time.Time { return time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC) }
	series, err := service.Calculate(context.Background(), Query{AccountID: 7, From: "2026-09-01", To: "2026-09-05"})
	if err != nil {
		t.Fatal(err)
	}
	if repo.saves != 0 {
		t.Fatalf("transient mode persisted %d times", repo.saves)
	}
	if len(series.Points) != 5 {
		t.Fatalf("points = %d", len(series.Points))
	}
	wantTotals := []string{"1000", "1000", "1250", "1155", "1155"}
	for i, want := range wantTotals {
		if series.Points[i].TotalValue == nil || *series.Points[i].TotalValue != want {
			t.Fatalf("day %d total = %v, want %s", i, series.Points[i].TotalValue, want)
		}
	}
	if got := value(series.Points[2].ExternalCashFlow); got != "200" {
		t.Fatalf("deposit flow = %s", got)
	}
	if got := value(series.Points[2].DailyReturn); got != "0.05" {
		t.Fatalf("deposit day return = %s", got)
	}
	if got := value(series.Points[3].ExternalCashFlow); got != "-100" {
		t.Fatalf("withdrawal flow = %s", got)
	}
	if got := value(series.Points[3].DailyPnL); got != "5" {
		t.Fatalf("dividend/fee pnl = %s", got)
	}
	if got := value(series.Points[3].DailyReturn); got != "0.004" {
		t.Fatalf("withdrawal day return = %s", got)
	}
	if got := value(series.Points[4].DailyReturn); got != "0" {
		t.Fatalf("split return = %s", got)
	}
	if got := value(series.Points[4].CumulativeTWR); got != "0.0542" {
		t.Fatalf("twr = %s", got)
	}
}

func TestMissingPriceAndFXArePartialAndIntradayIsNeverOfficial(t *testing.T) {
	repo := &testRepository{input: Input{
		AccountID: 8, BaseCurrency: "USD",
		Instruments: map[int64]Instrument{2: {ID: 2, Symbol: "BMW", Currency: "EUR"}},
		Activities: []activity.Activity{
			testActivity("deposit", "2026-09-20", activity.Leg{Kind: "cash", Currency: "EUR", CashDelta: "100"}),
			testActivity("security_transfer", "2026-09-20", activity.Leg{Kind: "position", InstrumentID: 2, QuantityDelta: "1"}),
		},
	}}
	service := New(repo, testValuation{}, RetentionTransient)
	service.now = func() time.Time { return time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC) }
	series, err := service.Calculate(context.Background(), Query{AccountID: 8, From: "2026-09-20", To: "2026-09-20"})
	if err != nil {
		t.Fatal(err)
	}
	point := series.Points[0]
	if point.Quality != "partial" || point.Finality != "intraday" {
		t.Fatalf("quality/finality = %s/%s", point.Quality, point.Finality)
	}
	if point.TotalValue != nil || point.DailyReturn != nil {
		t.Fatalf("partial values must be null: %#v", point)
	}
	if !reflect.DeepEqual(point.MissingPrices, []string{"BMW"}) {
		t.Fatalf("missing prices = %#v", point.MissingPrices)
	}
	if !reflect.DeepEqual(point.MissingFX, []string{"EUR/USD"}) {
		t.Fatalf("missing fx = %#v", point.MissingFX)
	}
	if !reflect.DeepEqual(point.MissingCostBasis, []string{"BMW"}) {
		t.Fatalf("missing cost = %#v", point.MissingCostBasis)
	}
}

func TestIncompleteActivityCoverageKeepsNAVButSuppressesReturnsAndActivitySources(t *testing.T) {
	repo := &testRepository{input: Input{
		AccountID: 81, BaseCurrency: "USD",
		Coverage: []CoverageWindow{{Source: "flex", From: "2026-09-01", To: "2026-09-02", FetchStatus: "partial", NormalizeStatus: "complete", ReconcileStatus: "unknown"}},
		Activities: []activity.Activity{
			testActivity("deposit", "2026-09-01", activity.Leg{Kind: "cash", Currency: "USD", CashDelta: "100"}),
		},
	}}
	service := New(repo, testValuation{}, RetentionTransient)
	service.now = func() time.Time { return time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC) }
	series, err := service.Calculate(context.Background(), Query{AccountID: 81, From: "2026-09-01", To: "2026-09-02"})
	if err != nil {
		t.Fatal(err)
	}
	if series.Coverage.Status != "partial" || len(series.Points) != 2 {
		t.Fatalf("coverage=%#v points=%d", series.Coverage, len(series.Points))
	}
	for _, point := range series.Points {
		if value(point.TotalValue) != "100" || value(point.CashValue) != "100" {
			t.Fatalf("partial NAV was discarded: %#v", point)
		}
		if point.DailyPnL != nil || point.DailyReturn != nil || point.CumulativeTWR != nil {
			t.Fatalf("partial coverage exposed precise returns: %#v", point)
		}
		if len(point.SourceMix) != 0 {
			t.Fatalf("activity provider leaked into source_mix: %#v", point.SourceMix)
		}
		if point.Quality != "partial" {
			t.Fatalf("quality=%s", point.Quality)
		}
	}
}

func TestLicensedRecalculationStartsAtDirtyDateAndIsIdempotent(t *testing.T) {
	zero := "0"
	total := "1000"
	repo := &testRepository{dirty: "2026-09-03", anchor: Anchor{Date: "2026-09-02", TotalValue: &total, CumulativeTWR: &zero}, input: Input{
		AccountID: 9, BaseCurrency: "USD", ActivityGeneration: 3,
		Activities: []activity.Activity{testActivity("deposit", "2026-09-01", activity.Leg{Kind: "cash", Currency: "USD", CashDelta: "1000"})},
	}}
	service := New(repo, testValuation{}, RetentionLicensedPersistence)
	service.now = func() time.Time { return time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC) }
	query := Query{AccountID: 9, From: "2026-09-01", To: "2026-09-05"}
	first, err := service.Recalculate(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if repo.savedFrom != "2026-09-03" || !repo.clearDirty {
		t.Fatalf("saved from=%s clear=%v", repo.savedFrom, repo.clearDirty)
	}
	if len(first.Points) != 3 || first.Points[0].Date != "2026-09-03" {
		t.Fatalf("recalculated points = %#v", first.Points)
	}
	repo.dirty = "2026-09-03"
	second, err := service.Recalculate(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Points, second.Points) {
		t.Fatalf("recalculation is not deterministic\nfirst=%#v\nsecond=%#v", first.Points, second.Points)
	}
}

func TestCoverageSummaryDistinguishesReconciledNormalizedAndPartial(t *testing.T) {
	complete := []CoverageWindow{
		{From: "2026-01-01", To: "2026-01-15", FetchStatus: "complete", NormalizeStatus: "complete", ReconcileStatus: "passed"},
		{From: "2026-01-16", To: "2026-01-31", FetchStatus: "complete", NormalizeStatus: "complete", ReconcileStatus: "passed"},
	}
	if got := summarizeCoverage(complete, "2026-01-01", "2026-01-31"); got != "complete" {
		t.Fatalf("complete = %s", got)
	}
	complete[1].ReconcileStatus = "unknown"
	if got := summarizeCoverage(complete, "2026-01-01", "2026-01-31"); got != "normalized" {
		t.Fatalf("normalized = %s", got)
	}
	complete[1].FetchStatus = "partial"
	if got := summarizeCoverage(complete, "2026-01-01", "2026-01-31"); got != "partial" {
		t.Fatalf("partial = %s", got)
	}
	if got := summarizeCoverage(nil, "2026-01-01", "2026-01-31"); got != "unknown" {
		t.Fatalf("unknown = %s", got)
	}
}

func testActivity(kind, date string, legs ...activity.Leg) activity.Activity {
	for i := range legs {
		legs[i].Component = "principal"
		legs[i].EffectiveDate = date
	}
	return activity.Activity{Type: kind, Status: activity.StatusEffective, BookingStatus: activity.BookingBooked, TradeDate: date, TimePrecision: "date", Legs: legs}
}

func testTrade(date, quantity, cash, price string) activity.Activity {
	item := testActivity("trade", date,
		activity.Leg{Kind: "position", InstrumentID: 1, QuantityDelta: quantity, Currency: "USD"},
		activity.Leg{Kind: "cash", Currency: "USD", CashDelta: cash},
	)
	item.Fill = &activity.Fill{Quantity: quantity, Price: price, PriceCurrency: "USD", Multiplier: "1"}
	return item
}

func value(value *string) string {
	if value == nil {
		return "<nil>"
	}
	return *value
}
