package ibkr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const managerResponseLimit = 2 << 20

// ManagerClient reads the public state exposed by ibkr-gateway-manager. It is
// deliberately read-only: instance lifecycle remains owned by the Manager UI.
type ManagerClient struct {
	origin     string
	apiToken   string
	httpClient *http.Client
}

type ManagerHealth struct {
	Status string `json:"status"`
}

type ManagerGatewayStatus struct {
	Running           bool   `json:"running"`
	Authenticated     bool   `json:"authenticated"`
	Account           string `json:"account"`
	Lifecycle         string `json:"lifecycle"`
	SessionAgeSeconds int64  `json:"session_age_seconds"`
	LoginMode         string `json:"login_mode"`
	LoginURL          string `json:"login_url,omitempty"`
	AuthMessage       string `json:"auth_message,omitempty"`
	State             string `json:"state"`
	LastError         string `json:"last_error,omitempty"`
	StateUpdatedAt    string `json:"state_updated_at,omitempty"`
	InstalledVersion  string `json:"installed_version,omitempty"`
	PinnedVersion     string `json:"pinned_version,omitempty"`
	InstallVerified   bool   `json:"install_verified"`
	RollbackAvailable bool   `json:"rollback_available"`
}

type ManagerGateway struct {
	ID                   string               `json:"id"`
	AutoStart            bool                 `json:"auto_start"`
	ProxyListenAddr      string               `json:"proxy_listen_addr,omitempty"`
	ProxyURL             string               `json:"proxy_url,omitempty"`
	ProxyListening       bool                 `json:"proxy_listening"`
	ProxyTokenConfigured bool                 `json:"proxy_token_configured"`
	Status               ManagerGatewayStatus `json:"status"`
}

func NewManagerClient(origin, apiToken string) (*ManagerClient, error) {
	normalized, err := normalizeManagerOrigin(origin)
	if err != nil {
		return nil, err
	}
	return &ManagerClient{
		origin:     normalized,
		apiToken:   strings.TrimSpace(apiToken),
		httpClient: &http.Client{Timeout: 12 * time.Second},
	}, nil
}

func normalizeManagerOrigin(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("manager_url must be an HTTP(S) origin")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("manager_url must be an origin without credentials, path, query, or fragment")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (c *ManagerClient) Origin() string { return c.origin }

func (c *ManagerClient) ManagementURL() string { return c.origin + "/manager/" }

func (c *ManagerClient) Health(ctx context.Context) (ManagerHealth, error) {
	var health ManagerHealth
	if err := c.getJSON(ctx, "/healthz", false, &health); err != nil {
		return ManagerHealth{}, err
	}
	if health.Status != "ok" {
		return ManagerHealth{}, fmt.Errorf("IBKR Gateway Manager health status is %q", health.Status)
	}
	return health, nil
}

func (c *ManagerClient) Gateways(ctx context.Context) ([]ManagerGateway, error) {
	var response struct {
		Gateways []ManagerGateway `json:"gateways"`
	}
	if err := c.getJSON(ctx, "/management/v1/gateways", true, &response); err != nil {
		return nil, err
	}
	if response.Gateways == nil {
		response.Gateways = []ManagerGateway{}
	}
	return response.Gateways, nil
}

func (c *ManagerClient) GatewayStatus(ctx context.Context, gatewayID string) (ManagerGatewayStatus, error) {
	gatewayID = strings.TrimSpace(gatewayID)
	if gatewayID == "" {
		return ManagerGatewayStatus{}, fmt.Errorf("gateway ID is required")
	}
	var status ManagerGatewayStatus
	path := "/management/v1/gateways/" + url.PathEscape(gatewayID) + "/status"
	if err := c.getJSON(ctx, path, true, &status); err != nil {
		return ManagerGatewayStatus{}, err
	}
	return status, nil
}

func (c *ManagerClient) getJSON(ctx context.Context, path string, authenticated bool, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+path, nil)
	if err != nil {
		return err
	}
	if authenticated && c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request IBKR Gateway Manager: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, managerResponseLimit+1))
	if err != nil {
		return fmt.Errorf("read IBKR Gateway Manager response: %w", err)
	}
	if len(body) > managerResponseLimit {
		return fmt.Errorf("IBKR Gateway Manager response is too large")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			detail = http.StatusText(resp.StatusCode)
		}
		if len(detail) > 1024 {
			detail = detail[:1024] + "…"
		}
		return fmt.Errorf("IBKR Gateway Manager returned %d: %s", resp.StatusCode, detail)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode IBKR Gateway Manager response: %w", err)
	}
	return nil
}
