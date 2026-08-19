package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/alpaca"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

type mutableAlpacaRuntime struct {
	brokerConnectionRuntime
	session  broker.BrokerSession
	acquired int
	released int
}

type alpacaRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn alpacaRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func (r *mutableAlpacaRuntime) AcquireDefaultSession(string) (broker.BrokerSession, func()) {
	r.acquired++
	return r.session, func() { r.released++ }
}

type testAlpacaSession struct{ *alpaca.Client }

func (*testAlpacaSession) ConnectionID() int64  { return 1 }
func (*testAlpacaSession) ProviderCode() string { return "ALPACA" }
func (*testAlpacaSession) Health(context.Context) (broker.ConnectionHealth, error) {
	return broker.ConnectionHealth{}, nil
}
func (*testAlpacaSession) Close(context.Context) error { return nil }

func TestAlpacaStatusResolvesClientAfterRuntimeReload(t *testing.T) {
	runtime := &mutableAlpacaRuntime{}
	router := gin.New()
	router.GET("/alpaca/status", alpacaStatus(currentBrokerSession(runtime, "ALPACA")))

	request := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/alpaca/status", nil))
		return response
	}
	if response := request(); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"available":false`) {
		t.Fatalf("status before loading Alpaca: %d %s", response.Code, response.Body.String())
	}
	client := alpaca.New(
		config.AlpacaConfig{APIKey: "key", APISecret: "secret", BaseURL: "https://api.test"},
		alpaca.WithHTTPClient(&http.Client{Transport: alpacaRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     http.StatusText(http.StatusOK),
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"account-1","currency":"USD","equity":"1000"}`)),
			}, nil
		})}),
	)
	runtime.session = &testAlpacaSession{Client: client}
	if response := request(); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"configured":true`) {
		t.Fatalf("status after loading Alpaca: %d %s", response.Code, response.Body.String())
	}
	if runtime.acquired != runtime.released {
		t.Fatalf("session leases: acquired=%d released=%d", runtime.acquired, runtime.released)
	}
}

func TestSaveAlpacaConfigStoresConnectionSecrets(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "alpaca-config.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	reloads := 0
	router := gin.New()
	router.GET("/alpaca/config", alpacaConfiguration(st))
	router.PUT("/alpaca/config", saveAlpacaConfiguration(st, func(context.Context) error {
		reloads++
		return nil
	}))
	request := func(method, body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/alpaca/config", bytes.NewBufferString(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		router.ServeHTTP(response, req)
		return response
	}

	if response := request(http.MethodGet, ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"available":false`) {
		t.Fatalf("empty config response: %d %s", response.Code, response.Body.String())
	}
	response := request(http.MethodPut, `{
		"api_key":"api-key",
		"api_secret":"api-secret",
		"base_url":"https://paper-api.alpaca.markets"
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("save Alpaca config: %d %s", response.Code, response.Body.String())
	}
	var saved alpacaConfigResponse
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Available || !saved.APIKeyConfigured || !saved.APISecretConfigured || saved.ConnectionID == 0 {
		t.Fatalf("unexpected saved config response: %#v", saved)
	}
	if strings.Contains(response.Body.String(), "api-key") || strings.Contains(response.Body.String(), "api-secret") {
		t.Fatalf("config response leaked credentials: %s", response.Body.String())
	}
	connection, err := st.GetBrokerConnectionRuntimeConfig(t.Context(), saved.ConnectionID)
	if err != nil || connection.Secrets["api_key"] != "api-key" || connection.Secrets["api_secret"] != "api-secret" {
		t.Fatalf("connection secrets were not stored: connection=%#v err=%v", connection, err)
	}

	response = request(http.MethodPut, `{"base_url":"https://api.alpaca.markets"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("update base URL while preserving credentials: %d %s", response.Code, response.Body.String())
	}
	connection, err = st.GetBrokerConnectionRuntimeConfig(t.Context(), saved.ConnectionID)
	if err != nil || connection.Secrets["api_key"] != "api-key" || connection.Config["base_url"] != "https://api.alpaca.markets" || reloads != 2 {
		t.Fatalf("Alpaca config update was not idempotent: connection=%#v reloads=%d err=%v", connection, reloads, err)
	}
}
