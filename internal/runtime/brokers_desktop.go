//go:build !ios

package runtime

import (
	"context"
	"database/sql"

	"github.com/nite/traio/internal/account"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/alpaca"
	"github.com/nite/traio/internal/broker/ibkr"
	"github.com/nite/traio/internal/broker/schwab"
	"github.com/nite/traio/internal/broker/snaptrade"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/portfolio"
	"github.com/nite/traio/internal/store"
)

// Brokers bundles broker clients and capability interfaces for the API layer.
type Brokers struct {
	Schwab      *schwab.Client
	Alpaca      *alpaca.Client
	Gateway     broker.GatewayController  // nil on builds without IBKR
	Instruments broker.InstrumentProvider // nil on builds without IBKR
	Quotes      broker.BatchMarketDataProvider
	Candles     broker.CandleProvider // nil on builds without IBKR

	snap    *snaptrade.Client
	ibkr    *ibkr.Client
	alpaca  *alpaca.Client
	gateway *ibkr.GatewayManager
}

// gatewayAdapter wraps *ibkr.GatewayManager to satisfy api.GatewayController.
type gatewayAdapter struct{ m *ibkr.GatewayManager }

func (g gatewayAdapter) Status() any                            { return g.m.Status() }
func (g gatewayAdapter) StartGateway(ctx context.Context) error { return g.m.StartGateway(ctx) }
func (g gatewayAdapter) StopGateway(keepSession bool)           { g.m.StopGateway(keepSession) }
func (g gatewayAdapter) Reconnect()                             { _ = g.m.Reconnect() }

// BuildBrokers constructs the full desktop broker set, including IBKR.
func BuildBrokers(cfg config.Config, st *store.Store) Brokers {
	schwabClient := newSchwabClient(cfg.Schwab, st)
	alpacaClient := alpaca.New(cfg.Alpaca)
	snapClient := snaptrade.New(cfg.SnapTrade)
	ibkrClient := ibkr.New(cfg.IBKR)
	gatewayMgr := ibkr.NewGatewayManager(cfg.IBKR)

	return Brokers{
		Schwab:      schwabClient,
		Alpaca:      alpacaClient,
		Gateway:     gatewayAdapter{m: gatewayMgr},
		Instruments: ibkrClient,
		Quotes:      ibkrClient,
		Candles:     ibkrClient,
		snap:        snapClient,
		ibkr:        ibkrClient,
		alpaca:      alpacaClient,
		gateway:     gatewayMgr,
	}
}

func (b Brokers) PositionSources() []portfolio.Source {
	return []portfolio.Source{
		{Name: "SNAPTRADE", Provider: b.snap},
		{Name: "SCHWAB", Provider: b.Schwab},
		{Name: "ALPACA", Provider: b.alpaca},
		{Name: "IBKR", Provider: b.ibkr},
	}
}

func (b Brokers) AccountSources() []account.Source {
	return []account.Source{
		{Name: "IBKR", Provider: b.ibkr},
		{Name: "SCHWAB", Provider: b.Schwab},
		{Name: "ALPACA", Provider: b.alpaca},
	}
}

func newSchwabClient(cfg config.SchwabConfig, st *store.Store) *schwab.Client {
	client := schwab.New(cfg, schwab.WithTokenHandler(func(token schwab.Token) {
		_ = st.SaveOAuthToken(context.Background(), store.OAuthToken{
			Provider:     "schwab",
			AccessToken:  token.AccessToken,
			RefreshToken: token.RefreshToken,
			ExpiresAt:    token.ExpiresAt,
		})
	}))
	token, err := st.GetOAuthToken(context.Background(), "schwab")
	if err == nil {
		client.SetToken(schwab.Token{
			AccessToken:  token.AccessToken,
			RefreshToken: token.RefreshToken,
			ExpiresAt:    token.ExpiresAt,
		})
	} else if err != sql.ErrNoRows {
		// Authentication can be restored through the Schwab OAuth API.
	}
	return client
}

// ApplyConfig pushes updated config into the live broker clients.
func (b Brokers) ApplyConfig(updated config.Config) {
	b.Schwab.SetConfig(updated.Schwab)
	b.alpaca.SetConfig(updated.Alpaca)
	b.snap.SetConfig(updated.SnapTrade)
	b.ibkr.SetConfig(updated.IBKR)
	b.gateway.UpdateConfig(updated.IBKR)
	go b.gateway.Restart()
}

// StartGateway launches the IBKR gateway background loop (desktop only).
func (b Brokers) StartGateway(ctx context.Context) error {
	return b.gateway.Start(ctx)
}
