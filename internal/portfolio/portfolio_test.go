package portfolio

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

type lazyLeaseProvider struct {
	fakeBroker
	resolveStarted chan struct{}
	resolveRelease chan struct{}
	resolveOnce    sync.Once
}

func (p *lazyLeaseProvider) GetAccount(ctx context.Context, accountID string) (broker.Account, error) {
	p.resolveOnce.Do(func() { close(p.resolveStarted) })
	<-p.resolveRelease
	return p.fakeBroker.GetAccount(ctx, accountID)
}

func newTestSyncService(t *testing.T, sources ...Source) *SyncService {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for i := range sources {
		if sources[i].ConnectionID != 0 {
			continue
		}
		connection, err := st.UpsertBrokerConnection(context.Background(), store.BrokerConnection{
			ProviderCode:  sources[i].Name,
			ConnectionKey: fmt.Sprintf("test-%d", i+1),
			Name:          "Test connection",
			Environment:   "test",
			Enabled:       true,
		})
		if err != nil {
			t.Fatalf("create test broker connection: %v", err)
		}
		sources[i].ConnectionID = connection.ID
	}
	return NewSyncService(st, sources...)
}

func TestSyncHoldsProviderLeaseThroughLazySnapshotResolve(t *testing.T) {
	provider := &lazyLeaseProvider{resolveStarted: make(chan struct{}), resolveRelease: make(chan struct{})}
	portfolio := testPortfolioProvider(t, provider)
	var releases atomic.Int32
	svc := newTestSyncService(t, Source{
		Name: "IBKR",
		Acquire: func() (broker.PortfolioProvider, func()) {
			return portfolio, func() { releases.Add(1) }
		},
	})
	syncDone := make(chan error, 1)
	go func() { syncDone <- svc.Sync(t.Context()) }()
	select {
	case <-provider.resolveStarted:
	case <-time.After(time.Second):
		t.Fatal("lazy snapshot Resolve was not reached")
	}
	if got := releases.Load(); got != 0 {
		t.Fatalf("provider lease released before lazy Resolve completed: %d", got)
	}
	close(provider.resolveRelease)
	select {
	case err := <-syncDone:
		if err != nil {
			t.Fatalf("sync: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sync did not complete after lazy Resolve was released")
	}
	if got := releases.Load(); got != 1 {
		t.Fatalf("provider lease releases = %d, want 1", got)
	}
}

func TestSyncUsesOnlyPrimaryConnectionForSharedAccounts(t *testing.T) {
	primary := &fakeBroker{}
	secondary := &fakeBroker{}
	svc := newTestSyncService(t,
		StaticSource("IBKR", 0, testPortfolioProvider(t, primary)),
		StaticSource("IBKR", 0, testPortfolioProvider(t, secondary)),
	)
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("sync shared accounts: %v", err)
	}
	if primary.detailCalls != 2 || primary.positionCalls != 2 {
		t.Fatalf("primary connection did not sync projections: %#v", primary)
	}
	if secondary.listAccountCalls != 1 || secondary.detailCalls != 0 || secondary.positionCalls != 0 {
		t.Fatalf("secondary connection duplicated account projections: %#v", secondary)
	}
	accounts, err := svc.store.ListBrokerAccounts(context.Background())
	if err != nil || len(accounts) != 2 {
		t.Fatalf("shared accounts were duplicated: accounts=%#v err=%v", accounts, err)
	}
	for _, account := range accounts {
		if len(account.ConnectionIDs) != 2 {
			t.Fatalf("account missing connection relationships: %#v", account)
		}
	}
	positions, err := svc.AggregatedPositions(context.Background())
	if err != nil || len(positions) != 2 {
		t.Fatalf("shared account positions duplicated: positions=%#v err=%v", positions, err)
	}
}

func TestSyncConnectionOnlyRefreshesSelectedSource(t *testing.T) {
	first := &fakeBroker{}
	second := &fakeBroker{}
	svc := newTestSyncService(t,
		StaticSource("IBKR", 0, testPortfolioProvider(t, first)),
		StaticSource("IBKR", 0, testPortfolioProvider(t, second)),
	)
	secondConnectionID := svc.sources[1].ConnectionID
	if err := svc.SyncConnection(context.Background(), secondConnectionID); err != nil {
		t.Fatalf("sync selected connection: %v", err)
	}
	if first.listAccountCalls != 0 {
		t.Fatalf("unselected connection was synchronized: %#v", first)
	}
	if second.listAccountCalls != 1 || second.detailCalls != 2 || second.positionCalls != 2 {
		t.Fatalf("selected connection was not fully synchronized: %#v", second)
	}
	if err := svc.SyncConnection(context.Background(), 999999); err == nil {
		t.Fatal("missing sync source was accepted")
	}
}

func TestAggregatedPositionsReadOnlyDatabase(t *testing.T) {
	provider := &fakeBroker{}
	svc := newTestSyncService(t, StaticSource("IBKR", 0, testPortfolioProvider(t, provider)))
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("sync broker: %v", err)
	}
	callsAfterSync := provider.positionCalls

	first, err := svc.AggregatedPositions(context.Background())
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	second, err := svc.AggregatedPositions(context.Background())
	if err != nil {
		t.Fatalf("read positions again: %v", err)
	}

	if provider.positionCalls != callsAfterSync {
		t.Fatalf("database reads called broker; got %d additional calls", provider.positionCalls-callsAfterSync)
	}
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("unexpected positions: %#v", second)
	}
}

func TestSnapshotReadsOnlyDatabase(t *testing.T) {
	provider := &fakeBroker{}
	svc := newTestSyncService(t, StaticSource("IBKR", 0, testPortfolioProvider(t, provider)))
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("sync broker: %v", err)
	}
	callsAfterSync := *provider

	first, err := svc.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	second, err := svc.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("read snapshot again: %v", err)
	}

	if provider.listAccountCalls != callsAfterSync.listAccountCalls ||
		provider.detailCalls != callsAfterSync.detailCalls ||
		provider.balanceCalls != callsAfterSync.balanceCalls ||
		provider.positionCalls != callsAfterSync.positionCalls ||
		provider.performanceCalls != callsAfterSync.performanceCalls {
		t.Fatalf("database snapshot called broker: before=%#v after=%#v", callsAfterSync, provider)
	}
	if first.Summary.NetAssetValue != 2000 || second.Summary.SyncedAccounts != 2 {
		t.Fatalf("unexpected snapshot: %#v", second)
	}
	if len(second.Positions) != 2 || len(second.CashBalances) != 4 || len(second.Allocations) != 1 {
		t.Fatalf("unexpected snapshot resources: %#v", second)
	}
}

func TestSyncSkipsWhenDisabled(t *testing.T) {
	provider := &fakeBroker{}
	svc := newTestSyncService(t, StaticSource("IBKR", 0, testPortfolioProvider(t, provider)))
	svc.SetSyncConfig(config.BrokerSyncConfig{Enabled: false})

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if provider.listAccountCalls != 0 {
		t.Fatalf("expected no broker calls when sync is disabled, got %d", provider.listAccountCalls)
	}
}

func TestSyncSkipsDisabledConnection(t *testing.T) {
	provider := &fakeBroker{}
	svc := newTestSyncService(t, StaticSource("IBKR", 0, testPortfolioProvider(t, provider)))
	connectionID := svc.sources[0].ConnectionID
	controller, ok := svc.store.(interface {
		SetBrokerConnectionEnabled(context.Context, int64, bool) error
	})
	if !ok {
		t.Fatal("test store cannot disable broker connections")
	}
	if err := controller.SetBrokerConnectionEnabled(context.Background(), connectionID, false); err != nil {
		t.Fatalf("disable connection: %v", err)
	}
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("sync disabled connection: %v", err)
	}
	if provider.listAccountCalls != 0 {
		t.Fatalf("disabled connection called broker %d times", provider.listAccountCalls)
	}
}
