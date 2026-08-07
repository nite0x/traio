package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const apiTokenFilename = "api-token"

// APITokenPath returns the per-runtime token file shared by the sidecar,
// desktop shell, and local CLI clients.
func APITokenPath(runtimeDir string) string {
	return filepath.Join(runtimeDir, apiTokenFilename)
}

// LoadOrCreateAPIToken returns a cryptographically random local API token.
// TRAIO_API_TOKEN can supply a token for managed deployments; it is mirrored
// into the protected runtime file so the desktop shell and MCP client can use
// the same server instance.
func LoadOrCreateAPIToken(runtimeDir string) (string, error) {
	path := APITokenPath(runtimeDir)
	if token := strings.TrimSpace(os.Getenv("TRAIO_API_TOKEN")); token != "" {
		if err := validateAPIToken(token); err != nil {
			return "", err
		}
		if err := writeAPIToken(path, token); err != nil {
			return "", err
		}
		return token, nil
	}
	if token, err := ReadAPIToken(runtimeDir); err == nil {
		return token, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate API token: %w", err)
	}
	token := hex.EncodeToString(raw)
	if err := writeAPIToken(path, token); err != nil {
		return "", err
	}
	return token, nil
}

// ReadAPIToken loads the current runtime token and enforces private file mode.
func ReadAPIToken(runtimeDir string) (string, error) {
	path := APITokenPath(runtimeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if err := validateAPIToken(token); err != nil {
		return "", fmt.Errorf("read API token: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("secure API token: %w", err)
	}
	return token, nil
}

func validateAPIToken(token string) error {
	if len(token) < 32 {
		return fmt.Errorf("API token must contain at least 32 characters")
	}
	return nil
}

func writeAPIToken(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create API token directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".api-token-*")
	if err != nil {
		return fmt.Errorf("create API token: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure API token temp file: %w", err)
	}
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write API token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close API token: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install API token: %w", err)
	}
	return os.Chmod(path, 0o600)
}
