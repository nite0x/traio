package ibkr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nite/traio/internal/config"
)

func TestBrokerLoginOpensManagerAndProbesConfiguredGateway(t *testing.T) {
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

	adapter := NewBroker(config.IBKRConfig{GatewayURL: server.URL, ManagerURL: "https://manager.example.test/"})
	action, err := adapter.BeginLogin(t.Context())
	if err != nil {
		t.Fatalf("begin login: %v", err)
	}
	if action.URL != "https://manager.example.test/manager/" || action.Authenticated || action.AccountID != "" {
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

func TestBrokerLoginRequiresValidManagerWithoutGatewayFallback(t *testing.T) {
	for _, managerURL := range []string{"", "not-a-url", "javascript:alert(1)", "https://user:secret@manager.example.test", "https://manager.example.test/sso/Login", "https://manager.example.test?token=secret"} {
		adapter := NewBroker(config.IBKRConfig{GatewayURL: "https://gateway.example.test", ManagerURL: managerURL})
		action, err := adapter.BeginLogin(t.Context())
		if err == nil || action.URL != "" || !strings.Contains(err.Error(), "Gateway Manager") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("expected configuration guidance without a login URL or credentials, action=%#v err=%v", action, err)
		}
	}
}

func TestLoginStatusReportsGatewayProxyAuthenticationFailure(t *testing.T) {
	for _, challenge := range []string{`Basic realm="IBKR Gateway"`, "Bearer", `Bearer realm="gateway"`} {
		t.Run(challenge, func(t *testing.T) {
			testLoginStatusProxyRejection(t, challenge)
		})
	}
}

func testLoginStatusProxyRejection(t *testing.T, challenge string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", challenge)
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	adapter := NewBroker(config.IBKRConfig{GatewayURL: server.URL, GatewayToken: "wrong-token"})
	_, err := adapter.LoginStatus(t.Context())
	if err == nil || !strings.Contains(err.Error(), "gateway_token") {
		t.Fatalf("expected actionable proxy authentication error, got %v", err)
	}
}

func TestLoginStatusReportsProxyRejectionFromAuthStatusFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/api/tickle" {
			_, _ = w.Write([]byte(`{"authenticated":false}`))
			return
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	_, err := NewBroker(config.IBKRConfig{GatewayURL: server.URL}).LoginStatus(t.Context())
	if err == nil || !strings.Contains(err.Error(), "gateway_token") || !strings.Contains(err.Error(), "/iserver/auth/status") {
		t.Fatalf("expected proxy rejection with endpoint, got %v", err)
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
