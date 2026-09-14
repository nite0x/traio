package ibkr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

func TestAccountSnapshotAuthenticationErrors(t *testing.T) {
	for _, tt := range []struct {
		name, challenge, want string
	}{
		{"manager proxy", "Bearer", "gateway_token"},
		{"legacy proxy", `Basic realm="IBKR Gateway"`, "gateway_token"},
		{"IBKR session", "", "sign in to the configured Gateway"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/api/portfolio/accounts" || r.Header.Get("Authorization") != "Bearer test-secret" {
					t.Error("expected account request with configured proxy token")
				}
				if tt.challenge != "" {
					w.Header().Set("WWW-Authenticate", tt.challenge)
				}
				http.Error(w, "sensitive-upstream-body", http.StatusUnauthorized)
			}))
			defer server.Close()
			provider, ok := broker.AsPortfolioProvider(NewBroker(config.IBKRConfig{GatewayURL: server.URL, GatewayToken: "test-secret"}))
			if !ok {
				t.Fatal("missing portfolio provider")
			}
			_, err := provider.ListAccountSnapshots(t.Context())
			if err == nil {
				t.Fatal("expected authentication error")
			}
			for _, want := range []string{tt.want, "/portfolio/accounts", "HTTP 401"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
			if calls != 1 || strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "sensitive-upstream-body") {
				t.Fatalf("unexpected retry or sensitive error: calls=%d err=%v", calls, err)
			}
		})
	}
}
