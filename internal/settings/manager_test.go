package settings

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nite/traio/internal/store"
)

func TestLoadGlobalSettingsWithoutBrokerCredentials(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SaveSettings(context.Background(), []byte(`{"broker_sync":{"enabled":false}}`)); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(st, dir)
	if err := manager.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.Get().BrokerSync.Enabled {
		t.Fatal("stored global setting was not loaded")
	}
}
