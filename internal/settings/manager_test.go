package settings

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/nite/traio/internal/store"
)

func TestLoadRemovesDeprecatedIBKRCredentials(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	legacy := []byte(`{
		"ibkr": {
			"sub_account": "DU123",
			"password": "plaintext-password",
			"totp_secret": "plaintext-totp"
		}
	}`)
	if err := st.SaveSettings(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(st, dir)
	if err := manager.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := manager.Get().IBKR.SubAccount; got != "DU123" {
		t.Fatalf("expected non-secret IBKR setting to survive, got %q", got)
	}

	cleaned, err := st.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]map[string]json.RawMessage
	if err := json.Unmarshal(cleaned, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["ibkr"]["password"]; ok {
		t.Fatal("deprecated IBKR password was not removed")
	}
	if _, ok := root["ibkr"]["totp_secret"]; ok {
		t.Fatal("deprecated IBKR TOTP secret was not removed")
	}
}
