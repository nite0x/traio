package ibkr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	brokerapi "github.com/nite/traio/internal/broker"
)

func TestFactoryOpensNormalizedGatewaySession(t *testing.T) {
	session, err := NewFactory().Open(t.Context(), brokerapi.ConnectionConfig{
		ID: 42, ProviderCode: "IBKR",
		Config:  map[string]any{"gateway_id": "primary", "gateway_url": "https://gateway.example.test/v1/api/"},
		Secrets: map[string]string{"flex_token": "secret"},
	})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	ibkrSession := session.(*Session)
	if ibkrSession.ConnectionID() != 42 || ibkrSession.ProviderCode() != "IBKR" {
		t.Fatalf("unexpected identity: id=%d code=%s", ibkrSession.ConnectionID(), ibkrSession.ProviderCode())
	}
	if got := ibkrSession.BaseURL(); got != "https://gateway.example.test" {
		t.Fatalf("BaseURL = %q", got)
	}
}

func TestGatewaySessionExposesProviderNeutralAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer proxy-secret" {
			http.Error(w, "missing proxy authorization", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/api/tickle":
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authenticated":true,"account":"U123"}`))
	}))
	defer server.Close()
	sessionValue, err := NewFactory().Open(t.Context(), brokerapi.ConnectionConfig{
		ID: 42, ProviderCode: "IBKR", Config: map[string]any{"gateway_id": "primary", "gateway_url": server.URL}, Secrets: map[string]string{"gateway_token": "proxy-secret"},
	})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	authentication, ok := sessionValue.(brokerapi.AuthenticationProvider)
	if !ok {
		t.Fatal("IBKR session does not expose authentication")
	}
	begin, err := authentication.BeginAuthentication(t.Context(), brokerapi.AuthenticationRequest{State: "ignored"})
	if err != nil || begin.URL != server.URL+"/sso/Login" {
		t.Fatalf("begin = %#v, err=%v", begin, err)
	}
	status, err := authentication.AuthenticationStatus(t.Context())
	if err != nil || !status.Authenticated || status.AccountID != "U123" {
		t.Fatalf("status = %#v, err=%v", status, err)
	}
}

func TestFactoryRejectsInvalidGatewayOrigin(t *testing.T) {
	_, err := NewFactory().Open(t.Context(), brokerapi.ConnectionConfig{
		ID: 1, Config: map[string]any{"gateway_id": "primary", "gateway_url": "https://user@example.test/path?token=secret"},
	})
	if err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("Open error = %v", err)
	}
}
