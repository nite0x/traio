package snaptrade

import (
	"context"
	"fmt"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

// Client wraps SnapTrade for unified multi-broker accounts.
type Client struct {
	cfg config.SnapTradeConfig
}

func New(cfg config.SnapTradeConfig) *Client {
	return &Client{cfg: cfg}
}

func (c *Client) SetConfig(cfg config.SnapTradeConfig) {
	c.cfg = cfg
}

func (c *Client) ListPositions(ctx context.Context) ([]broker.Position, error) {
	_ = ctx
	return nil, fmt.Errorf("snaptrade: ListPositions not implemented")
}

func (c *Client) PlaceOrder(ctx context.Context, req broker.OrderRequest) (string, error) {
	_ = ctx
	_ = req
	return "", fmt.Errorf("snaptrade: PlaceOrder not implemented")
}
