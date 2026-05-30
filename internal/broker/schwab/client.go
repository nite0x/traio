package schwab

import (
	"context"
	"fmt"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

// Client wraps Schwab Market Data + OAuth APIs.
type Client struct {
	cfg config.SchwabConfig
}

func New(cfg config.SchwabConfig) *Client {
	return &Client{cfg: cfg}
}

func (c *Client) SetConfig(cfg config.SchwabConfig) {
	c.cfg = cfg
}

func (c *Client) GetQuote(ctx context.Context, symbol string) (*broker.Quote, error) {
	_ = ctx
	return nil, fmt.Errorf("schwab: GetQuote not implemented (symbol=%s)", symbol)
}

// AuthURL returns the OAuth authorization URL for user login.
func (c *Client) AuthURL(state string) string {
	_ = state
	return ""
}

// ExchangeCode exchanges an authorization code for tokens.
func (c *Client) ExchangeCode(ctx context.Context, code string) error {
	_ = ctx
	_ = code
	return fmt.Errorf("schwab: ExchangeCode not implemented")
}
