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
				"id":             "acct-1",
				"account_number": "PA123",
				"currency":       "USD",
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
