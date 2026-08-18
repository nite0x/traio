package runtime

import (
	"os"
	"strings"
)

// ResolveAPIBase returns the explicitly configured deployment address or the
// local server address published after the sidecar binds its dynamic port.
func ResolveAPIBase(runtimeDir string) (string, error) {
	if v := strings.TrimSpace(os.Getenv("TRAIO_API")); v != "" {
		return strings.TrimRight(v, "/"), nil
	}
	return ReadAPIURL(runtimeDir)
}
