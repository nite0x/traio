package config

import "testing"

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
