package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/schwab"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

type mutableSchwabRuntime struct {
	brokerConnectionRuntime
	session  broker.BrokerSession
	acquired int
	released int
}

func (r *mutableSchwabRuntime) AcquireDefaultSession(string) (broker.BrokerSession, func()) {
	r.acquired++
	return r.session, func() { r.released++ }
}

type testSchwabSession struct{ *schwab.Client }

func (*testSchwabSession) ConnectionID() int64  { return 1 }
func (*testSchwabSession) ProviderCode() string { return "SCHWAB" }
func (*testSchwabSession) Health(context.Context) (broker.ConnectionHealth, error) {
	return broker.ConnectionHealth{}, nil
}
func (*testSchwabSession) Close(context.Context) error { return nil }

func TestSchwabStatusResolvesClientAfterRuntimeReload(t *testing.T) {
	runtime := &mutableSchwabRuntime{}
	router := gin.New()
	router.GET("/schwab/status", schwabStatus(currentBrokerSession(runtime, "SCHWAB")))

	request := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/schwab/status", nil))
		return response
	}

	if response := request(); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"available":false`) ||
		!strings.Contains(response.Body.String(), `"authenticated":false`) {
		t.Fatalf("status before loading Schwab: %d %s", response.Code, response.Body.String())
	}

	client := schwab.New(config.SchwabConfig{})
	client.SetToken(schwab.Token{AccessToken: "access", ExpiresAt: time.Now().Add(time.Hour)})
	runtime.session = &testSchwabSession{Client: client}
	if response := request(); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"authenticated":true`) {
		t.Fatalf("status after loading Schwab: %d %s", response.Code, response.Body.String())
	}

	runtime.session = nil
	if response := request(); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"available":false`) {
		t.Fatalf("status after unloading Schwab: %d %s", response.Code, response.Body.String())
	}
	if runtime.acquired != runtime.released {
		t.Fatalf("session leases: acquired=%d released=%d", runtime.acquired, runtime.released)
	}
}

func TestSaveSchwabConfigStoresProviderSecretsAndCreatesConnection(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "schwab-config.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	reloads := 0
	router := gin.New()
	router.GET("/schwab/config", schwabConfig(st))
	router.PUT("/schwab/config", saveSchwabConfig(st, func(context.Context) error {
		reloads++
		return nil
	}))
	request := func(method, body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/schwab/config", bytes.NewBufferString(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		router.ServeHTTP(response, req)
		return response
	}

	if response := request(http.MethodGet, ""); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"available":false`) {
		t.Fatalf("empty config response: %d %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPut, `{"client_id":"client-only","redirect_uri":"https://127.0.0.1/callback"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("partial credentials should fail: %d %s", response.Code, response.Body.String())
	}

	response := request(http.MethodPut, `{
		"client_id":"client-id",
		"client_secret":"client-secret",
		"redirect_uri":"https://127.0.0.1/callback"
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("save Schwab config: %d %s", response.Code, response.Body.String())
	}
	var saved schwabConfigResponse
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Available || !saved.ClientIDConfigured || !saved.ClientSecretConfigured || saved.ConnectionID == 0 || !saved.ConnectionEnabled {
		t.Fatalf("unexpected saved config response: %#v", saved)
	}
	if strings.Contains(response.Body.String(), "client-id") || strings.Contains(response.Body.String(), "client-secret") {
		t.Fatalf("config response leaked credentials: %s", response.Body.String())
	}
	provider, err := st.GetBrokerProviderRuntimeConfig(t.Context(), "SCHWAB")
	if err != nil || provider.Secrets["client_id"] != "client-id" || provider.Secrets["client_secret"] != "client-secret" ||
		provider.Config["redirect_uri"] != "https://127.0.0.1/callback" {
		t.Fatalf("provider config was not stored: provider=%#v err=%v", provider, err)
	}
	connections, err := st.ListBrokerConnections(t.Context())
	if err != nil || len(connections) != 1 || connections[0].ProviderCode != "SCHWAB" || connections[0].AuthType != "oauth" {
		t.Fatalf("Schwab connection was not created: connections=%#v err=%v", connections, err)
	}

	response = request(http.MethodPut, `{"redirect_uri":"https://127.0.0.1/updated"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("update redirect while preserving credentials: %d %s", response.Code, response.Body.String())
	}
	provider, err = st.GetBrokerProviderRuntimeConfig(t.Context(), "SCHWAB")
	connections, connectionsErr := st.ListBrokerConnections(t.Context())
	if err != nil || connectionsErr != nil || provider.Secrets["client_id"] != "client-id" ||
		provider.Secrets["client_secret"] != "client-secret" || provider.Config["redirect_uri"] != "https://127.0.0.1/updated" ||
		len(connections) != 1 || reloads != 2 {
		t.Fatalf("Schwab config update was not idempotent: provider=%#v connections=%#v reloads=%d err=%v/%v", provider, connections, reloads, err, connectionsErr)
	}
}
