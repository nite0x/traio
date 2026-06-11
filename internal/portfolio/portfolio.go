package portfolio

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/store"
)

const DefaultPositionSyncInterval = 30 * time.Second

// Source identifies one broker adapter that can sync normalized positions.
type Source struct {
	Name     string
	Provider broker.PortfolioProvider
}

// Service separates broker synchronization from frontend reads.
// SyncPositions calls broker APIs and updates SQLite; AllPositions only reads SQLite.
type Service struct {
	store   *store.Store
	sources []Source
	account broker.AccountProvider
	syncNow chan struct{}
	syncMu  sync.Mutex
}

func New(st *store.Store, sources ...Source) *Service {
	svc := &Service{
		store:   st,
		sources: sources,
		syncNow: make(chan struct{}, 1),
	}
	for _, source := range sources {
		if provider, ok := source.Provider.(broker.AccountProvider); ok {
			svc.account = provider
			break
		}
	}
	return svc
}

// StartPositionSync runs an immediate sync and then refreshes on an interval or request.
func (s *Service) StartPositionSync(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultPositionSyncInterval
	}
	go func() {
		_ = s.SyncPositions(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.SyncPositions(ctx)
			case <-s.syncNow:
				_ = s.SyncPositions(ctx)
			}
		}
	}()
}

// InvalidatePositions requests an asynchronous refresh without coupling callers to a broker.
func (s *Service) InvalidatePositions() {
	select {
	case s.syncNow <- struct{}{}:
	default:
	}
}

// SyncPositions refreshes each broker projection independently.
// A failed source keeps its previous successful projection readable.
func (s *Service) SyncPositions(ctx context.Context) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	if s.store == nil {
		return fmt.Errorf("position store is not available")
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
func (s *Service) AllPositions(ctx context.Context) ([]broker.Position, error) {
	if s.store == nil {
		return nil, fmt.Errorf("position store is not available")
	}
	return s.store.ListBrokerPositions(ctx)
}

func (s *Service) PositionSyncs(ctx context.Context) ([]store.BrokerPositionSync, error) {
	if s.store == nil {
		return nil, fmt.Errorf("position store is not available")
	}
	return s.store.ListBrokerPositionSyncs(ctx)
}

// AccountTimeline remains provider-backed until account-equity projection is implemented.
func (s *Service) AccountTimeline(ctx context.Context) ([]broker.AccountEquityPoint, broker.AccountSummary, error) {
	if s.account == nil {
		return []broker.AccountEquityPoint{}, broker.AccountSummary{}, nil
	}

	points, historicalErr := s.account.HistoricalEquity(ctx)
	summary, summaryErr := s.account.AccountSummary(ctx)
	if summaryErr == nil && summary.NetLiquidation != 0 {
		points = appendOrReplaceToday(points, broker.AccountEquityPoint{
			Time:     summary.AsOf,
			Value:    summary.NetLiquidation,
			Currency: summary.Currency,
			Source:   summary.Broker + " realtime",
		})
	}
	if summaryErr != nil && historicalErr != nil {
		return nil, broker.AccountSummary{}, fmt.Errorf("historical: %w; realtime: %w", historicalErr, summaryErr)
	}
	if historicalErr != nil {
		return points, summary, historicalErr
	}
	if summaryErr != nil {
		return points, summary, summaryErr
	}
	return points, summary, nil
}

func appendOrReplaceToday(points []broker.AccountEquityPoint, realtime broker.AccountEquityPoint) []broker.AccountEquityPoint {
	if realtime.Time == "" {
		return points
	}
	realtimeDay := realtime.Time
	if len(realtimeDay) > 10 {
		realtimeDay = realtimeDay[:10]
	}
	for i, point := range points {
		if len(point.Time) >= 10 && point.Time[:10] == realtimeDay {
			points[i] = realtime
			return points
		}
	}
	return append(points, realtime)
}
