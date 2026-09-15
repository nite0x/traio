package store

import (
	"testing"

	"github.com/nite/traio/internal/activity"
	"github.com/nite/traio/internal/broker"
)

func TestReconcileHistoryJobPassedIncompleteMismatchAndUnknown(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			st := historyTestStore(t, driver)
			connection := createTestConnection(t, st, "reconcile")
			if err := st.ReplaceBrokerConnectionAccounts(t.Context(), connection.ID, []broker.Account{{ID: "U1", DisplayName: "One", BaseCurrency: "USD"}}); err != nil {
				t.Fatal(err)
			}
			accounts, err := st.ListBrokerAccountsByConnection(t.Context(), connection.ID)
			if err != nil || len(accounts) != 1 {
				t.Fatalf("accounts = %#v, err = %v", accounts, err)
			}
			accountID := accounts[0].ID

			first := claimReconciliationJob(t, st, HistoryRequest{AccountIDs: []int64{accountID}, ConnectionID: connection.ID, Source: "flex", From: "2026-09-01", To: "2026-09-01"})
			if err := st.SaveHistorySnapshots(t.Context(), first, []activity.ReconciliationSnapshot{cashSnapshot("U1", "2026-09-01", "100", "complete", "first")}); err != nil {
				t.Fatal(err)
			}
			unknown, err := st.ReconcileHistoryJob(t.Context(), first)
			if err != nil || unknown[accountID] != "unknown" {
				t.Fatalf("unknown = %#v, err = %v", unknown, err)
			}
			if err := st.FinishHistoryJob(t.Context(), first, "succeeded", HistoryCounts{}, ""); err != nil {
				t.Fatal(err)
			}

			second := claimReconciliationJob(t, st, HistoryRequest{AccountIDs: []int64{accountID}, ConnectionID: connection.ID, Source: "flex", From: "2026-09-02", To: "2026-09-02"})
			second.Coverage = completeActivityCoverage(accountID, "2026-09-02", "2026-09-02")
			if err := st.SaveHistorySnapshots(t.Context(), second, []activity.ReconciliationSnapshot{cashSnapshot("U1", "2026-09-02", "101", "complete", "second")}); err != nil {
				t.Fatal(err)
			}
			record := activity.RawRecord{Source: "flex", Namespace: "test.cash", Key: "cash-1", RecordType: "cash", ProviderAccountID: "U1", Payload: []byte(`{"amount":"1"}`), Activity: activity.Activity{Provider: "IBKR", ProviderAccountID: "U1", Type: "deposit", Status: activity.StatusEffective, BookingStatus: activity.BookingBooked, TradeDate: "2026-09-02", TimePrecision: "date", Legs: []activity.Leg{{Kind: "cash", Component: "principal", Currency: "USD", CashDelta: "1", EffectiveDate: "2026-09-02"}}}}
			if err := st.SaveHistoryRaw(t.Context(), second, []activity.RawRecord{record}); err != nil {
				t.Fatal(err)
			}
			second.Phase = "normalize"
			if _, err := st.NormalizeHistoryJob(t.Context(), second); err != nil {
				t.Fatal(err)
			}
			passed, err := st.ReconcileHistoryJob(t.Context(), second)
			if err != nil || passed[accountID] != "passed" {
				t.Fatalf("passed = %#v, err = %v", passed, err)
			}
			if err := st.FinishHistoryJob(t.Context(), second, "succeeded", HistoryCounts{}, ""); err != nil {
				t.Fatal(err)
			}

			third := claimReconciliationJob(t, st, HistoryRequest{AccountIDs: []int64{accountID}, ConnectionID: connection.ID, Source: "flex", From: "2026-09-03", To: "2026-09-03"})
			third.Coverage = completeActivityCoverage(accountID, "2026-09-03", "2026-09-03")
			if err := st.SaveHistorySnapshots(t.Context(), third, []activity.ReconciliationSnapshot{cashSnapshot("U1", "2026-09-03", "101", "partial", "third")}); err != nil {
				t.Fatal(err)
			}
			incomplete, err := st.ReconcileHistoryJob(t.Context(), third)
			if err != nil || incomplete[accountID] != "incomplete" {
				t.Fatalf("incomplete = %#v, err = %v", incomplete, err)
			}
			if err := st.FinishHistoryJob(t.Context(), third, "succeeded", HistoryCounts{}, ""); err != nil {
				t.Fatal(err)
			}

			fourth := claimReconciliationJob(t, st, HistoryRequest{AccountIDs: []int64{accountID}, ConnectionID: connection.ID, Source: "flex", From: "2026-09-04", To: "2026-09-04"})
			fourth.Coverage = completeActivityCoverage(accountID, "2026-09-04", "2026-09-04")
			if err := st.SaveHistorySnapshots(t.Context(), fourth, []activity.ReconciliationSnapshot{cashSnapshot("U1", "2026-09-04", "105", "complete", "fourth")}); err != nil {
				t.Fatal(err)
			}
			incompleteAfterPartial, err := st.ReconcileHistoryJob(t.Context(), fourth)
			if err != nil || incompleteAfterPartial[accountID] != "incomplete" {
				t.Fatalf("after partial baseline = %#v, err = %v", incompleteAfterPartial, err)
			}
			if err := st.FinishHistoryJob(t.Context(), fourth, "succeeded", HistoryCounts{}, ""); err != nil {
				t.Fatal(err)
			}

			baseline := claimReconciliationJob(t, st, HistoryRequest{AccountIDs: []int64{accountID}, ConnectionID: connection.ID, Source: "flex", From: "2026-09-05", To: "2026-09-05"})
			baseline.Coverage = completeActivityCoverage(accountID, "2026-09-05", "2026-09-05")
			if err := st.SaveHistorySnapshots(t.Context(), baseline, []activity.ReconciliationSnapshot{cashSnapshot("U1", "2026-09-05", "101", "complete", "baseline")}); err != nil {
				t.Fatal(err)
			}
			if err := st.FinishHistoryJob(t.Context(), baseline, "succeeded", HistoryCounts{}, ""); err != nil {
				t.Fatal(err)
			}

			mismatchJob := claimReconciliationJob(t, st, HistoryRequest{AccountIDs: []int64{accountID}, ConnectionID: connection.ID, Source: "flex", From: "2026-09-06", To: "2026-09-06"})
			mismatchJob.Coverage = completeActivityCoverage(accountID, "2026-09-06", "2026-09-06")
			if err := st.SaveHistorySnapshots(t.Context(), mismatchJob, []activity.ReconciliationSnapshot{cashSnapshot("U1", "2026-09-06", "105", "complete", "mismatch")}); err != nil {
				t.Fatal(err)
			}
			mismatch, err := st.ReconcileHistoryJob(t.Context(), mismatchJob)
			if err != nil || mismatch[accountID] != "mismatch" {
				t.Fatalf("mismatch = %#v, err = %v", mismatch, err)
			}
			issues, err := st.ListHistoryIssues(t.Context())
			if err != nil || len(issues) == 0 || issues[0].Type != "balance_mismatch" {
				t.Fatalf("issues = %#v, err = %v", issues, err)
			}
		})
	}
}

func TestReconcileRequiresCompleteCoverageAndMatchingBasis(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			st := historyTestStore(t, driver)
			connection := createTestConnection(t, st, "reconcile-preconditions")
			if err := st.ReplaceBrokerConnectionAccounts(t.Context(), connection.ID, []broker.Account{{ID: "U1", DisplayName: "One", BaseCurrency: "USD"}}); err != nil {
				t.Fatal(err)
			}
			accounts, _ := st.ListBrokerAccountsByConnection(t.Context(), connection.ID)
			accountID := accounts[0].ID

			opening := claimReconciliationJob(t, st, HistoryRequest{AccountIDs: []int64{accountID}, ConnectionID: connection.ID, Source: "flex", From: "2026-09-01", To: "2026-09-01"})
			if err := st.SaveHistorySnapshots(t.Context(), opening, []activity.ReconciliationSnapshot{cashSnapshot("U1", "2026-09-01", "100", "complete", "opening")}); err != nil {
				t.Fatal(err)
			}
			if err := st.FinishHistoryJob(t.Context(), opening, "succeeded", HistoryCounts{}, ""); err != nil {
				t.Fatal(err)
			}

			withoutCoverage := claimReconciliationJob(t, st, HistoryRequest{AccountIDs: []int64{accountID}, ConnectionID: connection.ID, Source: "flex", From: "2026-09-02", To: "2026-09-02"})
			if err := st.SaveHistorySnapshots(t.Context(), withoutCoverage, []activity.ReconciliationSnapshot{cashSnapshot("U1", "2026-09-02", "100", "complete", "without-coverage")}); err != nil {
				t.Fatal(err)
			}
			statuses, err := st.ReconcileHistoryJob(t.Context(), withoutCoverage)
			if err != nil || statuses[accountID] != "incomplete" {
				t.Fatalf("without coverage = %#v, err = %v", statuses, err)
			}
			if err := st.FinishHistoryJob(t.Context(), withoutCoverage, "succeeded", HistoryCounts{}, ""); err != nil {
				t.Fatal(err)
			}

			basisMismatch := claimReconciliationJob(t, st, HistoryRequest{AccountIDs: []int64{accountID}, ConnectionID: connection.ID, Source: "flex", From: "2026-09-03", To: "2026-09-03"})
			basisMismatch.Coverage = completeActivityCoverage(accountID, "2026-09-02", "2026-09-03")
			closing := cashSnapshot("U1", "2026-09-03", "100", "complete", "basis-mismatch")
			closing.Basis = "settled"
			if err := st.SaveHistorySnapshots(t.Context(), basisMismatch, []activity.ReconciliationSnapshot{closing}); err != nil {
				t.Fatal(err)
			}
			statuses, err = st.ReconcileHistoryJob(t.Context(), basisMismatch)
			if err != nil || statuses[accountID] != "incomplete" {
				t.Fatalf("basis mismatch = %#v, err = %v", statuses, err)
			}
		})
	}
}

func TestReconcileIncludesActivityUnitsAbsentFromBothSnapshots(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			st := historyTestStore(t, driver)
			connection := createTestConnection(t, st, "reconcile-units")
			if err := st.ReplaceBrokerConnectionAccounts(t.Context(), connection.ID, []broker.Account{{ID: "U1", DisplayName: "One", BaseCurrency: "USD"}}); err != nil {
				t.Fatal(err)
			}
			accounts, _ := st.ListBrokerAccountsByConnection(t.Context(), connection.ID)
			accountID := accounts[0].ID
			opening := claimReconciliationJob(t, st, HistoryRequest{AccountIDs: []int64{accountID}, ConnectionID: connection.ID, Source: "flex", From: "2026-09-01", To: "2026-09-01"})
			if err := st.SaveHistorySnapshots(t.Context(), opening, []activity.ReconciliationSnapshot{cashSnapshot("U1", "2026-09-01", "100", "complete", "opening")}); err != nil {
				t.Fatal(err)
			}
			if err := st.FinishHistoryJob(t.Context(), opening, "succeeded", HistoryCounts{}, ""); err != nil {
				t.Fatal(err)
			}

			closing := claimReconciliationJob(t, st, HistoryRequest{AccountIDs: []int64{accountID}, ConnectionID: connection.ID, Source: "flex", From: "2026-09-02", To: "2026-09-02"})
			closing.Coverage = completeActivityCoverage(accountID, "2026-09-02", "2026-09-02")
			if err := st.SaveHistorySnapshots(t.Context(), closing, []activity.ReconciliationSnapshot{cashSnapshot("U1", "2026-09-02", "100", "complete", "closing")}); err != nil {
				t.Fatal(err)
			}
			record := activity.RawRecord{Source: "flex", Namespace: "test.position", Key: "position-1", RecordType: "trade", ProviderAccountID: "U1", Payload: []byte(`{"quantity":"1"}`), Activity: activity.Activity{Provider: "IBKR", ProviderAccountID: "U1", Type: "security_transfer", Status: activity.StatusEffective, BookingStatus: activity.BookingBooked, TradeDate: "2026-09-02", TimePrecision: "date", Legs: []activity.Leg{{Kind: "position", Component: "transfer", ExternalInstrumentID: "123", Symbol: "ABC", AssetType: "stock", Currency: "USD", QuantityDelta: "1", EffectiveDate: "2026-09-02"}}}}
			if err := st.SaveHistoryRaw(t.Context(), closing, []activity.RawRecord{record}); err != nil {
				t.Fatal(err)
			}
			closing.Phase = "normalize"
			if _, err := st.NormalizeHistoryJob(t.Context(), closing); err != nil {
				t.Fatal(err)
			}
			statuses, err := st.ReconcileHistoryJob(t.Context(), closing)
			if err != nil || statuses[accountID] != "mismatch" {
				t.Fatalf("activity-only unit = %#v, err = %v", statuses, err)
			}
		})
	}
}

func TestSaveHistorySnapshotsRejectsInvalidBalances(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			st := historyTestStore(t, driver)
			connection := createTestConnection(t, st, "reconcile-validation")
			if err := st.ReplaceBrokerConnectionAccounts(t.Context(), connection.ID, []broker.Account{{ID: "U1", DisplayName: "One", BaseCurrency: "USD"}}); err != nil {
				t.Fatal(err)
			}
			accounts, _ := st.ListBrokerAccountsByConnection(t.Context(), connection.ID)
			job := claimReconciliationJob(t, st, HistoryRequest{AccountIDs: []int64{accounts[0].ID}, ConnectionID: connection.ID, Source: "flex", From: "2026-09-01", To: "2026-09-01"})
			invalid := []activity.ReconciliationSnapshot{
				{ProviderAccountID: "U1", AsOfDate: "2026-09-01", Basis: "trade_date", SourceRef: "bad-decimal", Completeness: "complete", Balances: []activity.ReconciliationBalance{{Balance: activity.Balance{Kind: "cash", Currency: "USD", Value: "NaN"}}}},
				{ProviderAccountID: "U1", AsOfDate: "2026-09-01", Basis: "trade_date", SourceRef: "bad-currency", Completeness: "complete", Balances: []activity.ReconciliationBalance{{Balance: activity.Balance{Kind: "cash", Currency: "usd", Value: "1"}}}},
				{ProviderAccountID: "U1", AsOfDate: "2026-09-01", Basis: "trade_date", SourceRef: "missing-position-id", Completeness: "complete", Balances: []activity.ReconciliationBalance{{Balance: activity.Balance{Kind: "position", Currency: "USD", Value: "1"}, Symbol: "ABC", AssetType: "stock"}}},
			}
			for _, snapshot := range invalid {
				if err := st.SaveHistorySnapshots(t.Context(), job, []activity.ReconciliationSnapshot{snapshot}); err == nil {
					t.Fatalf("accepted invalid snapshot %#v", snapshot)
				}
			}
		})
	}
}

func claimReconciliationJob(t *testing.T, st *Store, request HistoryRequest) HistoryJob {
	t.Helper()
	if _, err := st.CreateHistoryJob(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	job, err := st.ClaimHistoryJob(t.Context(), "reconciliation-test")
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func cashSnapshot(account, date, value, completeness, ref string) activity.ReconciliationSnapshot {
	return activity.ReconciliationSnapshot{ProviderAccountID: account, AsOfDate: date, Basis: "trade_date", SourceRef: ref, Completeness: completeness, Balances: []activity.ReconciliationBalance{{Balance: activity.Balance{Kind: "cash", Currency: "USD", Value: value}}}}
}

func completeActivityCoverage(accountID int64, from, to string) []HistoryCoverage {
	return []HistoryCoverage{{AccountID: accountID, Source: "flex", DataType: "activities", ScopeKey: "account", From: from, To: to, FetchStatus: "complete"}}
}
