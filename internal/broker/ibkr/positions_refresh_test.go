package ibkr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

func TestFreshPositionsInvalidatesAndReadsAllPages(t *testing.T) {
	var invalidated atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing proxy token")
		}
		switch r.URL.Path {
		case "/v1/api/portfolio/U1/positions/invalidate":
			if r.Method != http.MethodPost {
				t.Error("wrong invalidation method")
			}
			invalidated.Store(true)
			fmt.Fprint(w, `[]`)
		case "/v1/api/portfolio/U1/positions/0":
			if !invalidated.Load() {
				t.Error("read cached positions before invalidation")
			}
			rows := make([]map[string]any, 100)
			for i := range rows {
				rows[i] = map[string]any{"acctId": "U1", "conid": i + 1, "ticker": fmt.Sprintf("S%d", i), "position": 1, "currency": "USD"}
			}
			_ = json.NewEncoder(w).Encode(rows)
		case "/v1/api/portfolio/U1/positions/1":
			fmt.Fprint(w, `[{"acctId":"U1","conid":101,"ticker":"LAST","position":3,"currency":"USD"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := New(config.IBKRConfig{GatewayURL: server.URL, GatewayToken: "secret"})
	positions, err := c.ListAccountPositions(broker.WithFreshPositions(context.Background()), "U1")
	if err != nil || len(positions) != 101 || positions[100].Quantity != 3 {
		t.Fatalf("positions=%d err=%v", len(positions), err)
	}
}

func TestPositionsNeverReturnPartialOrMalformedSnapshot(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `[{"acctId":"U2","conid":1,"ticker":"A","position":1,"currency":"USD"}]`, `[{"acctId":"U1","conid":1,"position":1,"currency":"USD"}]`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			positions, err := New(config.IBKRConfig{GatewayURL: server.URL}).ListAccountPositions(context.Background(), "U1")
			if err == nil || positions != nil {
				t.Fatal("unsafe snapshot accepted")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/api/portfolio/U1/positions/0" {
			rows := make([]map[string]any, 100)
			for i := range rows {
				rows[i] = map[string]any{"conid": i + 1, "ticker": "A", "position": 1, "currency": "USD"}
			}
			_ = json.NewEncoder(w).Encode(rows)
		} else {
			http.Error(w, "failed second page", 500)
		}
	}))
	defer server.Close()
	positions, err := New(config.IBKRConfig{GatewayURL: server.URL}).ListAccountPositions(context.Background(), "U1")
	if err == nil || positions != nil {
		t.Fatal("failed second page exposed partial snapshot")
	}
}
