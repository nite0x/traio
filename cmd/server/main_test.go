package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nite/traio/internal/runtime"
)

func TestServerProcessHelper(t *testing.T) {
	if os.Getenv("TRAIO_TEST_SERVER_HELPER") != "1" {
		return
	}
	main()
	os.Exit(0)
}
func serverCommand(t *testing.T, ctx context.Context, dir string, values map[string]string) *exec.Cmd {
	t.Helper()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestServerProcessHelper$")
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "TRAIO_") && !strings.HasPrefix(v, "INFISICAL_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "TRAIO_TEST_SERVER_HELPER=1", "TRAIO_RUNTIME_DIR="+dir, "TRAIO_LISTEN_ADDR=127.0.0.1:0", "TRAIO_AUTH_MODE=local", "GIN_MODE=release")
	for k, v := range values {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	return cmd
}
func TestStartupRejectsMissingConfigurationBeforeOpeningDatabase(t *testing.T) {
	for _, tc := range []struct {
		name    string
		values  map[string]string
		message string
	}{
		{"DSN", map[string]string{"TRAIO_DATABASE_DRIVER": "postgres"}, "TRAIO_DATABASE_DSN"},
		{"provider", map[string]string{"TRAIO_CONFIG_PROVIDER": "infisical"}, "TRAIO_INFISICAL_SITE_URL"},
		{"invalid DSN", map[string]string{"TRAIO_DATABASE_DRIVER": "postgres", "TRAIO_DATABASE_DSN": "invalid-sensitive-dsn"}, "invalid TRAIO_DATABASE_DSN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			dir := t.TempDir()
			cmd := serverCommand(t, ctx, dir, tc.values)
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), tc.message) || strings.Contains(string(output), "invalid-sensitive-dsn") {
				t.Fatalf("startup result: error=%v output=%s", err, output)
			}
			if _, err := os.Stat(filepath.Join(dir, "data")); !os.IsNotExist(err) {
				t.Fatal("database initialized before configuration validation")
			}
			if _, err := runtime.ReadAPIURL(dir); err == nil {
				t.Fatal("listener opened on invalid configuration")
			}
		})
	}
}

func TestStartupExplainsInvalidAdministratorCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, username, password, message string
	}{
		{"missing", "", "", "built-in login is not initialized; set TRAIO_BOOTSTRAP_ADMIN_USERNAME and TRAIO_BOOTSTRAP_ADMIN_PASSWORD"},
		{"short password", "owner", "short-value", "TRAIO_BOOTSTRAP_ADMIN_PASSWORD must contain at least 12 bytes"},
		{"invalid username", "bad username", "long-enough-private-password", "TRAIO_BOOTSTRAP_ADMIN_USERNAME must be 3-64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			dir := t.TempDir()
			cmd := serverCommand(t, ctx, dir, map[string]string{
				"TRAIO_AUTH_MODE":                "password",
				"TRAIO_BOOTSTRAP_ADMIN_USERNAME": tc.username,
				"TRAIO_BOOTSTRAP_ADMIN_PASSWORD": tc.password,
			})
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "authentication: "+tc.message) {
				t.Fatalf("startup result: error=%v output=%s", err, output)
			}
			if tc.password != "" && strings.Contains(string(output), tc.password) {
				t.Fatal("password leaked into startup log")
			}
			if _, err := runtime.ReadAPIURL(dir); err == nil {
				t.Fatal("listener opened on invalid administrator credentials")
			}
		})
	}
}

func TestServerStartsAndSettingsExcludeStartupSecrets(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir := t.TempDir()
	cmd := serverCommand(t, ctx, dir, map[string]string{"TRAIO_OIDC_CLIENT_SECRET": "process-only-test-secret"})
	logPath := filepath.Join(dir, "test-server.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	client := &http.Client{Timeout: time.Second}
	var apiURL string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		apiURL, err = runtime.ReadAPIURL(dir)
		if err == nil {
			resp, e := client.Get(apiURL + "/health")
			if e == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode == 200 {
					break
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if apiURL == "" {
		raw, _ := os.ReadFile(logPath)
		t.Fatalf("server did not start: %s", raw)
	}
	token, err := runtime.ReadAPIToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("GET", apiURL+"/api/v1/settings", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("settings status: %d", resp.StatusCode)
	}
	if strings.Contains(string(raw), "process-only-test-secret") {
		t.Fatal("startup secret leaked through settings API")
	}
	logs, _ := os.ReadFile(logPath)
	if strings.Contains(string(logs), "process-only-test-secret") {
		t.Fatal("startup secret leaked through logs")
	}
}
