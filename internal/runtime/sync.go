package runtime

import (
	"github.com/nite/traio/internal/account"
	"github.com/nite/traio/internal/portfolio"
)

// BuildBrokerSync constructs portfolio synchronization from registered
// connection capabilities.
func BuildBrokerSync(st portfolio.Repository, b *ConnectionManager) *portfolio.SyncService {
	return portfolio.NewSyncService(st, b.SyncSources()...)
}

// BuildAccountEquity constructs the live account equity service from registered broker sources.
func BuildAccountEquity(b *ConnectionManager) *account.Service {
	return account.New(b.AccountSources()...)
}
