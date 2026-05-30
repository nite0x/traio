package ibkr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nite/traio/internal/config"
)

const sampleConf = `    ip2loc: "US"
    proxyRemoteSsl: true
    proxyRemoteHost: "https://api.ibkr.com"
    listenPort: 5001
    listenSsl: true
    ips:
      allow:
        - 192.*
        - 127.0.0.1
      deny:
        - 212.90.324.10
`

func writeConf(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	conf := filepath.Join(dir, "conf.yaml")
	if err := os.WriteFile(conf, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return conf
}

// --- patchListenPort (legacy) ---

func TestPatchListenPortIndented(t *testing.T) {
	conf := writeConf(t, "    ip2loc: \"US\"\n    listenPort: 5001\n    listenSsl: true\n")
	if err := patchListenPort(conf, 5680); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(conf)
	want := "    listenPort: 5680"
	if string(data) != "    ip2loc: \"US\"\n"+want+"\n    listenSsl: true\n" {
		t.Fatalf("unexpected conf:\n%s", data)
	}
}

// --- patchGatewayConf ---

func TestPatchGatewayConf_Port(t *testing.T) {
	conf := writeConf(t, sampleConf)
	cfg := config.IBKRConfig{
		GatewayPort:      5680,
		GatewayProxyHost: "https://api.ibkr.com",
		GatewayAllowIPs:  []string{"127.0.0.1"},
	}
	if err := patchGatewayConf(conf, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(conf)
	if !strings.Contains(string(data), "listenPort: 5680") {
		t.Fatalf("listenPort not updated:\n%s", data)
	}
}

func TestPatchGatewayConf_ProxyHost(t *testing.T) {
	conf := writeConf(t, sampleConf)
	cfg := config.IBKRConfig{
		GatewayPort:      5001,
		GatewayProxyHost: "https://paper-api.ibkr.com",
		GatewayAllowIPs:  []string{"127.0.0.1"},
	}
	if err := patchGatewayConf(conf, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(conf)
	if !strings.Contains(string(data), "paper-api.ibkr.com") {
		t.Fatalf("proxyRemoteHost not updated:\n%s", data)
	}
}

func TestPatchGatewayConf_AllowIPs(t *testing.T) {
	conf := writeConf(t, sampleConf)
	cfg := config.IBKRConfig{
		GatewayPort:      5001,
		GatewayProxyHost: "https://api.ibkr.com",
		GatewayAllowIPs:  []string{"127.0.0.1", "10.0.0.1"},
	}
	if err := patchGatewayConf(conf, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(conf)
	s := string(data)
	if !strings.Contains(s, "- 127.0.0.1") || !strings.Contains(s, "- 10.0.0.1") {
		t.Fatalf("allow IPs not updated:\n%s", s)
	}
	// Old entry should be gone.
	if strings.Contains(s, "- 192.*") {
		t.Fatalf("old IP still present:\n%s", s)
	}
}

func TestPatchGatewayConf_DenyUnchanged(t *testing.T) {
	conf := writeConf(t, sampleConf)
	cfg := config.IBKRConfig{
		GatewayPort:      5001,
		GatewayProxyHost: "https://api.ibkr.com",
		GatewayAllowIPs:  []string{"127.0.0.1"},
	}
	if err := patchGatewayConf(conf, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(conf)
	// deny block must be untouched.
	if !strings.Contains(string(data), "- 212.90.324.10") {
		t.Fatalf("deny block was modified:\n%s", data)
	}
}
