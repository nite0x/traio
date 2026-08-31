package ibkr

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagerClientUsesManagementContract(t *testing.T) {
	const token = "manager-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/management/v1/gateways":
			_, _ = w.Write([]byte(`{"gateways":[{"id":"paper","proxy_url":"https://paper.example.test","proxy_listening":true,"proxy_token_configured":true,"status":{"running":true,"authenticated":false,"state":"running"}}]}`))
		case "/management/v1/gateways/paper/status":
			_, _ = w.Write([]byte(`{"running":true,"authenticated":true,"account":"DU123","state":"authenticated"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewManagerClient(server.URL+"/", token)
	if err != nil {
		t.Fatal(err)
	}
	if health, err := client.Health(t.Context()); err != nil || health.Status != "ok" {
		t.Fatalf("health = %#v, err=%v", health, err)
	}
	gateways, err := client.Gateways(t.Context())
	if err != nil || len(gateways) != 1 || gateways[0].ID != "paper" || gateways[0].ProxyURL != "https://paper.example.test" {
		t.Fatalf("gateways = %#v, err=%v", gateways, err)
	}
	status, err := client.GatewayStatus(t.Context(), "paper")
	if err != nil || !status.Authenticated || status.Account != "DU123" {
		t.Fatalf("status = %#v, err=%v", status, err)
	}
	if client.ManagementURL() != server.URL+"/manager/" {
		t.Fatalf("management URL = %q", client.ManagementURL())
	}
}

func TestManagerClientRejectsNonOrigin(t *testing.T) {
	for _, value := range []string{"", "ftp://example.test", "https://user@example.test", "https://example.test/manager", "https://example.test?token=x"} {
		if _, err := NewManagerClient(value, ""); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}
