package store

import (
	"context"
	"testing"

	"github.com/nite/traio/internal/broker"
)

func TestReplaceBrokerCashBalancesStoresEveryAccount(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	connection := createTestConnection(t, st, "default")
	accounts := []broker.Account{
		{ID: "U1", BaseCurrency: "USD"},
		{ID: "U2", BaseCurrency: "TWD"},
	}
	if err := st.ReplaceBrokerConnectionAccounts(ctx, connection.ID, accounts); err != nil {
		t.Fatalf("replace accounts: %v", err)
	}
	if err := st.ReplaceBrokerConnectionCashBalances(ctx, connection.ID, "U1", []broker.CashBalance{
		{Currency: "USD", Total: 500, Settled: 450, ExchangeRate: 1, IsBaseCurrency: true},
	}); err != nil {
		t.Fatalf("replace U1 cash balances: %v", err)
	}
	if err := st.ReplaceBrokerConnectionCashBalances(ctx, connection.ID, "U2", []broker.CashBalance{
		{Currency: "TWD", Total: 12000, Settled: 11000, ExchangeRate: 1, IsBaseCurrency: true},
	}); err != nil {
		t.Fatalf("replace U2 cash balances: %v", err)
	}

	balances, err := st.ListBrokerAccountBalances(ctx)
	if err != nil {
		t.Fatalf("list account balances: %v", err)
	}
	if len(balances) != 2 {
		t.Fatalf("expected two account balances, got %#v", balances)
	}
	if balances[0].Broker != "IBKR" || balances[0].Account != "U1" || balances[0].Currency != "USD" {
		t.Fatalf("unexpected first account balance: %#v", balances[0])
	}
	if balances[1].Account != "U2" || balances[1].Currency != "TWD" || balances[1].TotalCashValue != 12000 {
		t.Fatalf("unexpected second account balance: %#v", balances[1])
	}

	if err := st.ReplaceBrokerConnectionAccounts(ctx, connection.ID, accounts[1:]); err != nil {
		t.Fatalf("replace account list again: %v", err)
	}
	balances, err = st.ListBrokerAccountBalances(ctx)
	if err != nil {
		t.Fatalf("list replaced account balances: %v", err)
	}
	if len(balances) != 1 || balances[0].Account != "U2" {
		t.Fatalf("expected stale account balance to be removed, got %#v", balances)
	}
}
