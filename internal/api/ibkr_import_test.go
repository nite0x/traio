package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/nite/traio/internal/config"
	appRuntime "github.com/nite/traio/internal/runtime"
	"github.com/nite/traio/internal/store"
)

func TestSaveManagerImportsAllConnectionsAndRuntimeUsesSavedTokens(t *testing.T) {
	var expectedToken atomic.Value
	expectedToken.Store("proxy-one")
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+expectedToken.Load().(string) {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/api/tickle" {
			_, _ = w.Write([]byte(`{"iserver":{"authStatus":{"authenticated":true,"connected":true,"established":true}},"selectedAccount":"DU123"}`))
		} else {
			_, _ = w.Write([]byte(`{"authenticated":true,"connected":true,"established":true,"selectedAccount":"DU123"}`))
		}
	}))
	defer proxy.Close()
	invalid := false
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer manager-key" {
			http.Error(w, "unauthorized", 401)
			return
		}
		url := "https://stopped.invalid"
		if invalid {
			url += "/bad-path"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"connections": []map[string]any{
			{"id": "paper", "proxy_url": proxy.URL, "proxy_token": expectedToken.Load()},
			{"id": "stopped", "proxy_url": url, "proxy_token": "proxy-two"},
		}})
	}))
	defer manager.Close()
	dbPath := filepath.Join(t.TempDir(), "import.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Existing manually configured connection must keep its ID and custom data.
	_, err = st.UpdateBrokerProviderConfig(t.Context(), "IBKR", map[string]any{"manager_url": manager.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "default", Name: "My paper account", Enabled: true,
		Config:  map[string]any{"gateway_id": "paper", "gateway_url": proxy.URL, "flex_query_id": "query"},
		Secrets: map[string]string{"gateway_token": "old-token", "flex_token": "flex-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	live, err := appRuntime.BuildConnectionManager(config.Config{}, st, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reloads := 0
	router := NewRouter(Deps{Brokers: st, OnBrokersChanged: func(ctx context.Context) error { reloads++; return live.Reload(ctx) }}, ServerControl{})
	save := func(secrets map[string]string, want int) {
		t.Helper()
		body, _ := json.Marshal(providerConfigRequest{Config: map[string]any{"manager_url": manager.URL + "/"}, Secrets: secrets})
		r := httptest.NewRequest(http.MethodPut, "/api/v1/brokers/IBKR", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("save: %d %s", w.Code, w.Body.String())
		}
		for _, secret := range []string{"manager-key", "proxy-one", "proxy-two", "rotated", "flex-secret"} {
			if bytes.Contains(w.Body.Bytes(), []byte(secret)) {
				t.Fatal("save response exposed a secret")
			}
		}
	}
	save(map[string]string{"manager_api_token": "manager-key"}, 200)
	if proxyCalls.Load() != 0 {
		t.Fatal("import must not require a running or logged-in Gateway")
	}
	connections, err := st.ListBrokerConnections(t.Context())
	if err != nil || len(connections) != 2 {
		t.Fatalf("imported count: %d err=%v", len(connections), err)
	}
	check := func(wantToken string) {
		t.Helper()
		stored, err := st.GetBrokerConnectionRuntimeConfig(t.Context(), legacy.ID)
		if err != nil || stored.Secrets["gateway_token"] != wantToken || stored.Secrets["flex_token"] != "flex-secret" || stored.Name != "My paper account" || stored.Config["flex_query_id"] != "query" || stored.Config["manager_url"] != manager.URL {
			t.Fatal("import did not preserve identity/config or update credentials")
		}
		action, err := live.ConnectionLoginStatus(t.Context(), legacy.ID)
		if err != nil || !action.Authenticated {
			t.Fatalf("runtime did not use imported token: %v", err)
		}
	}
	check("proxy-one")
	expectedToken.Store("rotated")
	save(nil, 200)
	check("rotated")
	connections, _ = st.ListBrokerConnections(t.Context())
	if len(connections) != 2 || reloads != 2 {
		t.Fatal("repeat save duplicated connections or missed runtime reload")
	}
	invalid = true
	save(nil, http.StatusBadGateway)
	if reloads != 2 {
		t.Fatal("invalid discovery must not reload or persist")
	}
	check("rotated")
	// A second store instance proves credentials survive a process/store reopen.
	reopened, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, err := reopened.GetBrokerConnectionRuntimeConfig(t.Context(), legacy.ID)
	if err != nil || persisted.Secrets["gateway_token"] != "rotated" {
		t.Fatal("instance token was not persisted")
	}
}
