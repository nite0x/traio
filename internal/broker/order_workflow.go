package broker

import (
	"context"
	"errors"
)

// OrderConfirmation is a broker warning awaiting an explicit user decision.
// Its ID is not an order ID and must never be passed to cancellation endpoints.
type OrderConfirmation struct {
	ReplyID    string   `json:"reply_id"`
	Messages   []string `json:"messages"`
	MessageIDs []string `json:"message_ids,omitempty"`
}
type OrderImpact struct {
	Current string `json:"current"`
	Change  string `json:"change"`
	After   string `json:"after"`
}
type OrderPreview struct {
	Amount            string      `json:"amount"`
	Commission        string      `json:"commission"`
	Total             string      `json:"total"`
	Equity            OrderImpact `json:"equity"`
	InitialMargin     OrderImpact `json:"initial_margin"`
	MaintenanceMargin OrderImpact `json:"maintenance_margin"`
	Position          OrderImpact `json:"position"`
	Warnings          []string    `json:"warnings"`
}
type TradingAccount struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	IsPaper           *bool  `json:"is_paper,omitempty"`
	SupportsFractions bool   `json:"supports_fractions"`
	AllowCustomerTime bool   `json:"-"`
}

// OrderWorkflowProvider is optional. Report-only connections never implement it.
type OrderWorkflowProvider interface {
	OrderAttempt(context.Context, string, string) (OrderAttemptState, error)
	TradingAccounts(context.Context) ([]TradingAccount, error)
	SearchOrderInstruments(context.Context, string) ([]Instrument, error)
	OrderInstrument(context.Context, string) (Instrument, error)
	PreviewOrder(context.Context, OrderRequest) (OrderPreview, error)
	ReplyOrder(context.Context, string, string, bool) (Order, error)
}

// OrderError provides a stable API classification without exposing raw transport errors.
type OrderError struct {
	Code    string
	Message string
}

func (e *OrderError) Error() string            { return e.Message }
func NewOrderError(code, message string) error { return &OrderError{Code: code, Message: message} }

func (s *TradingService) workflowLocked(id int64) (OrderWorkflowProvider, error) {
	p, err := s.providerLocked(id)
	if err != nil {
		return nil, err
	}
	w, ok := p.(OrderWorkflowProvider)
	if !ok {
		return nil, errors.New("this connection does not support interactive order entry")
	}
	return w, nil
}
func (s *TradingService) TradingAccounts(ctx context.Context, id int64) ([]TradingAccount, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, err := s.workflowLocked(id)
	if err != nil {
		return nil, err
	}
	return p.TradingAccounts(ctx)
}
func (s *TradingService) SearchOrderInstruments(ctx context.Context, id int64, query string) ([]Instrument, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, err := s.workflowLocked(id)
	if err != nil {
		return nil, err
	}
	return p.SearchOrderInstruments(ctx, query)
}
func (s *TradingService) OrderInstrument(ctx context.Context, id int64, conid string) (Instrument, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, err := s.workflowLocked(id)
	if err != nil {
		return Instrument{}, err
	}
	return p.OrderInstrument(ctx, conid)
}
func (s *TradingService) PreviewOrder(ctx context.Context, id int64, r OrderRequest) (OrderPreview, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, err := s.workflowLocked(id)
	if err != nil {
		return OrderPreview{}, err
	}
	return p.PreviewOrder(ctx, r)
}
func (s *TradingService) ReplyOrder(ctx context.Context, id int64, account, reply string, confirmed bool) (Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, err := s.workflowLocked(id)
	if err != nil {
		return Order{}, err
	}
	return p.ReplyOrder(ctx, account, reply, confirmed)
}

// OrderAttemptState recovers a browser-disconnected submission without resending it.
// A missing attempt after a server restart requires reconciliation with the broker.
type OrderAttemptState struct {
	Request OrderRequest `json:"request"`
	Order   Order        `json:"order"`
	Error   string       `json:"error,omitempty"`
	Code    string       `json:"code,omitempty"`
}

func (s *TradingService) OrderAttempt(ctx context.Context, id int64, account, clientID string) (OrderAttemptState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, err := s.workflowLocked(id)
	if err != nil {
		return OrderAttemptState{}, err
	}
	return p.OrderAttempt(ctx, account, clientID)
}
