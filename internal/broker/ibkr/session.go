package ibkr

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nite/traio/internal/broker"
)

// BaseURL is the configured Gateway origin. The endpoint may be managed by
// this process, another Traio server, or an external deployment.
func (c *Client) BaseURL() string {
	return strings.TrimRight(c.cfg.GatewayURL, "/")
}

// BeginLogin returns the Gateway login page without starting or mutating any
// Gateway process. An unreachable endpoint is still useful as a login target
// and is reported as unauthenticated.
func (c *Client) BeginLogin(_ context.Context) (broker.LoginAction, error) {
	return broker.LoginAction{URL: c.BaseURL() + "/sso/Login"}, nil
}

// LoginStatus probes only the configured endpoint. Lifecycle state belongs to
// the independent Gateway registry and is deliberately not consulted here.
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
		return action, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnauthorized {
		return action, nil
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
		return fallback, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fallback, nil
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fallback, nil
	}
	fallback.Authenticated, _ = payload["authenticated"].(bool)
	if account, ok := payload["selectedAccount"].(string); ok {
		fallback.AccountID = account
	}
	return fallback, nil
}
