package portfolio

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/store"
)

type fakeProvider struct {
	mu        sync.Mutex
	calls     int
	positions []broker.Position
	err       error
}

func (f *fakeProvider) ListPositions(context.Context) ([]broker.Position, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	out := make([]broker.Position, len(f.positions))
	copy(out, f.positions)
	return out, f.err
}

func (f *fakeProvider) PlaceOrder(context.Context, broker.OrderRequest) (string, error) {
	return "", nil
}

func (f *fakeProvider) setResult(positions []broker.Position, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.positions = positions
	f.err = err
}

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newTestSyncService(t *testing.T, sources ...Source) *SyncService {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewSyncService(st, sources...)
}

func TestAllPositionsReadsOnlyDatabase(t *testing.T) {
	provider := &fakeProvider{positions: []broker.Position{{
		Symbol: "AAPL", Quantity: 2, MarketValue: 400, Account: "U1",
	}}}
	svc := newTestSyncService(t, Source{Name: "IBKR", Provider: provider})

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("sync positions: %v", err)
	}
	first, err := svc.AllPositions(context.Background())
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	second, err := svc.AllPositions(context.Background())
	if err != nil {
		t.Fatalf("read positions again: %v", err)
	}

	if provider.callCount() != 1 {
		t.Fatalf("database reads called provider; got %d provider calls", provider.callCount())
	}
	if len(first) != 1 || len(second) != 1 || second[0].Broker != "IBKR" {
		t.Fatalf("unexpected positions: %#v", second)
	}
	if got := second[0].MarketPrice; got != 200 {
		t.Fatalf("expected derived market price 200, got %v", got)
	}
}

func TestSyncReplacesOneBrokerProjection(t *testing.T) {
	provider := &fakeProvider{positions: []broker.Position{{Symbol: "AAPL", Quantity: 2}}}
	svc := newTestSyncService(t, Source{Name: "IBKR", Provider: provider})

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	provider.setResult([]broker.Position{{Symbol: "MSFT", Quantity: 3}}, nil)
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	positions, err := svc.AllPositions(context.Background())
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	if len(positions) != 1 || positions[0].Symbol != "MSFT" {
		t.Fatalf("expected replaced projection, got %#v", positions)
	}
}

func TestFailedSyncKeepsLastSuccessfulProjection(t *testing.T) {
	provider := &fakeProvider{positions: []broker.Position{{Symbol: "NVDA", Quantity: 3}}}
	svc := newTestSyncService(t, Source{Name: "IBKR", Provider: provider})

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("prime projection: %v", err)
	}
	provider.setResult(nil, errors.New("gateway restarting"))
	if err := svc.Sync(context.Background()); err == nil {
		t.Fatal("expected sync error")
	}

	positions, err := svc.AllPositions(context.Background())
	if err != nil {
		t.Fatalf("read stale projection: %v", err)
	}
	if len(positions) != 1 || positions[0].Symbol != "NVDA" {
		t.Fatalf("expected previous projection, got %#v", positions)
	}
}

func TestSyncKeepsSuccessfulBrokerWhenAnotherFails(t *testing.T) {
	failing := &fakeProvider{err: errors.New("not authenticated")}
	successful := &fakeProvider{positions: []broker.Position{{Symbol: "BTCUSD", Quantity: 1}}}
	svc := newTestSyncService(t,
		Source{Name: "IBKR", Provider: failing},
		Source{Name: "BINANCE", Provider: successful},
	)

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("expected partial sync success, got %v", err)
	}
	positions, err := svc.AllPositions(context.Background())
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	if len(positions) != 1 || positions[0].Broker != "BINANCE" {
		t.Fatalf("unexpected positions: %#v", positions)
	}
}
