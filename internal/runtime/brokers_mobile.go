//go:build ios

package runtime

import (
	"context"
	"database/sql"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/schwab"
	"github.com/nite/traio/internal/broker/snaptrade"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/portfolio"
	"github.com/nite/traio/internal/store"
)

// Brokers bundles the per-build broker dependencies handed to the API layer.
// The iOS build excludes IBKR entirely (no chromedp / os.exec / Java gateway),
// so Gateway is always nil and the /ibkr/* routes degrade gracefully.
type Brokers struct {
	Schwab      *schwab.Client
	Portfolio   *portfolio.Service
	Gateway     broker.GatewayController  // always nil on iOS
	Instruments broker.InstrumentProvider // always nil on iOS
	Quotes      broker.BatchMarketDataProvider
	Candles     broker.CandleProvider // always nil on iOS

	schwab *schwab.Client
	snap   *snaptrade.Client
}

// BuildBrokers constructs the Schwab-only broker set for iOS.
func BuildBrokers(cfg config.Config, st *store.Store) Brokers {
	schwabClient := newSchwabClient(cfg.Schwab, st)
	snapClient := snaptrade.New(cfg.SnapTrade)

	return Brokers{
		Schwab: schwabClient,
		Portfolio: portfolio.New(
			st,
			portfolio.Source{Name: "SNAPTRADE", Provider: snapClient},
			portfolio.Source{Name: "SCHWAB", Provider: schwabClient},
		),
		Gateway:     nil,
		Instruments: nil,
		Quotes:      nil,
		Candles:     nil,

		schwab: schwabClient,
		snap:   snapClient,
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

// ApplyConfig pushes updated config into the live clients (no IBKR on iOS).
func (b Brokers) ApplyConfig(updated config.Config) {
	b.Portfolio.InvalidatePositions()
	b.schwab.SetConfig(updated.Schwab)
	b.snap.SetConfig(updated.SnapTrade)
}

// StartGateway is a no-op on iOS (no IBKR gateway to launch).
func (b Brokers) StartGateway(ctx context.Context) error { return nil }
