package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nite/traio/internal/config"
)

const endpointFile = "server.json"

// Endpoint describes the running Traio HTTP server (written on startup).
type Endpoint struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	APIURL string `json:"api_url"`
}

func EndpointPath(baseDir string) string {
	return filepath.Join(baseDir, endpointFile)
}

func WriteEndpoint(baseDir string, ep Endpoint) error {
	if ep.Host == "" {
		ep.Host = "127.0.0.1"
	}
	if ep.APIURL == "" {
		ep.APIURL = fmt.Sprintf("http://%s:%d", ep.Host, ep.Port)
	}
	data, err := json.Marshal(ep)
	if err != nil {
		return err
	}
	path := EndpointPath(baseDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func ReadEndpoint(baseDir string) (Endpoint, error) {
	data, err := os.ReadFile(EndpointPath(baseDir))
	if err != nil {
		return Endpoint{}, err
	}
	var ep Endpoint
	if err := json.Unmarshal(data, &ep); err != nil {
		return Endpoint{}, err
	}
	if ep.APIURL == "" && ep.Port > 0 {
		ep.APIURL = fmt.Sprintf("http://%s:%d", ep.Host, ep.Port)
	}
	return ep, nil
}

func RemoveEndpoint(baseDir string) {
	_ = os.Remove(EndpointPath(baseDir))
}

// ResolveAPIBase returns TRAIO_API env or reads server.json from runtime dir.
func ResolveAPIBase(runtimeDir string) string {
	if v := os.Getenv("TRAIO_API"); v != "" {
		return v
	}
	ep, err := ReadEndpoint(runtimeDir)
	if err == nil && ep.APIURL != "" {
		return ep.APIURL
	}
	return fmt.Sprintf("http://127.0.0.1:%d", config.DefaultServerPort)
}
