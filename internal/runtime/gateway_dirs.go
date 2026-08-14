package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

// migrateLegacyIBKRGatewayDirectories moves only the former built-in default
// (<runtime>/ibkr-gateway). Explicit absolute directories remain untouched.
func migrateLegacyIBKRGatewayDirectories(ctx context.Context, runtimeDir string, st store.IBKRGatewayRepository) (bool, error) {
	runtimeDir = strings.TrimSpace(runtimeDir)
	if runtimeDir == "" || st == nil {
		return false, nil
	}
	gateways, err := st.ListIBKRGateways(ctx)
	if err != nil {
		return false, fmt.Errorf("list IBKR gateways for directory migration: %w", err)
	}
	legacyDir := filepath.Clean(filepath.Join(runtimeDir, "ibkr-gateway"))
	migrated := false
	for _, gateway := range gateways {
		if filepath.Clean(gateway.GatewayDir) != legacyDir {
			continue
		}
		targetDir := config.DefaultIBKRGatewayDir(runtimeDir, gateway.GatewayKey)
		if targetDir == "" {
			return false, fmt.Errorf("migrate IBKR gateway %q: unsafe gateway key", gateway.GatewayKey)
		}
		moves, err := moveLegacyGatewayFiles(legacyDir, targetDir)
		if err != nil {
			return false, fmt.Errorf("migrate IBKR gateway %q directory: %w", gateway.GatewayKey, err)
		}
		gateway.GatewayDir = targetDir
		if _, err := st.UpsertIBKRGateway(ctx, gateway); err != nil {
			rollbackGatewayFileMoves(moves)
			return false, fmt.Errorf("save migrated IBKR gateway %q directory: %w", gateway.GatewayKey, err)
		}
		migrated = true
	}
	return migrated, nil
}

type gatewayFileMove struct {
	from string
	to   string
}

func moveLegacyGatewayFiles(sourceDir, targetDir string) ([]gatewayFileMove, error) {
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o700); err != nil {
		return nil, err
	}
	pairs := []gatewayFileMove{
		{from: sourceDir, to: targetDir},
		{from: sourceDir + ".rollback", to: targetDir + ".rollback"},
		{from: sourceDir + ".manager.lock", to: targetDir + ".manager.lock"},
		{from: sourceDir + ".audit.jsonl", to: targetDir + ".audit.jsonl"},
		{from: sourceDir + ".audit.jsonl.1", to: targetDir + ".audit.jsonl.1"},
	}
	available := make([]gatewayFileMove, 0, len(pairs))
	for _, pair := range pairs {
		if _, err := os.Lstat(pair.from); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if _, err := os.Lstat(pair.to); err == nil {
			return nil, fmt.Errorf("target already exists: %s", pair.to)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		available = append(available, pair)
	}
	moved := make([]gatewayFileMove, 0, len(available))
	for _, pair := range available {
		if err := os.Rename(pair.from, pair.to); err != nil {
			rollbackGatewayFileMoves(moved)
			return nil, err
		}
		moved = append(moved, pair)
	}
	return moved, nil
}

func rollbackGatewayFileMoves(moves []gatewayFileMove) {
	for i := len(moves) - 1; i >= 0; i-- {
		_ = os.Rename(moves[i].to, moves[i].from)
	}
}
