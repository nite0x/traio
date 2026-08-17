package store

import (
	"context"
	"errors"

	"github.com/nite/traio/internal/broker"
)

// ErrNotFound is returned when a repository lookup has no matching record.
// It keeps database/sql errors from leaking into service and runtime packages.
var ErrNotFound = errors.New("store: not found")

// WatchlistRepository persists watchlist groups and items.
type WatchlistRepository interface {
	ListWatchlistGroups(context.Context) ([]WatchlistGroup, error)
	ListWatchlistItems(context.Context, int64) ([]WatchlistItem, error)
	UpsertWatchlistItem(context.Context, WatchlistItem) (WatchlistItem, error)
	DeleteWatchlistItem(context.Context, int64, string) error
}

// SettingsRepository persists the application configuration payload.
type SettingsRepository interface {
	GetSettings(context.Context) ([]byte, error)
	SaveSettings(context.Context, []byte) error
	HasSettings(context.Context) (bool, error)
}

// CandleCacheRepository provides the historical market-data cache used by the API.
type CandleCacheRepository interface {
	GetCachedCandles(context.Context, string, string, string) ([]broker.Candle, error)
	SetCachedCandles(context.Context, string, int64, string, string, []broker.Candle) error
	PurgeExpiredCandles(context.Context) error
}

// BrokerCatalogRepository manages static providers and configured connection instances.
type BrokerCatalogRepository interface {
	ListBrokerProviders(context.Context) ([]BrokerProvider, error)
	UpdateBrokerProviderConfig(context.Context, string, map[string]any, map[string]string) (BrokerProvider, error)
	ListBrokerConnections(context.Context) ([]BrokerConnection, error)
	GetBrokerConnection(context.Context, int64) (BrokerConnection, error)
	UpsertBrokerConnection(context.Context, BrokerConnection) (BrokerConnection, error)
	SetBrokerConnectionEnabled(context.Context, int64, bool) error
	GetBrokerConnectionDeleteImpact(context.Context, int64) (BrokerConnectionDeleteImpact, error)
	DeleteBrokerConnection(context.Context, int64) error
}

// IBKRGatewayRepository manages locally-owned Client Portal Gateway instances.
// Broker connections intentionally do not reference these records: they only
// store the network address of whichever Gateway they use, local or remote.
type IBKRGatewayRepository interface {
	ListIBKRGateways(context.Context) ([]IBKRGateway, error)
	GetIBKRGateway(context.Context, int64) (IBKRGateway, error)
	UpsertIBKRGateway(context.Context, IBKRGateway) (IBKRGateway, error)
	DeleteIBKRGateway(context.Context, int64) error
}

// BrokerRuntimeConfigRepository exposes write-only broker secrets only to the
// in-process adapter registry. It must never be used by HTTP response DTOs.
type BrokerRuntimeConfigRepository interface {
	GetBrokerProviderRuntimeConfig(context.Context, string) (BrokerProviderRuntimeConfig, error)
	GetBrokerConnectionRuntimeConfig(context.Context, int64) (BrokerConnection, error)
}

// PortfolioRepository stores the latest broker projections and synchronization status.
type PortfolioRepository interface {
	ReplaceBrokerConnectionAccounts(context.Context, int64, []broker.Account) error
	BrokerAccountConnectionIsPrimary(context.Context, int64, string) (bool, error)
	ReplaceBrokerConnectionAccountDetails(context.Context, int64, broker.Account) error
	ReplaceBrokerConnectionCashBalances(context.Context, int64, string, []broker.CashBalance) error
	ReplaceBrokerConnectionAccountPositions(context.Context, int64, string, []broker.Position) error
	ReplaceBrokerConnectionAccountPerformance(context.Context, int64, broker.DailyPerformance) error
	RecordBrokerConnectionSyncError(context.Context, int64, string, SyncDataType, error) error
	ListBrokerAccounts(context.Context) ([]BrokerAccount, error)
	ListBrokerAccountsByConnection(context.Context, int64) ([]BrokerAccount, error)
	ListBrokerAccountBalances(context.Context) ([]BrokerAccountBalance, error)
	ListBrokerAccountPerformance(context.Context) ([]BrokerAccountPerformance, error)
	ListBrokerPositions(context.Context) ([]broker.Position, error)
	ListBrokerSyncStatuses(context.Context) ([]BrokerSyncStatus, error)
}

// InstrumentRepository owns Traio's canonical asset identities and the
// provider-specific identifiers that resolve to them.
type InstrumentRepository interface {
	ResolveInstrument(context.Context, InstrumentIdentity) (Instrument, error)
	GetInstrument(context.Context, int64) (Instrument, error)
	ListInstruments(context.Context) ([]Instrument, error)
}

// Repository is the complete persistence contract assembled at process startup.
// Consumers should accept one of the narrower interfaces above.
type Repository interface {
	WatchlistRepository
	SettingsRepository
	CandleCacheRepository
	BrokerCatalogRepository
	BrokerRuntimeConfigRepository
	IBKRGatewayRepository
	InstrumentRepository
	PortfolioRepository
	Close() error
}

var _ Repository = (*Store)(nil)
