package broker

import (
	"context"
	"errors"
	"testing"
)

type granularPortfolioFake struct {
	detailCalls      int
	balanceCalls     int
	positionCalls    int
	performanceCalls int
	positionErr      error
}

func (*granularPortfolioFake) ListAccounts(context.Context) ([]Account, error) {
	return []Account{{ID: " A1 ", DisplayName: "listed"}}, nil
}

func (f *granularPortfolioFake) GetAccount(_ context.Context, accountID string) (Account, error) {
	f.detailCalls++
	return Account{ID: accountID, DisplayName: "resolved"}, nil
}

func (f *granularPortfolioFake) GetCashBalances(_ context.Context, accountID string) ([]CashBalance, error) {
	f.balanceCalls++
	return []CashBalance{{AccountID: accountID, Currency: "USD", Total: 10}}, nil
}

func (f *granularPortfolioFake) ListAccountPositions(context.Context, string) ([]Position, error) {
	f.positionCalls++
	return nil, f.positionErr
}

func (f *granularPortfolioFake) GetDailyPerformance(_ context.Context, accountID string) (DailyPerformance, error) {
	f.performanceCalls++
	return DailyPerformance{AccountID: accountID, DailyPnL: 5}, nil
}

func TestCompositePortfolioProviderDefersResourcesAndKeepsPartialResults(t *testing.T) {
	positionErr := errors.New("positions unavailable")
	granular := &granularPortfolioFake{positionErr: positionErr}
	provider, ok := AsPortfolioProvider(granular)
	if !ok {
		t.Fatal("complete granular provider was not adapted")
	}

	snapshots, err := provider.ListAccountSnapshots(t.Context())
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("list snapshots: snapshots=%#v err=%v", snapshots, err)
	}
	if granular.detailCalls+granular.balanceCalls+granular.positionCalls+granular.performanceCalls != 0 {
		t.Fatalf("account resources were loaded before resolve: %#v", granular)
	}
	resolved, resourceErrors := snapshots[0].Resolve(t.Context())
	if !errors.Is(resourceErrors.Positions, positionErr) {
		t.Fatalf("position error = %v, want %v", resourceErrors.Positions, positionErr)
	}
	if resourceErrors.AccountDetails != nil || resourceErrors.CashBalances != nil || resourceErrors.DailyPerformance != nil {
		t.Fatalf("unexpected resource errors: %#v", resourceErrors)
	}
	if resolved.Account.ID != "A1" || len(resolved.CashBalances) != 1 || resolved.DailyPerformance.DailyPnL != 5 {
		t.Fatalf("partial snapshot was not retained: %#v", resolved)
	}
	if granular.detailCalls != 1 || granular.balanceCalls != 1 || granular.positionCalls != 1 || granular.performanceCalls != 1 {
		t.Fatalf("unexpected resource calls: %#v", granular)
	}
}

func TestAsPortfolioProviderRejectsIncompleteCapabilitySet(t *testing.T) {
	if provider, ok := AsPortfolioProvider(struct{}{}); ok || provider != nil {
		t.Fatalf("incomplete provider was adapted: %#v", provider)
	}
}
