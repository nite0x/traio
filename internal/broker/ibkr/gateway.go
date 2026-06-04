package ibkr

import (
	"archive/zip"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nite/traio/internal/config"
)

// pidFile returns the path used to track the gateway's OS process ID.
func pidFile(gatewayDir string) string {
	return filepath.Join(gatewayDir, "gateway.pid")
}

const (
	gatewayDownloadURL = "https://download2.interactivebrokers.com/portal/clientportal.gw.zip"
	startupTimeout     = 30 * time.Second
)

// GatewayStatus is the public gateway state exposed via REST API.
type GatewayStatus struct {
	Running           bool   `json:"running"`
	Authenticated     bool   `json:"authenticated"`
	Account           string `json:"account"`
	SessionAgeSeconds int64  `json:"session_age_seconds"`
	LoginMode         string `json:"login_mode"` // auto | manual
	LoginURL          string `json:"login_url,omitempty"`
	AuthMessage       string `json:"auth_message,omitempty"`
}

// GatewayManager manages IBKR Client Portal Gateway lifecycle.
type GatewayManager struct {
	config     config.IBKRConfig
	cmd        *exec.Cmd
	httpClient *http.Client

	mu              sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc
	authenticatedAt time.Time
	account         string
	monitorsStarted bool
	restarting      atomic.Bool
}

func NewGatewayManager(cfg config.IBKRConfig) *GatewayManager {
	return &GatewayManager{
		config: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // local self-signed gateway cert
			},
		},
	}
}

// UpdateConfig replaces IBKR settings and should be followed by Reconnect().
func (g *GatewayManager) UpdateConfig(cfg config.IBKRConfig) {
	g.mu.Lock()
	g.config = cfg
	g.mu.Unlock()
}

// Start ensures gateway is installed, running, authenticated, and monitored.
func (g *GatewayManager) Start(ctx context.Context) error {
	g.mu.Lock()
	if g.cancel != nil {
		g.cancel()
	}
	g.ctx, g.cancel = context.WithCancel(ctx)
	runCtx := g.ctx
	g.mu.Unlock()

	if err := g.EnsureInstalled(); err != nil {
		return fmt.Errorf("ensure installed: %w", err)
	}
	if err := g.EnsureRunning(runCtx); err != nil {
		return fmt.Errorf("ensure running: %w", err)
	}
	if err := g.EnsureAuthenticated(runCtx); err != nil {
		log.Printf("[IBKR] authentication pending: %v", err)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.monitorsStarted {
		g.StartTickler(runCtx)
		g.StartHealthMonitor(runCtx)
		g.monitorsStarted = true
	}
	return nil
}

// Stop shuts down background tasks and kills the gateway process.
func (g *GatewayManager) Stop() {
	g.StopGateway(false)
}

// StartGateway ensures the IBKR gateway process is running and monitored.
func (g *GatewayManager) StartGateway(ctx context.Context) error {
	g.mu.Lock()
	if g.ctx == nil || g.cancel == nil {
		g.ctx, g.cancel = context.WithCancel(ctx)
	}
	runCtx := g.ctx
	g.mu.Unlock()

	if err := g.EnsureInstalled(); err != nil {
		return fmt.Errorf("ensure installed: %w", err)
	}
	if err := g.EnsureRunning(runCtx); err != nil {
		return fmt.Errorf("ensure running: %w", err)
	}
	if err := g.EnsureAuthenticated(runCtx); err != nil {
		log.Printf("[IBKR] authentication pending: %v", err)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.monitorsStarted {
		g.StartTickler(runCtx)
		g.StartHealthMonitor(runCtx)
		g.monitorsStarted = true
	}
	return nil
}

// StopGateway stops monitoring and optionally kills the gateway process.
// When keepSession is true, the Java process keeps running (session preserved);
// traio simply detaches. When false, the process is killed.
func (g *GatewayManager) StopGateway(keepSession bool) {
	g.mu.Lock()
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
	g.ctx = nil
	g.monitorsStarted = false
	g.mu.Unlock()

	if keepSession {
		g.mu.Lock()
		g.cmd = nil
		g.mu.Unlock()
		log.Println("[IBKR] detached from gateway (session preserved)")
		return
	}

	g.stopProcess()
	killProcessOnPort(g.config.GatewayPort)
	_ = os.Remove(pidFile(g.config.GatewayDir))
	g.resetSession()
}

func (g *GatewayManager) Status() GatewayStatus {
	tickle, online := g.fetchTickle()
	status := GatewayStatus{
		Running:   online,
		Account:   g.account,
		LoginMode: g.loginMode(),
	}
	if !g.hasCredentials() {
		status.LoginURL = g.config.GatewayURL + "/sso/Login"
	}
	if tickle != nil {
		status.Authenticated = tickleAuthenticated(tickle)
		if acct := tickleAccount(tickle); acct != "" {
			status.Account = acct
		}
	}
	if online && !status.Authenticated {
		if auth, acct := g.fetchAuthStatus(); auth {
			status.Authenticated = true
			if acct != "" {
				status.Account = acct
			}
			g.markAuthenticated(acct)
		} else if ok, msg := g.fetchSSOValidate(); ok {
			status.Authenticated = true
			g.markAuthenticated("")
		} else if msg != "" {
			status.AuthMessage = msg
		}
	}
	if status.Authenticated {
		g.mu.Lock()
		if g.authenticatedAt.IsZero() {
			g.authenticatedAt = time.Now()
		}
		if !g.authenticatedAt.IsZero() {
			status.SessionAgeSeconds = int64(time.Since(g.authenticatedAt).Seconds())
		}
		if status.Account != "" {
			g.account = status.Account
		}
		g.mu.Unlock()
	}
	return status
}

// Reconnect manually triggers a full gateway restart cycle.
func (g *GatewayManager) Reconnect() error {
	g.restart()
	return nil
}

func (g *GatewayManager) EnsureInstalled() error {
	if gatewayInstalled(g.config.GatewayDir) {
		return g.ensureGatewayConf()
	}

	if g.config.BundledGatewayDir != "" && gatewayInstalled(g.config.BundledGatewayDir) {
		log.Printf("[IBKR] installing gateway from bundled dir %s", g.config.BundledGatewayDir)
		if err := os.MkdirAll(g.config.GatewayDir, 0o755); err != nil {
			return fmt.Errorf("mkdir gateway dir: %w", err)
		}
		if err := copyDir(g.config.BundledGatewayDir, g.config.GatewayDir); err != nil {
			return fmt.Errorf("copy bundled gateway: %w", err)
		}
		if err := g.ensureGatewayConf(); err != nil {
			return fmt.Errorf("configure gateway port: %w", err)
		}
		log.Printf("[IBKR] gateway installed at %s", g.config.GatewayDir)
		return nil
	}

	log.Printf("[IBKR] downloading gateway to %s", g.config.GatewayDir)
	if err := os.MkdirAll(g.config.GatewayDir, 0o755); err != nil {
		return fmt.Errorf("mkdir gateway dir: %w", err)
	}

	zipPath := filepath.Join(g.config.GatewayDir, "clientportal.gw.zip")
	if err := downloadFile(gatewayDownloadURL, zipPath, g.config.DownloadProxy); err != nil {
		return fmt.Errorf("download gateway: %w", err)
	}
	defer os.Remove(zipPath)

	if err := unzip(zipPath, g.config.GatewayDir); err != nil {
		return fmt.Errorf("unzip gateway: %w", err)
	}
	if err := g.ensureGatewayConf(); err != nil {
		return fmt.Errorf("configure gateway port: %w", err)
	}
	log.Printf("[IBKR] gateway installed at %s", g.config.GatewayDir)
	return nil
}

func (g *GatewayManager) ensureGatewayConf() error {
	confFile := filepath.Join(g.config.GatewayDir, "root", "conf.yaml")
	return patchGatewayConf(confFile, g.config)
}

func (g *GatewayManager) EnsureRunning(ctx context.Context) error {
	if g.isOnline() {
		return nil
	}

	// Reuse an already-running gateway process that survived a traio restart.
	if pid, err := readPIDFile(pidFile(g.config.GatewayDir)); err == nil {
		if processAlive(pid) {
			log.Printf("[IBKR] reusing existing gateway process (pid=%d)", pid)
			if g.waitUntilOnline(ctx) {
				return nil
			}
			// Process alive but not responding — fall through to restart.
		}
	}

	// No usable process; kill anything squatting on the port and start fresh.
	killProcessOnPort(g.config.GatewayPort)
	time.Sleep(time.Second)

	if !gatewayInstalled(g.config.GatewayDir) {
		return fmt.Errorf("gateway not installed at %s", g.config.GatewayDir)
	}
	if err := g.ensureGatewayConf(); err != nil {
		return fmt.Errorf("configure gateway: %w", err)
	}

	runJar := filepath.Join(g.config.GatewayDir, "root", "run.jar")
	runSh := filepath.Join(g.config.GatewayDir, "bin", "run.sh")

	var cmd *exec.Cmd
	switch {
	case fileExists(runJar):
		cmd = exec.Command("java", "-jar", runJar, "root/conf.yaml")
	case fileExists(runSh):
		cmd = exec.Command("bash", "bin/run.sh", "root/conf.yaml")
	default:
		return fmt.Errorf("gateway startup script missing in %s", g.config.GatewayDir)
	}
	cmd.Dir = g.config.GatewayDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start gateway: %w", err)
	}

	// Detach: gateway lives beyond traio's context.
	pid := cmd.Process.Pid
	if err := writePIDFile(pidFile(g.config.GatewayDir), pid); err != nil {
		log.Printf("[IBKR] warning: could not write pid file: %v", err)
	}
	go func() { _ = cmd.Wait() }()

	g.mu.Lock()
	g.cmd = cmd
	g.mu.Unlock()

	log.Printf("[IBKR] gateway process started (pid=%d)", pid)
	if !g.waitUntilOnline(ctx) {
		return fmt.Errorf("gateway did not become ready within %s", startupTimeout)
	}
	return nil
}

func (g *GatewayManager) EnsureAuthenticated(ctx context.Context) error {
	// Always check tickle first — the gateway session may still be alive from a
	// previous traio run, in which case we must not open the browser unnecessarily.
	if tickle, online := g.fetchTickle(); online && tickleAuthenticated(tickle) {
		g.markAuthenticated(tickleAccount(tickle))
		log.Printf("[IBKR] session already authenticated (account=%s)", tickleAccount(tickle))
		return nil
	}

	if !g.hasCredentials() {
		loginURL := g.config.GatewayURL + "/sso/Login"
		log.Printf("[IBKR] credentials not configured — opening browser for manual login at %s", loginURL)
		openBrowser(loginURL)
		return nil
	}

	tickle, online := g.fetchTickle()
	if !online {
		return fmt.Errorf("gateway offline")
	}
	if tickleAuthenticated(tickle) {
		g.markAuthenticated(tickleAccount(tickle))
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	log.Println("[IBKR] session not authenticated, starting auto login")
	if err := g.autoLogin(ctx); err != nil {
		return fmt.Errorf("auto login: %w", err)
	}

	deadline := time.Now().Add(startupTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		tickle, online = g.fetchTickle()
		if online && tickleAuthenticated(tickle) {
			g.markAuthenticated(tickleAccount(tickle))
			log.Println("[IBKR] authentication successful")
			return nil
		}
	}
	return fmt.Errorf("authentication not confirmed within %s", startupTimeout)
}

func (g *GatewayManager) isOnline() bool {
	_, online := g.fetchTickle()
	return online
}

func (g *GatewayManager) isHealthy() bool {
	if !g.isOnline() {
		return false
	}
	// Manual login mode: gateway online is enough; user authenticates in browser.
	if !g.hasCredentials() {
		return true
	}
	tickle, _ := g.fetchTickle()
	return tickleAuthenticated(tickle)
}

func (g *GatewayManager) fetchTickle() (map[string]interface{}, bool) {
	resp, err := g.httpClient.Post(
		g.config.GatewayURL+"/v1/api/tickle",
		"application/json",
		strings.NewReader("{}"),
	)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		result = nil
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusUnauthorized:
		return result, true
	default:
		return nil, false
	}
}

func (g *GatewayManager) fetchAuthStatus() (authenticated bool, account string) {
	resp, err := g.httpClient.Get(g.config.GatewayURL + "/v1/api/iserver/auth/status")
	if err != nil {
		return false, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, ""
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, ""
	}
	authenticated, _ = result["authenticated"].(bool)
	if acct, ok := result["selectedAccount"].(string); ok && acct != "" {
		account = acct
	}
	return authenticated, account
}

func (g *GatewayManager) fetchSSOValidate() (ok bool, message string) {
	resp, err := g.httpClient.Get(g.config.GatewayURL + "/v1/api/sso/validate?gw=1")
	if err != nil {
		return false, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return true, ""
	}
	body, _ := io.ReadAll(resp.Body)
	text := strings.TrimSpace(string(body))
	if text != "" {
		return false, text
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return false, "Access Denied — IBKR 账户可能未开通 Client Portal API，或登录会话无效"
	}
	return false, fmt.Sprintf("validate failed (%d)", resp.StatusCode)
}

func (g *GatewayManager) waitUntilOnline(ctx context.Context) bool {
	deadline := time.Now().Add(startupTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
		if g.isOnline() {
			return true
		}
	}
	return false
}

func (g *GatewayManager) hasCredentials() bool {
	return g.config.SubAccount != "" &&
		g.config.Password != "" &&
		g.config.TOTPSecret != ""
}

func (g *GatewayManager) loginMode() string {
	if g.hasCredentials() {
		return "auto"
	}
	return "manual"
}

func (g *GatewayManager) markAuthenticated(account string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.authenticatedAt.IsZero() {
		g.authenticatedAt = time.Now()
	}
	if account != "" {
		g.account = account
	}
}

func (g *GatewayManager) resetSession() {
	g.mu.Lock()
	g.authenticatedAt = time.Time{}
	g.account = ""
	g.mu.Unlock()
}

func (g *GatewayManager) stopProcess() {
	g.mu.Lock()
	cmd := g.cmd
	g.cmd = nil
	g.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	log.Println("[IBKR] gateway process stopped")
}

func killProcessOnPort(port int) {
	if port <= 0 {
		return
	}
	out, err := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", port)).Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		_ = proc.Kill()
		log.Printf("[IBKR] killed process %d on port %d", pid, port)
	}
}

func (g *GatewayManager) restart() {
	if !g.restarting.CompareAndSwap(false, true) {
		return
	}
	defer g.restarting.Store(false)

	log.Println("[IBKR] gateway restarting...")
	g.resetSession()
	g.stopProcess()

	time.Sleep(3 * time.Second)

	g.mu.Lock()
	parent := context.Background()
	if g.ctx != nil {
		parent = context.WithoutCancel(g.ctx)
	}
	g.monitorsStarted = false
	if g.cancel != nil {
		g.cancel()
	}
	g.mu.Unlock()

	if err := g.Start(parent); err != nil {
		log.Printf("[IBKR] restart failed: %v", err)
	}
}

func tickleAuthenticated(result map[string]interface{}) bool {
	if v, ok := result["authenticated"].(bool); ok {
		return v
	}
	iserver, ok := result["iserver"].(map[string]interface{})
	if !ok {
		return false
	}
	authStatus, ok := iserver["authStatus"].(map[string]interface{})
	if !ok {
		return false
	}
	authenticated, _ := authStatus["authenticated"].(bool)
	return authenticated
}

func tickleAccount(result map[string]interface{}) string {
	if acct, ok := result["account"].(string); ok && acct != "" {
		return acct
	}
	if acct, ok := result["selectedAccount"].(string); ok && acct != "" {
		return acct
	}
	if uid, ok := result["userId"].(float64); ok && uid > 0 {
		return fmt.Sprintf("U%d", int(uid))
	}
	return ""
}

// patchGatewayConf rewrites the fields in conf.yaml that traio controls:
//   - listenPort      ← cfg.GatewayPort
//   - proxyRemoteHost ← cfg.GatewayProxyHost
//   - ips.allow list  ← cfg.GatewayAllowIPs
//
// All other fields are left untouched.
func patchGatewayConf(confFile string, cfg config.IBKRConfig) error {
	data, err := os.ReadFile(confFile)
	if err != nil {
		return err
	}
	content := string(data)

	// --- listenPort ---
	rePort := regexp.MustCompile(`(?m)^(\s*)listenPort:\s*\d+\s*$`)
	if !rePort.MatchString(content) {
		return fmt.Errorf("listenPort not found in %s", confFile)
	}
	content = rePort.ReplaceAllStringFunc(content, func(line string) string {
		indent := rePort.FindStringSubmatch(line)[1]
		return indent + fmt.Sprintf("listenPort: %d", cfg.GatewayPort)
	})

	// --- proxyRemoteHost ---
	reProxy := regexp.MustCompile(`(?m)^(\s*)proxyRemoteHost:\s*\S+\s*$`)
	if reProxy.MatchString(content) {
		content = reProxy.ReplaceAllStringFunc(content, func(line string) string {
			indent := reProxy.FindStringSubmatch(line)[1]
			return indent + fmt.Sprintf("proxyRemoteHost: %q", cfg.GatewayProxyHost)
		})
	}

	// --- ips.allow ---
	// Replace the entire allow block:
	//     allow:
	//       - <ip>
	//       - <ip>
	reAllow := regexp.MustCompile(`(?m)^(\s*)allow:\s*\n(?:(\s+)-[^\n]*\n)*`)
	if reAllow.MatchString(content) && len(cfg.GatewayAllowIPs) > 0 {
		content = reAllow.ReplaceAllStringFunc(content, func(block string) string {
			// Detect indent of the "allow:" line itself.
			blockIndent := reAllow.FindStringSubmatch(block)[1]
			itemIndent := blockIndent + "  "
			var sb strings.Builder
			sb.WriteString(blockIndent + "allow:\n")
			for _, ip := range cfg.GatewayAllowIPs {
				sb.WriteString(itemIndent + "- " + ip + "\n")
			}
			return sb.String()
		})
	}

	return os.WriteFile(confFile, []byte(content), 0o644)
}

// patchListenPort is kept for backward compatibility with existing tests.
func patchListenPort(confFile string, port int) error {
	data, err := os.ReadFile(confFile)
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`(?m)^(\s*)listenPort:\s*\d+\s*$`)
	if !re.MatchString(string(data)) {
		return fmt.Errorf("listenPort not found in %s", confFile)
	}
	updated := re.ReplaceAllStringFunc(string(data), func(line string) string {
		indent := re.FindStringSubmatch(line)[1]
		return indent + fmt.Sprintf("listenPort: %d", port)
	})
	return os.WriteFile(confFile, []byte(updated), 0o644)
}

// writePIDFile atomically writes pid to path.
func writePIDFile(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o644)
}

// readPIDFile reads a PID from path.
func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// processAlive reports whether the process with the given PID is still running.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// os.FindProcess always succeeds on Unix; send signal 0 to test liveness.
	return proc.Signal(syscall.Signal(0)) == nil
}

func downloadFile(urlStr, dest, proxyURL string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return fmt.Errorf("parse download proxy: %w", err)
		}
		client.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
	} else if p := os.Getenv("HTTPS_PROXY"); p != "" {
		if u, err := url.Parse(p); err == nil {
			client.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
		}
	} else if p := os.Getenv("HTTP_PROXY"); p != "" {
		if u, err := url.Parse(p); err == nil {
			client.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
		}
	}

	resp, err := client.Get(urlStr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download status %d", resp.StatusCode)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func gatewayInstalled(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "root", "conf.yaml")); err != nil {
		return false
	}
	return fileExists(filepath.Join(dir, "root", "run.jar")) ||
		fileExists(filepath.Join(dir, "bin", "run.sh"))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func copyDir(src, dst string) error {
	src = filepath.Clean(src)
	dst = filepath.Clean(dst)
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	dest = filepath.Clean(dest)
	for _, f := range r.File {
		target := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(filepath.Clean(target), dest+string(os.PathSeparator)) && filepath.Clean(target) != dest {
			return fmt.Errorf("invalid zip entry: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, f.Mode()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("[IBKR] failed to open browser: %v", err)
	}
}
