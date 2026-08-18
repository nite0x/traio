package broker

import (
	"context"
	"testing"
)

type tradingStub struct{ placed OrderRequest }

func (s *tradingStub) PlaceOrder(_ context.Context, r OrderRequest) (Order, error) {
	s.placed = r
	return Order{ID: "42", AccountID: r.AccountID}, nil
}
func (*tradingStub) GetOrder(context.Context, string, string) (Order, error) { return Order{}, nil }
func (*tradingStub) ListOrders(context.Context, OrderQuery) ([]Order, error) { return []Order{}, nil }
func (*tradingStub) CancelOrder(context.Context, string, string) error       { return nil }

func TestTradingServiceRoutesByConnection(t *testing.T) {
	service := NewTradingService()
	stub := &tradingStub{}
	service.Replace(map[int64]TradingProvider{7: stub})
	order, err := service.PlaceOrder(context.Background(), 7, OrderRequest{AccountID: "A", Symbol: "AAPL"})
	if err != nil {
		t.Fatal(err)
	}
	if order.ID != "42" || stub.placed.Symbol != "AAPL" {
		t.Fatalf("unexpected routing result: %#v %#v", order, stub.placed)
	}
	if _, err := service.PlaceOrder(context.Background(), 8, OrderRequest{}); err == nil {
		t.Fatal("expected unavailable connection error")
	}
}
