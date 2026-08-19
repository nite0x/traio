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
	Name         string
	ConnectionID int64
	Acquire      PortfolioProviderAcquirer
}

// PortfolioProviderAcquirer leases a provider for one complete synchronization
// operation. release must remain held through deferred snapshot Resolve calls.
type PortfolioProviderAcquirer func() (provider broker.PortfolioProvider, release func())

// StaticSource wraps providers whose lifecycle is owned outside SyncService.
// Runtime-managed sessions should provide a real lease acquirer instead.
func StaticSource(name string, connectionID int64, provider broker.PortfolioProvider) Source {
	return Source{
		Name: name, ConnectionID: connectionID,
		Acquire: func() (broker.PortfolioProvider, func()) { return provider, func() {} },
	}
}

func (s Source) acquirePortfolio() (broker.PortfolioProvider, func()) {
	if s.Acquire == nil {
		return nil, func() {}
	}
	provider, release := s.Acquire()
	if release == nil {
		release = func() {}
	}
	return provider, release
}

type Repository interface {
	store.PortfolioRepository
	GetBrokerConnection(context.Context, int64) (store.BrokerConnection, error)
}

// SyncService separates broker synchronization from portfolio reads.
type SyncService struct {
	store     Repository
	sourcesMu sync.RWMutex
	sources   []Source
	syncNow   chan struct{}
	syncMu    sync.Mutex

	cfgMu sync.RWMutex
	cfg   config.BrokerSyncConfig
}

func (s *SyncService) SetSources(sources ...Source) {
	s.sourcesMu.Lock()
	s.sources = append([]Source(nil), sources...)
	s.sourcesMu.Unlock()
}

func (s *SyncService) brokerSources() []Source {
	s.sourcesMu.RLock()
	defer s.sourcesMu.RUnlock()
	return append([]Source(nil), s.sources...)
}

func NewSyncService(st Repository, sources ...Source) *SyncService {
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
	return s.syncSources(ctx, s.brokerSources())
}

// SyncConnection refreshes only the adapter registered for connectionID.
func (s *SyncService) SyncConnection(ctx context.Context, connectionID int64) error {
	if connectionID <= 0 {
		return fmt.Errorf("connection is required")
	}
	for _, source := range s.brokerSources() {
		if source.ConnectionID == connectionID {
			return s.syncSources(ctx, []Source{source})
		}
	}
	return fmt.Errorf("broker connection %d does not support account synchronization", connectionID)
}

func (s *SyncService) syncSources(ctx context.Context, sources []Source) error {
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
	for _, source := range sources {
		name := strings.ToUpper(strings.TrimSpace(source.Name))
		if name == "" {
			continue
		}
		if source.Acquire == nil {
			continue
		}
		if source.ConnectionID == 0 {
			errs = append(errs, name+": connection is not configured")
			continue
		}
		connection, err := s.store.GetBrokerConnection(ctx, source.ConnectionID)
		if err != nil {
			errs = append(errs, name+": load connection: "+err.Error())
			continue
		}
		if connection.ProviderCode != name {
			errs = append(errs, fmt.Sprintf("%s: connection belongs to provider %s", name, connection.ProviderCode))
			continue
		}
		if !connection.Enabled {
			continue
		}
		provider, release := source.acquirePortfolio()
		if provider == nil {
			release()
			continue
		}
		err = func() error {
			defer release()
			return s.syncBrokerResources(ctx, name, source.ConnectionID, provider)
		}()
		if err != nil {
			errs = append(errs, name+": "+err.Error())
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(errs, "; "))
}

func (s *SyncService) syncBrokerResources(ctx context.Context, name string, connectionID int64, provider broker.PortfolioProvider) error {
	brokerSnapshots, err := provider.ListAccountSnapshots(ctx)
	if err != nil {
		return s.recordBrokerResourceError(ctx, connectionID, "", store.SyncDataAccounts, fmt.Errorf("list account snapshots: %w", err))
	}
	listed := make([]broker.Account, 0, len(brokerSnapshots))
	snapshots := make(map[string]broker.AccountSnapshot, len(brokerSnapshots))
	for _, snapshot := range brokerSnapshots {
		accountID := strings.TrimSpace(snapshot.Account.ID)
		listed = append(listed, snapshot.Account)
		snapshots[accountID] = snapshot
	}
	if len(listed) == 0 {
		return s.recordBrokerResourceError(ctx, connectionID, "", store.SyncDataAccounts, fmt.Errorf("list accounts: no accounts returned"))
	}
	for i := range listed {
		listed[i].Broker = name
	}
	if err := s.store.ReplaceBrokerConnectionAccounts(ctx, connectionID, listed); err != nil {
		return s.recordBrokerResourceError(ctx, connectionID, "", store.SyncDataAccounts, fmt.Errorf("store accounts: %w", err))
	}

	var errs []string
	for _, listedAccount := range listed {
		accountID := strings.TrimSpace(listedAccount.ID)
		if accountID == "" {
			errs = append(errs, "account list contains an empty ID")
			continue
		}
		primary, err := s.store.BrokerAccountConnectionIsPrimary(ctx, connectionID, accountID)
		if err != nil {
			errs = append(errs, fmt.Sprintf("account %s relationship: %v", accountID, err))
			continue
		}
		if !primary {
			continue
		}

		snapshot, resourceErrors := snapshots[accountID].Resolve(ctx)
		account := snapshot.Account
		if resourceErrors.AccountDetails != nil {
			err = fmt.Errorf("account %s details: %w", accountID, resourceErrors.AccountDetails)
			errs = append(errs, s.recordBrokerResourceError(ctx, connectionID, accountID, store.SyncDataAccountDetails, err).Error())
		} else {
			if account.ID == "" {
				account.ID = accountID
			}
			if account.ID != accountID {
				err = fmt.Errorf("account %s details returned ID %s", accountID, account.ID)
				errs = append(errs, s.recordBrokerResourceError(ctx, connectionID, accountID, store.SyncDataAccountDetails, err).Error())
			} else {
				account.Broker = name
				if err := s.store.ReplaceBrokerConnectionAccountDetails(ctx, connectionID, account); err != nil {
					err = fmt.Errorf("account %s details store: %w", accountID, err)
					errs = append(errs, s.recordBrokerResourceError(ctx, connectionID, accountID, store.SyncDataAccountDetails, err).Error())
				}
			}
		}

		balances := snapshot.CashBalances
		if resourceErrors.CashBalances != nil {
			err = fmt.Errorf("account %s cash balances: %w", accountID, resourceErrors.CashBalances)
			errs = append(errs, s.recordBrokerResourceError(ctx, connectionID, accountID, store.SyncDataCashBalances, err).Error())
		} else {
			for i := range balances {
				balances[i].AccountID = accountID
			}
			if err := s.store.ReplaceBrokerConnectionCashBalances(ctx, connectionID, accountID, balances); err != nil {
				err = fmt.Errorf("account %s cash balances store: %w", accountID, err)
				errs = append(errs, s.recordBrokerResourceError(ctx, connectionID, accountID, store.SyncDataCashBalances, err).Error())
			}
		}

		positions := snapshot.Positions
		if resourceErrors.Positions != nil {
			err = fmt.Errorf("account %s positions: %w", accountID, resourceErrors.Positions)
			errs = append(errs, s.recordBrokerResourceError(ctx, connectionID, accountID, store.SyncDataPositions, err).Error())
		} else {
			for i := range positions {
				positions[i].Account = accountID
				positions[i].Broker = name
			}
			if err := s.store.ReplaceBrokerConnectionAccountPositions(ctx, connectionID, accountID, positions); err != nil {
				err = fmt.Errorf("account %s positions store: %w", accountID, err)
				errs = append(errs, s.recordBrokerResourceError(ctx, connectionID, accountID, store.SyncDataPositions, err).Error())
			}
		}

		performance := snapshot.DailyPerformance
		if resourceErrors.DailyPerformance != nil {
			err = fmt.Errorf("account %s daily performance: %w", accountID, resourceErrors.DailyPerformance)
			errs = append(errs, s.recordBrokerResourceError(ctx, connectionID, accountID, store.SyncDataDailyPerformance, err).Error())
		} else {
			performance.AccountID = accountID
			if err := s.store.ReplaceBrokerConnectionAccountPerformance(ctx, connectionID, performance); err != nil {
				err = fmt.Errorf("account %s daily performance store: %w", accountID, err)
				errs = append(errs, s.recordBrokerResourceError(ctx, connectionID, accountID, store.SyncDataDailyPerformance, err).Error())
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
	connectionID int64,
	account string,
	dataType store.SyncDataType,
	syncErr error,
) error {
	if err := s.store.RecordBrokerConnectionSyncError(ctx, connectionID, account, dataType, syncErr); err != nil {
		return fmt.Errorf("%w (record sync status: %v)", syncErr, err)
	}
	return syncErr
}

func (s *SyncService) SyncStatus(ctx context.Context) ([]store.BrokerSyncStatus, error) {
	if s.store == nil {
		return nil, fmt.Errorf("broker store is not available")
	}
	return s.store.ListBrokerSyncStatuses(ctx)
}
