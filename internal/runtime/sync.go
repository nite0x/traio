package runtime

import (
	"github.com/nite/traio/internal/account"
	"github.com/nite/traio/internal/portfolio"
)

// BuildBrokerSync constructs the IBKR account projection sync service.
func BuildBrokerSync(st portfolio.Repository, b *Brokers) *portfolio.SyncService {
	return portfolio.NewSyncService(st, b.SyncSources()...)
}

// BuildAccountEquity constructs the live account equity service from registered broker sources.
func BuildAccountEquity(b *Brokers) *account.Service {
	return account.New(b.AccountSources()...)
}
