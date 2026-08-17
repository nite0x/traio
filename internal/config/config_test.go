package config

import (
	"path/filepath"
	"testing"
)

func TestResolveServerPortDevDefault(t *testing.T) {
	t.Setenv("TRAIO_SERVER_PORT", "")
	if got := ResolveServerPort(); got != DevServerPort {
		t.Fatalf("got %d, want %d", got, DevServerPort)
	}
}

func TestResolveServerPortEnvOverride(t *testing.T) {
	t.Setenv("TRAIO_SERVER_PORT", "40000")
	if got := ResolveServerPort(); got != 40000 {
		t.Fatalf("got %d, want %d", got, 40000)
	}
}

func TestResolveIBKRGatewayPortDefaultsByRuntime(t *testing.T) {
	t.Setenv("TRAIO_IBKR_GATEWAY_PORT", "")
	if got := resolveIBKRGatewayPort(false); got != DevIBKRGatewayPort {
		t.Fatalf("got %d, want %d", got, DevIBKRGatewayPort)
	}
	if got := resolveIBKRGatewayPort(true); got != DesktopIBKRGatewayPort {
		t.Fatalf("got %d, want %d", got, DesktopIBKRGatewayPort)
	}
}

func TestResolveIBKRGatewayPortRange(t *testing.T) {
	t.Setenv("TRAIO_IBKR_GATEWAY_PORT", "6200")
	start, end := ResolveIBKRGatewayPortRange()
	if start != 6200 || end != 6219 {
		t.Fatalf("got %d-%d, want 6200-6219", start, end)
	}
}

func TestResolveIBKRGatewayPortRangeCapsAtMaximumPort(t *testing.T) {
	t.Setenv("TRAIO_IBKR_GATEWAY_PORT", "65530")
	start, end := ResolveIBKRGatewayPortRange()
	if start != 65530 || end != 65535 {
		t.Fatalf("got %d-%d, want 65530-65535", start, end)
	}
}

func TestResolveIBKRGatewayPortEnvOverride(t *testing.T) {
	t.Setenv("TRAIO_IBKR_GATEWAY_PORT", "5688")
	if got := ResolveIBKRGatewayPort(); got != 5688 {
		t.Fatalf("got %d, want %d", got, 5688)
	}
}

func TestEmbeddedExecutableLocations(t *testing.T) {
	for _, path := range []string{
		"/Applications/Traio.app/Contents/MacOS/traio-server",
		"/Applications/Traio.app/Contents/Resources/traio-server",
	} {
		if !isEmbeddedExecutable(path) {
			t.Fatalf("expected %q to be recognized as embedded", path)
		}
	}
	if isEmbeddedExecutable("/Users/alice/open/traio/bin/traio-server") {
		t.Fatal("development binary was recognized as embedded")
	}
}

func TestLocalAPIURL(t *testing.T) {
	if got := LocalAPIURL(38181); got != "http://127.0.0.1:38181" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveServerListenAddr(t *testing.T) {
	t.Setenv("TRAIO_LISTEN_ADDR", "0.0.0.0:8080")
	if got := ResolveServerListenAddr(); got != "0.0.0.0:8080" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveAllowedAPIHosts(t *testing.T) {
	t.Setenv("TRAIO_ALLOWED_API_HOSTS", "api.example.com, admin.example.com ")
	got := ResolveAllowedAPIHosts()
	if len(got) != 2 || got[0] != "api.example.com" || got[1] != "admin.example.com" {
		t.Fatalf("unexpected hosts: %#v", got)
	}
}

func TestResolveIBKRProxyURL(t *testing.T) {
	t.Setenv("TRAIO_IBKR_PROXY_URL", " https://ibkr.example.com/ ")
	if got := ResolveIBKRProxyURL(); got != "https://ibkr.example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveIBKRGatewayLifecycle(t *testing.T) {
	t.Setenv("TRAIO_IBKR_GATEWAY_LIFECYCLE", "persistent")
	if got := ResolveIBKRGatewayLifecycle(); got != IBKRGatewayLifecyclePersistent {
		t.Fatalf("got %q", got)
	}

	t.Setenv("TRAIO_IBKR_GATEWAY_LIFECYCLE", "managed")
	if got := ResolveIBKRGatewayLifecycle(); got != IBKRGatewayLifecycleManaged {
		t.Fatalf("got %q", got)
	}
}

func TestResolveIBKRGatewayLifecycleInvalidDefaultsToManaged(t *testing.T) {
	t.Setenv("TRAIO_IBKR_GATEWAY_LIFECYCLE", "invalid")
	if got := ResolveIBKRGatewayLifecycle(); got != IBKRGatewayLifecycleManaged {
		t.Fatalf("got %q", got)
	}
}

func TestDefaultGatewayDirectories(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "Users", "alice")
	desktopRuntime := DefaultDesktopRuntimeDir(home)
	if want := filepath.Join(home, "Library", "Application Support", "Traio"); desktopRuntime != want {
		t.Fatalf("desktop runtime: got %q, want %q", desktopRuntime, want)
	}
	if got, want := DefaultIBKRGatewayDir(desktopRuntime, "paper-local"), filepath.Join(desktopRuntime, "ibkr-gateways", "paper-local"); got != want {
		t.Fatalf("desktop Gateway: got %q, want %q", got, want)
	}
	if got, want := DefaultIBKRGatewayDir(DefaultServerRuntimeDir, "live"), filepath.Join(DefaultServerRuntimeDir, "ibkr-gateways", "live"); got != want {
		t.Fatalf("server Gateway: got %q, want %q", got, want)
	}
}

func TestDefaultConfigUsesManagedGatewayRoot(t *testing.T) {
	runtimeDir := t.TempDir()
	cfg := IBKRConfig{}
	cfg.normalize(runtimeDir)
	if got, want := cfg.GatewayDir, DefaultIBKRGatewayDir(runtimeDir, "local"); got != want {
		t.Fatalf("default Gateway directory: got %q, want %q", got, want)
	}
}

func TestDefaultIBKRGatewayDirRejectsUnsafeKey(t *testing.T) {
	for _, key := range []string{"", ".", "..", "../outside", "nested/gateway", `nested\gateway`, "/absolute"} {
		if got := DefaultIBKRGatewayDir(t.TempDir(), key); got != "" {
			t.Fatalf("key %q produced unsafe default %q", key, got)
		}
	}
}

func TestNormalizeAlpacaBaseURL(t *testing.T) {
	cfg := AlpacaConfig{BaseURL: "https://paper-api.alpaca.markets/v2/"}
	cfg.Normalize()
	if got, want := cfg.BaseURL, "https://paper-api.alpaca.markets"; got != want {
		t.Fatalf("base URL: got %q, want %q", got, want)
	}
}

func TestDefaultBrokerSyncEnabled(t *testing.T) {
	cfg := Default(t.TempDir())
	if !cfg.BrokerSync.Enabled {
		t.Fatal("expected broker sync enabled by default")
	}
}

func TestResolveBootstrapDatabaseDefaultsToSQLite(t *testing.T) {
	t.Setenv("TRAIO_DATABASE_DRIVER", "")
	t.Setenv("TRAIO_DATABASE_DSN", "")
	baseDir := t.TempDir()

	got := ResolveBootstrapDatabase(baseDir)
	if got.Driver != "sqlite" {
		t.Fatalf("driver: got %q, want sqlite", got.Driver)
	}
	if want := filepath.Join(baseDir, "data", "traio.db"); got.DataSource != want {
		t.Fatalf("data source: got %q, want %q", got.DataSource, want)
	}
}

func TestResolveBootstrapDatabaseUsesEnvironment(t *testing.T) {
	t.Setenv("TRAIO_DATABASE_DRIVER", "postgres")
	t.Setenv("TRAIO_DATABASE_DSN", "postgres://traio@example/traio")

	got := ResolveBootstrapDatabase(t.TempDir())
	if got.Driver != "postgres" || got.DataSource != "postgres://traio@example/traio" {
		t.Fatalf("unexpected database config: %#v", got)
	}
}
