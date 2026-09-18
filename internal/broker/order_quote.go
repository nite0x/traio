package broker

import (
	"context"
	"errors"
)

// OrderQuote keeps missing prices distinct from zero and preserves IBKR's data status.
type OrderQuote struct {
	ConID        int64    `json:"conid"`
	Last         *float64 `json:"last"`
	Bid          *float64 `json:"bid"`
	Ask          *float64 `json:"ask"`
	Change       *float64 `json:"change"`
	ChangePct    *float64 `json:"change_pct"`
	PriorClose   *float64 `json:"prior_close"`
	LastKind     string   `json:"last_kind"`
	Availability string   `json:"availability"`
	UpdatedAt    string   `json:"updated_at"`
}

type OrderQuoteProvider interface {
	OrderQuote(context.Context, string) (OrderQuote, error)
}

func (s *TradingService) OrderQuote(ctx context.Context, id int64, conid string) (OrderQuote, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, err := s.providerLocked(id)
	if err != nil {
		return OrderQuote{}, err
	}
	quotes, ok := p.(OrderQuoteProvider)
	if !ok {
		return OrderQuote{}, errors.New("this connection does not support order quotes")
	}
	return quotes.OrderQuote(ctx, conid)
}
