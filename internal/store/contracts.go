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

// OAuthTokenRepository persists OAuth credentials independently of a broker client.
type OAuthTokenRepository interface {
	GetOAuthToken(context.Context, string) (OAuthToken, error)
	SaveOAuthToken(context.Context, OAuthToken) error
}

// CandleCacheRepository provides the historical market-data cache used by the API.
type CandleCacheRepository interface {
	GetCachedCandles(context.Context, string, string, string) ([]broker.Candle, error)
	SetCachedCandles(context.Context, string, int64, string, string, []broker.Candle) error
	PurgeExpiredCandles(context.Context) error
}

// PortfolioRepository stores the latest broker projections and synchronization status.
type PortfolioRepository interface {
	ReplaceBrokerAccounts(context.Context, string, []broker.Account) error
	ReplaceBrokerAccountDetails(context.Context, string, broker.Account) error
	ReplaceBrokerCashBalances(context.Context, string, string, []broker.CashBalance) error
	ReplaceBrokerAccountPositions(context.Context, string, string, []broker.Position) error
	ReplaceBrokerAccountPerformance(context.Context, string, broker.DailyPerformance) error
	RecordBrokerSyncError(context.Context, string, string, SyncDataType, error) error
	ListBrokerAccounts(context.Context) ([]BrokerAccount, error)
	ListBrokerAccountBalances(context.Context) ([]BrokerAccountBalance, error)
	ListBrokerAccountPerformance(context.Context) ([]BrokerAccountPerformance, error)
	ListBrokerPositions(context.Context) ([]broker.Position, error)
	ListBrokerSyncStatuses(context.Context) ([]BrokerSyncStatus, error)
}

// Repository is the complete persistence contract assembled at process startup.
// Consumers should accept one of the narrower interfaces above.
type Repository interface {
	WatchlistRepository
	SettingsRepository
	OAuthTokenRepository
	CandleCacheRepository
	PortfolioRepository
	Close() error
}

var _ Repository = (*Store)(nil)
