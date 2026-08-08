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

func TestLocalAPIURL(t *testing.T) {
	if got := LocalAPIURL(38181); got != "http://127.0.0.1:38181" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeMigratesLegacySchwabRedirectURI(t *testing.T) {
	cfg := Config{
		Schwab: SchwabConfig{RedirectURI: "https://127.0.0.1:8182"},
	}
	cfg.Normalize(t.TempDir())
	if got, want := cfg.Schwab.RedirectURI, "https://127.0.0.1:8182/callback"; got != want {
		t.Fatalf("redirect URI: got %q, want %q", got, want)
	}
}

func TestNormalizePreservesCustomSchwabRedirectURI(t *testing.T) {
	const redirectURI = "https://127.0.0.1:8183/callback"
	cfg := Config{
		Schwab: SchwabConfig{RedirectURI: redirectURI},
	}
	cfg.Normalize(t.TempDir())
	if cfg.Schwab.RedirectURI != redirectURI {
		t.Fatalf("redirect URI changed: got %q, want %q", cfg.Schwab.RedirectURI, redirectURI)
	}
}

func TestNormalizeAlpacaBaseURL(t *testing.T) {
	cfg := Config{
		Alpaca: AlpacaConfig{BaseURL: "https://paper-api.alpaca.markets/v2/"},
	}
	cfg.Normalize(t.TempDir())
	if got, want := cfg.Alpaca.BaseURL, "https://paper-api.alpaca.markets"; got != want {
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
