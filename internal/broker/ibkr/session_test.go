package ibkr

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nite/traio/internal/config"
)

func TestBrokerLoginUsesConfiguredGatewayWithoutManagingProcess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/api/tickle" {
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
