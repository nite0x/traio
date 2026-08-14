package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

func TestMigrateLegacyIBKRGatewayDirectory(t *testing.T) {
	runtimeDir := t.TempDir()
	st, err := store.Open(filepath.Join(runtimeDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	legacyDir := filepath.Join(runtimeDir, "ibkr-gateway")
	if err := os.MkdirAll(filepath.Join(legacyDir, "root"), 0o700); err != nil {
		t.Fatalf("create legacy directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "root", "conf.yaml"), []byte("listenPort: 5680\n"), 0o600); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}
	if err := os.WriteFile(legacyDir+".audit.jsonl", []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write legacy audit: %v", err)
	}
	gateway, err := st.UpsertIBKRGateway(t.Context(), store.IBKRGateway{
		GatewayKey: "primary", Name: "Primary", GatewayURL: "https://localhost:5680",
		GatewayDir: legacyDir, GatewayPort: 5680, Lifecycle: "managed", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create Gateway: %v", err)
	}

	migrated, err := migrateLegacyIBKRGatewayDirectories(t.Context(), runtimeDir, st)
	if err != nil || !migrated {
		t.Fatalf("migrate: migrated=%v err=%v", migrated, err)
	}
	targetDir := config.DefaultIBKRGatewayDir(runtimeDir, "primary")
	if _, err := os.Stat(filepath.Join(targetDir, "root", "conf.yaml")); err != nil {
		t.Fatalf("migrated installation: %v", err)
	}
	if _, err := os.Stat(targetDir + ".audit.jsonl"); err != nil {
		t.Fatalf("migrated audit: %v", err)
	}
	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("legacy directory still exists: %v", err)
	}
	got, err := st.GetIBKRGateway(t.Context(), gateway.ID)
	if err != nil || got.GatewayDir != targetDir {
		t.Fatalf("stored Gateway: got=%#v err=%v", got, err)
	}
}

func TestMigrateLegacyIBKRGatewayDirectoryPreservesCustomPath(t *testing.T) {
	runtimeDir := t.TempDir()
	st, err := store.Open(filepath.Join(runtimeDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	customDir := filepath.Join(t.TempDir(), "custom-gateway")
	gateway, err := st.UpsertIBKRGateway(t.Context(), store.IBKRGateway{
		GatewayKey: "custom", Name: "Custom", GatewayURL: "https://localhost:5682",
		GatewayDir: customDir, GatewayPort: 5682, Lifecycle: "managed", Enabled: false,
	})
	if err != nil {
		t.Fatalf("create Gateway: %v", err)
	}
	migrated, err := migrateLegacyIBKRGatewayDirectories(t.Context(), runtimeDir, st)
	if err != nil || migrated {
		t.Fatalf("unexpected migration: migrated=%v err=%v", migrated, err)
	}
	got, err := st.GetIBKRGateway(t.Context(), gateway.ID)
	if err != nil || got.GatewayDir != customDir {
		t.Fatalf("custom directory changed: got=%#v err=%v", got, err)
	}
}
