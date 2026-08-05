package portfolio

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

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
	provider := &fakeBroker{}
	svc := newTestSyncService(t, Source{Name: "IBKR", Broker: provider})
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("sync broker: %v", err)
	}
	callsAfterSync := provider.positionCalls

	first, err := svc.AllPositions(context.Background())
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	second, err := svc.AllPositions(context.Background())
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

func TestSyncSkipsWhenDisabled(t *testing.T) {
	provider := &fakeBroker{}
	svc := newTestSyncService(t, Source{Name: "IBKR", Broker: provider})
	svc.SetSyncConfig(config.BrokerSyncConfig{Enabled: false})

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if provider.listAccountCalls != 0 {
		t.Fatalf("expected no broker calls when sync is disabled, got %d", provider.listAccountCalls)
	}
}
