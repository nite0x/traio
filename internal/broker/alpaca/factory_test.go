package alpaca

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nite/traio/internal/broker"
)

func TestFactorySelectsEnvironmentBaseURLAndConnectionOverride(t *testing.T) {
	factory := NewFactory()
	provider := map[string]any{
		"paper_base_url": "https://paper.example.test", "live_base_url": "https://live.example.test",
	}
	for _, test := range []struct {
		name, environment, override, want string
	}{
		{name: "paper", environment: "paper", want: "https://paper.example.test"},
		{name: "live", environment: "live", want: "https://live.example.test"},
		{name: "override", environment: "live", override: "https://custom.example.test/v2/", want: "https://custom.example.test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sessionValue, err := factory.Open(t.Context(), broker.ConnectionConfig{
				ID: 3, Environment: test.environment, ProviderConfig: provider,
				Config:  map[string]any{"base_url": test.override},
				Secrets: map[string]string{"api_key": "key", "api_secret": "secret"},
			})
			if err != nil {
				t.Fatalf("open session: %v", err)
			}
			session := sessionValue.(*Session)
			if got := session.baseURL(); got != test.want {
				t.Fatalf("baseURL = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAPIKeySessionExposesProviderNeutralAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/account" || r.Header.Get("APCA-API-KEY-ID") != "key" || r.Header.Get("APCA-API-SECRET-KEY") != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"account-1","currency":"USD"}`))
	}))
	defer server.Close()
	sessionValue, err := NewFactory().Open(t.Context(), broker.ConnectionConfig{
		ID: 3, ProviderCode: "ALPACA", Config: map[string]any{"base_url": server.URL},
		Secrets: map[string]string{"api_key": "key", "api_secret": "secret"},
	})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	authentication, ok := sessionValue.(broker.AuthenticationProvider)
	if !ok {
		t.Fatal("Alpaca session does not expose authentication")
	}
	status, err := authentication.AuthenticationStatus(t.Context())
	if err != nil || !status.Authenticated || status.AccountID != "account-1" {
		t.Fatalf("status = %#v, err=%v", status, err)
	}
	health, err := sessionValue.Health(t.Context())
	if err != nil || health.State != broker.ConnectionStateConnected {
		t.Fatalf("health = %#v, err=%v", health, err)
	}
}
