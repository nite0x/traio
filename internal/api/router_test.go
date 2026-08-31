package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/portfolio"
	"github.com/nite/traio/internal/store"
)

func assumeGatewayPortsAvailable(int) bool { return true }

func TestBrokerSyncRoutes(t *testing.T) {
	router := NewRouter(Deps{}, ServerControl{})
	routes := map[string]bool{}
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}

	for _, route := range []string{
		"GET /api/v1/brokers",
		"GET /api/v1/brokers/IBKR/manager/health",
		"GET /api/v1/brokers/IBKR/manager/gateways",
		"GET /api/v1/brokers/IBKR/manager/gateways/:gateway_id/status",
		"POST /api/v1/brokers/:code/connections",
		"GET /api/v1/broker-connections/:connection_id",
		"GET /api/v1/broker-connections/:connection_id/delete-impact",
		"DELETE /api/v1/broker-connections/:connection_id",
		"POST /api/v1/broker-connections/:connection_id/login",
		"GET /api/v1/broker-connections/:connection_id/auth/status",
		"POST /api/v1/broker-connections/:connection_id/oauth/exchange",
		"GET /api/v1/broker-connections/:connection_id/accounts",
		"POST /api/v1/broker-connections/:connection_id/sync",
		"GET /api/v1/broker-accounts",
		"GET /api/v1/portfolio/overview",
		"GET /api/v1/portfolio/positions",
		"GET /api/v1/portfolio/positions/:position_id",
		"GET /api/v1/portfolio/cash",
		"POST /api/v1/portfolio/sync",
		"GET /api/v1/portfolio/sync-status",
		"GET /api/v1/schwab/config",
		"PUT /api/v1/schwab/config",
		"GET /api/v1/alpaca/config",
		"PUT /api/v1/alpaca/config",
	} {
		if !routes[route] {
			t.Fatalf("missing broker sync route %s", route)
		}
	}
	for _, removed := range []string{
		"GET /api/v1/positions",
		"GET /api/v1/portfolio/snapshot",
		"POST /api/v1/brokers/sync",
		"GET /api/v1/brokers/sync-status",
		"GET /api/v1/ibkr/gateway/status",
		"GET /api/v1/ibkr/gateway/login",
		"POST /api/v1/ibkr/gateway/start",
		"POST /api/v1/ibkr/gateway/stop",
		"POST /api/v1/ibkr/gateway/reconnect",
		"POST /api/v1/ibkr/gateway/upgrade",
		"POST /api/v1/ibkr/gateway/rollback",
	} {
		if routes[removed] {
			t.Fatalf("legacy route still registered: %s", removed)
		}
	}
}

func TestPortfolioPageAPIsReadStoredProjections(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "snapshot-api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "snapshot", Name: "Snapshot", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if err := st.ReplaceBrokerConnectionAccounts(t.Context(), connection.ID, []broker.Account{{ID: "U1", BaseCurrency: "USD"}}); err != nil {
		t.Fatalf("store account: %v", err)
	}
	if err := st.ReplaceBrokerConnectionCashBalances(t.Context(), connection.ID, "U1", []broker.CashBalance{{Currency: "USD", Total: 400, ExchangeRate: 1, IsBaseCurrency: true}}); err != nil {
		t.Fatalf("store cash: %v", err)
	}
	if err := st.ReplaceBrokerConnectionAccountPositions(t.Context(), connection.ID, "U1", []broker.Position{{Symbol: "AAPL", Quantity: 2, MarketValue: 600, Currency: "USD"}}); err != nil {
		t.Fatalf("store positions: %v", err)
	}
	if err := st.ReplaceBrokerConnectionAccountPerformance(t.Context(), connection.ID, broker.DailyPerformance{AccountID: "U1", NetLiquidation: 1000, MarketValue: 600, DailyPnL: 10}); err != nil {
		t.Fatalf("store performance: %v", err)
	}

	router := NewRouter(Deps{BrokerSync: portfolio.NewSyncService(st)}, ServerControl{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portfolio/overview", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"net_asset_value":1000`) {
		t.Fatalf("unexpected overview response: %d %s", res.Code, res.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/portfolio/positions", nil)
	res = httptest.NewRecorder()
	router.ServeHTTP(res, req)
	var positions []portfolio.AggregatedPosition
	if err := json.Unmarshal(res.Body.Bytes(), &positions); err != nil || len(positions) != 1 {
		t.Fatalf("positions API did not expose canonical positions: positions=%#v err=%v", positions, err)
	}
	positionID := positions[0].PositionID
	for _, path := range []string{
		"/api/v1/portfolio/positions/" + positionID,
		"/api/v1/portfolio/cash",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("GET %s failed: %d %s", path, res.Code, res.Body.String())
		}
	}
}

func TestDeleteBrokerConnectionRequiresAccountConfirmation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "primary", Name: "Primary", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if err := st.ReplaceBrokerConnectionAccounts(t.Context(), connection.ID, []broker.Account{{ID: "U1"}}); err != nil {
		t.Fatalf("store account: %v", err)
	}
	router := NewRouter(Deps{Brokers: st}, ServerControl{})
	for _, path := range []string{
		"/api/v1/broker-connections/" + strconv.FormatInt(connection.ID, 10),
		"/api/v1/broker-connections/" + strconv.FormatInt(connection.ID, 10) + "/accounts",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("GET %s failed: %d %s", path, res.Code, res.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/broker-connections/"+strconv.FormatInt(connection.ID, 10), nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected delete conflict, got %d: %s", res.Code, res.Body.String())
	}
	var conflict map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &conflict); err != nil || conflict["error"] != "connection_has_accounts" {
		t.Fatalf("unexpected conflict response: %s err=%v", res.Body.String(), err)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/broker-connections/"+strconv.FormatInt(connection.ID, 10)+"?confirm=true", bytes.NewReader(nil))
	res = httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("confirmed delete failed: %d %s", res.Code, res.Body.String())
	}
}

func TestLocalAPIAuthenticationIsEnforced(t *testing.T) {
	const token = "test-token"

	tests := []struct {
		name  string
		host  string
		token string
		want  int
	}{
		{name: "missing token", host: "127.0.0.1:38180", want: http.StatusUnauthorized},
		{name: "wrong token", host: "127.0.0.1:38180", token: "wrong", want: http.StatusUnauthorized},
		{name: "valid token", host: "127.0.0.1:38180", token: token, want: http.StatusOK},
		{name: "remote host", host: "example.com", token: token, want: http.StatusMisdirectedRequest},
		{name: "allowed remote host", host: "api.example.com", token: token, want: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := NewRouter(Deps{APIToken: token, AllowedAPIHosts: []string{"api.example.com"}}, ServerControl{})
			req := httptest.NewRequest(http.MethodGet, "/api/v1/server/status", nil)
			req.Host = tt.host
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, res.Code, res.Body.String())
			}
		})
	}
}

func TestWebsocketToken(t *testing.T) {
	if got := websocketToken("traio, abc123"); got != "abc123" {
		t.Fatalf("unexpected token %q", got)
	}
	for _, header := range []string{"", "abc123", "other, abc123", "traio, one, two"} {
		if got := websocketToken(header); got != "" {
			t.Fatalf("expected invalid header %q to be rejected, got %q", header, got)
		}
	}
}
