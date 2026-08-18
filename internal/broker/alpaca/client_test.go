package alpaca_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nite/traio/internal/broker/alpaca"
	"github.com/nite/traio/internal/config"
)

func TestListPositions(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/account":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id":                "acct-1",
				"account_number":    "PA123",
				"status":            "ACTIVE",
				"currency":          "USD",
				"cash":              "500",
				"equity":            "720",
				"last_equity":       "700",
				"buying_power":      "1000",
				"long_market_value": "220",
			})
		case "/v2/positions":
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{
					"symbol":          "AAPL",
					"qty":             "2",
					"avg_entry_price": "100",
					"current_price":   "110",
					"market_value":    "220",
					"unrealized_pl":   "20",
					"side":            "long",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := alpaca.New(config.AlpacaConfig{
		APIKey:    "key",
		APISecret: "secret",
		BaseURL:   srv.URL,
	})

	positions, err := client.ListPositions(context.Background())
	if err != nil {
		t.Fatalf("ListPositions: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("got %d positions, want 1", len(positions))
	}
	if positions[0].Symbol != "AAPL" || positions[0].Quantity != 2 {
		t.Fatalf("unexpected position: %+v", positions[0])
	}

	accounts, err := client.ListAccounts(context.Background())
	if err != nil || len(accounts) != 1 || accounts[0].ID != "PA123" {
		t.Fatalf("unexpected accounts: accounts=%+v err=%v", accounts, err)
	}
	balances, err := client.GetCashBalances(context.Background(), "PA123")
	if err != nil || len(balances) != 1 || balances[0].Total != 500 {
		t.Fatalf("unexpected cash balances: balances=%+v err=%v", balances, err)
	}
	accountPositions, err := client.ListAccountPositions(context.Background(), "PA123")
	if err != nil || len(accountPositions) != 1 || accountPositions[0].Symbol != "AAPL" {
		t.Fatalf("unexpected account positions: positions=%+v err=%v", accountPositions, err)
	}
	performance, err := client.GetDailyPerformance(context.Background(), "PA123")
	if err != nil || performance.DailyPnL != 20 || performance.NetLiquidation != 720 {
		t.Fatalf("unexpected daily performance: performance=%+v err=%v", performance, err)
	}
}

func TestConfiguredSkipsWhenEmpty(t *testing.T) {
	t.Parallel()

	client := alpaca.New(config.AlpacaConfig{})
	positions, err := client.ListPositions(context.Background())
	if err != nil {
		t.Fatalf("ListPositions: %v", err)
	}
	if len(positions) != 0 {
		t.Fatalf("expected empty positions, got %d", len(positions))
	}
}

func TestLoginStatusVerifiesCredentials(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("APCA-API-KEY-ID"); got != "key" {
			t.Errorf("API key header = %q", got)
		}
		if got := r.Header.Get("APCA-API-SECRET-KEY"); got != "secret" {
			t.Errorf("API secret header = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"account_number": "PA123"})
	}))
	defer srv.Close()

	client := alpaca.New(config.AlpacaConfig{APIKey: "key", APISecret: "secret", BaseURL: srv.URL})
	status, err := client.LoginStatus(context.Background())
	if err != nil {
		t.Fatalf("LoginStatus: %v", err)
	}
	if !status.Authenticated || status.AccountID != "PA123" {
		t.Fatalf("unexpected login status: %+v", status)
	}
}

func TestLoginStatusRejectsInvalidCredentials(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := alpaca.New(config.AlpacaConfig{APIKey: "invalid", APISecret: "invalid", BaseURL: srv.URL})
	status, err := client.LoginStatus(context.Background())
	if err == nil {
		t.Fatal("LoginStatus returned nil error for rejected credentials")
	}
	if status.Authenticated {
		t.Fatalf("unexpected authenticated status: %+v", status)
	}
}
