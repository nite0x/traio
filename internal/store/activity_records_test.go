package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/nite/traio/internal/activity"
	"github.com/nite/traio/internal/broker"
)

func historyTestStore(t *testing.T, driver string) *Store {
	t.Helper()
	if driver == "sqlite" {
		s, e := Open(filepath.Join(t.TempDir(), "history.db"))
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	dsn := os.Getenv("TRAIO_HISTORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL test DSN not configured")
	}
	// Every test receives a new schema in an explicitly opted-in test database.
	admin, e := sql.Open("pgx", dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "history_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = admin.Exec(`CREATE SCHEMA ` + schema); e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	s, e := openPostgres(u.String())
	if e != nil {
		admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		admin.Close()
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close(); admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); admin.Close() })
	return s
}
func seedHistoryAccount(t *testing.T, s *Store) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	c, e := s.UpsertBrokerConnection(ctx, BrokerConnection{ProviderCode: "IBKR", ConnectionKey: uuid.NewString(), Name: "History test", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ReplaceBrokerConnectionAccounts(ctx, c.ID, []broker.Account{{ID: "HISTORY-TEST", BaseCurrency: "USD"}}); e != nil {
		t.Fatal(e)
	}
	a, e := s.ListHistoryAccounts(ctx)
	if e != nil || len(a) != 1 {
		t.Fatalf("accounts: %#v %v", a, e)
	}
	return c.ID, a[0].ID
}
func historyTrade(key, fee, updated string) activity.RawRecord {
	return activity.RawRecord{Source: "flex", Namespace: "ibkr.flex", Key: key, RecordType: "Trade", ProviderAccountID: "HISTORY-TEST", SourceUpdatedAt: updated,
		Payload: json.RawMessage(`{"id":"` + key + `","fee":"` + fee + `","updated":"` + updated + `"}`), Identities: []activity.Identity{{Namespace: "ibkr.execution", Kind: "execution", ExternalID: key}},
		Activity: activity.Activity{Type: "trade", Status: activity.StatusEffective, BookingStatus: activity.BookingBooked, TradeDate: "2026-09-01", TimePrecision: "date", Description: "Synthetic AAPL buy", Fill: &activity.Fill{ExecutionID: key, OrderID: "order-1", Side: "buy", Quantity: "10", Price: "200", PriceCurrency: "USD", Multiplier: "1"}, Legs: []activity.Leg{
			{Kind: "position", Component: "principal", Symbol: "AAPL", AssetType: "stock", ExternalInstrumentID: "265598", Currency: "USD", QuantityDelta: "10", EffectiveDate: "2026-09-01"},
			{Kind: "cash", Component: "principal", Currency: "USD", CashDelta: "-2000", EffectiveDate: "2026-09-01"},
			{Kind: "cash", Component: "commission", Currency: "USD", CashDelta: fee, EffectiveDate: "2026-09-01"},
		}}}
}
func runHistoryRecords(t *testing.T, s *Store, records ...activity.RawRecord) HistoryCounts {
	t.Helper()
	ctx := context.Background()
	j, e := s.CreateHistoryJob(ctx, HistoryRequest{Source: "xml", From: "2026-09-01", To: "2026-09-02"})
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.ClaimHistoryJob(ctx, "test-worker")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SaveHistoryRaw(ctx, j, records); e != nil {
		t.Fatal(e)
	}
	c, e := s.NormalizeHistoryJob(ctx, j)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SaveHistoryCoverage(ctx, j, c); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishHistoryJob(ctx, j, "succeeded", c, ""); e != nil {
		t.Fatal(e)
	}
	return c
}

func TestHistoryRepositoryContract(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			s := historyTestStore(t, driver)
			connection, account := seedHistoryAccount(t, s)
			ctx := context.Background()
			r := historyTrade("exec-1", "-1", "2026-09-02T10:00:00Z")
			c := runHistoryRecords(t, s, r)
			if c.Added != 1 {
				t.Fatalf("first counts %#v", c)
			}
			p, e := s.ListActivities(ctx, HistoryQuery{Limit: 1})
			if e != nil || len(p.Items) != 1 {
				t.Fatalf("page %#v %v", p, e)
			}
			id := p.Items[0].ID
			a, e := s.GetActivity(ctx, id)
			if e != nil {
				t.Fatal(e)
			}
			if a.CashEffectsByCurrency["USD"] != "-2001" || a.Legs[0].InstrumentID == 0 {
				t.Fatalf("bad booking %#v", a)
			}
			c = runHistoryRecords(t, s, r)
			if c.Duplicates != 1 {
				t.Fatalf("repeat %#v", c)
			}
			r2 := historyTrade("exec-2", "-1", "2026-09-02T10:00:00Z")
			runHistoryRecords(t, s, r2)
			p, e = s.ListActivities(ctx, HistoryQuery{Limit: 1})
			if e != nil || p.NextCursor == "" {
				t.Fatalf("no next cursor %v", e)
			}
			r.Activity.Fill.OrderID = "order-1"
			r = historyTrade("exec-1", "-2", "2026-09-03T10:00:00Z")
			c = runHistoryRecords(t, s, r)
			if c.Updated != 1 {
				t.Fatalf("correction %#v", c)
			}
			if _, e = s.ListActivities(ctx, HistoryQuery{Limit: 1, Cursor: p.NextCursor}); !errors.Is(e, ErrHistoryCursorStale) {
				t.Fatalf("expected stale got %v", e)
			}
			a, e = s.GetActivity(ctx, id)
			if e != nil || a.CashEffectsByCurrency["USD"] != "-2002" || a.Revision != 2 {
				t.Fatalf("correction %#v %v", a, e)
			}
			old := historyTrade("exec-1", "-9", "2026-09-01T10:00:00Z")
			runHistoryRecords(t, s, old)
			a, _ = s.GetActivity(ctx, id)
			if a.Revision != 2 {
				t.Fatal("late old report replaced correction")
			}
			gateway := historyTrade("exec-1", "-5", "2026-09-04T10:00:00Z")
			gateway.Source = "gateway"
			gateway.Namespace = "ibkr.gateway"
			runHistoryRecords(t, s, gateway)
			a, _ = s.GetActivity(ctx, id)
			if a.Revision != 2 || len(a.Sources) < 2 {
				t.Fatalf("source authority %#v", a)
			}
			revs, e := s.ActivityRevisions(ctx, id)
			if e != nil || len(revs) != 2 {
				t.Fatalf("revisions %d %v", len(revs), e)
			}
			r = historyTrade("exec-1", "-2", "2026-09-05T10:00:00Z")
			r.Activity.Status = activity.StatusVoided
			r.Payload = json.RawMessage(`{"cancel":"exec-1"}`)
			runHistoryRecords(t, s, r)
			a, _ = s.GetActivity(ctx, id)
			if a.Status != activity.StatusVoided {
				t.Fatal("cancellation not current")
			}
			if e = s.DeleteBrokerConnection(ctx, connection); e != nil {
				t.Fatal(e)
			}
			accounts, e := s.ListHistoryAccounts(ctx)
			if e != nil || len(accounts) != 1 || accounts[0].ArchivedAt == "" {
				t.Fatalf("history deleted on disconnect %#v %v", accounts, e)
			}
			active, e := s.ListBrokerAccounts(ctx)
			if e != nil || len(active) != 0 {
				t.Fatalf("archived account visible in portfolio %#v %v", active, e)
			}
			if _, e = s.GetActivity(ctx, id); e != nil {
				t.Fatal("lost history", e)
			}
			conn, e := s.UpsertBrokerConnection(ctx, BrokerConnection{ProviderCode: "IBKR", ConnectionKey: "reconnected", Name: "Reconnect", Enabled: true})
			if e != nil {
				t.Fatal(e)
			}
			if e = s.ReplaceBrokerConnectionAccounts(ctx, conn.ID, []broker.Account{{ID: "HISTORY-TEST", BaseCurrency: "USD"}}); e != nil {
				t.Fatal(e)
			}
			active, e = s.ListBrokerAccounts(ctx)
			if e != nil || len(active) != 1 || active[0].ID != account {
				t.Fatalf("account identity changed %#v %v", active, e)
			}
			if _, e = s.ListHistoryIssues(ctx); e != nil {
				t.Fatal(e)
			}
			if e = s.migrateActivities(); e != nil {
				t.Fatal("migration reopen", e)
			}
		})
	}
}

func TestHistoryImportAndLeaseContract(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			s := historyTestStore(t, driver)
			_, _ = seedHistoryAccount(t, s)
			ctx := context.Background()
			r := historyTrade("execution", "-1", "")
			p, e := s.CreateHistoryImport(ctx, []activity.RawRecord{r}, "fixture-file", 1)
			if e != nil {
				t.Fatal(e)
			}
			before, _ := s.ListActivities(ctx, HistoryQuery{})
			if len(before.Items) != 0 {
				t.Fatal("preview booked data")
			}
			j, e := s.CommitHistoryImport(ctx, p.ID)
			if e != nil {
				t.Fatal(e)
			}
			same, e := s.CommitHistoryImport(ctx, p.ID)
			if e != nil || j.ID != same.ID {
				t.Fatal("commit not idempotent", e)
			}
			claimed, e := s.ClaimHistoryJob(ctx, "worker-a")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.ClaimHistoryJob(ctx, "worker-b"); !errors.Is(e, ErrNotFound) {
				t.Fatal("double claimed", e)
			}
			if e = s.SaveHistoryRaw(ctx, claimed, []activity.RawRecord{r}); e != nil {
				t.Fatal(e)
			}
			if _, e = s.execContext(ctx, `UPDATE broker_history_jobs SET lease_until='2000-01-01' WHERE id=?`, j.ID); e != nil {
				t.Fatal(e)
			}
			resumed, e := s.ClaimHistoryJob(ctx, "worker-b")
			if e != nil || resumed.Phase != "normalize" {
				t.Fatal("did not resume normalization", e)
			}
			if e = s.FinishHistoryJob(ctx, claimed, "succeeded", HistoryCounts{}, ""); !errors.Is(e, ErrHistoryLeaseLost) {
				t.Fatal("stale worker wrote", e)
			}
			c, e := s.NormalizeHistoryJob(ctx, resumed)
			if e != nil || c.Added != 1 {
				t.Fatalf("resume %#v %v", c, e)
			}
			replayed, e := s.NormalizeHistoryJob(ctx, resumed)
			if e != nil || replayed != c {
				t.Fatalf("normalization retry lost original outcomes: %#v %#v %v", c, replayed, e)
			}
			if e = s.FinishHistoryJob(ctx, resumed, "succeeded", c, ""); e != nil {
				t.Fatal(e)
			}
			after, e := s.CommitHistoryImport(ctx, p.ID)
			if e != nil || after.ID != j.ID {
				t.Fatal("completed import duplicated", e)
			}
			unknown := r
			unknown.ProviderAccountID = "NOT-AUTHORIZED"
			p, e = s.CreateHistoryImport(ctx, []activity.RawRecord{unknown}, "another", 1)
			if e != nil || p.Status != "needs_action" {
				t.Fatal("unknown account accepted", e)
			}
			if _, e = s.CommitHistoryImport(ctx, p.ID); e == nil {
				t.Fatal("committed unknown account")
			}
		})
	}
}

func TestHistoryQuarantineAndSourceOrder(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			s := historyTestStore(t, driver)
			_, account := seedHistoryAccount(t, s)
			ctx := context.Background()
			r := historyTrade("first", "-1", "")
			runHistoryRecords(t, s, r)
			changed := historyTrade("first", "-20", "")
			counts := runHistoryRecords(t, s, changed)
			p, e := s.ListActivities(ctx, HistoryQuery{})
			if e != nil || len(p.Items) != 1 || p.Items[0].CashEffectsByCurrency["USD"] != "-2001" || counts.Conflicts != 1 {
				t.Fatalf("unordered source replaced confirmed value: %#v %#v %v", p, counts, e)
			}
			malformed := historyTrade("bad", "-1", "")
			malformed.Activity.Type = ""
			malformed.Activity.BookingStatus = "bad"
			malformed.Activity.TradeDate = "yesterday"
			malformed.Activity.OccurredAt = "12:01"
			malformed.Activity.TimePrecision = "guess"
			runHistoryRecords(t, s, malformed)
			p, e = s.ListActivities(ctx, HistoryQuery{Status: activity.StatusNeedsReview})
			if e != nil || len(p.Items) != 1 {
				t.Fatalf("quarantine: %#v %v", p, e)
			}
			a := p.Items[0]
			if e = activity.Validate(&a); e != nil || len(a.Legs) != 0 {
				t.Fatalf("invalid quarantined projection %#v %v", a, e)
			}
			p, e = s.ListActivities(ctx, HistoryQuery{AccountIDs: []int64{account + 1000}})
			if e != nil || len(p.Coverage) != 0 {
				t.Fatalf("coverage scope leaked: %#v %v", p, e)
			}
			// Unrelated source ID namespaces cannot prove that equal executions differ.
			g := historyTrade("gateway-other", "-1", "")
			g.Source = "gateway"
			g.Namespace = "ibkr.gateway"
			g.Identities = []activity.Identity{{Namespace: "gateway.local", Kind: "execution", ExternalID: "g1"}}
			counts = runHistoryRecords(t, s, g)
			if counts.Conflicts != 1 {
				t.Fatalf("weak cross-source duplicate booked: %#v", counts)
			}
		})
	}
}
func TestHistoryJobScopeCanonicalization(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			s := historyTestStore(t, driver)
			_, id := seedHistoryAccount(t, s)
			ctx := context.Background()
			a, e := s.CreateHistoryJob(ctx, HistoryRequest{Source: "xml", AccountIDs: []int64{id, id + 1, id}})
			if e != nil {
				t.Fatal(e)
			}
			b, e := s.CreateHistoryJob(ctx, HistoryRequest{Source: "xml", AccountIDs: []int64{id + 1, id}})
			if e != nil || a.ID != b.ID {
				t.Fatalf("same scope duplicated %v", e)
			}
		})
	}
}

func TestHistoryConcurrentClaimsShareSourceLease(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			s := historyTestStore(t, driver)
			connection, account := seedHistoryAccount(t, s)
			ctx := context.Background()
			for _, from := range []string{"2026-08-01", "2026-09-01"} {
				if _, e := s.CreateHistoryJob(ctx, HistoryRequest{Source: "flex", ConnectionID: connection, AccountIDs: []int64{account}, From: from, To: "2026-09-02"}); e != nil {
					t.Fatal(e)
				}
			}
			start := make(chan struct{})
			results := make(chan error, 4)
			for i := 0; i < 4; i++ {
				go func() { <-start; _, e := s.ClaimHistoryJob(ctx, uuid.NewString()); results <- e }()
			}
			close(start)
			won := 0
			for i := 0; i < 4; i++ {
				e := <-results
				if e == nil {
					won++
				} else if !errors.Is(e, ErrNotFound) {
					t.Fatal(e)
				}
			}
			if won != 1 {
				t.Fatalf("same connection/source had %d concurrent live jobs", won)
			}
		})
	}
}

func TestHistoryExpiredPreviewEvidenceIsPurged(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			s := historyTestStore(t, driver)
			seedHistoryAccount(t, s)
			ctx := context.Background()
			p, e := s.CreateHistoryImport(ctx, []activity.RawRecord{historyTrade("expiry", "-1", "")}, "expiry-fixture", 0)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.execContext(ctx, `UPDATE broker_history_imports SET expires_at='2000-01-01' WHERE id=?`, p.ID); e != nil {
				t.Fatal(e)
			}
			if _, e = s.ClaimHistoryJob(ctx, "cleanup"); !errors.Is(e, ErrNotFound) {
				t.Fatal(e)
			}
			got, e := s.GetHistoryImport(ctx, p.ID)
			if e != nil || got.Status != "expired" || len(got.Records) != 0 || len(got.Snapshots) != 0 {
				t.Fatalf("preview retained raw evidence: %#v %v", got, e)
			}
			if _, e = s.CommitHistoryImport(ctx, p.ID); e == nil {
				t.Fatal("expired preview committed")
			}
		})
	}
}
