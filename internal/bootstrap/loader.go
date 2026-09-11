// Package bootstrap loads process-only configuration. Its values must never be
// merged into config.Config, which is persisted and exposed by the settings API.
package bootstrap

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nite/traio/internal/auth"
	"github.com/nite/traio/internal/config"
)

const LoadTimeout = 15 * time.Second

// FetchResult distinguishes an absent key from an unavailable key. Errors must
// describe categories only, never include remote response bodies or secret values.
type FetchResult struct {
	Values      map[string]string
	Unavailable map[string]error
}

type Provider interface {
	Fetch(context.Context, []string) (FetchResult, error)
}

type ProviderFactory func(ProviderConfig) (Provider, error)

// Result is process-only; even accidental JSON serialization excludes secrets.
type Result struct {
	Database config.BootstrapDatabaseConfig `json:"-"`
	Auth     auth.Config                    `json:"-"`
	Sources  map[string]string              `json:"-"`
	Warnings []string                       `json:"-"`
}

func (Result) String() string     { return "[redacted startup configuration]" }
func (r Result) GoString() string { return r.String() }

type field struct {
	name   string
	secret bool
}

var selectors = []field{{"TRAIO_DATABASE_DRIVER", false}, {"TRAIO_AUTH_MODE", false}}
var common = []field{{"TRAIO_DATABASE_DSN", true}, {"TRAIO_COOKIE_SECURE", false}, {"TRAIO_SESSION_TTL", false}}
var oidc = []field{{"TRAIO_OIDC_ISSUER_URL", false}, {"TRAIO_OIDC_CLIENT_ID", false}, {"TRAIO_OIDC_CLIENT_SECRET", true}, {"TRAIO_OIDC_REDIRECT_URL", false}}
var password = []field{{"TRAIO_BOOTSTRAP_ADMIN_USERNAME", false}, {"TRAIO_BOOTSTRAP_ADMIN_PASSWORD", true}, {"TRAIO_BOOTSTRAP_ADMIN_EMAIL", false}, {"TRAIO_BOOTSTRAP_ADMIN_NAME", false}}

func allFields() []field {
	var out []field
	for _, group := range [][]field{selectors, common, oidc, password} {
		out = append(out, group...)
	}
	return out
}

// Load reads startup configuration once; provider credentials are local-only.
func Load(ctx context.Context, baseDir string) (Result, error) {
	return LoadFrom(ctx, baseDir, os.Getenv, NewInfisical)
}

// LoadFrom permits alternative providers and deterministic testing without global environment writes.
func LoadFrom(ctx context.Context, baseDir string, get func(string) string, factory ProviderFactory) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, LoadTimeout)
	defer cancel()
	result := Result{Sources: map[string]string{}}
	values := map[string]string{"TRAIO_DEPLOYMENT_MODE": strings.TrimSpace(get("TRAIO_DEPLOYMENT_MODE"))}
	for _, f := range allFields() {
		value := get(f.name)
		source := "environment"
		if f.secret {
			var err error
			value, err = config.ResolveSecretFrom(get, f.name, f.name+"_FILE")
			if err != nil {
				return Result{}, err
			}
			if strings.TrimSpace(get(f.name+"_FILE")) != "" {
				source = "file"
			}
		} else {
			value = strings.TrimSpace(value)
		}
		if value == "" {
			continue
		}
		if err := validateField(f.name, value); err != nil {
			return Result{}, err
		}
		values[f.name], result.Sources[f.name] = value, source
	}
	if d := strings.ToLower(values["TRAIO_DATABASE_DRIVER"]); (d == "postgres" || d == "postgresql") && values["TRAIO_DATABASE_DSN"] != "" {
		if _, err := pgx.ParseConfig(values["TRAIO_DATABASE_DSN"]); err != nil {
			return Result{}, fmt.Errorf("invalid TRAIO_DATABASE_DSN for PostgreSQL")
		}
	}
	providerConfig, err := readProviderConfig(get)
	if err != nil {
		return Result{}, err
	}
	var provider Provider
	unavailable := map[string]error{}
	// Provider construction is lazy: an unused provider never authenticates.
	fill := func(fields []field, selection bool) error {
		var keys []string
		for _, f := range fields {
			if values[f.name] == "" {
				keys = append(keys, f.name)
			}
		}
		if len(keys) == 0 || providerConfig.Name == "none" {
			return nil
		}
		if provider == nil {
			if factory == nil {
				return fmt.Errorf("configuration provider factory is required")
			}
			provider, err = factory(providerConfig)
			if err != nil || provider == nil {
				return fmt.Errorf("configuration provider initialization failed")
			}
		}
		fetched, fetchErr := provider.Fetch(ctx, keys)
		for _, key := range keys {
			keyErr := fetchErr
			if keyErr == nil {
				keyErr = fetched.Unavailable[key]
			}
			if keyErr != nil {
				unavailable[key] = keyErr
				if selection {
					return fmt.Errorf("cannot retrieve configuration selector %s: %s", key, providerFailureSummary(keyErr))
				}
				result.Warnings = append(result.Warnings, key+": "+providerFailureSummary(keyErr)+"; local/default configuration will be used if permitted")
				continue
			}
			value := fetched.Values[key]
			for _, f := range fields {
				if f.name == key && !f.secret {
					value = strings.TrimSpace(value)
				}
			}
			if value == "" {
				continue
			}
			if err := validateField(key, value); err != nil {
				return err
			}
			values[key], result.Sources[key] = value, providerConfig.Name
		}
		return nil
	}
	if err := fill(selectors, true); err != nil {
		return Result{}, err
	}
	driver := strings.ToLower(values["TRAIO_DATABASE_DRIVER"])
	if driver == "" {
		driver = "sqlite"
	}
	mode := auth.Mode(strings.ToLower(values["TRAIO_AUTH_MODE"]))
	if mode == "" {
		mode = auth.ModeLocal
		if strings.EqualFold(values["TRAIO_DEPLOYMENT_MODE"], config.DeploymentModeServer) {
			mode = auth.ModeOIDC
		}
	}
	if mode == auth.ModeDisabledDev && strings.EqualFold(values["TRAIO_DEPLOYMENT_MODE"], config.DeploymentModeServer) {
		return Result{}, fmt.Errorf("disabled-dev authentication is not allowed in server deployment mode")
	}
	fields := append([]field(nil), common...)
	if mode == auth.ModeOIDC {
		fields = append(fields, oidc...)
	}
	if mode == auth.ModePassword {
		fields = append(fields, password...)
	}
	if err := fill(fields, false); err != nil {
		return Result{}, err
	}
	// An unavailable SQLite DSN might refer to a different file; never silently
	// create a new database. An absent SQLite DSN still uses the existing default.
	if err := unavailable["TRAIO_DATABASE_DSN"]; err != nil {
		return Result{}, fmt.Errorf("cannot retrieve TRAIO_DATABASE_DSN: %s", providerFailureSummary(err))
	}
	lookup := func(key string) string { return values[key] }
	if driver == "sqlite" {
		dsn := strings.ToLower(strings.TrimSpace(values["TRAIO_DATABASE_DSN"]))
		if strings.HasPrefix(dsn, "postgres:") || strings.HasPrefix(dsn, "postgresql:") {
			return Result{}, fmt.Errorf("TRAIO_DATABASE_DRIVER must select PostgreSQL for this TRAIO_DATABASE_DSN")
		}
	}
	result.Database = config.ResolveBootstrapDatabaseFrom(baseDir, lookup)
	if driver == "postgres" || driver == "postgresql" {
		if strings.TrimSpace(result.Database.DataSource) == "" {
			return Result{}, fmt.Errorf("TRAIO_DATABASE_DSN is required for PostgreSQL")
		}
		if _, err := pgx.ParseConfig(result.Database.DataSource); err != nil {
			return Result{}, fmt.Errorf("invalid TRAIO_DATABASE_DSN for PostgreSQL")
		}
	}
	result.Auth, err = config.ResolveAuthConfigFrom(lookup)
	if err != nil {
		return Result{}, err
	}
	// Missing bootstrap credentials are checked against the database by NewService.
	// Missing OIDC secrets caused by transport/permission failures cannot silently
	// turn a confidential client into a public client.
	if err := unavailable["TRAIO_OIDC_CLIENT_SECRET"]; mode == auth.ModeOIDC && err != nil {
		return Result{}, fmt.Errorf("cannot retrieve TRAIO_OIDC_CLIENT_SECRET: %s", providerFailureSummary(err))
	}
	if result.Sources["TRAIO_DATABASE_DRIVER"] == "" {
		result.Sources["TRAIO_DATABASE_DRIVER"] = "default"
	}
	if result.Sources["TRAIO_AUTH_MODE"] == "" {
		result.Sources["TRAIO_AUTH_MODE"] = "default"
	}
	return result, nil
}

func validateField(key, value string) error {
	invalid := func() error { return fmt.Errorf("invalid %s", key) }
	switch key {
	case "TRAIO_DATABASE_DRIVER":
		switch strings.ToLower(value) {
		case "sqlite", "postgres", "postgresql":
		default:
			return invalid()
		}
	case "TRAIO_AUTH_MODE":
		switch auth.Mode(strings.ToLower(value)) {
		case auth.ModeLocal, auth.ModePassword, auth.ModeOIDC, auth.ModeDisabledDev:
		default:
			return invalid()
		}
	case "TRAIO_COOKIE_SECURE":
		if _, err := strconv.ParseBool(value); err != nil {
			return invalid()
		}
	case "TRAIO_SESSION_TTL":
		if d, err := time.ParseDuration(value); err != nil || d <= 0 {
			return invalid()
		}
	case "TRAIO_OIDC_ISSUER_URL", "TRAIO_OIDC_REDIRECT_URL":
		u, err := url.Parse(value)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" {
			return invalid()
		}
	}
	return nil
}
