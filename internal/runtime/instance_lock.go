package runtime

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

const instanceLockFilename = "traio-server.lock"

// InstanceLock is an OS-backed exclusive lock for one Traio server per
// runtime directory. The lock file intentionally remains after unlock; the OS
// releases the actual lock automatically if the process crashes.
type InstanceLock struct {
	file *flock.Flock
}

func AcquireInstanceLock(runtimeDir string) (*InstanceLock, error) {
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return nil, fmt.Errorf("create runtime directory: %w", err)
	}
	path := filepath.Join(runtimeDir, instanceLockFilename)
	file := flock.New(path, flock.SetPermissions(0o600))
	locked, err := file.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock runtime directory: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("another Traio server is already using %s", runtimeDir)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = file.Unlock()
		return nil, fmt.Errorf("secure instance lock: %w", err)
	}
	return &InstanceLock{file: file}, nil
}

func (l *InstanceLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Unlock()
}
