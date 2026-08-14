package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestIBKRGatewaysAreIndependentResources(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "gateways.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	created, err := st.UpsertIBKRGateway(t.Context(), IBKRGateway{
		GatewayKey: "paper", Name: "Paper", GatewayURL: "https://localhost:5680",
		GatewayDir: filepath.Join(t.TempDir(), "paper"), GatewayPort: 5680,
		Lifecycle: "persistent", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create Gateway: %v", err)
	}
	gateways, err := st.ListIBKRGateways(t.Context())
	if err != nil || len(gateways) != 1 || gateways[0].ID != created.ID {
		t.Fatalf("list Gateways: gateways=%#v err=%v", gateways, err)
	}
	connections, err := st.ListBrokerConnections(t.Context())
	if err != nil || len(connections) != 0 {
		t.Fatalf("Gateway creation invented a connection: connections=%#v err=%v", connections, err)
	}
	if err := st.DeleteIBKRGateway(t.Context(), created.ID); err != nil {
		t.Fatalf("delete Gateway: %v", err)
	}
	if _, err := st.GetIBKRGateway(t.Context(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted Gateway lookup: %v", err)
	}
}

func TestManagedIBKRGatewayMustBeLocal(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "gateways.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, err = st.UpsertIBKRGateway(t.Context(), IBKRGateway{
		GatewayKey: "remote", GatewayURL: "https://gateway.example.test:5680",
		GatewayDir: filepath.Join(t.TempDir(), "remote"), GatewayPort: 5680, Enabled: true,
	})
	if err == nil {
		t.Fatal("remote endpoint must be used by a connection, not a local Gateway manager")
	}
}
