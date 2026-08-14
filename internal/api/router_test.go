package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/store"
)

func TestBrokerSyncRoutes(t *testing.T) {
	router := NewRouter(Deps{}, ServerControl{})
	routes := map[string]bool{}
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}

	for _, route := range []string{
		"GET /api/v1/brokers",
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
		"GET /api/v1/ibkr/gateways",
		"POST /api/v1/ibkr/gateways",
		"GET /api/v1/ibkr/gateways/defaults",
		"GET /api/v1/ibkr/gateways/:gateway_id/status",
		"POST /api/v1/ibkr/gateways/:gateway_id/start",
		"POST /api/v1/brokers/sync",
		"GET /api/v1/brokers/sync-status",
		"GET /api/v1/ibkr/gateway/login",
		"POST /api/v1/ibkr/gateway/upgrade",
		"POST /api/v1/ibkr/gateway/rollback",
	} {
		if !routes[route] {
			t.Fatalf("missing broker sync route %s", route)
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

func TestIBKRGatewayResourceDoesNotCreateBrokerConnection(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "gateways-api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	router := NewRouter(Deps{Brokers: st}, ServerControl{})
	body := `{"gateway_key":"paper","name":"Paper","gateway_url":"https://localhost:5680","gateway_dir":"/tmp/traio-test-paper","gateway_port":5680,"lifecycle":"managed","enabled":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ibkr/gateways", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create Gateway: got %d %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/ibkr/gateways", nil)
	res = httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"gateway_key":"paper"`) {
		t.Fatalf("list Gateways: got %d %s", res.Code, res.Body.String())
	}
	connections, err := st.ListBrokerConnections(t.Context())
	if err != nil || len(connections) != 0 {
		t.Fatalf("Gateway API created a broker connection: connections=%#v err=%v", connections, err)
	}
}

func TestIBKRGatewayCreateUsesRuntimeDefaultDirectory(t *testing.T) {
	t.Setenv("TRAIO_IBKR_GATEWAY_LIFECYCLE", "managed")
	runtimeDir := t.TempDir()
	st, err := store.Open(filepath.Join(runtimeDir, "api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	router := NewRouter(Deps{Brokers: st, RuntimeDir: runtimeDir}, ServerControl{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ibkr/gateways/defaults?gateway_key=paper", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), filepath.Join(runtimeDir, "ibkr-gateways", "paper")) {
		t.Fatalf("Gateway defaults: got %d %s", res.Code, res.Body.String())
	}

	body := `{"gateway_key":"paper","name":"Paper","gateway_url":"https://localhost:5682","gateway_port":5682,"enabled":false}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/ibkr/gateways", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create Gateway with defaults: got %d %s", res.Code, res.Body.String())
	}
	var gateway store.IBKRGateway
	if err := json.Unmarshal(res.Body.Bytes(), &gateway); err != nil {
		t.Fatalf("decode Gateway: %v", err)
	}
	if want := filepath.Join(runtimeDir, "ibkr-gateways", "paper"); gateway.GatewayDir != want {
		t.Fatalf("gateway_dir: got %q, want %q", gateway.GatewayDir, want)
	}
	if gateway.Lifecycle != "managed" {
		t.Fatalf("lifecycle: got %q", gateway.Lifecycle)
	}
}

func TestDeleteIBKRGatewayRemovesManagedDefaultDirectory(t *testing.T) {
	runtimeDir := t.TempDir()
	st, err := store.Open(filepath.Join(runtimeDir, "api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	gatewayDir := filepath.Join(runtimeDir, "ibkr-gateways", "paper")
	if err := os.MkdirAll(filepath.Join(gatewayDir, "root"), 0o700); err != nil {
		t.Fatalf("create Gateway directory: %v", err)
	}
	if err := os.WriteFile(gatewayDir+".audit.jsonl", []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("create Gateway audit: %v", err)
	}
	gateway, err := st.UpsertIBKRGateway(t.Context(), store.IBKRGateway{
		GatewayKey: "paper", Name: "Paper", GatewayURL: "https://localhost:5682",
		GatewayDir: gatewayDir, GatewayPort: 5682, Lifecycle: "managed", Enabled: false,
	})
	if err != nil {
		t.Fatalf("create Gateway: %v", err)
	}
	router := NewRouter(Deps{Brokers: st, RuntimeDir: runtimeDir}, ServerControl{})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/ibkr/gateways/"+strconv.FormatInt(gateway.ID, 10)+"?delete_files=true", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("delete Gateway: got %d %s", res.Code, res.Body.String())
	}
	for _, path := range []string{gatewayDir, gatewayDir + ".audit.jsonl"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("Gateway artifact still exists at %s: %v", path, err)
		}
	}
}

func TestDeleteIBKRGatewayRejectsCustomDirectoryRemoval(t *testing.T) {
	runtimeDir := t.TempDir()
	st, err := store.Open(filepath.Join(runtimeDir, "api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	customDir := filepath.Join(t.TempDir(), "custom-gateway")
	if err := os.MkdirAll(customDir, 0o700); err != nil {
		t.Fatalf("create custom directory: %v", err)
	}
	gateway, err := st.UpsertIBKRGateway(t.Context(), store.IBKRGateway{
		GatewayKey: "custom", Name: "Custom", GatewayURL: "https://localhost:5683",
		GatewayDir: customDir, GatewayPort: 5683, Lifecycle: "managed", Enabled: false,
	})
	if err != nil {
		t.Fatalf("create Gateway: %v", err)
	}
	router := NewRouter(Deps{Brokers: st, RuntimeDir: runtimeDir}, ServerControl{})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/ibkr/gateways/"+strconv.FormatInt(gateway.ID, 10)+"?delete_files=true", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("custom directory delete: got %d %s", res.Code, res.Body.String())
	}
	if _, err := st.GetIBKRGateway(t.Context(), gateway.ID); err != nil {
		t.Fatalf("custom Gateway record was deleted: %v", err)
	}
	if _, err := os.Stat(customDir); err != nil {
		t.Fatalf("custom directory was removed: %v", err)
	}
}

type fakeGatewayController struct {
	startCalls int
	startErr   error
	loginURL   string
}

func (f *fakeGatewayController) Status() any { return nil }

func (f *fakeGatewayController) LoginURL() string { return f.loginURL }

func (f *fakeGatewayController) StartGateway(context.Context) error {
	f.startCalls++
	return f.startErr
}

func (f *fakeGatewayController) StopGateway(bool) error { return nil }

func (f *fakeGatewayController) Reconnect() error { return nil }

func (f *fakeGatewayController) Upgrade(context.Context) error { return nil }

func (f *fakeGatewayController) Rollback(context.Context) error { return nil }

func TestIBKRGatewayLoginRedirectsWithoutStartingGateway(t *testing.T) {
	gw := &fakeGatewayController{loginURL: "https://localhost:5680/sso/Login"}
	router := NewRouter(Deps{IBKR: gw}, ServerControl{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ibkr/gateway/login", nil)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusFound {
		t.Fatalf("expected redirect, got status %d body %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Location"); got != gw.loginURL {
		t.Fatalf("unexpected login redirect %q", got)
	}
	if gw.startCalls != 0 {
		t.Fatalf("login redirect must be side-effect free, got %d start calls", gw.startCalls)
	}
}

func TestIBKRGatewayStartReturnsOperationError(t *testing.T) {
	gw := &fakeGatewayController{startErr: context.DeadlineExceeded}
	router := NewRouter(Deps{IBKR: gw}, ServerControl{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ibkr/gateway/start", nil)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadGateway {
		t.Fatalf("expected synchronous gateway error, got %d: %s", res.Code, res.Body.String())
	}
	if gw.startCalls != 1 {
		t.Fatalf("expected one start call, got %d", gw.startCalls)
	}
}

func TestAPIAuthenticationTemporarilyDisabled(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	gw := &fakeGatewayController{}

	tests := []struct {
		name  string
		host  string
		token string
	}{
		{name: "missing token", host: "127.0.0.1:38180"},
		{name: "wrong token", host: "127.0.0.1:38180", token: "wrong"},
		{name: "valid token", host: "127.0.0.1:38180", token: token},
		{name: "remote host", host: "example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := NewRouter(Deps{IBKR: gw, APIToken: token, AllowedAPIHosts: []string{"api.example.com"}}, ServerControl{})
			req := httptest.NewRequest(http.MethodGet, "/api/v1/ibkr/gateway/status", nil)
			req.Host = tt.host
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("expected authentication bypass, got %d: %s", res.Code, res.Body.String())
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
