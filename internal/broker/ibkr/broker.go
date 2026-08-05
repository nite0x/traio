package ibkr

import (
	"context"

	brokerapi "github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

// Broker combines the IBKR data client and Client Portal Gateway lifecycle
// into the minimum first-class broker capability set.
type Broker struct {
	client  *Client
	gateway *GatewayManager
}

var _ brokerapi.Broker = (*Broker)(nil)

func NewBroker(cfg config.IBKRConfig) *Broker {
	return &Broker{
		client:  New(cfg),
		gateway: NewGatewayManager(cfg),
	}
}

func (b *Broker) Client() *Client {
	return b.client
}

func (b *Broker) Gateway() *GatewayManager {
	return b.gateway
}

func (b *Broker) SetConfig(cfg config.IBKRConfig) {
	b.client.SetConfig(cfg)
	b.gateway.UpdateConfig(cfg)
}

func (b *Broker) BeginLogin(ctx context.Context) (brokerapi.LoginAction, error) {
	if err := b.gateway.StartGateway(ctx); err != nil {
		return brokerapi.LoginAction{}, err
	}
	status := b.gateway.Status()
	loginURL := status.LoginURL
	if loginURL == "" && !status.Authenticated {
		loginURL = b.gateway.LoginURL()
	}
	return brokerapi.LoginAction{
		URL:           loginURL,
		Authenticated: status.Authenticated,
		AccountID:     status.Account,
	}, nil
}

func (b *Broker) ListAccounts(ctx context.Context) ([]brokerapi.Account, error) {
	return b.client.ListAccounts(ctx)
}

func (b *Broker) GetAccount(ctx context.Context, accountID string) (brokerapi.Account, error) {
	return b.client.GetAccount(ctx, accountID)
}

func (b *Broker) GetCashBalances(ctx context.Context, accountID string) ([]brokerapi.CashBalance, error) {
	return b.client.GetCashBalances(ctx, accountID)
}

func (b *Broker) ListAccountPositions(ctx context.Context, accountID string) ([]brokerapi.Position, error) {
	return b.client.ListAccountPositions(ctx, accountID)
}

func (b *Broker) GetDailyPerformance(ctx context.Context, accountID string) (brokerapi.DailyPerformance, error) {
	return b.client.GetDailyPerformance(ctx, accountID)
}
