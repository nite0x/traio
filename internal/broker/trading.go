package broker

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// ValidateOrder enforces invariants shared by every provider before a live request.
func ValidateOrder(r OrderRequest) error {
	if strings.TrimSpace(r.AccountID) == "" {
		return fmt.Errorf("account_id is required")
	}
	if strings.TrimSpace(r.Symbol) == "" && strings.TrimSpace(r.InstrumentID) == "" {
		return fmt.Errorf("symbol or instrument_id is required")
	}
	if r.Side != "buy" && r.Side != "sell" {
		return fmt.Errorf("side must be buy or sell")
	}
	if (r.Quantity > 0) == (r.Notional > 0) {
		return fmt.Errorf("exactly one of quantity or notional is required")
	}
	switch r.OrderType {
	case "market":
	case "limit":
		if r.LimitPrice <= 0 {
			return fmt.Errorf("limit_price is required")
		}
	case "stop":
		if r.StopPrice <= 0 {
			return fmt.Errorf("stop_price is required")
		}
	case "stop_limit":
		if r.StopPrice <= 0 || r.LimitPrice <= 0 {
			return fmt.Errorf("stop_price and limit_price are required")
		}
	case "trailing_stop":
		if (r.TrailPrice > 0) == (r.TrailPercent > 0) {
			return fmt.Errorf("exactly one trailing value is required")
		}
	default:
		return fmt.Errorf("unsupported order_type %q", r.OrderType)
	}
	if strings.TrimSpace(r.TimeInForce) == "" {
		return fmt.Errorf("time_in_force is required")
	}
	return nil
}

// TradingService is the provider-neutral entry point used by HTTP, MCP, and
// future automation callers. Connections, rather than broker names, select an
// adapter so multiple accounts at one broker remain isolated.
type TradingService struct {
	mu        sync.RWMutex
	providers map[int64]TradingProvider
}

func NewTradingService() *TradingService {
	return &TradingService{providers: make(map[int64]TradingProvider)}
}
func (s *TradingService) Replace(providers map[int64]TradingProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers = providers
}
func (s *TradingService) provider(connectionID int64) (TradingProvider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.providers[connectionID]
	if p == nil {
		return nil, fmt.Errorf("trading connection %d is unavailable", connectionID)
	}
	return p, nil
}
func (s *TradingService) PlaceOrder(ctx context.Context, id int64, r OrderRequest) (Order, error) {
	p, e := s.provider(id)
	if e != nil {
		return Order{}, e
	}
	return p.PlaceOrder(ctx, r)
}
func (s *TradingService) GetOrder(ctx context.Context, id int64, account, order string) (Order, error) {
	p, e := s.provider(id)
	if e != nil {
		return Order{}, e
	}
	return p.GetOrder(ctx, account, order)
}
func (s *TradingService) ListOrders(ctx context.Context, id int64, q OrderQuery) ([]Order, error) {
	p, e := s.provider(id)
	if e != nil {
		return nil, e
	}
	return p.ListOrders(ctx, q)
}
func (s *TradingService) CancelOrder(ctx context.Context, id int64, account, order string) error {
	p, e := s.provider(id)
	if e != nil {
		return e
	}
	return p.CancelOrder(ctx, account, order)
}
