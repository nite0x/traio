//go:build ios

package runtime

import (
	"context"
	"database/sql"

	"github.com/nite/traio/internal/account"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/alpaca"
	"github.com/nite/traio/internal/broker/schwab"
	"github.com/nite/traio/internal/broker/snaptrade"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/portfolio"
	"github.com/nite/traio/internal/store"
)

// Brokers bundles broker clients and capability interfaces for the API layer.
// The iOS build excludes IBKR entirely, so Gateway is always nil.
type Brokers struct {
	Schwab      *schwab.Client
	Alpaca      *alpaca.Client
	Gateway     broker.GatewayController  // always nil on iOS
	Instruments broker.InstrumentProvider // always nil on iOS
	Quotes      broker.BatchMarketDataProvider
	Candles     broker.CandleProvider // always nil on iOS

	schwab *schwab.Client
	alpaca *alpaca.Client
	snap   *snaptrade.Client
}

// BuildBrokers constructs the Schwab-only broker set for iOS.
func BuildBrokers(cfg config.Config, st *store.Store) Brokers {
	schwabClient := newSchwabClient(cfg.Schwab, st)
	alpacaClient := alpaca.New(cfg.Alpaca)
	snapClient := snaptrade.New(cfg.SnapTrade)

	return Brokers{
		Schwab:      schwabClient,
		Alpaca:      alpacaClient,
		Gateway:     nil,
		Instruments: nil,
		Quotes:      nil,
		Candles:     nil,
		schwab:      schwabClient,
		alpaca:      alpacaClient,
		snap:        snapClient,
	}
}

func (b Brokers) PositionSources() []portfolio.Source {
	return []portfolio.Source{
		{Name: "SNAPTRADE", Provider: b.snap},
		{Name: "SCHWAB", Provider: b.Schwab},
		{Name: "ALPACA", Provider: b.alpaca},
	}
}

func (b Brokers) AccountSources() []account.Source {
	return []account.Source{
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
	b.schwab.SetConfig(updated.Schwab)
	b.alpaca.SetConfig(updated.Alpaca)
	b.snap.SetConfig(updated.SnapTrade)
}

// StartGateway is a no-op on iOS.
func (b Brokers) StartGateway(ctx context.Context) error { return nil }
