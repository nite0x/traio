package market

import (
	"context"
	"errors"

	"github.com/nite/traio/internal/broker"
)

var (
	ErrInvalidRequest = errors.New("invalid market data request")
	ErrNotFound       = errors.New("market data not found")
)

// SymbolProvider provides public market data without a brokerage account or conid.
type SymbolProvider interface {
	GetQuote(context.Context, string) (*broker.Quote, error)
	GetQuotes(context.Context, []string) ([]broker.Quote, error)
	GetHistory(ctx context.Context, symbol, period, bar string, refresh bool) ([]broker.Candle, error)
}
