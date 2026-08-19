package broker

import (
	"context"
	"sync"
	"testing"
	"time"
)

type tradingStub struct{ placed OrderRequest }

func (s *tradingStub) PlaceOrder(_ context.Context, r OrderRequest) (Order, error) {
	s.placed = r
	return Order{ID: "42", AccountID: r.AccountID}, nil
}

type blockingTradingStub struct {
	tradingStub
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingTradingStub) PlaceOrder(ctx context.Context, request OrderRequest) (Order, error) {
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.release:
		return s.tradingStub.PlaceOrder(ctx, request)
	case <-ctx.Done():
		return Order{}, ctx.Err()
	}
}

func TestTradingServiceReplaceWaitsForInFlightCalls(t *testing.T) {
	service := NewTradingService()
	old := &blockingTradingStub{started: make(chan struct{}), release: make(chan struct{})}
	service.Replace(map[int64]TradingProvider{7: old})
	callDone := make(chan error, 1)
	go func() {
		_, err := service.PlaceOrder(t.Context(), 7, OrderRequest{AccountID: "A", Symbol: "AAPL"})
		callDone <- err
	}()
	<-old.started
	replaceDone := make(chan struct{})
	go func() {
		service.Replace(nil)
		close(replaceDone)
	}()
	select {
	case <-replaceDone:
		t.Fatal("Replace completed while the old trading provider was in use")
	case <-time.After(20 * time.Millisecond):
	}
	close(old.release)
	if err := <-callDone; err != nil {
		t.Fatalf("in-flight trading call: %v", err)
	}
	select {
	case <-replaceDone:
	case <-time.After(time.Second):
		t.Fatal("Replace did not complete after the trading call finished")
	}
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
