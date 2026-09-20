package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/nite/traio/internal/activity"
	perf "github.com/nite/traio/internal/performance"
)

type storePerformanceValuation struct {
	prices map[int64][]perf.Price
}

func (v storePerformanceValuation) DailyPrices(context.Context, []perf.Instrument, time.Time, time.Time) (map[int64][]perf.Price, error) {
	return v.prices, nil
}
func (storePerformanceValuation) DailyFX(context.Context, []perf.FXPair, time.Time, time.Time) (map[perf.FXPair][]perf.FXRate, error) {
	return map[perf.FXPair][]perf.FXRate{}, nil
}

func TestPerformanceEndToEndFromLedgerAndRevision(t *testing.T) {
	s := historyTestStore(t, "sqlite")
	_, accountID := seedHistoryAccount(t, s)
	today := time.Now().UTC().Format(time.DateOnly)
	day0 := time.Now().UTC().AddDate(0, 0, -2).Format(time.DateOnly)
	day1 := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)

	deposit := activity.RawRecord{
		Source: "flex", Namespace: "ibkr.flex", Key: "deposit-1", RecordType: "CashTransaction",
		ProviderAccountID: "HISTORY-TEST", SourceUpdatedAt: day0 + "T10:00:00Z",
		Payload:    json.RawMessage(`{"id":"deposit-1"}`),
		Identities: []activity.Identity{{Namespace: "ibkr.cash", Kind: "cash", ExternalID: "deposit-1"}},
		Activity:   activity.Activity{Type: "deposit", Status: activity.StatusEffective, BookingStatus: activity.BookingBooked, TradeDate: day0, TimePrecision: "date", Description: "Deposit", Legs: []activity.Leg{{Kind: "cash", Component: "principal", Currency: "USD", CashDelta: "1000", EffectiveDate: day0}}},
	}
	trade := datedHistoryTrade("exec-performance", "-1", day1, day1+"T10:00:00Z")
	runHistoryRecords(t, s, deposit, trade)

	input, err := s.LoadPerformanceInput(context.Background(), accountID, today)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Instruments) != 1 {
		t.Fatalf("instruments = %#v", input.Instruments)
	}
	var instrumentID int64
	for instrumentID = range input.Instruments {
	}
	market := storePerformanceValuation{prices: map[int64][]perf.Price{instrumentID: {
		{Date: day1, Value: "200", Source: "fixture"},
		{Date: today, Value: "210", Source: "fixture"},
	}}}
	service := perf.New(s, market, perf.RetentionLicensedPersistence)
	series, err := service.Calculate(context.Background(), perf.Query{AccountID: accountID, From: day0, To: today})
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Points) != 3 {
		t.Fatalf("points = %d", len(series.Points))
	}
	if got := pointerValue(series.Points[1].TotalValue); got != "999" {
		t.Fatalf("trade day total = %s", got)
	}
	if series.Coverage.Status == "complete" || series.Points[1].DailyPnL != nil || series.Points[1].DailyReturn != nil || series.Points[1].CumulativeTWR != nil {
		t.Fatalf("incomplete activity coverage exposed returns: coverage=%#v point=%#v", series.Coverage, series.Points[1])
	}
	if got := pointerValue(series.Points[2].TotalValue); got != "1099" {
		t.Fatalf("final total = %s", got)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM daily_account_performance WHERE account_id=?`, accountID).Scan(&count); err != nil || count != 3 {
		t.Fatalf("persisted count=%d err=%v", count, err)
	}
	var originalGeneration int64
	if err := s.db.QueryRow(`SELECT activity_generation FROM daily_account_performance WHERE account_id=? AND performance_date=?`, accountID, day0).Scan(&originalGeneration); err != nil {
		t.Fatal(err)
	}

	revised := datedHistoryTrade("exec-performance", "-2", day1, day1+"T11:00:00Z")
	runHistoryRecords(t, s, revised)
	dirty, err := s.PerformanceDirtyFrom(context.Background(), accountID)
	if err != nil || dirty != day1 {
		t.Fatalf("dirty=%s err=%v", dirty, err)
	}
	recalculated, err := service.Recalculate(context.Background(), perf.Query{AccountID: accountID, From: day0, To: today})
	if err != nil {
		t.Fatal(err)
	}
	if recalculated.Coverage.RecalculatedFrom != day1 || len(recalculated.Points) != 2 {
		t.Fatalf("recalculation coverage=%#v points=%d", recalculated.Coverage, len(recalculated.Points))
	}
	if got := pointerValue(recalculated.Points[0].TotalValue); got != "998" {
		t.Fatalf("revised total = %s", got)
	}
	var day0Generation, day1Generation int64
	if err := s.db.QueryRow(`SELECT activity_generation FROM daily_account_performance WHERE account_id=? AND performance_date=?`, accountID, day0).Scan(&day0Generation); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT activity_generation FROM daily_account_performance WHERE account_id=? AND performance_date=?`, accountID, day1).Scan(&day1Generation); err != nil {
		t.Fatal(err)
	}
	if day0Generation != originalGeneration || day1Generation <= day0Generation {
		t.Fatalf("unaffected/affected generations = %d/%d (original %d)", day0Generation, day1Generation, originalGeneration)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM daily_account_performance WHERE account_id=?`, accountID).Scan(&count); err != nil || count != 3 {
		t.Fatalf("idempotent count=%d err=%v", count, err)
	}
}

func TestInvalidatePerformanceSourceDeletesDependentSuffixAndMarksDirty(t *testing.T) {
	s := historyTestStore(t, "sqlite")
	_, accountID := seedHistoryAccount(t, s)
	for _, row := range []struct {
		date   string
		source string
	}{
		{"2026-09-01", `["yahoo"]`},
		{"2026-09-02", `["tiingo"]`},
		{"2026-09-03", `["yahoo"]`},
	} {
		_, err := s.db.Exec(`INSERT INTO daily_account_performance(account_id,performance_date,base_currency,quality,finality,missing_prices,missing_fx,missing_cost_basis,source_mix,coverage,calculation_version,activity_generation,calculated_at) VALUES(?,?,'USD','official','eod','[]','[]','[]',?,'{}',?,1,?)`,
			accountID, row.date, row.source, perf.CalculationVersion, time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InvalidatePerformanceSource(t.Context(), "tiingo"); err != nil {
		t.Fatal(err)
	}
	var dates []string
	rows, err := s.db.Query(`SELECT performance_date FROM daily_account_performance WHERE account_id=? ORDER BY performance_date`, accountID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			t.Fatal(err)
		}
		dates = append(dates, date)
	}
	rows.Close()
	if len(dates) != 1 || dates[0] != "2026-09-01" {
		t.Fatalf("remaining dates=%v", dates)
	}
	dirty, err := s.PerformanceDirtyFrom(t.Context(), accountID)
	if err != nil || dirty != "2026-09-02" {
		t.Fatalf("dirty=%q err=%v", dirty, err)
	}
}

func datedHistoryTrade(key, fee, date, updated string) activity.RawRecord {
	record := historyTrade(key, fee, updated)
	record.Activity.TradeDate = date
	for i := range record.Activity.Legs {
		record.Activity.Legs[i].EffectiveDate = date
		record.Activity.Legs[i].SettlementDate = date
	}
	record.Payload = json.RawMessage(fmt.Sprintf(`{"id":%q,"fee":%q,"updated":%q}`, key, fee, updated))
	return record
}

func pointerValue(value *string) string {
	if value == nil {
		return "<nil>"
	}
	return *value
}
