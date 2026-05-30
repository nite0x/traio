package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds all Traio runtime settings (persisted in SQLite, editable via UI).
type Config struct {
	Database  DatabaseConfig  `json:"database" yaml:"database"`
	SnapTrade SnapTradeConfig `json:"snaptrade" yaml:"snaptrade"`
	Schwab    SchwabConfig    `json:"schwab" yaml:"schwab"`
	IBKR      IBKRConfig      `json:"ibkr" yaml:"ibkr"`
	Finnhub   FinnhubConfig   `json:"finnhub" yaml:"finnhub"`
	Claude    ClaudeConfig    `json:"claude" yaml:"claude"`
}

const DefaultServerPort = 38180

type DatabaseConfig struct {
	Path string `json:"path" yaml:"path"`
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

type IBKRConfig struct {
	SubAccount        string `json:"sub_account" yaml:"sub_account"`
	Password          string `json:"password" yaml:"password"`
	TOTPSecret        string `json:"totp_secret" yaml:"totp_secret"`
	FlexToken         string `json:"flex_token" yaml:"flex_token"`
	FlexQueryID       string `json:"flex_query_id" yaml:"flex_query_id"`
	FlexBaseURL       string `json:"flex_base_url" yaml:"flex_base_url"`
	GatewayDir        string `json:"gateway_dir" yaml:"gateway_dir"`
	BundledGatewayDir string `json:"bundled_gateway_dir" yaml:"bundled_gateway_dir"`
	GatewayPort       int    `json:"gateway_port" yaml:"gateway_port"`
	GatewayURL        string `json:"gateway_url" yaml:"gateway_url"`
	DownloadProxy     string `json:"download_proxy" yaml:"download_proxy"`
	// GatewayProxyHost overrides the IBKR API endpoint in conf.yaml.
	// Use "https://paper-api.ibkr.com" for paper trading.
	// Defaults to "https://api.ibkr.com" when empty.
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
	bundledGW := ResolveBundledGatewayDir()
	if bundledGW == "" {
		bundledGW = filepath.Join(baseDir, "third_party", "clientportal.gw")
	}
	cfg := Config{
		Database: DatabaseConfig{
			Path: filepath.Join(baseDir, "data", "traio.db"),
		},
		SnapTrade: SnapTradeConfig{},
		Schwab: SchwabConfig{
			RedirectURI: "https://127.0.0.1:8182",
		},
		IBKR: IBKRConfig{
			GatewayDir:        filepath.Join(baseDir, "ibkr-gateway"),
			BundledGatewayDir: bundledGW,
			GatewayPort:       5680,
			GatewayURL:        "https://localhost:5680",
		},
		Finnhub: FinnhubConfig{},
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
	if c.Schwab.RedirectURI == "" {
		c.Schwab.RedirectURI = "https://127.0.0.1:8182"
	}
	if c.Claude.Model == "" {
		c.Claude.Model = "claude-sonnet-4-20250514"
	}
	c.IBKR.normalize(baseDir)
}

func (c *IBKRConfig) normalize(baseDir string) {
	c.resolvePath(&c.GatewayDir, baseDir)
	c.resolvePath(&c.BundledGatewayDir, baseDir)
	if c.GatewayDir == "" {
		c.GatewayDir = filepath.Join(baseDir, "ibkr-gateway")
	}
	if c.BundledGatewayDir == "" {
		c.BundledGatewayDir = ResolveBundledGatewayDir()
		if c.BundledGatewayDir == "" {
			c.BundledGatewayDir = filepath.Join(baseDir, "third_party", "clientportal.gw")
		}
	}
	if c.GatewayPort == 0 {
		c.GatewayPort = 5680
	}
	if c.GatewayURL == "" {
		c.GatewayURL = fmt.Sprintf("https://localhost:%d", c.GatewayPort)
	}
	c.GatewayURL = strings.TrimSuffix(strings.TrimRight(c.GatewayURL, "/"), "/v1/api")
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

// ResolveRuntimeDir is the writable data root (App Support when embedded in .app).
func ResolveRuntimeDir() string {
	if v := os.Getenv("TRAIO_RUNTIME_DIR"); v != "" {
		_ = os.MkdirAll(v, 0o755)
		return v
	}
	if IsEmbedded() {
		home, err := os.UserHomeDir()
		if err != nil {
			return "."
		}
		dir := filepath.Join(home, "Library", "Application Support", "Traio")
		_ = os.MkdirAll(dir, 0o755)
		return dir
	}
	return ResolveBaseDir()
}

// IsEmbedded reports whether this binary runs from a macOS .app bundle Resources folder.
func IsEmbedded() bool {
	exe, err := os.Executable()
	return err == nil && strings.Contains(exe, ".app/Contents/Resources")
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
