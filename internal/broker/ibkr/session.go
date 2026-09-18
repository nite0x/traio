package ibkr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/nite/traio/internal/broker"
)

// BaseURL is the configured external Gateway origin.
func (c *Client) BaseURL() string {
	return strings.TrimRight(c.cfg.GatewayURL, "/")
}

func tickleAuthenticated(result map[string]interface{}) bool {
	if value, ok := result["authenticated"].(bool); ok {
		return value
	}
	iserver, ok := result["iserver"].(map[string]interface{})
	if !ok {
		return false
	}
	authStatus, ok := iserver["authStatus"].(map[string]interface{})
	if !ok {
		return false
	}
	authenticated, _ := authStatus["authenticated"].(bool)
	return authenticated
}

func tickleAccount(result map[string]interface{}) string {
	if account, ok := result["account"].(string); ok && account != "" {
		return account
	}
	if account, ok := result["selectedAccount"].(string); ok && account != "" {
		return account
	}
	if userID, ok := result["userId"].(float64); ok && userID > 0 {
		return fmt.Sprintf("U%d", int(userID))
	}
	return ""
}

// BeginLogin sends users to Gateway Manager to choose and manage their session.
// Opening management must not probe or mutate the Gateway, or expose its SSO URL.
func (c *Client) BeginLogin(_ context.Context) (broker.LoginAction, error) {
	manager, err := NewManagerClient(c.cfg.ManagerURL, "")
	if err != nil {
		return broker.LoginAction{}, fmt.Errorf("请在连接设置中配置有效的 IBKR Gateway Manager 地址")
	}
	return broker.LoginAction{URL: manager.ManagementURL()}, nil
}

// LoginStatus probes only the configured external Gateway endpoint.
func (c *Client) LoginStatus(ctx context.Context) (broker.LoginAction, error) {
	action := broker.LoginAction{}
	tickleURL := c.BaseURL() + "/v1/api/tickle"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tickleURL, strings.NewReader("{}"))
	if err != nil {
		return action, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return action, fmt.Errorf("ibkr: probe gateway session: %w", err)
	}
	defer resp.Body.Close()
	if gatewayProxyRejected(resp) {
		return action, gatewayUnauthorizedError(resp, "/tickle")
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnauthorized {
		return action, fmt.Errorf("ibkr: gateway session probe returned %s", resp.Status)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil {
		action.Authenticated = tickleAuthenticated(payload)
		action.AccountID = tickleAccount(payload)
	}
	if action.Authenticated {
		return action, nil
	}
	return c.authStatus(ctx, action)
}

func (c *Client) authStatus(ctx context.Context, fallback broker.LoginAction) (broker.LoginAction, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL()+"/v1/api/iserver/auth/status", nil)
	if err != nil {
		return fallback, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fallback, fmt.Errorf("ibkr: read gateway authentication status: %w", err)
	}
	defer resp.Body.Close()
	if gatewayProxyRejected(resp) {
		return fallback, gatewayUnauthorizedError(resp, "/iserver/auth/status")
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fallback, nil
	}
	if resp.StatusCode != http.StatusOK {
		return fallback, fmt.Errorf("ibkr: gateway authentication status returned %s", resp.Status)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fallback, fmt.Errorf("ibkr: decode gateway authentication status: %w", err)
	}
	fallback.Authenticated, _ = payload["authenticated"].(bool)
	if account, ok := payload["selectedAccount"].(string); ok {
		fallback.AccountID = account
	}
	return fallback, nil
}

func gatewayProxyRejected(resp *http.Response) bool {
	if resp.StatusCode != http.StatusUnauthorized {
		return false
	}
	for _, challenge := range resp.Header.Values("WWW-Authenticate") {
		challenge = strings.ToLower(strings.TrimSpace(challenge))
		// Gateway Manager challenges with plain Bearer; older proxies used
		// an explicit IBKR Gateway realm.
		if challenge == "bearer" || strings.HasPrefix(challenge, "bearer ") || strings.Contains(challenge, "ibkr gateway") {
			return true
		}
	}
	return false
}

func gatewayUnauthorizedError(resp *http.Response, path string) error {
	if gatewayProxyRejected(resp) {
		return fmt.Errorf("ibkr: %s: gateway proxy authentication failed (HTTP 401); check the selected instance's Gateway Proxy Token (gateway_token) and gateway URL", path)
	}
	return fmt.Errorf("ibkr: %s: gateway session unavailable (HTTP 401); sign in to the configured Gateway instance and check its IBKR session status", path)
}
