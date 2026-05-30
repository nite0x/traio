package portfolio

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nite/traio/internal/broker"
)

type fakeProvider struct {
	mu        sync.Mutex
	calls     int
	positions []broker.Position
	err       error
	wait      chan struct{}
}

func (f *fakeProvider) ListPositions(ctx context.Context) ([]broker.Position, error) {
	f.mu.Lock()
	f.calls++
	wait := f.wait
	positions := clonePositions(f.positions)
	err := f.err
	f.mu.Unlock()

	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return positions, err
}

func (f *fakeProvider) PlaceOrder(context.Context, broker.OrderRequest) (string, error) {
	return "", nil
}

func (f *fakeProvider) setResult(positions []broker.Position, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.positions = clonePositions(positions)
	f.err = err
}

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newTestService(provider broker.PortfolioProvider, ttl time.Duration) *Service {
	s := New(nil, provider)
	s.positionsCacheTTL = ttl
	s.positionsErrorCacheTTL = ttl
	return s
}

func TestAllPositionsCachesSuccessfulFetch(t *testing.T) {
	provider := &fakeProvider{positions: []broker.Position{{Symbol: "AAPL", Quantity: 2}}}
	svc := newTestService(provider, time.Minute)

	first, err := svc.AllPositions(context.Background())
	if err != nil {
		t.Fatalf("first AllPositions: %v", err)
	}
	first[0].Symbol = "MUTATED"

	second, err := svc.AllPositions(context.Background())
	if err != nil {
		t.Fatalf("second AllPositions: %v", err)
	}

	if provider.callCount() != 1 {
		t.Fatalf("expected one provider call, got %d", provider.callCount())
	}
	if got := second[0].Symbol; got != "AAPL" {
		t.Fatalf("expected cached positions to be cloned, got %q", got)
	}
}

func TestAllPositionsCoalescesConcurrentRefresh(t *testing.T) {
	release := make(chan struct{})
	provider := &fakeProvider{
		positions: []broker.Position{{Symbol: "MSFT", Quantity: 1}},
		wait:      release,
	}
	svc := newTestService(provider, time.Minute)

	const callers = 8
	var wg sync.WaitGroup
	wg.Add(callers)
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			pos, err := svc.AllPositions(context.Background())
			if err != nil {
				errs <- err
				return
			}
			if len(pos) != 1 || pos[0].Symbol != "MSFT" {
				errs <- errors.New("unexpected positions")
			}
		}()
	}

	for deadline := time.Now().Add(time.Second); provider.callCount() == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if provider.callCount() != 1 {
		t.Fatalf("expected one provider call, got %d", provider.callCount())
	}
}

func TestAllPositionsCachesErrors(t *testing.T) {
	provider := &fakeProvider{err: errors.New("gateway busy")}
	svc := newTestService(provider, time.Minute)

	if _, err := svc.AllPositions(context.Background()); err == nil {
		t.Fatal("expected first error")
	}
	if _, err := svc.AllPositions(context.Background()); err == nil {
		t.Fatal("expected cached error")
	}
	if provider.callCount() != 1 {
		t.Fatalf("expected one provider call, got %d", provider.callCount())
	}
}

func TestAllPositionsAllowsSuccessfulEmptyProvider(t *testing.T) {
	failingProvider := &fakeProvider{err: errors.New("not implemented")}
	emptyProvider := &fakeProvider{}
	svc := New(failingProvider, emptyProvider)
	svc.positionsCacheTTL = time.Minute
	svc.positionsErrorCacheTTL = time.Minute

	pos, err := svc.AllPositions(context.Background())
	if err != nil {
		t.Fatalf("expected empty successful positions, got error: %v", err)
	}
	if len(pos) != 0 {
		t.Fatalf("expected empty positions, got %#v", pos)
	}
}

func TestAllPositionsServesStaleDataWhenRefreshFails(t *testing.T) {
	provider := &fakeProvider{positions: []broker.Position{{Symbol: "NVDA", Quantity: 3}}}
	svc := newTestService(provider, time.Millisecond)

	if _, err := svc.AllPositions(context.Background()); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	provider.setResult(nil, errors.New("gateway restarting"))

	pos, err := svc.AllPositions(context.Background())
	if err != nil {
		t.Fatalf("expected stale positions instead of refresh error: %v", err)
	}
	if len(pos) != 1 || pos[0].Symbol != "NVDA" {
		t.Fatalf("unexpected stale positions: %#v", pos)
	}
	if provider.callCount() != 2 {
		t.Fatalf("expected refresh attempt after ttl, got %d calls", provider.callCount())
	}
}
