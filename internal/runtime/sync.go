package runtime

import (
	"github.com/nite/traio/internal/account"
	"github.com/nite/traio/internal/portfolio"
	"github.com/nite/traio/internal/store"
)

// BuildPositionSync constructs the position projection sync service from registered broker sources.
func BuildPositionSync(st *store.Store, b Brokers) *portfolio.SyncService {
	return portfolio.NewSyncService(st, b.PositionSources()...)
}

// BuildAccountEquity constructs the live account equity service from registered broker sources.
func BuildAccountEquity(b Brokers) *account.Service {
	return account.New(b.AccountSources()...)
}
