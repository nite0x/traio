package broker

import (
	"context"
	"strings"
)

// PortfolioProvider is the complete account-projection capability consumed by
// portfolio synchronization. Providers with a native bulk endpoint can
// implement it directly; granular providers can be adapted with
// NewCompositePortfolioProvider.
type PortfolioProvider interface {
	ListAccountSnapshots(ctx context.Context) ([]AccountSnapshot, error)
}

// AccountSnapshotErrors keeps failures isolated by resource. A provider may
// still return the other resources for an account when one upstream call
// fails, allowing synchronization to retain only the affected old projection.
type AccountSnapshotErrors struct {
	AccountDetails   error
	CashBalances     error
	Positions        error
	DailyPerformance error
}

// Resolve loads deferred resources, when present, and returns their independent
// errors. Native bulk snapshots resolve immediately without extra upstream
// calls.
func (s AccountSnapshot) Resolve(ctx context.Context) (AccountSnapshot, AccountSnapshotErrors) {
	if s.resolve == nil {
		return s, AccountSnapshotErrors{}
	}
	return s.resolve(ctx)
}

type accountSnapshotResolver func(context.Context) (AccountSnapshot, AccountSnapshotErrors)

// CompositePortfolioProvider adapts the legacy granular account capabilities
// to PortfolioProvider. Account discovery remains eager while per-account
// resources are deferred until Resolve, so callers can avoid duplicating work
// for accounts owned by another primary connection.
type CompositePortfolioProvider struct {
	accounts    AccountProvider
	positions   PositionProvider
	performance PerformanceProvider
}

func NewCompositePortfolioProvider(
	accounts AccountProvider,
	positions PositionProvider,
	performance PerformanceProvider,
) *CompositePortfolioProvider {
	return &CompositePortfolioProvider{
		accounts: accounts, positions: positions, performance: performance,
	}
}

func (p *CompositePortfolioProvider) ListAccountSnapshots(ctx context.Context) ([]AccountSnapshot, error) {
	accounts, err := p.accounts.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	snapshots := make([]AccountSnapshot, 0, len(accounts))
	for _, listedAccount := range accounts {
		account := listedAccount
		accountID := strings.TrimSpace(account.ID)
		snapshots = append(snapshots, AccountSnapshot{
			Account: account,
			resolve: func(ctx context.Context) (AccountSnapshot, AccountSnapshotErrors) {
				resolved := AccountSnapshot{Account: account}
				var errs AccountSnapshotErrors
				resolved.Account, errs.AccountDetails = p.accounts.GetAccount(ctx, accountID)
				resolved.CashBalances, errs.CashBalances = p.accounts.GetCashBalances(ctx, accountID)
				resolved.Positions, errs.Positions = p.positions.ListAccountPositions(ctx, accountID)
				resolved.DailyPerformance, errs.DailyPerformance = p.performance.GetDailyPerformance(ctx, accountID)
				return resolved, errs
			},
		})
	}
	return snapshots, nil
}

// AsPortfolioProvider returns a native portfolio capability when available or
// composes one from the complete legacy account capability set.
func AsPortfolioProvider(provider any) (PortfolioProvider, bool) {
	if portfolio, ok := provider.(PortfolioProvider); ok {
		return portfolio, true
	}
	accounts, accountsOK := provider.(AccountProvider)
	positions, positionsOK := provider.(PositionProvider)
	performance, performanceOK := provider.(PerformanceProvider)
	if !accountsOK || !positionsOK || !performanceOK {
		return nil, false
	}
	return NewCompositePortfolioProvider(accounts, positions, performance), true
}

var _ PortfolioProvider = (*CompositePortfolioProvider)(nil)
