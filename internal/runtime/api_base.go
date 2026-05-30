package runtime

import (
	"fmt"
	"os"

	"github.com/nite/traio/internal/config"
)

// ResolveAPIBase returns TRAIO_API or the fixed local backend address.
func ResolveAPIBase(runtimeDir string) string {
	if v := os.Getenv("TRAIO_API"); v != "" {
		return v
	}
	return fmt.Sprintf("http://127.0.0.1:%d", config.DefaultServerPort)
}
