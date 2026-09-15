package broker

import "context"

// TradingEvent is a notification to refresh authoritative broker projections.
// It must never be applied as a position or cash delta.
type TradingEvent struct {
	AccountID   string
	Order       *Order
	ExecutionID string
	Refresh     bool
}

// TradingEventProvider owns reconnects, initial snapshots and polling fallback.
// emit must be fast and must not retain mutable provider data.
type TradingEventProvider interface {
	WatchTradingEvents(context.Context, func(TradingEvent)) error
}

type freshPositionsKey struct{}

// WithFreshPositions asks adapters to bypass their broker-side portfolio cache.
func WithFreshPositions(ctx context.Context) context.Context {
	return context.WithValue(ctx, freshPositionsKey{}, true)
}

func FreshPositionsRequested(ctx context.Context) bool {
	fresh, _ := ctx.Value(freshPositionsKey{}).(bool)
	return fresh
}
