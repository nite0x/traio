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

const DefaultBrokerSyncInterval = 30 * time.Second

// Source identifies one broker adapter that can sync account projections.
type Source struct {
	Name   string
	Broker broker.Broker
}

// SyncService separates broker synchronization from frontend reads.
// Sync calls broker APIs and updates the repository; AllPositions only reads it.
type SyncService struct {
	store   store.PortfolioRepository
	sources []Source
	syncNow chan struct{}
	syncMu  sync.Mutex

	cfgMu sync.RWMutex
	cfg   config.BrokerSyncConfig
}

func NewSyncService(st store.PortfolioRepository, sources ...Source) *SyncService {
	return &SyncService{
		store:   st,
		sources: sources,
		syncNow: make(chan struct{}, 1),
		cfg:     config.BrokerSyncConfig{Enabled: true},
	}
}

// SetSyncConfig updates whether background broker synchronization is enabled.
func (s *SyncService) SetSyncConfig(cfg config.BrokerSyncConfig) {
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
}

func (s *SyncService) syncConfig() config.BrokerSyncConfig {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

// StartBackground runs an immediate sync and then refreshes on an interval or request.
func (s *SyncService) StartBackground(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultBrokerSyncInterval
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
		return fmt.Errorf("broker store is not available")
	}

	cfg := s.syncConfig()
	if !cfg.Enabled {
		return nil
	}

	var errs []string
	for _, source := range s.sources {
		name := strings.ToUpper(strings.TrimSpace(source.Name))
		if name == "" {
			continue
		}
		if source.Broker == nil {
			continue
		}
		if err := s.syncBrokerResources(ctx, name, source.Broker); err != nil {
			errs = append(errs, name+": "+err.Error())
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(errs, "; "))
}

func (s *SyncService) syncBrokerResources(ctx context.Context, name string, provider broker.Broker) error {
	listed, err := provider.ListAccounts(ctx)
	if err != nil {
		return s.recordBrokerResourceError(ctx, name, "", store.SyncDataAccounts, fmt.Errorf("list accounts: %w", err))
	}
	if len(listed) == 0 {
		return s.recordBrokerResourceError(ctx, name, "", store.SyncDataAccounts, fmt.Errorf("list accounts: no accounts returned"))
	}
	for i := range listed {
		listed[i].Broker = name
	}
	if err := s.store.ReplaceBrokerAccounts(ctx, name, listed); err != nil {
		return s.recordBrokerResourceError(ctx, name, "", store.SyncDataAccounts, fmt.Errorf("store accounts: %w", err))
	}

	var errs []string
	for _, listedAccount := range listed {
		accountID := strings.TrimSpace(listedAccount.ID)
		if accountID == "" {
			errs = append(errs, "account list contains an empty ID")
			continue
		}

		account, err := provider.GetAccount(ctx, accountID)
		if err != nil {
			err = fmt.Errorf("account %s details: %w", accountID, err)
			errs = append(errs, s.recordBrokerResourceError(ctx, name, accountID, store.SyncDataAccountDetails, err).Error())
		} else {
			if account.ID == "" {
				account.ID = accountID
			}
			if account.ID != accountID {
				err = fmt.Errorf("account %s details returned ID %s", accountID, account.ID)
				errs = append(errs, s.recordBrokerResourceError(ctx, name, accountID, store.SyncDataAccountDetails, err).Error())
			} else {
				account.Broker = name
				if err := s.store.ReplaceBrokerAccountDetails(ctx, name, account); err != nil {
					err = fmt.Errorf("account %s details store: %w", accountID, err)
					errs = append(errs, s.recordBrokerResourceError(ctx, name, accountID, store.SyncDataAccountDetails, err).Error())
				}
			}
		}

		balances, err := provider.GetCashBalances(ctx, accountID)
		if err != nil {
			err = fmt.Errorf("account %s cash balances: %w", accountID, err)
			errs = append(errs, s.recordBrokerResourceError(ctx, name, accountID, store.SyncDataCashBalances, err).Error())
		} else {
			for i := range balances {
				balances[i].AccountID = accountID
			}
			if err := s.store.ReplaceBrokerCashBalances(ctx, name, accountID, balances); err != nil {
				err = fmt.Errorf("account %s cash balances store: %w", accountID, err)
				errs = append(errs, s.recordBrokerResourceError(ctx, name, accountID, store.SyncDataCashBalances, err).Error())
			}
		}

		positions, err := provider.ListAccountPositions(ctx, accountID)
		if err != nil {
			err = fmt.Errorf("account %s positions: %w", accountID, err)
			errs = append(errs, s.recordBrokerResourceError(ctx, name, accountID, store.SyncDataPositions, err).Error())
		} else {
			for i := range positions {
				positions[i].Account = accountID
				positions[i].Broker = name
			}
			if err := s.store.ReplaceBrokerAccountPositions(ctx, name, accountID, positions); err != nil {
				err = fmt.Errorf("account %s positions store: %w", accountID, err)
				errs = append(errs, s.recordBrokerResourceError(ctx, name, accountID, store.SyncDataPositions, err).Error())
			}
		}

		performance, err := provider.GetDailyPerformance(ctx, accountID)
		if err != nil {
			err = fmt.Errorf("account %s daily performance: %w", accountID, err)
			errs = append(errs, s.recordBrokerResourceError(ctx, name, accountID, store.SyncDataDailyPerformance, err).Error())
		} else {
			performance.AccountID = accountID
			if err := s.store.ReplaceBrokerAccountPerformance(ctx, name, performance); err != nil {
				err = fmt.Errorf("account %s daily performance store: %w", accountID, err)
				errs = append(errs, s.recordBrokerResourceError(ctx, name, accountID, store.SyncDataDailyPerformance, err).Error())
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (s *SyncService) recordBrokerResourceError(
	ctx context.Context,
	name, account string,
	dataType store.SyncDataType,
	syncErr error,
) error {
	if err := s.store.RecordBrokerSyncError(ctx, name, account, dataType, syncErr); err != nil {
		return fmt.Errorf("%w (record sync status: %v)", syncErr, err)
	}
	return syncErr
}

// AllPositions reads the latest successful normalized projection from SQLite.
func (s *SyncService) AllPositions(ctx context.Context) ([]broker.Position, error) {
	if s.store == nil {
		return nil, fmt.Errorf("broker store is not available")
	}
	return s.store.ListBrokerPositions(ctx)
}

func (s *SyncService) SyncStatus(ctx context.Context) ([]store.BrokerSyncStatus, error) {
	if s.store == nil {
		return nil, fmt.Errorf("broker store is not available")
	}
	return s.store.ListBrokerSyncStatuses(ctx)
}
