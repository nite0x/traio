package ibkr

import (
	"archive/zip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
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

	"github.com/gofrs/flock"
	"github.com/nite/traio/internal/config"
)

// pidFile returns the path used to track the gateway's OS process ID.
func pidFile(gatewayDir string) string {
	return filepath.Join(gatewayDir, "gateway.pid")
}

func processRecordFile(gatewayDir string) string {
	return filepath.Join(gatewayDir, "gateway-process.json")
}

type gatewayProcessRecord struct {
	Version     int    `json:"version"`
	PID         int    `json:"pid"`
	WrapperPID  int    `json:"wrapper_pid,omitempty"`
	StartedAt   string `json:"started_at"`
	GatewayDir  string `json:"gateway_dir"`
	GatewayPort int    `json:"gateway_port"`
}

const (
	gatewayDownloadURL  = "https://download2.interactivebrokers.com/portal/clientportal.gw.zip"
	startupTimeout      = 30 * time.Second
	gatewayLogRetention = 14 * 24 * time.Hour
)

const (
	gatewayStateStopped      = "stopped"
	gatewayStateDetached     = "detached"
	gatewayStateInstalling   = "installing"
	gatewayStateStarting     = "starting"
	gatewayStateAuthRequired = "authentication_required"
	gatewayStateRunning      = "running"
	gatewayStateStopping     = "stopping"
	gatewayStateRestarting   = "restarting"
	gatewayStateUpgrading    = "upgrading"
	gatewayStateRollingBack  = "rolling_back"
	gatewayStateError        = "error"
)

var errManualAuthRequired = errors.New("manual authentication required")

// GatewayStatus is the public gateway state exposed via REST API.
type GatewayStatus struct {
	Running           bool   `json:"running"`
	Authenticated     bool   `json:"authenticated"`
	Account           string `json:"account"`
	Lifecycle         string `json:"lifecycle"`
	SessionAgeSeconds int64  `json:"session_age_seconds"`
	LoginMode         string `json:"login_mode"` // manual
	LoginURL          string `json:"login_url,omitempty"`
	AuthMessage       string `json:"auth_message,omitempty"`
	State             string `json:"state"`
	LastError         string `json:"last_error,omitempty"`
	StateUpdatedAt    string `json:"state_updated_at,omitempty"`
	InstalledVersion  string `json:"installed_version,omitempty"`
	PinnedVersion     string `json:"pinned_version,omitempty"`
	InstallVerified   bool   `json:"install_verified"`
	RollbackAvailable bool   `json:"rollback_available"`
}

// GatewayManager manages IBKR Client Portal Gateway lifecycle.
type GatewayManager struct {
	config     config.IBKRConfig
	cmd        *exec.Cmd
	httpClient *http.Client

	mu              sync.Mutex
	opMu            sync.Mutex
	auditMu         sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc
	authenticatedAt time.Time
	account         string
	monitorsStarted bool
	restarting      atomic.Bool
	state           string
	lastError       string
	stateUpdatedAt  time.Time
	processLock     *flock.Flock
	release         gatewayRelease
}

func NewGatewayManager(cfg config.IBKRConfig) *GatewayManager {
	cfg.GatewayLifecycle = config.NormalizeIBKRGatewayLifecycle(cfg.GatewayLifecycle)
	return &GatewayManager{
		config:         cfg,
		httpClient:     newGatewayHTTPClient(cfg.GatewayURL, 10*time.Second),
		state:          gatewayStateStopped,
		stateUpdatedAt: time.Now().UTC(),
		release:        officialGatewayRelease,
	}
}

// LoginURL returns the local Client Portal Gateway login page.
func (g *GatewayManager) LoginURL() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return strings.TrimRight(g.config.GatewayURL, "/") + "/sso/Login"
}

// BaseURL returns the private Client Portal Gateway origin used by this
// connection. Callers must still enforce that it resolves to loopback before
// exposing it through a reverse proxy.
func (g *GatewayManager) BaseURL() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return strings.TrimRight(g.config.GatewayURL, "/")
}

func (g *GatewayManager) acquireProcessLock() error {
	g.mu.Lock()
	if g.processLock != nil {
		g.mu.Unlock()
		return nil
	}
	gatewayDir := g.config.GatewayDir
	g.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(gatewayDir), 0o700); err != nil {
		return err
	}
	lockPath := gatewayDir + ".manager.lock"
	lock := flock.New(lockPath, flock.SetPermissions(0o600))
	locked, err := lock.TryLock()
	if err != nil {
		return fmt.Errorf("lock gateway manager: %w", err)
	}
	if !locked {
		return fmt.Errorf("another process is already managing gateway directory %s", gatewayDir)
	}
	if err := os.Chmod(lockPath, 0o600); err != nil {
		_ = lock.Unlock()
		return fmt.Errorf("secure gateway manager lock: %w", err)
	}
	g.mu.Lock()
	g.processLock = lock
	g.mu.Unlock()
	return nil
}

func (g *GatewayManager) releaseProcessLock() {
	g.mu.Lock()
	lock := g.processLock
	g.processLock = nil
	g.mu.Unlock()
	if lock != nil {
		_ = lock.Unlock()
	}
}

func (g *GatewayManager) setLifecycle(state string, err error) {
	g.mu.Lock()
	g.state = state
	if err == nil {
		g.lastError = ""
	} else {
		g.lastError = sanitizeAuditValue(err.Error())
	}
	g.stateUpdatedAt = time.Now().UTC()
	g.mu.Unlock()
}

func (g *GatewayManager) failLifecycle(event string, err error) error {
	g.setLifecycle(gatewayStateError, err)
	g.audit(event, "error", err.Error())
	return err
}

// UpdateConfig replaces IBKR settings and should be followed by Reconnect().
func (g *GatewayManager) UpdateConfig(cfg config.IBKRConfig) {
	g.mu.Lock()
	g.config = cfg
	g.httpClient = newGatewayHTTPClient(cfg.GatewayURL, 10*time.Second)
	g.mu.Unlock()
}

// Reconfigure safely stops the process using the old paths, releases the old
// manager lock, applies new settings, and starts under the new ownership scope.
func (g *GatewayManager) Reconfigure(cfg config.IBKRConfig) error {
	g.opMu.Lock()
	defer g.opMu.Unlock()
	g.cancelMonitoring()
	if err := g.stopProcess(); err != nil {
		return g.failLifecycle("gateway.reconfigure", err)
	}
	g.releaseProcessLock()
	g.mu.Lock()
	g.config = cfg
	g.httpClient = newGatewayHTTPClient(cfg.GatewayURL, 10*time.Second)
	g.mu.Unlock()
	g.resetSession()
	if err := g.startLocked(context.Background()); err != nil {
		return g.failLifecycle("gateway.reconfigure", err)
	}
	g.audit("gateway.reconfigure", "success", "configuration applied")
	return nil
}

// Start ensures gateway is installed, running, authenticated, and monitored.
func (g *GatewayManager) Start(ctx context.Context) error {
	g.opMu.Lock()
	defer g.opMu.Unlock()
	return g.startLocked(ctx)
}

func (g *GatewayManager) startLocked(ctx context.Context) error {
	if err := g.acquireProcessLock(); err != nil {
		return g.failLifecycle("gateway.lock", err)
	}
	g.mu.Lock()
	if g.cancel != nil {
		g.cancel()
	}
	g.ctx, g.cancel = context.WithCancel(ctx)
	runCtx := g.ctx
	g.mu.Unlock()

	g.setLifecycle(gatewayStateInstalling, nil)
	if err := g.EnsureInstalled(ctx); err != nil {
		return g.failLifecycle("gateway.install", fmt.Errorf("ensure installed: %w", err))
	}
	g.setLifecycle(gatewayStateStarting, nil)
	if err := g.EnsureRunning(runCtx); err != nil {
		return g.failLifecycle("gateway.start", fmt.Errorf("ensure running: %w", err))
	}
	if err := g.EnsureAuthenticated(runCtx); err != nil {
		if errors.Is(err, errManualAuthRequired) {
			g.setLifecycle(gatewayStateAuthRequired, nil)
			g.audit("gateway.authentication", "required", "manual browser login required")
			log.Printf("[IBKR] authentication pending: %v", err)
		} else {
			return g.failLifecycle("gateway.authentication", err)
		}
	} else {
		g.setLifecycle(gatewayStateRunning, nil)
		g.audit("gateway.start", "success", "gateway online")
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
	if err := g.StopGateway(false); err != nil {
		log.Printf("[IBKR] stop failed: %v", err)
	}
}

// Shutdown applies the configured process lifecycle. Server and Docker
// instances stop their Gateway; packaged desktop instances detach so the
// authenticated session can survive a sidecar restart.
func (g *GatewayManager) Shutdown() error {
	g.mu.Lock()
	keepSession := g.config.GatewayLifecycle == config.IBKRGatewayLifecyclePersistent
	g.mu.Unlock()
	return g.StopGateway(keepSession)
}

// StartGateway ensures the IBKR gateway process is running and monitored.
func (g *GatewayManager) StartGateway(ctx context.Context) error {
	return g.Start(ctx)
}

// StopGateway stops monitoring and optionally kills the gateway process.
// When keepSession is true, the Java process keeps running (session preserved);
// traio simply detaches. When false, the process is killed.
func (g *GatewayManager) StopGateway(keepSession bool) error {
	g.opMu.Lock()
	defer g.opMu.Unlock()
	g.setLifecycle(gatewayStateStopping, nil)
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
		g.releaseProcessLock()
		g.setLifecycle(gatewayStateDetached, nil)
		g.audit("gateway.stop", "detached", "session preserved")
		return nil
	}

	if err := g.stopProcess(); err != nil {
		return g.failLifecycle("gateway.stop", err)
	}
	g.releaseProcessLock()
	g.resetSession()
	g.setLifecycle(gatewayStateStopped, nil)
	g.audit("gateway.stop", "success", "gateway process stopped")
	return nil
}

func (g *GatewayManager) Status() GatewayStatus {
	g.mu.Lock()
	account := g.account
	state := g.state
	lastError := g.lastError
	stateUpdatedAt := g.stateUpdatedAt
	gatewayURL := g.config.GatewayURL
	gatewayDir := g.config.GatewayDir
	lifecycle := g.config.GatewayLifecycle
	pinnedVersion := g.release.Version
	g.mu.Unlock()

	tickle, online := g.fetchTickle()
	status := GatewayStatus{
		Running:        online,
		Account:        account,
		Lifecycle:      lifecycle,
		LoginMode:      g.loginMode(),
		State:          state,
		LastError:      lastError,
		StateUpdatedAt: stateUpdatedAt.Format(time.RFC3339),
		PinnedVersion:  pinnedVersion,
	}
	status.LoginURL = gatewayURL + "/sso/Login"
	if manifest, err := readInstallManifest(gatewayDir); err == nil {
		status.InstalledVersion = manifest.Version
		status.InstallVerified = manifest.Verified
	}
	status.RollbackAvailable = gatewayInstalled(rollbackGatewayDir(gatewayDir))
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
		if g.state == gatewayStateAuthRequired || g.state == gatewayStateStarting {
			g.state = gatewayStateRunning
			g.lastError = ""
			g.stateUpdatedAt = time.Now().UTC()
			status.State = g.state
			status.LastError = ""
			status.StateUpdatedAt = g.stateUpdatedAt.Format(time.RFC3339)
		}
		g.mu.Unlock()
	}
	return status
}

// Restart triggers a full gateway restart cycle without opening the login page.
func (g *GatewayManager) Restart() {
	g.restart()
}

// Reconnect manually triggers a full gateway restart cycle and opens the login
// page when manual authentication is required.
func (g *GatewayManager) Reconnect() error {
	if err := g.restart(); err != nil {
		return err
	}
	if g.isAuthenticated() {
		return nil
	}
	loginURL := g.config.GatewayURL + "/sso/Login"
	log.Printf("[IBKR] opening browser for manual login at %s", loginURL)
	openBrowser(loginURL)
	return nil
}

// Upgrade installs the pinned, SHA-256 verified Gateway release. The current
// installation is retained as a rollback candidate until the next upgrade.
func (g *GatewayManager) Upgrade(ctx context.Context) error {
	g.opMu.Lock()
	defer g.opMu.Unlock()
	if err := g.acquireProcessLock(); err != nil {
		return g.failLifecycle("gateway.upgrade", err)
	}

	g.setLifecycle(gatewayStateUpgrading, nil)
	g.audit("gateway.upgrade", "started", "target="+g.release.Version)
	wasRunning := g.isOnline()
	g.cancelMonitoring()
	if err := g.stopProcess(); err != nil {
		return g.failLifecycle("gateway.upgrade", err)
	}
	if err := g.installVerifiedRelease(ctx); err != nil {
		if wasRunning {
			_ = g.startLocked(context.WithoutCancel(ctx))
		}
		return g.failLifecycle("gateway.upgrade", err)
	}
	if err := g.startLocked(context.WithoutCancel(ctx)); err != nil {
		startErr := err
		_ = g.stopProcess()
		if rollbackErr := g.swapWithRollback(); rollbackErr != nil {
			return g.failLifecycle("gateway.upgrade", fmt.Errorf("new gateway failed: %v; rollback failed: %w", startErr, rollbackErr))
		}
		if restoreErr := g.startLocked(context.Background()); restoreErr != nil {
			return g.failLifecycle("gateway.upgrade", fmt.Errorf("new gateway failed: %v; previous gateway restored but failed to start: %w", startErr, restoreErr))
		}
		warning := fmt.Errorf("upgrade failed and previous gateway was restored: %w", startErr)
		g.setLifecycle(gatewayStateRunning, warning)
		g.audit("gateway.upgrade", "rolled_back", warning.Error())
		return warning
	}
	g.audit("gateway.upgrade", "success", "version="+g.release.Version)
	return nil
}

// Rollback swaps the active installation with the retained previous version.
// The replaced version remains available, so another rollback acts as a
// controlled roll-forward.
func (g *GatewayManager) Rollback(ctx context.Context) error {
	g.opMu.Lock()
	defer g.opMu.Unlock()
	if err := g.acquireProcessLock(); err != nil {
		return g.failLifecycle("gateway.rollback", err)
	}
	g.setLifecycle(gatewayStateRollingBack, nil)
	g.audit("gateway.rollback", "started", "manual rollback requested")
	g.cancelMonitoring()
	if err := g.stopProcess(); err != nil {
		return g.failLifecycle("gateway.rollback", err)
	}
	if err := g.swapWithRollback(); err != nil {
		return g.failLifecycle("gateway.rollback", err)
	}
	if err := g.startLocked(context.WithoutCancel(ctx)); err != nil {
		startErr := err
		_ = g.stopProcess()
		if swapErr := g.swapWithRollback(); swapErr != nil {
			return g.failLifecycle("gateway.rollback", fmt.Errorf("rollback version failed to start: %v; restore failed: %w", startErr, swapErr))
		}
		_ = g.startLocked(context.Background())
		return g.failLifecycle("gateway.rollback", fmt.Errorf("rollback version failed to start: %w", startErr))
	}
	g.audit("gateway.rollback", "success", "previous gateway activated")
	return nil
}

func (g *GatewayManager) cancelMonitoring() {
	g.mu.Lock()
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
	g.ctx = nil
	g.monitorsStarted = false
	g.mu.Unlock()
}

func (g *GatewayManager) EnsureInstalled(ctx context.Context) error {
	if gatewayInstalled(g.config.GatewayDir) {
		if err := g.adoptLegacyInstall(); err != nil {
			return err
		}
		if err := g.ensureGatewayConf(); err != nil {
			return err
		}
		return g.secureRuntimePermissions()
	}

	if g.config.BundledGatewayDir != "" && gatewayInstalled(g.config.BundledGatewayDir) {
		log.Printf("[IBKR] installing gateway from bundled dir %s", g.config.BundledGatewayDir)
		if err := g.installBundledAtomic(g.config.BundledGatewayDir); err != nil {
			return err
		}
		g.audit("gateway.install", "success", "installed from application bundle")
		log.Printf("[IBKR] gateway installed at %s", g.config.GatewayDir)
		return nil
	}

	log.Printf("[IBKR] downloading verified gateway release %s", g.release.Version)
	if err := g.installVerifiedRelease(ctx); err != nil {
		return err
	}
	g.audit("gateway.install", "success", "installed verified release "+g.release.Version)
	log.Printf("[IBKR] gateway installed at %s", g.config.GatewayDir)
	return nil
}

func (g *GatewayManager) ensureGatewayConf() error {
	confFile := filepath.Join(g.config.GatewayDir, "root", "conf.yaml")
	if err := patchGatewayConf(confFile, g.config); err != nil {
		return err
	}
	return os.Chmod(confFile, 0o600)
}

// secureRuntimePermissions limits Gateway state to the current OS user. The
// IBKR distribution itself remains executable, while configuration, PID,
// certificates, caches and logs are treated as sensitive runtime data.
func (g *GatewayManager) secureRuntimePermissions() error {
	return secureGatewayRuntime(g.config.GatewayDir)
}

func secureGatewayRuntime(gatewayDir string) error {
	if err := os.Chmod(gatewayDir, 0o700); err != nil {
		return err
	}

	sensitiveFiles := []string{
		filepath.Join(gatewayDir, "root", "conf.yaml"),
		filepath.Join(gatewayDir, "root", "vertx.jks"),
		pidFile(gatewayDir),
		processRecordFile(gatewayDir),
		installManifestPath(gatewayDir),
	}
	for _, path := range sensitiveFiles {
		if err := os.Chmod(path, 0o600); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	for _, dir := range []string{
		filepath.Join(gatewayDir, "logs"),
		filepath.Join(gatewayDir, ".vertx"),
	} {
		if err := secureDataTree(dir); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return pruneGatewayLogs(filepath.Join(gatewayDir, "logs"), time.Now().Add(-gatewayLogRetention))
}

func secureDataTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return os.Chmod(path, 0o600)
	})
}

func pruneGatewayLogs(logDir string, before time.Time) error {
	entries, err := os.ReadDir(logDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(before) {
			if err := os.Remove(filepath.Join(logDir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *GatewayManager) EnsureRunning(ctx context.Context) error {
	// Reuse a validated Gateway that survived a persistent desktop shutdown.
	// Recovery also migrates the old numeric PID file by resolving the process
	// that actually owns this connection's configured listening port.
	if record, err := g.loadOrRecoverOwnedProcess(); err == nil {
		log.Printf("[IBKR] reusing existing gateway process (pid=%d)", record.PID)
		if g.isOnline() || g.waitUntilOnline(ctx) {
			return nil
		}
		if err := terminateProcess(record.PID); err != nil {
			return fmt.Errorf("stop unresponsive gateway process %d: %w", record.PID, err)
		}
		g.removeProcessRecord()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	// Never terminate an arbitrary process merely because it occupies the
	// configured port. The operator must resolve that conflict explicitly.
	if portInUse(g.config.GatewayPort) {
		return fmt.Errorf("gateway port %d is occupied by an unowned process", g.config.GatewayPort)
	}

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
	isolateGatewayProcess(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start gateway: %w", err)
	}

	// Store the wrapper temporarily. Once the listener is ready this is replaced
	// by the actual Java PID, while WrapperPID remains available for cleanup.
	wrapperPID := cmd.Process.Pid
	if err := writePIDFile(pidFile(g.config.GatewayDir), wrapperPID); err != nil {
		_ = terminateGatewayProcessGroup(wrapperPID)
		_ = cmd.Wait()
		return fmt.Errorf("write gateway pid file: %w", err)
	}

	g.mu.Lock()
	g.cmd = cmd
	g.mu.Unlock()

	log.Printf("[IBKR] gateway wrapper started (pid=%d)", wrapperPID)
	if !g.waitUntilOnline(ctx) {
		_ = terminateGatewayProcessGroup(wrapperPID)
		_ = cmd.Wait()
		g.removeProcessRecord()
		return fmt.Errorf("gateway did not become ready within %s", startupTimeout)
	}

	pid, err := g.findGatewayListener()
	if err != nil {
		_ = terminateGatewayProcessGroup(wrapperPID)
		_ = cmd.Wait()
		g.removeProcessRecord()
		return fmt.Errorf("identify gateway Java process: %w", err)
	}
	record, err := g.newProcessRecord(pid, wrapperPID)
	if err != nil {
		_ = terminateGatewayProcessGroup(wrapperPID)
		_ = cmd.Wait()
		g.removeProcessRecord()
		return fmt.Errorf("record gateway Java process: %w", err)
	}
	if err := g.writeProcessRecord(record); err != nil {
		_ = terminateGatewayProcessGroup(wrapperPID)
		_ = cmd.Wait()
		g.removeProcessRecord()
		return fmt.Errorf("write gateway process record: %w", err)
	}

	go func() {
		_ = cmd.Wait()
		g.mu.Lock()
		if g.cmd == cmd {
			g.cmd = nil
		}
		g.mu.Unlock()
		if !processAlive(record.PID) {
			g.removeProcessRecordIfPID(record.PID)
		}
	}()

	log.Printf("[IBKR] gateway Java process started (pid=%d, wrapper_pid=%d)", pid, wrapperPID)
	return nil
}

func (g *GatewayManager) EnsureAuthenticated(ctx context.Context) error {
	_ = ctx
	// Always check tickle first — the gateway session may still be alive from a
	// previous traio run, in which case we must not open the browser unnecessarily.
	if tickle, online := g.fetchTickle(); online && tickleAuthenticated(tickle) {
		g.markAuthenticated(tickleAccount(tickle))
		log.Printf("[IBKR] session already authenticated (account=%s)", tickleAccount(tickle))
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

	return fmt.Errorf("%w at %s/sso/Login", errManualAuthRequired, g.config.GatewayURL)
}

func (g *GatewayManager) isOnline() bool {
	_, online := g.fetchTickle()
	return online
}

func (g *GatewayManager) isAuthenticated() bool {
	tickle, online := g.fetchTickle()
	return online && tickleAuthenticated(tickle)
}

func (g *GatewayManager) isHealthy() bool {
	if !g.isOnline() {
		return false
	}
	// Authentication is user-driven and should not cause a healthy local Java
	// process to restart continuously while it is waiting for browser login.
	return true
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

func (g *GatewayManager) loginMode() string {
	return "manual"
}

func newGatewayHTTPClient(gatewayURL string, timeout time.Duration) *http.Client {
	transport := &http.Transport{}
	if parsed, err := url.Parse(gatewayURL); err == nil && isLoopbackHost(parsed.Hostname()) {
		// IBKR ships a self-signed certificate for the local Gateway. Certificate
		// verification is relaxed only for a loopback destination.
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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

func (g *GatewayManager) stopProcess() error {
	g.mu.Lock()
	cmd := g.cmd
	g.cmd = nil
	g.mu.Unlock()

	record, err := g.loadOrRecoverOwnedProcess()
	if err == nil {
		if err := terminateProcess(record.PID); err != nil {
			return err
		}
		// The run.sh wrapper normally exits when Java exits. If it does not,
		// terminate only the isolated process group created by this manager.
		if record.WrapperPID > 1 && processAlive(record.WrapperPID) {
			_ = terminateGatewayProcessGroup(record.WrapperPID)
		}
		g.removeProcessRecord()
		log.Printf("[IBKR] gateway process stopped (pid=%d)", record.PID)
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if cmd != nil && cmd.Process != nil && processAlive(cmd.Process.Pid) {
		if err := terminateGatewayProcessGroup(cmd.Process.Pid); err != nil {
			return err
		}
	}
	g.removeProcessRecord()
	return nil
}

// gatewayProcessMatches validates both the command line and working directory
// before Traio adopts or terminates a PID loaded from disk.
func (g *GatewayManager) gatewayProcessMatches(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	command := strings.ToLower(string(out))
	if !strings.Contains(command, "clientportal.gw") &&
		!strings.Contains(command, "root/run.jar") &&
		!strings.Contains(command, "bin/run.sh") {
		return false
	}

	cwdOutput, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(cwdOutput), "\n") {
		if !strings.HasPrefix(line, "n") {
			continue
		}
		cwd, cwdErr := filepath.EvalSymlinks(strings.TrimPrefix(line, "n"))
		gatewayDir, dirErr := filepath.EvalSymlinks(g.config.GatewayDir)
		return cwdErr == nil && dirErr == nil && filepath.Clean(cwd) == filepath.Clean(gatewayDir)
	}
	return false
}

func (g *GatewayManager) findGatewayListener() (int, error) {
	out, err := exec.Command(
		"lsof", "-nP", "-t", "-iTCP:"+strconv.Itoa(g.config.GatewayPort), "-sTCP:LISTEN",
	).Output()
	if err != nil {
		return 0, os.ErrNotExist
	}
	for _, value := range strings.Fields(string(out)) {
		pid, parseErr := strconv.Atoi(value)
		if parseErr == nil && g.gatewayProcessMatches(pid) && processListensOnPort(pid, g.config.GatewayPort) {
			return pid, nil
		}
	}
	return 0, os.ErrNotExist
}

func (g *GatewayManager) newProcessRecord(pid, wrapperPID int) (gatewayProcessRecord, error) {
	startedAt, err := processStartSignature(pid)
	if err != nil {
		return gatewayProcessRecord{}, err
	}
	gatewayDir, err := filepath.EvalSymlinks(g.config.GatewayDir)
	if err != nil {
		return gatewayProcessRecord{}, err
	}
	return gatewayProcessRecord{
		Version:     1,
		PID:         pid,
		WrapperPID:  wrapperPID,
		StartedAt:   startedAt,
		GatewayDir:  filepath.Clean(gatewayDir),
		GatewayPort: g.config.GatewayPort,
	}, nil
}

func (g *GatewayManager) writeProcessRecord(record gatewayProcessRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	path := processRecordFile(g.config.GatewayDir)
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	return writePIDFile(pidFile(g.config.GatewayDir), record.PID)
}

func (g *GatewayManager) loadOrRecoverOwnedProcess() (gatewayProcessRecord, error) {
	record, err := readProcessRecord(processRecordFile(g.config.GatewayDir))
	if err == nil {
		if !processAlive(record.PID) {
			g.removeProcessRecord()
			return gatewayProcessRecord{}, os.ErrNotExist
		}
		if err := g.validateProcessRecord(record); err != nil {
			return gatewayProcessRecord{}, err
		}
		return record, nil
	}
	if !os.IsNotExist(err) {
		return gatewayProcessRecord{}, fmt.Errorf("read gateway process record: %w", err)
	}

	// Backward compatibility: old versions stored only a PID. It may identify
	// the run.sh wrapper, so prefer it only when it owns the configured port.
	if pid, pidErr := readPIDFile(pidFile(g.config.GatewayDir)); pidErr == nil &&
		processAlive(pid) && g.gatewayProcessMatches(pid) && processListensOnPort(pid, g.config.GatewayPort) {
		record, err = g.newProcessRecord(pid, 0)
	} else {
		pid, listenerErr := g.findGatewayListener()
		if listenerErr != nil {
			return gatewayProcessRecord{}, os.ErrNotExist
		}
		record, err = g.newProcessRecord(pid, 0)
	}
	if err != nil {
		return gatewayProcessRecord{}, err
	}
	if err := g.writeProcessRecord(record); err != nil {
		return gatewayProcessRecord{}, err
	}
	log.Printf("[IBKR] recovered gateway ownership (pid=%d, port=%d)", record.PID, record.GatewayPort)
	return record, nil
}

func (g *GatewayManager) validateProcessRecord(record gatewayProcessRecord) error {
	if record.Version != 1 || record.PID <= 1 {
		return fmt.Errorf("invalid gateway process record")
	}
	if record.GatewayPort != g.config.GatewayPort {
		return fmt.Errorf("gateway process record port %d does not match configured port %d", record.GatewayPort, g.config.GatewayPort)
	}
	gatewayDir, err := filepath.EvalSymlinks(g.config.GatewayDir)
	if err != nil || filepath.Clean(record.GatewayDir) != filepath.Clean(gatewayDir) {
		return fmt.Errorf("gateway process record directory does not match configured directory")
	}
	if !g.gatewayProcessMatches(record.PID) || !processListensOnPort(record.PID, record.GatewayPort) {
		return fmt.Errorf("gateway process %d does not match its recorded command, directory, and port", record.PID)
	}
	startedAt, err := processStartSignature(record.PID)
	if err != nil || startedAt != record.StartedAt {
		return fmt.Errorf("gateway process %d start identity changed", record.PID)
	}
	return nil
}

func (g *GatewayManager) removeProcessRecord() {
	_ = os.Remove(pidFile(g.config.GatewayDir))
	_ = os.Remove(processRecordFile(g.config.GatewayDir))
}

func (g *GatewayManager) removeProcessRecordIfPID(pid int) {
	record, err := readProcessRecord(processRecordFile(g.config.GatewayDir))
	if err == nil && record.PID == pid {
		g.removeProcessRecord()
	}
}

func readProcessRecord(path string) (gatewayProcessRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return gatewayProcessRecord{}, err
	}
	var record gatewayProcessRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return gatewayProcessRecord{}, err
	}
	return record, nil
}

func processStartSignature(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", fmt.Errorf("process %d has no start time", pid)
	}
	return value, nil
}

func processListensOnPort(pid, port int) bool {
	if pid <= 1 || port <= 0 {
		return false
	}
	out, err := exec.Command(
		"lsof", "-nP", "-a", "-p", strconv.Itoa(pid),
		"-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t",
	).Output()
	return err == nil && strings.TrimSpace(string(out)) == strconv.Itoa(pid)
}

func terminateProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil && processAlive(pid) {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !processAlive(pid) {
		return nil
	}
	return proc.Kill()
}

func portInUse(port int) bool {
	if port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (g *GatewayManager) restart() error {
	if !g.restarting.CompareAndSwap(false, true) {
		return fmt.Errorf("gateway restart already in progress")
	}
	defer g.restarting.Store(false)
	g.opMu.Lock()
	defer g.opMu.Unlock()

	log.Println("[IBKR] gateway restarting...")
	g.setLifecycle(gatewayStateRestarting, nil)
	g.audit("gateway.restart", "started", "health or reconnect requested")
	g.resetSession()
	g.mu.Lock()
	parent := context.Background()
	if g.ctx != nil {
		parent = context.WithoutCancel(g.ctx)
	}
	g.mu.Unlock()
	g.cancelMonitoring()
	if err := g.stopProcess(); err != nil {
		return g.failLifecycle("gateway.restart", err)
	}

	time.Sleep(3 * time.Second)
	if err := g.startLocked(parent); err != nil {
		return g.failLifecycle("gateway.restart", err)
	}
	g.audit("gateway.restart", "success", "gateway restarted")
	return nil
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

	return os.WriteFile(confFile, []byte(content), 0o600)
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
	return os.WriteFile(confFile, []byte(updated), 0o600)
}

// writePIDFile atomically writes pid to path.
func writePIDFile(path string, pid int) error {
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
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
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
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
