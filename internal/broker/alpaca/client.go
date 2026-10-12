package alpaca

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

const defaultPaperBaseURL = "https://paper-api.alpaca.markets"
const defaultLiveBaseURL = "https://api.alpaca.markets"

func officialEnvironment(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Hostname()) {
	case "paper-api.alpaca.markets":
		return "paper"
	case "api.alpaca.markets":
		return "live"
	default:
		return ""
	}
}

// Client wraps Alpaca Trading API v2.
type Client struct {
	mu         sync.RWMutex
	cfg        config.AlpacaConfig
	httpClient *http.Client
	orders     broker.OrderSessionState
}

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string { return e.Message }

type Option func(*Client)

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func New(cfg config.AlpacaConfig, opts ...Option) *Client {
	cfg.Normalize()
	c := &Client{
		cfg:        cfg,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) SetConfig(cfg config.AlpacaConfig) {
	c.mu.Lock()
	cfg.Normalize()
	c.cfg = cfg
	c.mu.Unlock()
}

func (c *Client) Configured() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.APIKey != "" && c.cfg.APISecret != ""
}

func (c *Client) baseURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cfg.BaseURL != "" {
		return c.cfg.BaseURL
	}
	return defaultPaperBaseURL
}

func (c *Client) Do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	c.mu.RLock()
	keyID := c.cfg.APIKey
	secret := c.cfg.APISecret
	baseURL := c.cfg.BaseURL
	c.mu.RUnlock()
	if baseURL == "" {
		baseURL = defaultPaperBaseURL
	}

	if keyID == "" || secret == "" {
		return nil, fmt.Errorf("alpaca: api_key and api_secret are required")
	}

	path = strings.TrimPrefix(path, "/")
	endpoint := baseURL + "/v2/" + path
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("alpaca: build request: %w", err)
	}
	req.Header.Set("APCA-API-KEY-ID", keyID)
	req.Header.Set("APCA-API-SECRET-KEY", secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	httpClient := c.httpClient
	if method != http.MethodGet && method != http.MethodHead {
		copy := *httpClient
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		httpClient = &copy
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("alpaca: request %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(payload))
		if msg == "" {
			msg = resp.Status
		}
		return nil, &APIError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("alpaca: %s %s: %s", method, path, msg)}
	}
	return resp, nil
}
