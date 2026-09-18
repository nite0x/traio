package history

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

func TestImportJobRunsFetchAndNormalizePhases(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{ProviderCode: "IBKR", ConnectionKey: "history", Name: "History", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceBrokerConnectionAccounts(t.Context(), connection.ID, []broker.Account{{ID: "U123", DisplayName: "Brokerage", BaseCurrency: "USD"}}); err != nil {
		t.Fatal(err)
	}

	svc := New(st)
	fixture := []byte(`<?xml version="1.0"?><FlexQueryResponse><FlexStatements><FlexStatement accountId="U123" fromDate="20260901" toDate="20260901"><Trades></Trades><CashTransactions><CashTransaction transactionID="C1" type="Deposit" currency="USD" amount="100" dateTime="20260901;120000" description="Opening deposit"/></CashTransactions><Transfers></Transfers><CorporateActions></CorporateActions><CashReport><CashReportCurrency currency="USD" endingCash="100"/></CashReport><OpenPositions></OpenPositions></FlexStatement></FlexStatements></FlexQueryResponse>`)
	preview, err := svc.PreviewImport(t.Context(), fixture, 0)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	storedPreview, err := st.GetHistoryImport(t.Context(), preview.ID)
	if err != nil || len(storedPreview.Coverage) == 0 || len(storedPreview.Snapshots) != 1 || storedPreview.Coverage[len(storedPreview.Coverage)-1].FetchStatus != "complete" {
		t.Fatalf("stored preview evidence = %#v, err = %v", storedPreview, err)
	}
	queued, err := svc.CommitImport(t.Context(), preview.ID, 0)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	claimed, err := st.ClaimHistoryJob(t.Context(), "test-worker")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.ID != queued.ID || claimed.Phase != "fetch" {
		t.Fatalf("claimed = %#v", claimed)
	}

	svc.runJob(context.Background(), claimed)
	finished, err := st.GetHistoryJob(t.Context(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != "succeeded" || finished.Phase != "normalize" || finished.Counts.Added != 1 {
		t.Fatalf("finished = %#v", finished)
	}
	page, err := st.ListActivities(t.Context(), store.HistoryQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Type != "deposit" {
		t.Fatalf("activities = %#v", page.Items)
	}

	secondFixture := []byte(`<?xml version="1.0"?><FlexQueryResponse><FlexStatements><FlexStatement accountId="U123" fromDate="20260902" toDate="20260902"><Trades></Trades><CashTransactions><CashTransaction transactionID="C2" type="Deposit" currency="USD" amount="1" dateTime="20260902;120000" description="Deposit"/></CashTransactions><Transfers></Transfers><CorporateActions></CorporateActions><CashReport><CashReportCurrency currency="USD" endingCash="101"/></CashReport><OpenPositions></OpenPositions></FlexStatement></FlexStatements></FlexQueryResponse>`)
	secondPreview, err := svc.PreviewImport(t.Context(), secondFixture, 0)
	if err != nil {
		t.Fatal(err)
	}
	secondJob, err := svc.CommitImport(t.Context(), secondPreview.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	secondClaim, err := st.ClaimHistoryJob(t.Context(), "test-worker")
	if err != nil {
		t.Fatal(err)
	}
	svc.runJob(context.Background(), secondClaim)
	secondFinished, err := st.GetHistoryJob(t.Context(), secondJob.ID)
	if err != nil || secondFinished.Status != "succeeded" {
		t.Fatalf("second finished = %#v, err = %v", secondFinished, err)
	}
	coverage, err := st.ListHistoryCoverage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var verifiedAndPassed bool
	for _, item := range coverage {
		if item.Source == SourceXML && item.DataType == "activities" && item.From == "2026-09-02" && item.FetchStatus == "complete" && item.ReconcileStatus == "passed" {
			verifiedAndPassed = true
		}
	}
	if !verifiedAndPassed {
		t.Fatalf("manual import coverage = %#v", coverage)
	}
}

func TestHistoryWindowsAndCompleteCoverage(t *testing.T) {
	windows := historyWindows("2025-01-01", "2026-01-01")
	if len(windows) != 2 || windows[0] != [2]string{"2025-01-01", "2025-12-31"} || windows[1] != [2]string{"2026-01-01", "2026-01-01"} {
		t.Fatalf("windows = %#v", windows)
	}
	coverage := []store.HistoryCoverage{{AccountID: 1, Source: SourceFlex, DataType: "activities", ScopeKey: "account", From: "2025-01-01", To: "2025-12-31", FetchStatus: "complete"}}
	if !completelyCovered([]int64{1}, "2025-02-01", "2025-03-01", coverage) {
		t.Fatal("expected complete coverage")
	}
	if completelyCovered([]int64{1, 2}, "2025-02-01", "2025-03-01", coverage) {
		t.Fatal("missing account must not count as complete")
	}
	joined := []store.HistoryCoverage{
		{AccountID: 1, Source: SourceFlex, DataType: "activities", ScopeKey: "account", From: "2025-01-01", To: "2025-01-31", FetchStatus: "complete"},
		{AccountID: 1, Source: SourceFlex, DataType: "activities", ScopeKey: "account", From: "2025-02-01", To: "2025-02-28", FetchStatus: "complete"},
		{AccountID: 1, Source: SourceFlex, DataType: "activities", ScopeKey: "account", From: "2025-02-15", To: "2025-03-31", FetchStatus: "complete"},
	}
	if !completelyCovered([]int64{1}, "2025-01-15", "2025-03-15", joined) {
		t.Fatal("adjacent and overlapping coverage should merge")
	}
	joined[1].From = "2025-02-02"
	if completelyCovered([]int64{1}, "2025-01-15", "2025-03-15", joined) {
		t.Fatal("a one-day gap must remain incomplete")
	}
}

func TestScheduledWindowSkipsFreshCoverageAndRechecksStaleCoverage(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "history-schedule.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{ProviderCode: "IBKR", ConnectionKey: "schedule", Name: "Schedule", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceBrokerConnectionAccounts(t.Context(), connection.ID, []broker.Account{{ID: "U1", DisplayName: "One", BaseCurrency: "USD"}}); err != nil {
		t.Fatal(err)
	}
	accounts, err := st.ListBrokerAccountsByConnection(t.Context(), connection.ID)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts = %#v, err = %v", accounts, err)
	}

	svc := New(st)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	from, to := "2026-09-07", "2026-09-14"
	proof := store.HistoryCoverage{AccountID: accounts[0].ID, Source: SourceFlex, DataType: "activities", ScopeKey: "account", From: from, To: to, FetchStatus: "complete"}

	fresh := proof
	fresh.LastCheckedAt = now.Add(-23 * time.Hour).Format(time.RFC3339Nano)
	svc.enqueueScheduledWindow(t.Context(), connection.ID, []int64{accounts[0].ID}, from, to, []store.HistoryCoverage{fresh}, true)
	if _, err := st.ClaimHistoryJob(t.Context(), "fresh-check"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("fresh coverage queued a job: %v", err)
	}

	stale := proof
	stale.LastCheckedAt = now.Add(-25 * time.Hour).Format(time.RFC3339Nano)
	svc.enqueueScheduledWindow(t.Context(), connection.ID, []int64{accounts[0].ID}, from, to, []store.HistoryCoverage{stale}, true)
	job, err := st.ClaimHistoryJob(t.Context(), "stale-check")
	if err != nil {
		t.Fatalf("stale coverage did not queue a job: %v", err)
	}
	if job.Request.ConnectionID != connection.ID || job.Request.Source != SourceFlex || job.Request.From != from || job.Request.To != to {
		t.Fatalf("queued request = %#v", job.Request)
	}
}

func TestEnqueueInfersSingleConnectionAndDefaultsAccountScope(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{ProviderCode: "IBKR", ConnectionKey: "only", Name: "Only", Enabled: true, Config: map[string]any{"gateway_url": "https://gateway.example"}, Secrets: map[string]string{"gateway_token": "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceBrokerConnectionAccounts(t.Context(), connection.ID, []broker.Account{{ID: "U1", DisplayName: "One", BaseCurrency: "USD"}}); err != nil {
		t.Fatal(err)
	}
	svc := New(st)
	svc.now = func() time.Time { return time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC) }
	job, err := svc.Enqueue(t.Context(), store.HistoryRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if job.Request.ConnectionID != connection.ID || job.Request.Source != SourceGateway || len(job.Request.AccountIDs) != 1 || job.Request.From != "2026-09-07" || job.Request.To != "2026-09-14" {
		t.Fatalf("job request = %#v", job.Request)
	}
	if _, err := svc.Enqueue(t.Context(), store.HistoryRequest{ConnectionID: connection.ID, Source: SourceGateway, From: "2026-09-01", To: "2026-09-14"}); err == nil || err.Error() != "history_not_supported" {
		t.Fatalf("old gateway range error = %v", err)
	}
	if _, err := svc.Enqueue(t.Context(), store.HistoryRequest{AccountIDs: []int64{999}}); !errors.Is(err, ErrUnknownAccount) {
		t.Fatalf("unknown account error = %v", err)
	}
}

func TestVerifiedFlexCoverageIsPerAccountAndSection(t *testing.T) {
	body := []byte(`<FlexQueryResponse><FlexStatements><FlexStatement accountId="U1" fromDate="20260901" toDate="20260903"><Trades></Trades><CashTransactions></CashTransactions><Transfers></Transfers><CorporateActions></CorporateActions></FlexStatement><FlexStatement accountId="U2" fromDate="20260902" toDate="20260903"><Trades></Trades></FlexStatement></FlexStatements></FlexQueryResponse>`)
	accounts := map[int64]store.BrokerAccount{1: {ID: 1, ProviderAccountID: "U1"}, 2: {ID: 2, ProviderAccountID: "U2"}}
	coverage, err := verifiedFlexCoverage(body, accounts, map[string]bool{"U1": true, "U2": true})
	if err != nil {
		t.Fatal(err)
	}
	var u1Complete, u2Observed bool
	for _, item := range coverage {
		if item.AccountID == 1 && item.DataType == "activities" && item.From == "2026-09-01" && item.FetchStatus == "complete" {
			u1Complete = true
		}
		if item.AccountID == 2 && item.DataType == "activities" && item.From == "2026-09-02" && item.FetchStatus == "observed" {
			u2Observed = true
		}
	}
	if !u1Complete || !u2Observed {
		t.Fatalf("coverage = %#v", coverage)
	}
}

func TestStandaloneFlexDiscoversAccountsBeforeHistoryImport(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "flex.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{ProviderCode: "IBKR", ConnectionKey: "flex", Enabled: true, Config: map[string]any{"connection_type": "flex", "flex_activity_query_id": "123"}, Secrets: map[string]string{"flex_token": "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(st)
	job, err := svc.Enqueue(t.Context(), store.HistoryRequest{ConnectionID: connection.ID, Source: "flex", From: "2026-09-01", To: "2026-09-02"})
	if err != nil {
		t.Fatal(err)
	}
	if len(job.Request.AccountIDs) != 0 {
		t.Fatal("unexpected prerequisite accounts")
	}
	body := []byte(`<FlexQueryResponse><FlexStatements><FlexStatement accountId="U_NEW" fromDate="20260901" toDate="20260902"><CashTransactions><CashTransaction transactionID="NEW1" type="Deposit" currency="USD" amount="100" dateTime="20260901;120000"/></CashTransactions></FlexStatement></FlexStatements></FlexQueryResponse>`)
	if err = svc.discoverFlexAccounts(t.Context(), connection.ID, body); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.PreviewImport(t.Context(), body, 0)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := svc.CommitImport(t.Context(), preview.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimHistoryJob(t.Context(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	if err = st.FinishHistoryJob(t.Context(), claimed, "succeeded", store.HistoryCounts{}, ""); err != nil {
		t.Fatal(err)
	}
	claimed, err = st.ClaimHistoryJob(t.Context(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	svc.runJob(t.Context(), claimed)
	finished, err := st.GetHistoryJob(t.Context(), queued.ID)
	if err != nil || finished.Counts.Added != 1 {
		t.Fatalf("finished=%+v err=%v", finished, err)
	}
	accounts, err := st.ListBrokerAccounts(t.Context())
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts=%+v err=%v", accounts, err)
	}
	gateway, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{ProviderCode: "IBKR", ConnectionKey: "gateway", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.ReplaceBrokerConnectionAccounts(t.Context(), gateway.ID, []broker.Account{{ID: "U_NEW"}}); err != nil {
		t.Fatal(err)
	}
	again, err := st.ListBrokerAccounts(t.Context())
	if err != nil || len(again) != 1 || again[0].ID != accounts[0].ID {
		t.Fatalf("duplicated accounts=%+v err=%v", again, err)
	}
}

func TestDefaultFlexSyncUsesConfiguredReportPeriod(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "period.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{ProviderCode: "IBKR", ConnectionKey: "flex", Enabled: true, Config: map[string]any{"connection_type": "flex", "flex_activity_query_id": "123"}, Secrets: map[string]string{"flex_token": "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(st)
	job, err := svc.Enqueue(t.Context(), store.HistoryRequest{ConnectionID: connection.ID, Source: "flex"})
	if err != nil || !job.Request.UseQueryPeriod {
		t.Fatalf("default request=%+v err=%v", job.Request, err)
	}
	explicit, err := svc.Enqueue(t.Context(), store.HistoryRequest{ConnectionID: connection.ID, Source: "flex", From: "2026-09-01", To: "2026-09-02"})
	if err != nil || explicit.Request.UseQueryPeriod {
		t.Fatalf("explicit request=%+v err=%v", explicit.Request, err)
	}
	claimed, err := st.ClaimHistoryJob(t.Context(), "period-test")
	if err != nil {
		t.Fatal(err)
	}
	if err = st.FinishHistoryJob(t.Context(), claimed, "retry_wait", store.HistoryCounts{}, "ibkr_flex_1003"); err != nil {
		t.Fatal(err)
	}
	if err = st.RetryHistoryConnectionFetches(t.Context(), connection.ID); err != nil {
		t.Fatal(err)
	}
	reset, err := st.GetHistoryJob(t.Context(), claimed.ID)
	if err != nil || reset.Status != "queued" || reset.Attempt != 0 || reset.Error != "" {
		t.Fatalf("retry=%+v err=%v", reset, err)
	}
}

func TestPausedSchedulerAllowsManualHistoryJob(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "paused.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{ProviderCode: "IBKR", ConnectionKey: "flex", Enabled: true, Config: map[string]any{"connection_type": "flex", "flex_activity_query_id": "123", "activity_history_enabled": true}, Secrets: map[string]string{"flex_token": "test-only"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(st)
	paused := false
	svc.SetSyncConfig(config.BrokerSyncConfig{Enabled: true, IBKREnabled: &paused})
	svc.schedule(t.Context())
	if _, err := st.ClaimHistoryJob(t.Context(), "paused-test"); err == nil {
		t.Fatal("paused scheduler created a job")
	}
	job, err := svc.Enqueue(t.Context(), store.HistoryRequest{ConnectionID: connection.ID, Source: "flex"})
	if err != nil || job.ID == "" || !job.Request.UseQueryPeriod {
		t.Fatalf("manual job while paused: %+v %v", job, err)
	}
	enabled := true
	svc.SetSyncConfig(config.BrokerSyncConfig{Enabled: false, IBKREnabled: &enabled})
	svc.schedule(t.Context())
	if len(svc.lastRun) == 0 {
		t.Fatal("resuming IBKR did not resume the scheduler")
	}
}
