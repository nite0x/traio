package portfolio

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/store"
)

type fakeBroker struct {
	loginCalls       int
	listAccountCalls int
	detailCalls      int
	balanceCalls     int
	positionCalls    int
	performanceCalls int
	failList         bool
	failAccount      string
	positionVersion  string
}

type fakeSnapshotBroker struct {
	fakeBroker
	snapshotCalls int
}

func testPortfolioProvider(t *testing.T, provider any) broker.PortfolioProvider {
	t.Helper()
	portfolio, ok := broker.AsPortfolioProvider(provider)
	if !ok {
		t.Fatalf("%T does not provide a complete portfolio capability", provider)
	}
	return portfolio
}

func (f *fakeSnapshotBroker) ListAccountSnapshots(context.Context) ([]broker.AccountSnapshot, error) {
	f.snapshotCalls++
	return []broker.AccountSnapshot{{
		Account: broker.Account{
			ID: "S1", DisplayName: "Schwab S1", AccountType: "MARGIN", Status: "open", BaseCurrency: "USD",
		},
		CashBalances: []broker.CashBalance{{Currency: "USD", Total: 250, Settled: 200, ExchangeRate: 1, IsBaseCurrency: true}},
		Positions: []broker.Position{{
			ExternalID: "037833100", Symbol: "AAPL", Quantity: 2, MarketValue: 400, Currency: "USD",
		}},
		DailyPerformance: broker.DailyPerformance{DailyPnL: 12, NetLiquidation: 650, MarketValue: 400},
	}}, nil
}

func (f *fakeBroker) BeginLogin(context.Context) (broker.LoginAction, error) {
	f.loginCalls++
	return broker.LoginAction{}, nil
}

func (f *fakeBroker) ListAccounts(context.Context) ([]broker.Account, error) {
	f.listAccountCalls++
	if f.failList {
		return nil, errors.New("account discovery unavailable")
	}
	return []broker.Account{{ID: "U1"}, {ID: "U2"}}, nil
}

func (f *fakeBroker) GetAccount(_ context.Context, accountID string) (broker.Account, error) {
	f.detailCalls++
	return broker.Account{
		ID:           accountID,
		DisplayName:  "Account " + accountID,
		AccountType:  "INDIVIDUAL",
		Status:       "open",
		BaseCurrency: "USD",
	}, nil
}

func (f *fakeBroker) GetCashBalances(_ context.Context, accountID string) ([]broker.CashBalance, error) {
	f.balanceCalls++
	return []broker.CashBalance{
		{AccountID: accountID, Currency: "BASE", Total: 1000, Settled: 900, ExchangeRate: 1, IsBaseCurrency: true},
		{AccountID: accountID, Currency: "USD", Total: 500, Settled: 450, ExchangeRate: 1},
	}, nil
}

func (f *fakeBroker) ListAccountPositions(_ context.Context, accountID string) ([]broker.Position, error) {
	f.positionCalls++
	if accountID == f.failAccount {
		return nil, errors.New("positions unavailable")
	}
	return []broker.Position{{
		Symbol: "ASSET" + accountID + f.positionVersion, Quantity: 1, MarketValue: 100, Currency: "USD",
	}}, nil
}

func (f *fakeBroker) GetDailyPerformance(_ context.Context, accountID string) (broker.DailyPerformance, error) {
	f.performanceCalls++
	return broker.DailyPerformance{
		AccountID: accountID, DailyPnL: 10, NetLiquidation: 1000, MarketValue: 600,
	}, nil
}

func TestSyncBrokerRefreshesEveryAccountCapability(t *testing.T) {
	provider := &fakeBroker{}
	svc := newTestSyncService(t, StaticSource("IBKR", 0, testPortfolioProvider(t, provider)))

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("sync broker snapshot: %v", err)
	}
	connection, err := svc.store.GetBrokerConnection(context.Background(), svc.sources[0].ConnectionID)
	if err != nil || connection.Status != store.BrokerConnectionStatusConnected || connection.LastAuthenticatedAt == "" {
		t.Fatalf("successful account discovery did not authenticate connection: connection=%#v err=%v", connection, err)
	}
	if provider.loginCalls != 0 {
		t.Fatalf("periodic sync must not open login, got %d calls", provider.loginCalls)
	}
	if provider.listAccountCalls != 1 || provider.detailCalls != 2 || provider.balanceCalls != 2 || provider.positionCalls != 2 || provider.performanceCalls != 2 {
		t.Fatalf("unexpected capability calls: %#v", provider)
	}

	accounts, err := svc.store.ListBrokerAccounts(context.Background())
	if err != nil {
		t.Fatalf("list stored accounts: %v", err)
	}
	balances, err := svc.store.ListBrokerAccountBalances(context.Background())
	if err != nil {
		t.Fatalf("list stored balances: %v", err)
	}
	positions, err := svc.AggregatedPositions(context.Background())
	if err != nil {
		t.Fatalf("list stored positions: %v", err)
	}
	performance, err := svc.store.ListBrokerAccountPerformance(context.Background())
	if err != nil {
		t.Fatalf("list stored performance: %v", err)
	}
	if len(accounts) != 2 || len(balances) != 4 || len(positions) != 2 || len(performance) != 2 {
		t.Fatalf("unexpected snapshot sizes accounts=%d balances=%d positions=%d performance=%d", len(accounts), len(balances), len(positions), len(performance))
	}
	if accounts[0].DisplayName != "Account U1" || !balances[0].IsBaseCurrency || performance[1].DailyPnL != 10 {
		t.Fatalf("unexpected stored snapshot accounts=%#v balances=%#v performance=%#v", accounts, balances, performance)
	}
	statuses, err := svc.SyncStatus(context.Background())
	if err != nil {
		t.Fatalf("list sync statuses: %v", err)
	}
	if len(statuses) != 9 {
		t.Fatalf("expected account discovery plus four statuses per account, got %#v", statuses)
	}
	assertSyncStatus(t, statuses, "", store.SyncDataAccounts, "", 2)
	for _, accountID := range []string{"U1", "U2"} {
		assertSyncStatus(t, statuses, accountID, store.SyncDataAccountDetails, "", 1)
		assertSyncStatus(t, statuses, accountID, store.SyncDataCashBalances, "", 2)
		assertSyncStatus(t, statuses, accountID, store.SyncDataPositions, "", 1)
		assertSyncStatus(t, statuses, accountID, store.SyncDataDailyPerformance, "", 1)
	}
}

func TestSyncBrokerPrefersConsistentAccountSnapshots(t *testing.T) {
	provider := &fakeSnapshotBroker{}
	svc := newTestSyncService(t, StaticSource("SCHWAB", 0, testPortfolioProvider(t, provider)))

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("sync broker snapshot: %v", err)
	}
	if provider.snapshotCalls != 1 || provider.listAccountCalls != 0 || provider.detailCalls != 0 ||
		provider.balanceCalls != 0 || provider.positionCalls != 0 || provider.performanceCalls != 0 {
		t.Fatalf("sync did not use bulk snapshot exclusively: %#v", provider)
	}
	accounts, err := svc.store.ListBrokerAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	balances, err := svc.store.ListBrokerAccountBalances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	positions, err := svc.AggregatedPositions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	performance, err := svc.store.ListBrokerAccountPerformance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].DisplayName != "Schwab S1" || len(balances) != 1 || balances[0].TotalCashValue != 250 ||
		len(positions) != 1 || positions[0].Symbol != "AAPL" || len(performance) != 1 || performance[0].DailyPnL != 12 {
		t.Fatalf("unexpected stored snapshot accounts=%#v balances=%#v positions=%#v performance=%#v", accounts, balances, positions, performance)
	}
}

func TestFailedBrokerCapabilityKeepsPreviousSnapshot(t *testing.T) {
	provider := &fakeBroker{}
	svc := newTestSyncService(t, StaticSource("IBKR", 0, testPortfolioProvider(t, provider)))
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("prime broker snapshot: %v", err)
	}

	provider.failAccount = "U2"
	provider.positionVersion = "-NEW"
	if err := svc.Sync(context.Background()); err == nil {
		t.Fatal("expected account capability failure")
	}
	positions, err := svc.AggregatedPositions(context.Background())
	if err != nil {
		t.Fatalf("list previous positions: %v", err)
	}
	if len(positions) != 2 {
		t.Fatalf("expected previous complete snapshot, got %#v", positions)
	}
	positionsByAccount := map[string]AggregatedPosition{}
	for _, position := range positions {
		positionsByAccount[position.Legs[0].Account] = position
	}
	if positionsByAccount["U1"].Symbol != "ASSETU1-NEW" || positionsByAccount["U2"].Symbol != "ASSETU2" {
		t.Fatalf("expected U1 positions refreshed and U2 positions retained, got %#v", positionsByAccount)
	}
	statuses, err := svc.SyncStatus(context.Background())
	if err != nil {
		t.Fatalf("list sync statuses: %v", err)
	}
	assertSyncStatus(t, statuses, "U2", store.SyncDataPositions, "positions unavailable", 1)
	assertSyncStatus(t, statuses, "U2", store.SyncDataCashBalances, "", 2)
	assertSyncStatus(t, statuses, "U2", store.SyncDataDailyPerformance, "", 1)
}

func TestFailedSourceDoesNotBlockFollowingSource(t *testing.T) {
	failing := &fakeBroker{failList: true}
	healthy := &fakeBroker{}
	svc := newTestSyncService(t,
		StaticSource("IBKR", 0, testPortfolioProvider(t, failing)),
		StaticSource("IBKR", 0, testPortfolioProvider(t, healthy)),
	)

	err := svc.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "account discovery unavailable") {
		t.Fatalf("sync error = %v, want failing source error", err)
	}
	if healthy.listAccountCalls != 1 || healthy.detailCalls != 2 || healthy.positionCalls != 2 {
		t.Fatalf("healthy source was blocked by previous failure: %#v", healthy)
	}
	statuses, statusErr := svc.SyncStatus(context.Background())
	if statusErr != nil {
		t.Fatalf("list sync statuses: %v", statusErr)
	}
	failingConnectionID := svc.sources[0].ConnectionID
	for _, status := range statuses {
		if status.ConnectionID == failingConnectionID && status.DataType == store.SyncDataAccounts &&
			strings.Contains(status.LastError, "account discovery unavailable") {
			return
		}
	}
	t.Fatalf("missing account discovery error for connection %d: %#v", failingConnectionID, statuses)
}

func assertSyncStatus(
	t *testing.T,
	statuses []store.BrokerSyncStatus,
	account string,
	dataType store.SyncDataType,
	errorContains string,
	itemCount int,
) {
	t.Helper()
	for _, status := range statuses {
		if status.Broker != "IBKR" || status.Account != account || status.DataType != dataType {
			continue
		}
		if errorContains == "" && status.LastError != "" {
			t.Fatalf("unexpected %s/%s error: %s", account, dataType, status.LastError)
		}
		if errorContains != "" && !strings.Contains(status.LastError, errorContains) {
			t.Fatalf("expected %s/%s error containing %q, got %q", account, dataType, errorContains, status.LastError)
		}
		if status.ItemCount != itemCount {
			t.Fatalf("unexpected %s/%s item count: %d", account, dataType, status.ItemCount)
		}
		return
	}
	t.Fatalf("missing IBKR/%s/%s sync status in %#v", account, dataType, statuses)
}
