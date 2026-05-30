//go:build ios

package runtime

import (
	"context"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/schwab"
	"github.com/nite/traio/internal/broker/snaptrade"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/portfolio"
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

	schwab *schwab.Client
	snap   *snaptrade.Client
}

// BuildBrokers constructs the Schwab-only broker set for iOS.
func BuildBrokers(cfg config.Config) Brokers {
	schwabClient := schwab.New(cfg.Schwab)
	snapClient := snaptrade.New(cfg.SnapTrade)

	return Brokers{
		Schwab:      schwabClient,
		Portfolio:   portfolio.New(snapClient, nil), // no IBKR position source
		Gateway:     nil,
		Instruments: nil,
		Quotes:      nil,

		schwab: schwabClient,
		snap:   snapClient,
	}
}

// ApplyConfig pushes updated config into the live clients (no IBKR on iOS).
func (b Brokers) ApplyConfig(updated config.Config) {
	b.Portfolio.InvalidatePositions()
	b.schwab.SetConfig(updated.Schwab)
	b.snap.SetConfig(updated.SnapTrade)
}

// StartGateway is a no-op on iOS (no IBKR gateway to launch).
func (b Brokers) StartGateway(ctx context.Context) error { return nil }
