package ibkr

import (
	"context"

	brokerapi "github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

// Broker is an IBKR connection adapter. It talks to a configured Client Portal
// Gateway address but does not own or manage that Gateway process.
type Broker struct {
	client *Client
}

var _ brokerapi.AccountProvider = (*Broker)(nil)
var _ brokerapi.PositionProvider = (*Broker)(nil)
var _ brokerapi.PerformanceProvider = (*Broker)(nil)
var _ brokerapi.TradingProvider = (*Broker)(nil)

func NewBroker(cfg config.IBKRConfig) *Broker {
	return &Broker{
		client: New(cfg),
	}
}

func (b *Broker) Client() *Client {
	return b.client
}

func (b *Broker) SetConfig(cfg config.IBKRConfig) {
	b.client.SetConfig(cfg)
}

func (b *Broker) BeginLogin(ctx context.Context) (brokerapi.LoginAction, error) {
	return b.client.BeginLogin(ctx)
}

func (b *Broker) LoginStatus(ctx context.Context) (brokerapi.LoginAction, error) {
	return b.client.LoginStatus(ctx)
}

func (b *Broker) BaseURL() string {
	return b.client.BaseURL()
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

func (b *Broker) PlaceOrder(ctx context.Context, req brokerapi.OrderRequest) (brokerapi.Order, error) {
	return b.client.PlaceOrder(ctx, req)
}
func (b *Broker) GetOrder(ctx context.Context, accountID, orderID string) (brokerapi.Order, error) {
	return b.client.GetOrder(ctx, accountID, orderID)
}
func (b *Broker) ListOrders(ctx context.Context, query brokerapi.OrderQuery) ([]brokerapi.Order, error) {
	return b.client.ListOrders(ctx, query)
}
func (b *Broker) CancelOrder(ctx context.Context, accountID, orderID string) error {
	return b.client.CancelOrder(ctx, accountID, orderID)
}
