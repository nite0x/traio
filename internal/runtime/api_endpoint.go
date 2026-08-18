package runtime

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const apiEndpointFilename = "api-url"

// APIURLPath is the runtime-discovered local API address published by the
// server after its listener has been bound.
func APIURLPath(runtimeDir string) string {
	return filepath.Join(runtimeDir, apiEndpointFilename)
}

// WriteAPIURL atomically publishes the local API address selected at startup.
func WriteAPIURL(runtimeDir, apiURL string) error {
	apiURL, err := validateAPIURL(apiURL)
	if err != nil {
		return err
	}
	path := APIURLPath(runtimeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create API URL directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".api-url-*")
	if err != nil {
		return fmt.Errorf("create API URL temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure API URL temp file: %w", err)
	}
	if _, err := tmp.WriteString(apiURL + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write API URL: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close API URL temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install API URL: %w", err)
	}
	return os.Chmod(path, 0o600)
}

// ReadAPIURL loads the server address selected at runtime.
func ReadAPIURL(runtimeDir string) (string, error) {
	data, err := os.ReadFile(APIURLPath(runtimeDir))
	if err != nil {
		return "", err
	}
	return validateAPIURL(string(data))
}

// RemoveAPIURL removes a stale endpoint record after a clean shutdown.
func RemoveAPIURL(runtimeDir string) {
	_ = os.Remove(APIURLPath(runtimeDir))
}

func validateAPIURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return "", fmt.Errorf("invalid local API URL %q", value)
	}
	return strings.TrimRight(value, "/"), nil
}
