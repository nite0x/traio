package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	// DefaultServerPort is used by the packaged desktop app and MCP clients.
	DefaultServerPort = 38180
	// DevServerPort is used for local development (go run, make server, tauri dev).
	DevServerPort = 38181
	// DevIBKRGatewayPort keeps locally-run development Gateways on IBKR's
	// conventional port.
	DevIBKRGatewayPort = 5680
	// DesktopIBKRGatewayPort starts the packaged desktop allocation range far
	// enough from development to keep multiple Gateways isolated.
	DesktopIBKRGatewayPort = 5780
	// IBKRGatewayPortRangeSize is the number of automatically allocated ports
	// reserved for one Traio runtime class.
	IBKRGatewayPortRangeSize = 20

	IBKRGatewayLifecycleManaged    = "managed"
	IBKRGatewayLifecyclePersistent = "persistent"

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
	SubAccount        string `json:"sub_account" yaml:"sub_account"`
	FlexToken         string `json:"flex_token" yaml:"flex_token"`
	FlexQueryID       string `json:"flex_query_id" yaml:"flex_query_id"`
	FlexBaseURL       string `json:"flex_base_url" yaml:"flex_base_url"`
	GatewayDir        string `json:"gateway_dir" yaml:"gateway_dir"`
	BundledGatewayDir string `json:"bundled_gateway_dir" yaml:"bundled_gateway_dir"`
	GatewayPort       int    `json:"gateway_port" yaml:"gateway_port"`
	GatewayURL        string `json:"gateway_url" yaml:"gateway_url"`
	GatewayLifecycle  string `json:"gateway_lifecycle" yaml:"gateway_lifecycle"`
	DownloadProxy     string `json:"download_proxy" yaml:"download_proxy"`
	// GatewayProxyHost overrides the IBKR API endpoint in conf.yaml.
	// Keep the official https://api.ibkr.com default unless IBKR documents
	// another endpoint for the account type in use.
	GatewayProxyHost string `json:"gateway_proxy_host" yaml:"gateway_proxy_host"`
	// GatewayAllowIPs is the IP whitelist written into conf.yaml ips.allow.
	// Defaults to ["127.0.0.1"] when empty.
	GatewayAllowIPs []string `json:"gateway_allow_ips" yaml:"gateway_allow_ips"`
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

func (c *IBKRConfig) normalize(baseDir string) {
	c.resolvePath(&c.GatewayDir, baseDir)
	c.resolvePath(&c.BundledGatewayDir, baseDir)
	if c.GatewayDir == "" {
		c.GatewayDir = DefaultIBKRGatewayDir(baseDir, "local")
	}
	if c.BundledGatewayDir == "" {
		c.BundledGatewayDir = ResolveBundledGatewayDir()
		if c.BundledGatewayDir == "" {
			c.BundledGatewayDir = filepath.Join(baseDir, "third_party", "clientportal.gw")
		}
	}
	if c.GatewayPort == 0 {
		c.GatewayPort = ResolveIBKRGatewayPort()
	}
	if c.GatewayURL == "" {
		c.GatewayURL = fmt.Sprintf("https://localhost:%d", c.GatewayPort)
	}
	c.GatewayURL = strings.TrimSuffix(strings.TrimRight(c.GatewayURL, "/"), "/v1/api")
	c.GatewayLifecycle = NormalizeIBKRGatewayLifecycle(c.GatewayLifecycle)
	if c.FlexBaseURL == "" {
		c.FlexBaseURL = "https://ndcdyn.interactivebrokers.com/AccountManagement/FlexWebService"
	}
	c.FlexBaseURL = strings.TrimRight(c.FlexBaseURL, "/")
	if c.GatewayProxyHost == "" {
		c.GatewayProxyHost = "https://api.ibkr.com"
	}
	if len(c.GatewayAllowIPs) == 0 {
		c.GatewayAllowIPs = []string{"127.0.0.1"}
	}
}

func (c *IBKRConfig) resolvePath(p *string, baseDir string) {
	if *p == "" || filepath.IsAbs(*p) {
		return
	}
	*p = filepath.Join(baseDir, *p)
}

// ResolveServerPort picks the HTTP listen port for traio-server.
// TRAIO_SERVER_PORT overrides; embedded .app binaries use DefaultServerPort;
// everything else (go run, dev sidecar) uses DevServerPort.
func ResolveServerPort() int {
	if v := os.Getenv("TRAIO_SERVER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			return p
		}
	}
	if IsEmbedded() {
		return DefaultServerPort
	}
	return DevServerPort
}

// ResolveIBKRGatewayPort selects the default port for a newly managed local
// Gateway. Persisted Gateway records remain authoritative once created.
func ResolveIBKRGatewayPort() int {
	return resolveIBKRGatewayPort(IsEmbedded())
}

// ResolveIBKRGatewayPortRange returns the inclusive automatic allocation range.
// TRAIO_IBKR_GATEWAY_PORT overrides the first port in the range.
func ResolveIBKRGatewayPortRange() (int, int) {
	start := ResolveIBKRGatewayPort()
	end := start + IBKRGatewayPortRangeSize - 1
	if end > 65535 {
		end = 65535
	}
	return start, end
}

func resolveIBKRGatewayPort(embedded bool) int {
	if v := os.Getenv("TRAIO_IBKR_GATEWAY_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			return p
		}
	}
	if embedded {
		return DesktopIBKRGatewayPort
	}
	return DevIBKRGatewayPort
}

// LocalAPIURL builds a loopback base URL for the given port.
func LocalAPIURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// ResolveServerAPIURL is the API base URL for the running traio-server instance.
func ResolveServerAPIURL() string {
	return LocalAPIURL(ResolveServerPort())
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

// ResolveIBKRProxyURL is the public origin dedicated to the IBKR login proxy,
// for example https://alice-ibkr.traio.example.com.
func ResolveIBKRProxyURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("TRAIO_IBKR_PROXY_URL")), "/")
}

// ResolveIBKRGatewayLifecycle selects how the Gateway behaves when Traio
// exits. Packaged desktop builds preserve the local session; server and Docker
// deployments own the Gateway for the lifetime of the Traio process.
func ResolveIBKRGatewayLifecycle() string {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("TRAIO_IBKR_GATEWAY_LIFECYCLE")))
	switch value {
	case IBKRGatewayLifecycleManaged, IBKRGatewayLifecyclePersistent:
		return value
	case "":
		if IsEmbedded() {
			return IBKRGatewayLifecyclePersistent
		}
	}
	return IBKRGatewayLifecycleManaged
}

// NormalizeIBKRGatewayLifecycle validates a connection override and otherwise
// applies the process-level desktop/server default.
func NormalizeIBKRGatewayLifecycle(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case IBKRGatewayLifecycleManaged:
		return IBKRGatewayLifecycleManaged
	case IBKRGatewayLifecyclePersistent:
		return IBKRGatewayLifecyclePersistent
	default:
		return ResolveIBKRGatewayLifecycle()
	}
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

// DefaultDesktopRuntimeDir returns the writable root used by the packaged
// macOS desktop application.
func DefaultDesktopRuntimeDir(home string) string {
	return filepath.Join(home, "Library", "Application Support", "Traio")
}

// DefaultIBKRGatewayRoot contains all managed Gateway instances for one Traio
// runtime. Each instance gets a stable directory derived from gateway_key.
func DefaultIBKRGatewayRoot(runtimeDir string) string {
	return filepath.Join(runtimeDir, "ibkr-gateways")
}

// DefaultIBKRGatewayDir returns an empty string when gatewayKey is unsafe to
// use as a path component. Callers may still supply a validated absolute path.
func DefaultIBKRGatewayDir(runtimeDir, gatewayKey string) string {
	key := strings.TrimSpace(gatewayKey)
	if key == "" || key == "." || key == ".." || filepath.IsAbs(key) || strings.ContainsAny(key, `/\`) {
		return ""
	}
	return filepath.Join(DefaultIBKRGatewayRoot(runtimeDir), key)
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

// ResolveBundledGatewayDir locates the packaged IBKR gateway next to the executable.
func ResolveBundledGatewayDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	candidates := []string{
		filepath.Join(filepath.Dir(exe), "third_party", "clientportal.gw"),
		filepath.Join(ResolveBaseDir(), "third_party", "clientportal.gw"),
	}
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "bin", "run.sh")); err == nil {
			return dir
		}
	}
	return filepath.Join(ResolveRuntimeDir(), "third_party", "clientportal.gw")
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
