package portfolio

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

const DefaultPositionSyncInterval = 30 * time.Second

// Source identifies one broker adapter that can sync normalized positions.
type Source struct {
	Name     string
	Provider broker.PortfolioProvider
}

// SyncService separates broker position synchronization from frontend reads.
// Sync calls broker APIs and updates SQLite; AllPositions only reads SQLite.
type SyncService struct {
	store   *store.Store
	sources []Source
	syncNow chan struct{}
	syncMu  sync.Mutex

	cfgMu sync.RWMutex
	cfg   config.PositionSyncConfig
}

func NewSyncService(st *store.Store, sources ...Source) *SyncService {
	return &SyncService{
		store:   st,
		sources: sources,
		syncNow: make(chan struct{}, 1),
		cfg: config.PositionSyncConfig{
			Enabled: true,
			Brokers: config.PositionSyncBrokers{
				Schwab:    true,
				Alpaca:    true,
				IBKR:      true,
				SnapTrade: true,
			},
		},
	}
}

// SetSyncConfig updates which brokers may sync. Safe for concurrent use.
func (s *SyncService) SetSyncConfig(cfg config.PositionSyncConfig) {
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
}

func (s *SyncService) syncConfig() config.PositionSyncConfig {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

// StartBackground runs an immediate sync and then refreshes on an interval or request.
func (s *SyncService) StartBackground(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultPositionSyncInterval
	}
	go func() {
		_ = s.Sync(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.Sync(ctx)
			case <-s.syncNow:
				_ = s.Sync(ctx)
			}
		}
	}()
}

// Invalidate requests an asynchronous refresh.
func (s *SyncService) Invalidate() {
	select {
	case s.syncNow <- struct{}{}:
	default:
	}
}

// Sync refreshes each enabled broker projection independently.
// A failed source keeps its previous successful projection readable.
// When the master switch is off, Sync is a no-op.
func (s *SyncService) Sync(ctx context.Context) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	if s.store == nil {
		return fmt.Errorf("position store is not available")
	}

	cfg := s.syncConfig()
	if !cfg.Enabled {
		return nil
	}

	var errs []string
	succeeded := false
	for _, source := range s.sources {
		if source.Provider == nil {
			continue
		}
		name := strings.ToUpper(strings.TrimSpace(source.Name))
		if name == "" {
			continue
		}
		if !cfg.BrokerEnabled(name) {
			continue
		}
		positions, err := source.Provider.ListPositions(ctx)
		if err != nil {
			_ = s.store.RecordBrokerPositionSyncError(ctx, name, err)
			errs = append(errs, name+": "+err.Error())
			continue
		}
		for i := range positions {
			positions[i].Broker = name
		}
		if err := s.store.ReplaceBrokerPositions(ctx, name, positions); err != nil {
			_ = s.store.RecordBrokerPositionSyncError(ctx, name, err)
			errs = append(errs, name+": store: "+err.Error())
			continue
		}
		succeeded = true
	}

	if succeeded || len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(errs, "; "))
}

// AllPositions reads the latest successful normalized projection from SQLite.
func (s *SyncService) AllPositions(ctx context.Context) ([]broker.Position, error) {
	if s.store == nil {
		return nil, fmt.Errorf("position store is not available")
	}
	return s.store.ListBrokerPositions(ctx)
}

func (s *SyncService) SyncStatus(ctx context.Context) ([]store.BrokerPositionSync, error) {
	if s.store == nil {
		return nil, fmt.Errorf("position store is not available")
	}
	return s.store.ListBrokerPositionSyncs(ctx)
}
