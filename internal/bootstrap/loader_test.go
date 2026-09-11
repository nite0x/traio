package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nite/traio/internal/auth"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/settings"
	"github.com/nite/traio/internal/store"
)

type fakeProvider struct {
	values map[string]string
	err    error
	calls  [][]string
}

func (p *fakeProvider) Fetch(_ context.Context, keys []string) (FetchResult, error) {
	p.calls = append(p.calls, append([]string(nil), keys...))
	return FetchResult{Values: p.values}, p.err
}
func enabled() map[string]string {
	return map[string]string{
		"TRAIO_CONFIG_PROVIDER": "infisical", "TRAIO_INFISICAL_SITE_URL": "https://secrets.example",
		"TRAIO_INFISICAL_PROJECT_ID": "project", "TRAIO_INFISICAL_ENVIRONMENT": "prod",
		"INFISICAL_UNIVERSAL_AUTH_CLIENT_ID": "machine", "INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET": "bootstrap-secret",
	}
}
func getter(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
func runLoad(t *testing.T, local, remote map[string]string) (Result, *fakeProvider, error) {
	t.Helper()
	p := &fakeProvider{values: remote}
	r, err := LoadFrom(context.Background(), t.TempDir(), getter(local), func(ProviderConfig) (Provider, error) { return p, nil })
	return r, p, err
}

func TestDisabledProviderPreservesDefaults(t *testing.T) {
	r, _, err := runLoad(t, map[string]string{}, nil)
	if err != nil || r.Database.Driver != "sqlite" || r.Auth.Mode != auth.ModeLocal {
		t.Fatalf("defaults: %v", err)
	}
	_, err = LoadFrom(context.Background(), t.TempDir(), getter(map[string]string{}), func(ProviderConfig) (Provider, error) { t.Fatal("provider constructed"); return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
}
func TestLocalOverridesRemoteWithoutEnvironmentMutation(t *testing.T) {
	local := enabled()
	local["TRAIO_DATABASE_DRIVER"] = "postgres"
	local["TRAIO_DATABASE_DSN"] = "postgres://local:local-secret@localhost/traio"
	local["TRAIO_AUTH_MODE"] = "oidc"
	local["TRAIO_COOKIE_SECURE"] = "false"
	remote := map[string]string{
		"TRAIO_DATABASE_DSN": "postgres://remote:remote-secret@elsewhere/other", "TRAIO_COOKIE_SECURE": "true",
		"TRAIO_OIDC_ISSUER_URL": "https://id.example", "TRAIO_OIDC_CLIENT_ID": "traio", "TRAIO_OIDC_CLIENT_SECRET": "  exact secret  ",
		"TRAIO_OIDC_REDIRECT_URL": "https://traio.example/callback", "TRAIO_DEPLOYMENT_MODE": "server", "UNRELATED_SECRET": "ignored",
	}
	before := os.Getenv("TRAIO_OIDC_CLIENT_SECRET")
	r, p, err := runLoad(t, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if r.Database.DataSource != local["TRAIO_DATABASE_DSN"] || r.Auth.CookieSecure || r.Auth.ClientSecret != "  exact secret  " {
		t.Fatal("precedence or secret whitespace changed")
	}
	if os.Getenv("TRAIO_OIDC_CLIENT_SECRET") != before {
		t.Fatal("environment mutated")
	}
	if r.Sources["TRAIO_DATABASE_DSN"] != "environment" || r.Sources["TRAIO_OIDC_CLIENT_SECRET"] != "infisical" {
		t.Fatal("source tracking")
	}
	for _, keys := range p.calls {
		for _, k := range keys {
			if local[k] != "" {
				t.Fatalf("requested overridden key %s", k)
			}
		}
	}
}
func TestRemoteSelectorsPrecedeDefaults(t *testing.T) {
	r, _, err := runLoad(t, enabled(), map[string]string{"TRAIO_DATABASE_DRIVER": "postgres", "TRAIO_DATABASE_DSN": "postgres://localhost/traio", "TRAIO_AUTH_MODE": "password", "TRAIO_BOOTSTRAP_ADMIN_USERNAME": "owner", "TRAIO_BOOTSTRAP_ADMIN_PASSWORD": "very-long-secret"})
	if err != nil || r.Database.Driver != "postgres" || r.Auth.Mode != auth.ModePassword {
		t.Fatalf("selectors: %v", err)
	}
}
func TestValidationAndLocalFiles(t *testing.T) {
	for _, key := range []string{"TRAIO_AUTH_MODE", "TRAIO_DATABASE_DRIVER", "TRAIO_COOKIE_SECURE", "TRAIO_SESSION_TTL", "TRAIO_OIDC_ISSUER_URL"} {
		t.Run(key, func(t *testing.T) {
			local := enabled()
			local[key] = "do-not-print-this-value"
			_, p, err := runLoad(t, local, map[string]string{key: "valid"})
			if err == nil || strings.Contains(err.Error(), local[key]) || len(p.calls) != 0 {
				t.Fatal("invalid local value did not fail safely before provider")
			}
		})
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("  file-secret  \n"), 0600); err != nil {
		t.Fatal(err)
	}
	local := enabled()
	local["TRAIO_AUTH_MODE"] = "password"
	local["TRAIO_BOOTSTRAP_ADMIN_USERNAME"] = "owner"
	local["TRAIO_BOOTSTRAP_ADMIN_PASSWORD_FILE"] = path
	r, _, err := runLoad(t, local, map[string]string{"TRAIO_BOOTSTRAP_ADMIN_PASSWORD": "override"})
	if err != nil || r.Auth.BootstrapPassword != "  file-secret  " || r.Sources["TRAIO_BOOTSTRAP_ADMIN_PASSWORD"] != "file" {
		t.Fatalf("file: %v", err)
	}
	local["TRAIO_BOOTSTRAP_ADMIN_PASSWORD"] = "conflict"
	if _, _, err := runLoad(t, local, nil); err == nil {
		t.Fatal("conflicting file accepted")
	}
	delete(local, "TRAIO_BOOTSTRAP_ADMIN_PASSWORD")
	local["TRAIO_BOOTSTRAP_ADMIN_PASSWORD_FILE"] = path + "-missing"
	if _, _, err := runLoad(t, local, nil); err == nil {
		t.Fatal("unreadable file accepted")
	}
}
func TestRequiredAndUnavailableConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name          string
		local, remote map[string]string
		fail          bool
	}{
		{"postgres missing DSN", map[string]string{"TRAIO_DATABASE_DRIVER": "postgres"}, nil, true},
		{"invalid DSN", map[string]string{"TRAIO_DATABASE_DRIVER": "postgres", "TRAIO_DATABASE_DSN": "not-a-dsn-secret"}, nil, true},
		{"OIDC missing", map[string]string{"TRAIO_AUTH_MODE": "oidc"}, nil, true},
		{"partial administrator", map[string]string{"TRAIO_AUTH_MODE": "password", "TRAIO_BOOTSTRAP_ADMIN_USERNAME": "owner"}, nil, true},
		{"administrator deferred", map[string]string{"TRAIO_AUTH_MODE": "password"}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runLoad(t, tc.local, tc.remote)
			if (err != nil) != tc.fail {
				t.Fatalf("error: %v", err)
			}
		})
	}
	local := enabled()
	p := &fakeProvider{err: errors.New("do-not-log-response")}
	_, err := LoadFrom(context.Background(), t.TempDir(), getter(local), func(ProviderConfig) (Provider, error) { return p, nil })
	if err == nil || strings.Contains(err.Error(), "do-not-log") {
		t.Fatal("selector outage not safe")
	}
	local["TRAIO_AUTH_MODE"] = "local"
	local["TRAIO_DATABASE_DRIVER"] = "sqlite"
	local["TRAIO_DATABASE_DSN"] = filepath.Join(t.TempDir(), "data.db")
	r, err := LoadFrom(context.Background(), t.TempDir(), getter(local), func(ProviderConfig) (Provider, error) { return p, nil })
	if err != nil || len(r.Warnings) == 0 {
		t.Fatalf("optional-only outage: %v", err)
	}
	local["TRAIO_COOKIE_SECURE"] = "false"
	local["TRAIO_SESSION_TTL"] = "12h"
	_, err = LoadFrom(context.Background(), t.TempDir(), getter(local), func(ProviderConfig) (Provider, error) { t.Fatal("unneeded provider initialized"); return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
}
func TestProviderConfigValidation(t *testing.T) {
	for _, key := range []string{"TRAIO_INFISICAL_SITE_URL", "TRAIO_INFISICAL_PROJECT_ID", "TRAIO_INFISICAL_ENVIRONMENT", "INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", "INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET"} {
		local := enabled()
		delete(local, key)
		if _, err := readProviderConfig(getter(local)); err == nil {
			t.Fatalf("missing %s accepted", key)
		}
	}
	for _, site := range []string{"http://example.test", "https://user:secret@example.test", "https://example.test/?token=secret", "https://example.test/api"} {
		local := enabled()
		local["TRAIO_INFISICAL_SITE_URL"] = site
		if _, err := readProviderConfig(getter(local)); err == nil || strings.Contains(err.Error(), site) {
			t.Fatal("unsafe origin validation")
		}
	}
	local := enabled()
	local["TRAIO_CONFIG_PROVIDER"] = "typo"
	if _, err := readProviderConfig(getter(local)); err == nil {
		t.Fatal("unknown provider accepted")
	}
}

func TestStartupSecretsStayOutOfPersistedSettingsAndExistingLogin(t *testing.T) {
	dir := t.TempDir()
	local := map[string]string{"TRAIO_DATABASE_DRIVER": "sqlite", "TRAIO_DATABASE_DSN": filepath.Join(dir, "data.db"), "TRAIO_AUTH_MODE": "password", "TRAIO_BOOTSTRAP_ADMIN_USERNAME": "owner", "TRAIO_BOOTSTRAP_ADMIN_PASSWORD": "not-in-settings-secret"}
	first, err := LoadFrom(context.Background(), dir, getter(local), nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenRepository(first.Database.Driver, first.Database.DataSource)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := auth.NewService(context.Background(), st, first.Auth); err != nil {
		t.Fatal(err)
	}
	manager := settings.NewManager(st, dir)
	if err := manager.Save(context.Background(), config.Default(dir)); err != nil {
		t.Fatal(err)
	}
	raw, err := st.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	visible, _ := json.Marshal(manager.Get())
	for _, blob := range [][]byte{raw, visible} {
		if strings.Contains(string(blob), local["TRAIO_BOOTSTRAP_ADMIN_PASSWORD"]) || strings.Contains(string(blob), local["TRAIO_DATABASE_DSN"]) {
			t.Fatal("startup secret persisted or exposed")
		}
	}
	delete(local, "TRAIO_BOOTSTRAP_ADMIN_USERNAME")
	delete(local, "TRAIO_BOOTSTRAP_ADMIN_PASSWORD")
	second, err := LoadFrom(context.Background(), dir, getter(local), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.NewService(context.Background(), st, second.Auth); err != nil {
		t.Fatalf("existing administrator restart: %v", err)
	}
	fresh, err := store.Open(filepath.Join(dir, "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, err := auth.NewService(context.Background(), fresh, second.Auth); err == nil {
		t.Fatal("new database started without administrator")
	}
	// Types handed around during bootstrap redact accidental formatting and JSON.
	encoded, _ := json.Marshal(first)
	if !reflect.DeepEqual(string(encoded), "{}") || strings.Contains(fmt.Sprintf("%+v %#v", first, first), "not-in-settings-secret") {
		t.Fatal("configuration formatting leaked values")
	}
}
