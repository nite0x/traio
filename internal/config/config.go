package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nite/traio/internal/auth"
)

// Config holds all Traio runtime settings (persisted in SQLite, editable via UI).
type Config struct {
	Database   DatabaseConfig   `json:"database" yaml:"database"`
	BrokerSync BrokerSyncConfig `json:"broker_sync" yaml:"broker_sync"`
	SnapTrade  SnapTradeConfig  `json:"snaptrade" yaml:"snaptrade"`
	Finnhub    FinnhubConfig    `json:"finnhub" yaml:"finnhub"`
	Claude     ClaudeConfig     `json:"claude" yaml:"claude"`
}

// BrokerSyncConfig controls the IBKR account projection synchronization loop.
type BrokerSyncConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

const (
	// PackagedServerPort asks the operating system to allocate an available
	// loopback port for the packaged desktop sidecar. The desktop shell reads
	// the resolved address from its runtime directory after startup.
	PackagedServerPort = 0
	// DevServerPort is used for local development (go run, make server, tauri dev).
	DevServerPort = 38181

	DeploymentModeServer    = "server"
	DefaultServerRuntimeDir = "/var/lib/traio"
)

type DatabaseConfig struct {
	Path string `json:"path" yaml:"path"`
}

// BootstrapDatabaseConfig is resolved before persisted application settings
// can be loaded. Database credentials therefore belong in process-level
// configuration rather than in Config, which is itself stored in the database.
type BootstrapDatabaseConfig struct {
	Driver     string
	DataSource string
}

// ResolveAuthConfig selects local instance-token authentication for desktop
// runtimes and a browser-session authentication mode for server deployments.
func ResolveAuthConfig() (auth.Config, error) {
	deploymentMode := strings.ToLower(strings.TrimSpace(os.Getenv("TRAIO_DEPLOYMENT_MODE")))
	mode := auth.Mode(strings.ToLower(strings.TrimSpace(os.Getenv("TRAIO_AUTH_MODE"))))
	if mode == "" {
		if deploymentMode == DeploymentModeServer {
			mode = auth.ModeOIDC
		} else {
			mode = auth.ModeLocal
		}
	}
	if mode != auth.ModeLocal && mode != auth.ModeOIDC && mode != auth.ModePassword && mode != auth.ModeDisabledDev {
		return auth.Config{}, fmt.Errorf("unsupported TRAIO_AUTH_MODE %q", mode)
	}
	if mode == auth.ModeDisabledDev && deploymentMode == DeploymentModeServer {
		return auth.Config{}, fmt.Errorf("disabled-dev authentication is not allowed in server deployment mode")
	}
	redirectURL := strings.TrimSpace(os.Getenv("TRAIO_OIDC_REDIRECT_URL"))
	secureCookie := false
	if parsed, err := url.Parse(redirectURL); err == nil {
		secureCookie = strings.EqualFold(parsed.Scheme, "https")
	}
	if value := strings.TrimSpace(os.Getenv("TRAIO_COOKIE_SECURE")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return auth.Config{}, fmt.Errorf("invalid TRAIO_COOKIE_SECURE %q", value)
		}
		secureCookie = parsed
	}
	sessionTTL := 12 * time.Hour
	if value := strings.TrimSpace(os.Getenv("TRAIO_SESSION_TTL")); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return auth.Config{}, fmt.Errorf("invalid TRAIO_SESSION_TTL %q", value)
		}
		sessionTTL = parsed
	}
	bootstrapPassword, err := resolveSecret("TRAIO_BOOTSTRAP_ADMIN_PASSWORD", "TRAIO_BOOTSTRAP_ADMIN_PASSWORD_FILE")
	if err != nil {
		return auth.Config{}, err
	}
	config := auth.Config{
		Mode:              mode,
		IssuerURL:         strings.TrimRight(strings.TrimSpace(os.Getenv("TRAIO_OIDC_ISSUER_URL")), "/"),
		ClientID:          strings.TrimSpace(os.Getenv("TRAIO_OIDC_CLIENT_ID")),
		ClientSecret:      strings.TrimSpace(os.Getenv("TRAIO_OIDC_CLIENT_SECRET")),
		RedirectURL:       redirectURL,
		SessionTTL:        sessionTTL,
		CookieSecure:      secureCookie,
		BootstrapUsername: strings.TrimSpace(os.Getenv("TRAIO_BOOTSTRAP_ADMIN_USERNAME")),
		BootstrapPassword: bootstrapPassword,
		BootstrapEmail:    strings.TrimSpace(os.Getenv("TRAIO_BOOTSTRAP_ADMIN_EMAIL")),
		BootstrapName:     strings.TrimSpace(os.Getenv("TRAIO_BOOTSTRAP_ADMIN_NAME")),
	}
	if mode == auth.ModeOIDC && (config.IssuerURL == "" || config.ClientID == "" || config.RedirectURL == "") {
		return auth.Config{}, fmt.Errorf("OIDC server mode requires TRAIO_OIDC_ISSUER_URL, TRAIO_OIDC_CLIENT_ID, and TRAIO_OIDC_REDIRECT_URL")
	}
	if mode == auth.ModePassword && (config.BootstrapUsername == "") != (config.BootstrapPassword == "") {
		return auth.Config{}, fmt.Errorf("built-in login bootstrap requires both TRAIO_BOOTSTRAP_ADMIN_USERNAME and TRAIO_BOOTSTRAP_ADMIN_PASSWORD")
	}
	return config, nil
}

func resolveSecret(valueEnv, fileEnv string) (string, error) {
	value, file := os.Getenv(valueEnv), strings.TrimSpace(os.Getenv(fileEnv))
	if value != "" && file != "" {
		return "", fmt.Errorf("set only one of %s or %s", valueEnv, fileEnv)
	}
	if file == "" {
		return value, nil
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fileEnv, err)
	}
	return strings.TrimRight(string(raw), "\r\n"), nil
}

type SnapTradeConfig struct {
	ClientID    string `json:"client_id" yaml:"client_id"`
	ConsumerKey string `json:"consumer_key" yaml:"consumer_key"`
}

type SchwabConfig struct {
	ClientID     string `json:"client_id" yaml:"client_id"`
	ClientSecret string `json:"client_secret" yaml:"client_secret"`
	RedirectURI  string `json:"redirect_uri" yaml:"redirect_uri"`
}

type AlpacaConfig struct {
	APIKey    string `json:"api_key" yaml:"api_key"`
	APISecret string `json:"api_secret" yaml:"api_secret"`
	// BaseURL is the trading API root, e.g. https://paper-api.alpaca.markets
	BaseURL string `json:"base_url" yaml:"base_url"`
}

type IBKRConfig struct {
	SubAccount   string `json:"sub_account" yaml:"sub_account"`
	FlexToken    string `json:"flex_token" yaml:"flex_token"`
	FlexQueryID  string `json:"flex_query_id" yaml:"flex_query_id"`
	FlexBaseURL  string `json:"flex_base_url" yaml:"flex_base_url"`
	GatewayURL   string `json:"gateway_url" yaml:"gateway_url"`
	GatewayToken string `json:"gateway_token" yaml:"gateway_token"`
}

type FinnhubConfig struct {
	APIKey string `json:"api_key" yaml:"api_key"`
}

type ClaudeConfig struct {
	APIKey string `json:"api_key" yaml:"api_key"`
	Model  string `json:"model" yaml:"model"`
}

// Default returns built-in defaults; no external config file required.
func Default(baseDir string) Config {
	cfg := Config{
		Database: DatabaseConfig{
			Path: filepath.Join(baseDir, "data", "traio.db"),
		},
		BrokerSync: BrokerSyncConfig{Enabled: true},
		SnapTrade:  SnapTradeConfig{},
		Finnhub:    FinnhubConfig{},
		Claude: ClaudeConfig{
			Model: "claude-sonnet-4-20250514",
		},
	}
	cfg.Normalize(baseDir)
	return cfg
}

// Normalize fills empty fields and resolves relative paths against baseDir.
func (c *Config) Normalize(baseDir string) {
	if c.Database.Path == "" {
		c.Database.Path = filepath.Join(baseDir, "data", "traio.db")
	} else if !filepath.IsAbs(c.Database.Path) {
		c.Database.Path = filepath.Join(baseDir, c.Database.Path)
	}
	if c.Claude.Model == "" {
		c.Claude.Model = "claude-sonnet-4-20250514"
	}
}

func (c *AlpacaConfig) Normalize() {
	base := strings.TrimSpace(c.BaseURL)
	if base == "" {
		base = "https://paper-api.alpaca.markets"
	}
	base = strings.TrimRight(base, "/")
	base = strings.TrimSuffix(base, "/v2")
	c.BaseURL = base
}

// ResolveServerPort picks the HTTP listen port for traio-server.
// TRAIO_SERVER_PORT overrides; embedded .app binaries ask the OS for an
// available port; everything else (go run, dev sidecar) uses DevServerPort.
func ResolveServerPort() int {
	return resolveServerPort(IsEmbedded())
}

func resolveServerPort(embedded bool) int {
	if v := os.Getenv("TRAIO_SERVER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			return p
		}
	}
	if embedded {
		return PackagedServerPort
	}
	return DevServerPort
}

// LocalAPIURL builds a loopback base URL for the given port.
func LocalAPIURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// ResolveServerListenAddr selects the socket address used by traio-server.
// Docker deployments can set TRAIO_LISTEN_ADDR=0.0.0.0:8080 while desktop
// builds retain the loopback-only default.
func ResolveServerListenAddr() string {
	if value := strings.TrimSpace(os.Getenv("TRAIO_LISTEN_ADDR")); value != "" {
		return value
	}
	return fmt.Sprintf("127.0.0.1:%d", ResolveServerPort())
}

// ResolveAllowedAPIHosts returns exact, comma-separated public API hosts that
// are allowed in addition to loopback hosts.
func ResolveAllowedAPIHosts() []string {
	parts := strings.Split(os.Getenv("TRAIO_ALLOWED_API_HOSTS"), ",")
	hosts := make([]string, 0, len(parts))
	for _, part := range parts {
		if host := strings.TrimSpace(part); host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// ResolveAllowedOrigins returns exact browser origins that may call the API
// from a separately hosted web frontend. Origins are comma-separated and must
// include their scheme, for example https://traio-web.vercel.app.
func ResolveAllowedOrigins() []string {
	parts := strings.Split(os.Getenv("TRAIO_ALLOWED_ORIGINS"), ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		if origin := strings.TrimRight(strings.TrimSpace(part), "/"); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}

// ResolveBootstrapDatabase selects the process database from the environment.
// Desktop and development runs default to the embedded SQLite database.
func ResolveBootstrapDatabase(baseDir string) BootstrapDatabaseConfig {
	driver := strings.ToLower(strings.TrimSpace(os.Getenv("TRAIO_DATABASE_DRIVER")))
	if driver == "" {
		driver = "sqlite"
	}
	dataSource := strings.TrimSpace(os.Getenv("TRAIO_DATABASE_DSN"))
	if dataSource == "" && driver == "sqlite" {
		dataSource = filepath.Join(baseDir, "data", "traio.db")
	}
	return BootstrapDatabaseConfig{Driver: driver, DataSource: dataSource}
}

// ResolveRuntimeDir is the writable data root. Explicit configuration wins;
// packaged desktop apps use Application Support, and server deployments use
// /var/lib/traio.
func ResolveRuntimeDir() string {
	if v := strings.TrimSpace(os.Getenv("TRAIO_RUNTIME_DIR")); v != "" {
		_ = os.MkdirAll(v, 0o700)
		return v
	}
	if IsEmbedded() {
		home, err := os.UserHomeDir()
		if err != nil {
			return "."
		}
		dir := DefaultDesktopRuntimeDir(home)
		_ = os.MkdirAll(dir, 0o700)
		return dir
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TRAIO_DEPLOYMENT_MODE")), DeploymentModeServer) {
		_ = os.MkdirAll(DefaultServerRuntimeDir, 0o700)
		return DefaultServerRuntimeDir
	}
	return ResolveBaseDir()
}

// ResolveWebDir returns an optional Vite production build served by the Go
// process. Container images set this to /opt/traio/web; desktop builds leave it
// empty because Tauri owns the frontend lifecycle.
func ResolveWebDir() string {
	return strings.TrimSpace(os.Getenv("TRAIO_WEB_DIR"))
}

// DefaultDesktopRuntimeDir returns the writable root used by the packaged
// macOS desktop application.
func DefaultDesktopRuntimeDir(home string) string {
	return filepath.Join(home, "Library", "Application Support", "Traio")
}

// IsEmbedded reports whether this binary runs from a macOS .app bundle.
func IsEmbedded() bool {
	exe, err := os.Executable()
	return err == nil && isEmbeddedExecutable(exe)
}

func isEmbeddedExecutable(exe string) bool {
	return strings.Contains(exe, ".app/Contents/MacOS/") ||
		strings.Contains(exe, ".app/Contents/Resources/")
}

// ResolveBaseDir picks project dir (dev) or binary dir (release).
func ResolveBaseDir() string {
	if wd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		if _, err := os.Stat(filepath.Join(wd, "third_party")); err == nil {
			return wd
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}
