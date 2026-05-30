package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const pidFile = "traio-server.pid"

func PIDPath(baseDir string) string {
	return filepath.Join(baseDir, pidFile)
}

func WritePID(baseDir string, pid int) error {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(PIDPath(baseDir), []byte(strconv.Itoa(pid)), 0o600)
}

func ReadPID(baseDir string) (int, error) {
	data, err := os.ReadFile(PIDPath(baseDir))
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, fmt.Errorf("parse pid: %w", err)
	}
	return pid, nil
}

func RemovePID(baseDir string) {
	_ = os.Remove(PIDPath(baseDir))
}
