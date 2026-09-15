package portfolio

import (
	"context"
	"testing"
	"time"

	"github.com/nite/traio/internal/broker"
)

type eventRefreshBroker struct {
	fakeBroker
	fresh bool
}

func (f *eventRefreshBroker) ListAccountPositions(ctx context.Context, id string) ([]broker.Position, error) {
	f.fresh = broker.FreshPositionsRequested(ctx)
	return f.fakeBroker.ListAccountPositions(ctx, id)
}

func TestAccountRefreshScopesReplacesAndPreservesOnFailure(t *testing.T) {
	ctx := context.Background()
	f := &eventRefreshBroker{}
	svc := newTestSyncService(t, StaticSource("IBKR", 0, testPortfolioProvider(t, f)))
	if err := svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	id := svc.sources[0].ConnectionID
	f.positionVersion = "NEW"
	before := f.positionCalls
	if err := svc.SyncAccount(ctx, id, "U1"); err != nil {
		t.Fatal(err)
	}
	if !f.fresh || f.positionCalls != before+1 {
		t.Fatalf("refresh did not target one account: %+v", f)
	}
	positions, err := svc.store.ListBrokerPositions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"U1": "ASSETU1NEW", "U2": "ASSETU2"}
	for _, p := range positions {
		if p.Symbol != want[p.Account] {
			t.Fatalf("wrong replacement %+v", p)
		}
	}
	if len(positions) != 2 {
		t.Fatalf("positions %d", len(positions))
	}
	f.failAccount = "U1"
	f.positionVersion = "BAD"
	if svc.SyncAccount(ctx, id, "U1") == nil {
		t.Fatal("expected failure")
	}
	positions, _ = svc.store.ListBrokerPositions(ctx)
	for _, p := range positions {
		if p.Symbol != want[p.Account] {
			t.Fatal("failed refresh destroyed positions")
		}
	}
}

func TestAccountInvalidationCoalescesAndSchedulesReconciliation(t *testing.T) {
	ctx := context.Background()
	f := &eventRefreshBroker{}
	svc := newTestSyncService(t, StaticSource("IBKR", 0, testPortfolioProvider(t, f)))
	id := svc.sources[0].ConnectionID
	for i := 0; i < 20; i++ {
		svc.InvalidateAccount(id, "U1")
	}
	key := accountRefreshKey{id, "U1"}
	if len(svc.pending) != 1 {
		t.Fatal("fills not coalesced")
	}
	svc.pending[key] = accountRefresh{due: time.Now().Add(-time.Second)}
	svc.refreshPendingAccounts(ctx)
	if f.positionCalls != 1 || svc.pending[key].attempt != 1 {
		t.Fatal("first refresh or delayed reconciliation missing")
	}
	svc.InvalidateAccount(id, "U1")
	if svc.pending[key].attempt != 0 {
		t.Fatal("new fill must get its own follow-up passes")
	}
	cfg := svc.syncConfig()
	cfg.Enabled = false
	svc.SetSyncConfig(cfg)
	svc.refreshPendingAccounts(ctx)
	if len(svc.pending) != 0 || f.positionCalls != 1 {
		t.Fatal("disabled sync processed pending fills")
	}
}
