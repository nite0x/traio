package config

import (
	"os"
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

func TestPackagedServerPortUsesSystemAssignment(t *testing.T) {
	t.Setenv("TRAIO_SERVER_PORT", "")
	if got := resolveServerPort(true); got != 0 {
		t.Fatalf("got %d, want 0", got)
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

func TestDefaultDesktopRuntimeDir(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "Users", "alice")
	want := filepath.Join(home, "Library", "Application Support", "Traio")
	if got := DefaultDesktopRuntimeDir(home); got != want {
		t.Fatalf("desktop runtime: got %q, want %q", got, want)
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

func TestResolveWebDir(t *testing.T) {
	t.Setenv("TRAIO_WEB_DIR", " /opt/traio/web ")
	if got := ResolveWebDir(); got != "/opt/traio/web" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveAuthConfigDefaultsByDeployment(t *testing.T) {
	t.Setenv("TRAIO_AUTH_MODE", "")
	t.Setenv("TRAIO_DEPLOYMENT_MODE", "")
	local, err := ResolveAuthConfig()
	if err != nil || local.Mode != "local" {
		t.Fatalf("local auth config: %#v err=%v", local, err)
	}

	t.Setenv("TRAIO_DEPLOYMENT_MODE", DeploymentModeServer)
	if _, err := ResolveAuthConfig(); err == nil {
		t.Fatal("server mode without OIDC configuration should fail")
	}
	t.Setenv("TRAIO_OIDC_ISSUER_URL", "https://id.example")
	t.Setenv("TRAIO_OIDC_CLIENT_ID", "traio")
	t.Setenv("TRAIO_OIDC_REDIRECT_URL", "https://traio.example/auth/callback")
	server, err := ResolveAuthConfig()
	if err != nil || server.Mode != "oidc" || !server.CookieSecure {
		t.Fatalf("server auth config: %#v err=%v", server, err)
	}
}

func TestResolveAuthConfigRejectsDisabledServerAuth(t *testing.T) {
	t.Setenv("TRAIO_DEPLOYMENT_MODE", DeploymentModeServer)
	t.Setenv("TRAIO_AUTH_MODE", "disabled-dev")
	if _, err := ResolveAuthConfig(); err == nil {
		t.Fatal("disabled server authentication should fail")
	}
}

func TestResolvePasswordAuthConfig(t *testing.T) {
	t.Setenv("TRAIO_AUTH_MODE", "password")
	t.Setenv("TRAIO_BOOTSTRAP_ADMIN_USERNAME", " owner ")
	t.Setenv("TRAIO_BOOTSTRAP_ADMIN_PASSWORD", "a-secure-password")
	t.Setenv("TRAIO_COOKIE_SECURE", "true")
	config, err := ResolveAuthConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Mode != "password" || config.BootstrapUsername != "owner" || config.BootstrapPassword != "a-secure-password" || !config.CookieSecure {
		t.Fatalf("unexpected password auth config: %#v", config)
	}
}

func TestResolvePasswordAuthConfigReadsSecretFile(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "admin-password")
	if err := os.WriteFile(secretFile, []byte("a-file-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRAIO_AUTH_MODE", "password")
	t.Setenv("TRAIO_BOOTSTRAP_ADMIN_USERNAME", "owner")
	t.Setenv("TRAIO_BOOTSTRAP_ADMIN_PASSWORD_FILE", secretFile)
	config, err := ResolveAuthConfig()
	if err != nil || config.BootstrapPassword != "a-file-password" {
		t.Fatalf("unexpected password file config: %#v err=%v", config, err)
	}
}

func TestResolvePasswordAuthConfigRejectsPartialBootstrap(t *testing.T) {
	t.Setenv("TRAIO_AUTH_MODE", "password")
	t.Setenv("TRAIO_BOOTSTRAP_ADMIN_USERNAME", "owner")
	if _, err := ResolveAuthConfig(); err == nil {
		t.Fatal("partial built-in login bootstrap should fail")
	}
}
