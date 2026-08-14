package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	IBKRGatewayLifecycleManaged    = "managed"
	IBKRGatewayLifecyclePersistent = "persistent"
)

// IBKRGateway is one locally-owned Client Portal Gateway process. Connections
// do not point at this row; they use GatewayURL directly so a Gateway hosted by
// another Traio server is equally valid.
type IBKRGateway struct {
	ID          int64  `json:"id"`
	GatewayKey  string `json:"gateway_key"`
	Name        string `json:"name"`
	GatewayURL  string `json:"gateway_url"`
	GatewayDir  string `json:"gateway_dir"`
	GatewayPort int    `json:"gateway_port"`
	Lifecycle   string `json:"lifecycle"`
	Enabled     bool   `json:"enabled"`
}

func (s *Store) ListIBKRGateways(ctx context.Context) ([]IBKRGateway, error) {
	rows, err := s.queryContext(ctx, `
		SELECT id, gateway_key, name, gateway_url, gateway_dir, gateway_port, lifecycle, enabled
		FROM ibkr_gateways ORDER BY gateway_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IBKRGateway{}
	for rows.Next() {
		gateway, err := scanIBKRGateway(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, gateway)
	}
	return out, rows.Err()
}

func (s *Store) GetIBKRGateway(ctx context.Context, gatewayID int64) (IBKRGateway, error) {
	gateway, err := scanIBKRGateway(s.queryRowContext(ctx, `
		SELECT id, gateway_key, name, gateway_url, gateway_dir, gateway_port, lifecycle, enabled
		FROM ibkr_gateways WHERE id = ?`, gatewayID))
	if errors.Is(err, sql.ErrNoRows) {
		return IBKRGateway{}, ErrNotFound
	}
	return gateway, err
}

func (s *Store) UpsertIBKRGateway(ctx context.Context, gateway IBKRGateway) (IBKRGateway, error) {
	gateway.GatewayKey = strings.TrimSpace(gateway.GatewayKey)
	gateway.Name = strings.TrimSpace(gateway.Name)
	gateway.GatewayURL = strings.TrimRight(strings.TrimSpace(gateway.GatewayURL), "/")
	gateway.GatewayDir = strings.TrimSpace(gateway.GatewayDir)
	gateway.Lifecycle = strings.ToLower(strings.TrimSpace(gateway.Lifecycle))
	if gateway.GatewayKey == "" {
		return IBKRGateway{}, fmt.Errorf("gateway key is required")
	}
	if gateway.Name == "" {
		gateway.Name = gateway.GatewayKey
	}
	if gateway.GatewayPort <= 0 || gateway.GatewayPort > 65535 {
		return IBKRGateway{}, fmt.Errorf("gateway port must be between 1 and 65535")
	}
	if gateway.GatewayDir == "" {
		return IBKRGateway{}, fmt.Errorf("gateway directory is required")
	}
	if !filepath.IsAbs(gateway.GatewayDir) {
		return IBKRGateway{}, fmt.Errorf("gateway directory must be absolute")
	}
	parsed, err := url.Parse(gateway.GatewayURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return IBKRGateway{}, fmt.Errorf("gateway URL must be an HTTP(S) origin")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return IBKRGateway{}, fmt.Errorf("gateway URL must be an origin without credentials, path, query, or fragment")
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return IBKRGateway{}, fmt.Errorf("managed gateway URL must use a loopback host")
	}
	urlPort, err := strconv.Atoi(parsed.Port())
	if err != nil || urlPort != gateway.GatewayPort {
		return IBKRGateway{}, fmt.Errorf("managed gateway URL port must match gateway_port")
	}
	switch gateway.Lifecycle {
	case "":
		gateway.Lifecycle = IBKRGatewayLifecycleManaged
	case IBKRGatewayLifecycleManaged, IBKRGatewayLifecyclePersistent:
	default:
		return IBKRGateway{}, fmt.Errorf("unsupported gateway lifecycle %q", gateway.Lifecycle)
	}
	_, err = s.execContext(ctx, `
		INSERT INTO ibkr_gateways (
			gateway_key, name, gateway_url, gateway_dir, gateway_port, lifecycle, enabled
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(gateway_key) DO UPDATE SET
			name = excluded.name,
			gateway_url = excluded.gateway_url,
			gateway_dir = excluded.gateway_dir,
			gateway_port = excluded.gateway_port,
			lifecycle = excluded.lifecycle,
			enabled = excluded.enabled,
			updated_at = CURRENT_TIMESTAMP`,
		gateway.GatewayKey, gateway.Name, gateway.GatewayURL, gateway.GatewayDir,
		gateway.GatewayPort, gateway.Lifecycle, gateway.Enabled)
	if err != nil {
		return IBKRGateway{}, err
	}
	return scanIBKRGateway(s.queryRowContext(ctx, `
		SELECT id, gateway_key, name, gateway_url, gateway_dir, gateway_port, lifecycle, enabled
		FROM ibkr_gateways WHERE gateway_key = ?`, gateway.GatewayKey))
}

func (s *Store) DeleteIBKRGateway(ctx context.Context, gatewayID int64) error {
	result, err := s.execContext(ctx, `DELETE FROM ibkr_gateways WHERE id = ?`, gatewayID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err == nil && affected == 0 {
		return ErrNotFound
	}
	return err
}

func scanIBKRGateway(scanner rowScanner) (IBKRGateway, error) {
	var gateway IBKRGateway
	err := scanner.Scan(
		&gateway.ID, &gateway.GatewayKey, &gateway.Name, &gateway.GatewayURL,
		&gateway.GatewayDir, &gateway.GatewayPort, &gateway.Lifecycle, &gateway.Enabled,
	)
	return gateway, err
}
