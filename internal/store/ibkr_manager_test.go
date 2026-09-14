package store

import (
	"path/filepath"
	"testing"
)

func TestManagerImportRollsBackAndScopesInstanceIdentity(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	discovered := func(origin, token string) BrokerConnection {
		return BrokerConnection{ProviderCode: "IBKR", Name: "Paper", Enabled: true,
			Config:  map[string]any{"manager_url": origin, "gateway_id": "paper", "gateway_url": "https://paper.test"},
			Secrets: map[string]string{"gateway_token": token}}
	}
	first := discovered("https://manager-one.test", "first-token")
	if _, err := s.SaveIBKRManagerConfig(t.Context(), map[string]any{"manager_url": "https://manager-one.test"}, map[string]string{"manager_api_token": "first-manager"}, []BrokerConnection{first}); err != nil {
		t.Fatal(err)
	}
	connections, _ := s.ListBrokerConnections(t.Context())
	firstID := connections[0].ID
	second := discovered("https://manager-two.test", "second-token")
	bad := discovered("https://manager-two.test", "bad-token")
	bad.Config["invalid"] = make(chan int) // Fail the second upsert after the first write.
	if _, err := s.SaveIBKRManagerConfig(t.Context(), map[string]any{"manager_url": "https://manager-two.test"}, map[string]string{"manager_api_token": "second-manager"}, []BrokerConnection{second, bad}); err == nil {
		t.Fatal("expected transaction failure")
	}
	provider, _ := s.GetBrokerProviderRuntimeConfig(t.Context(), "IBKR")
	connections, _ = s.ListBrokerConnections(t.Context())
	if len(connections) != 1 || provider.Config["manager_url"] != "https://manager-one.test" || provider.Secrets["manager_api_token"] != "first-manager" {
		t.Fatal("failed import left partial provider or connection writes")
	}
	if _, err := s.SaveIBKRManagerConfig(t.Context(), map[string]any{"manager_url": "https://manager-two.test"}, map[string]string{"manager_api_token": "second-manager"}, []BrokerConnection{second}); err != nil {
		t.Fatal(err)
	}
	connections, _ = s.ListBrokerConnections(t.Context())
	old, _ := s.GetBrokerConnectionRuntimeConfig(t.Context(), firstID)
	if len(connections) != 2 || old.Secrets["gateway_token"] != "first-token" {
		t.Fatal("same instance ID on another Manager overwrote the old connection")
	}
	if _, err := s.SaveIBKRManagerConfig(t.Context(), map[string]any{"manager_url": "https://manager-two.test"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	connections, _ = s.ListBrokerConnections(t.Context())
	if len(connections) != 2 {
		t.Fatal("empty discovery deleted historical connections")
	}
}
