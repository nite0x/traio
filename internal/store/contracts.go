package store

import (
	"context"
	"errors"

	"github.com/nite/traio/internal/broker"
)

// ErrNotFound is returned when a repository lookup has no matching record.
// It keeps database/sql errors from leaking into service and runtime packages.
var ErrNotFound = errors.New("store: not found")
var ErrForbidden = errors.New("store: forbidden")

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

// AuthRepository persists users, workspace membership, browser sessions,
// short-lived OIDC login flows, and security audit events.
type AuthRepository interface {
	UpsertOIDCIdentity(context.Context, string, string, string, string) (AuthIdentity, error)
	HasPasswordIdentity(context.Context) (bool, error)
	BootstrapPasswordIdentity(context.Context, PasswordCredential) (AuthIdentity, bool, error)
	GetPasswordIdentity(context.Context, string) (AuthIdentity, string, error)
	GetAuthSession(context.Context, string) (AuthIdentity, AuthSession, error)
	CreateAuthSession(context.Context, AuthSession) error
	TouchAuthSession(context.Context, string, string) error
	DeleteAuthSession(context.Context, string) error
	CreateAuthFlow(context.Context, AuthFlow) error
	ConsumeAuthFlow(context.Context, string, string) (AuthFlow, error)
	CreateBrokerOAuthFlow(context.Context, BrokerOAuthFlow) error
	ConsumeBrokerOAuthFlow(context.Context, string, string) (BrokerOAuthFlow, error)
	ListWorkspaceMembers(context.Context, int64) ([]WorkspaceMember, error)
	InviteWorkspaceMember(context.Context, WorkspaceInvite) error
	UpdateWorkspaceMemberRole(context.Context, int64, int64, string) error
	DeleteWorkspaceMember(context.Context, int64, int64) error
	AppendAuditEvent(context.Context, AuditEvent) error
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
	AuthRepository
	WatchlistRepository
	SettingsRepository
	CandleCacheRepository
	BrokerCatalogRepository
	BrokerRuntimeConfigRepository
	InstrumentRepository
	PortfolioRepository
	Close() error
}

var _ Repository = (*Store)(nil)
