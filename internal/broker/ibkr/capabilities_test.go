package ibkr

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/nite/traio/internal/config"
)

func TestClientBrokerCapabilities(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/portfolio/accounts", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{
				"accountId":"U1",
				"accountAlias":"Primary",
				"currency":"USD",
				"type":"INDIVIDUAL",
				"clearingStatus":"O"
			}
		]`))
	})
	mux.HandleFunc("/v1/api/portfolio/U1/meta", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{
				"id":"U1",
				"displayName":"Primary Account",
				"currency":"USD",
				"type":"INDIVIDUAL",
				"clearingStatus":"O"
			}
		]`))
	})
	mux.HandleFunc("/v1/api/portfolio/U1/ledger", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"USD": {
				"cashbalance": 500.25,
				"settledcash": 450.25,
				"exchangerate": 1,
				"currency": "USD",
				"timestamp": 1700000000
			},
			"BASE": {
				"cashbalance": 500.25,
				"settledcash": 450.25,
				"exchangerate": 1,
				"currency": "BASE",
				"timestamp": 1700000000
			}
		}`))
	})
	mux.HandleFunc("/v1/api/portfolio/U1/positions/0", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{
				"conid":265598,
				"acctId":"U1",
				"contractDesc":"AAPL",
				"position":2,
				"mktPrice":210,
				"mktValue":420,
				"avgCost":190,
				"unrealizedPnl":40,
				"realizedPnl":5,
				"currency":"USD"
			}
		]`))
	})
	mux.HandleFunc("/v1/api/iserver/account/pnl/partitioned", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"upnl": {
				"U1.Core": {
					"dpl": 15.7,
					"nl": 10000,
					"upl": 607,
					"el": 9000,
					"mv": 2500
				}
			}
		}`))
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := New(config.IBKRConfig{GatewayURL: server.URL, SubAccount: "U1"})
	client.httpClient = server.Client()

	accounts, err := client.ListAccounts(t.Context())
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if len(accounts) != 1 || accounts[0].ID != "U1" || accounts[0].DisplayName != "Primary" || accounts[0].Status != "open" {
		t.Fatalf("unexpected accounts: %#v", accounts)
	}

	account, err := client.GetAccount(t.Context(), "U1")
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if account.ID != "U1" || account.DisplayName != "Primary Account" || account.BaseCurrency != "USD" {
		t.Fatalf("unexpected account: %#v", account)
	}

	balances, err := client.GetCashBalances(t.Context(), "U1")
	if err != nil {
		t.Fatalf("get cash balances: %v", err)
	}
	if len(balances) != 1 || !balances[0].IsBaseCurrency || balances[0].Currency != "USD" || balances[0].Settled != 450.25 {
		t.Fatalf("unexpected cash balances: %#v", balances)
	}

	positions, err := client.ListAccountPositions(t.Context(), "U1")
	if err != nil {
		t.Fatalf("list account positions: %v", err)
	}
	if len(positions) != 1 || positions[0].Account != "U1" || positions[0].Symbol != "AAPL" || positions[0].Unrealized != 40 {
		t.Fatalf("unexpected positions: %#v", positions)
	}

	performance, err := client.GetDailyPerformance(t.Context(), "U1")
	if err != nil {
		t.Fatalf("get daily performance: %v", err)
	}
	if performance.AccountID != "U1" || performance.DailyPnL != 15.7 || performance.NetLiquidation != 10000 || performance.MarketValue != 2500 {
		t.Fatalf("unexpected daily performance: %#v", performance)
	}
}

func TestGatewayLoginURL(t *testing.T) {
	gateway := NewGatewayManager(config.IBKRConfig{GatewayURL: "https://localhost:5000/"})
	if got := gateway.LoginURL(); got != "https://localhost:5000/sso/Login" {
		t.Fatalf("unexpected login URL: %s", got)
	}
}

func TestDailyPerformanceReusesOneIBKRSnapshotAcrossAccounts(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/api/iserver/account/pnl/partitioned" {
			http.NotFound(w, r)
			return
		}
		requests.Add(1)
		_, _ = w.Write([]byte(`{
			"upnl": {
				"U1.Core": {"dpl": 10, "nl": 1000},
				"U2.Core": {"dpl": 20, "nl": 2000}
			}
		}`))
	}))
	t.Cleanup(server.Close)

	client := New(config.IBKRConfig{GatewayURL: server.URL})
	client.httpClient = server.Client()
	first, err := client.GetDailyPerformance(t.Context(), "U1")
	if err != nil {
		t.Fatalf("first account performance: %v", err)
	}
	second, err := client.GetDailyPerformance(t.Context(), "U2")
	if err != nil {
		t.Fatalf("second account performance: %v", err)
	}
	if requests.Load() != 1 || first.DailyPnL != 10 || second.DailyPnL != 20 {
		t.Fatalf("unexpected cached performance requests=%d first=%#v second=%#v", requests.Load(), first, second)
	}
}
