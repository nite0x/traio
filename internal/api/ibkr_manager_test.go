package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/nite/traio/internal/store"
)

func TestIBKRManagerRoutesAndConnectionSelection(t *testing.T) {
	const managerToken = "manager-secret"
	const gatewayToken = "proxy-secret"
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+gatewayToken {
			w.Header().Set("WWW-Authenticate", `Basic realm="IBKR Gateway"`)
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/api/tickle":
			_, _ = w.Write([]byte(`{"iserver":{"authStatus":{"authenticated":true}},"selectedAccount":"DU123"}`))
		case "/v1/api/iserver/auth/status":
			_, _ = w.Write([]byte(`{"authenticated":true,"selectedAccount":"DU123"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()

	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && r.Header.Get("Authorization") != "Bearer "+managerToken {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/management/v1/gateways":
			_ = json.NewEncoder(w).Encode(map[string]any{"gateways": []map[string]any{{
				"id": "paper", "proxy_url": gateway.URL + "/", "proxy_listening": true, "proxy_token_configured": true,
				"status": map[string]any{"running": true, "authenticated": true, "account": "DU123", "state": "authenticated"},
			}}})
		case "/management/v1/gateways/paper/status":
			_, _ = w.Write([]byte(`{"running":true,"authenticated":true,"account":"DU123","state":"authenticated"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer manager.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "ibkr-manager-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	router := NewRouter(Deps{Brokers: st}, ServerControl{})

	providerBody, _ := json.Marshal(map[string]any{
		"config":  map[string]any{"manager_url": manager.URL},
		"secrets": map[string]string{"manager_api_token": managerToken},
	})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/brokers/IBKR", bytes.NewReader(providerBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("save provider = %d: %s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte(managerToken)) {
		t.Fatalf("provider response leaked manager token: %s", response.Body.String())
	}

	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/api/v1/brokers/IBKR/manager/health", want: `"status":"ok"`},
		{path: "/api/v1/brokers/IBKR/manager/gateways", want: `"id":"paper"`},
		{path: "/api/v1/brokers/IBKR/manager/gateways/paper/status", want: `"account":"DU123"`},
	} {
		request = httptest.NewRequest(http.MethodGet, test.path, nil)
		response = httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(test.want)) {
			t.Fatalf("GET %s = %d %s", test.path, response.Code, response.Body.String())
		}
	}

	connectionBody, _ := json.Marshal(map[string]any{
		"connection_key": "ibkr-paper",
		"name":           "IBKR Paper",
		"environment":    "paper",
		"auth_type":      "interactive",
		"enabled":        true,
		"config":         map[string]any{"gateway_id": "paper", "gateway_url": "https://attacker.invalid"},
		"secrets":        map[string]string{"gateway_token": gatewayToken},
	})
	request = httptest.NewRequest(http.MethodPost, "/api/v1/brokers/IBKR/connections", bytes.NewReader(connectionBody))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create connection = %d: %s", response.Code, response.Body.String())
	}
	var created store.BrokerConnection
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Config["gateway_id"] != "paper" || created.Config["gateway_url"] != gateway.URL {
		t.Fatalf("connection did not resolve selected Manager instance: %#v", created.Config)
	}
	if created.Status != store.BrokerConnectionStatusConnected || created.ProviderUserID != "DU123" {
		t.Fatalf("authenticated Gateway was not reflected on the connection: %#v", created)
	}
	runtime, err := st.GetBrokerConnectionRuntimeConfig(t.Context(), created.ID)
	if err != nil || runtime.Secrets["gateway_token"] != gatewayToken {
		t.Fatalf("gateway token was not stored: %#v err=%v", runtime.Secrets, err)
	}

	invalidBody, _ := json.Marshal(map[string]any{
		"connection_key": "ibkr-invalid", "name": "Invalid IBKR", "enabled": true,
		"config":  map[string]any{"gateway_id": "paper"},
		"secrets": map[string]string{"gateway_token": "wrong-token"},
	})
	request = httptest.NewRequest(http.MethodPost, "/api/v1/brokers/IBKR/connections", bytes.NewReader(invalidBody))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || !bytes.Contains(response.Body.Bytes(), []byte("gateway_token")) {
		t.Fatalf("invalid proxy token = %d: %s", response.Code, response.Body.String())
	}
}
