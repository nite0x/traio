package ibkr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nite/traio/internal/config"
)

func TestBrokerLoginUsesConfiguredGatewayWithoutManagingProcess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api/tickle":
		default:
			t.Fatalf("unexpected session probe %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"iserver":{"authStatus":{"authenticated":true}},"selectedAccount":"U123"}`))
	}))
	defer server.Close()

	adapter := NewBroker(config.IBKRConfig{GatewayURL: server.URL})
	action, err := adapter.BeginLogin(t.Context())
	if err != nil {
		t.Fatalf("begin login: %v", err)
	}
	if action.URL != server.URL+"/sso/Login" || action.Authenticated || action.AccountID != "" {
		t.Fatalf("unexpected login action: %#v", action)
	}
	status, err := adapter.LoginStatus(t.Context())
	if err != nil {
		t.Fatalf("login status: %v", err)
	}
	if !status.Authenticated || status.AccountID != "U123" {
		t.Fatalf("unexpected login status: %#v", status)
	}
}

func TestLoginStatusReportsGatewayProxyAuthenticationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="IBKR Gateway"`)
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	adapter := NewBroker(config.IBKRConfig{GatewayURL: server.URL, GatewayToken: "wrong-token"})
	_, err := adapter.LoginStatus(t.Context())
	if err == nil || !strings.Contains(err.Error(), "gateway_token") {
		t.Fatalf("expected actionable proxy authentication error, got %v", err)
	}
}

func TestLoginStatusAllowsUnauthenticatedGatewaySession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/api/tickle":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"authenticated":false}`))
		case "/v1/api/iserver/auth/status":
			_, _ = w.Write([]byte(`{"authenticated":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewBroker(config.IBKRConfig{GatewayURL: server.URL})
	status, err := adapter.LoginStatus(t.Context())
	if err != nil || status.Authenticated {
		t.Fatalf("expected an unauthenticated session without a proxy error, status=%#v err=%v", status, err)
	}
}
