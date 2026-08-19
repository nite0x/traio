package broker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type marketDataTestSession struct {
	id     int64
	label  string
	calls  atomic.Int64
	closed atomic.Int64
}

func (s *marketDataTestSession) ConnectionID() int64 { return s.id }
func (*marketDataTestSession) ProviderCode() string  { return "TEST" }
func (*marketDataTestSession) Health(context.Context) (ConnectionHealth, error) {
	return ConnectionHealth{State: ConnectionStateConnected}, nil
}
func (s *marketDataTestSession) Close(context.Context) error {
	s.closed.Add(1)
	return nil
}

type instrumentTestSession struct{ *marketDataTestSession }

func (s *instrumentTestSession) SearchInstruments(context.Context, string) ([]Instrument, error) {
	s.calls.Add(1)
	return []Instrument{{ConID: s.id, Symbol: s.label}}, nil
}

type batchTestSession struct{ *marketDataTestSession }

func (s *batchTestSession) GetQuotesByConID(context.Context, []int64) ([]Quote, error) {
	s.calls.Add(1)
	return []Quote{{ConID: s.id, Symbol: s.label}}, nil
}

type candleTestSession struct{ *marketDataTestSession }

func (s *candleTestSession) GetCandles(context.Context, int64, string, string) ([]Candle, error) {
	s.calls.Add(1)
	return []Candle{{Time: s.id, Close: float64(s.id)}}, nil
}

type quoteTestSession struct{ *marketDataTestSession }

func (s *quoteTestSession) GetQuote(context.Context, string) (*Quote, error) {
	s.calls.Add(1)
	return &Quote{ConID: s.id, Symbol: s.label}, nil
}

type completeMarketDataTestSession struct{ *marketDataTestSession }

func (s *completeMarketDataTestSession) SearchInstruments(context.Context, string) ([]Instrument, error) {
	s.calls.Add(1)
	return []Instrument{{ConID: s.id, Symbol: s.label}}, nil
}
func (s *completeMarketDataTestSession) GetQuote(context.Context, string) (*Quote, error) {
	s.calls.Add(1)
	return &Quote{ConID: s.id, Symbol: s.label}, nil
}
func (s *completeMarketDataTestSession) GetQuotesByConID(context.Context, []int64) ([]Quote, error) {
	s.calls.Add(1)
	return []Quote{{ConID: s.id, Symbol: s.label}}, nil
}
func (s *completeMarketDataTestSession) GetCandles(context.Context, int64, string, string) ([]Candle, error) {
	s.calls.Add(1)
	return []Candle{{Time: s.id, Close: float64(s.id)}}, nil
}

func TestMarketDataServiceRoutesExplicitConnectionsAndDeterministicDefaults(t *testing.T) {
	service := NewMarketDataService()
	low := &completeMarketDataTestSession{marketDataTestSession: &marketDataTestSession{id: 10, label: "low"}}
	high := &completeMarketDataTestSession{marketDataTestSession: &marketDataTestSession{id: 20, label: "high"}}
	service.Replace(map[int64]BrokerSession{20: high, 10: low})

	results, err := service.SearchInstruments(t.Context(), "ignored")
	if err != nil || len(results) != 1 || results[0].Symbol != "low" {
		t.Fatalf("deterministic default: results=%#v err=%v", results, err)
	}
	quotes, err := service.GetQuotesByConIDForConnection(t.Context(), 20, []int64{1})
	if err != nil || len(quotes) != 1 || quotes[0].Symbol != "high" {
		t.Fatalf("explicit connection: quotes=%#v err=%v", quotes, err)
	}
	service.SetDefaultConnectionID(20)
	candles, err := service.GetCandles(t.Context(), 1, "1d", "5min")
	if err != nil || len(candles) != 1 || candles[0].Time != 20 {
		t.Fatalf("preferred default: candles=%#v err=%v", candles, err)
	}
}

func TestMarketDataServiceKeepsIndependentCapabilitiesOnTheirOwnConnections(t *testing.T) {
	service := NewMarketDataService()
	instruments := &instrumentTestSession{marketDataTestSession: &marketDataTestSession{id: 11, label: "instruments"}}
	batch := &batchTestSession{marketDataTestSession: &marketDataTestSession{id: 12, label: "batch"}}
	candles := &candleTestSession{marketDataTestSession: &marketDataTestSession{id: 13, label: "candles"}}
	single := &quoteTestSession{marketDataTestSession: &marketDataTestSession{id: 14, label: "single"}}
	service.Replace(map[int64]BrokerSession{11: instruments, 12: batch, 13: candles, 14: single})

	if got, _ := service.DefaultConnectionID(MarketDataCapabilityInstruments); got != 11 {
		t.Fatalf("instrument default = %d", got)
	}
	if got, _ := service.DefaultConnectionID(MarketDataCapabilityBatchQuotes); got != 12 {
		t.Fatalf("batch quote default = %d", got)
	}
	if got, _ := service.DefaultConnectionID(MarketDataCapabilityCandles); got != 13 {
		t.Fatalf("candle default = %d", got)
	}
	if got, _ := service.DefaultConnectionID(MarketDataCapabilityQuotes); got != 14 {
		t.Fatalf("single quote default = %d", got)
	}
	quote, err := service.GetQuote(t.Context(), "AAPL")
	if err != nil || quote.Symbol != "single" || batch.calls.Load() != 0 {
		t.Fatalf("single quote crossed into batch provider: quote=%#v err=%v batch calls=%d", quote, err, batch.calls.Load())
	}
}

func TestMarketDataServiceReturnsTypedCapabilityUnavailableError(t *testing.T) {
	service := NewMarketDataService()
	service.Replace(map[int64]BrokerSession{
		9: &quoteTestSession{marketDataTestSession: &marketDataTestSession{id: 9, label: "single"}},
	})
	_, err := service.GetQuotesByConIDForConnection(t.Context(), 9, []int64{1})
	if !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("error = %v, want ErrCapabilityUnavailable", err)
	}
	var unavailable *CapabilityUnavailableError
	if !errors.As(err, &unavailable) || unavailable.ConnectionID != 9 || unavailable.Capability != MarketDataCapabilityBatchQuotes {
		t.Fatalf("unexpected typed error: %#v", unavailable)
	}
	if _, err := service.SearchInstruments(t.Context(), "AAPL"); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("missing default capability error: %v", err)
	}
}

type blockingInstrumentTestSession struct {
	*marketDataTestSession
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingInstrumentTestSession) SearchInstruments(context.Context, string) ([]Instrument, error) {
	s.once.Do(func() { close(s.started) })
	<-s.release
	return []Instrument{{ConID: s.id}}, nil
}

func TestMarketDataServiceReplaceWaitsForInFlightCalls(t *testing.T) {
	service := NewMarketDataService()
	old := &blockingInstrumentTestSession{
		marketDataTestSession: &marketDataTestSession{id: 1},
		started:               make(chan struct{}),
		release:               make(chan struct{}),
	}
	service.Replace(map[int64]BrokerSession{1: old})
	callDone := make(chan error, 1)
	go func() {
		_, err := service.SearchInstruments(t.Context(), "AAPL")
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
		t.Fatal("Replace completed while the old provider was in use")
	case <-time.After(20 * time.Millisecond):
	}
	close(old.release)
	if err := <-callDone; err != nil {
		t.Fatalf("in-flight call: %v", err)
	}
	select {
	case <-replaceDone:
	case <-time.After(time.Second):
		t.Fatal("Replace did not complete after the call released its lease")
	}
}

func TestMarketDataServiceConcurrentReplaceAndRouting(t *testing.T) {
	service := NewMarketDataService()
	providers := make([]*instrumentTestSession, 4)
	for i := range providers {
		providers[i] = &instrumentTestSession{marketDataTestSession: &marketDataTestSession{id: int64(i + 1), label: fmt.Sprint(i + 1)}}
	}
	service.Replace(map[int64]BrokerSession{1: providers[0]})

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				results, err := service.SearchInstruments(context.Background(), "AAPL")
				if err != nil || len(results) != 1 {
					t.Errorf("route during Replace: results=%#v err=%v", results, err)
					return
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		provider := providers[i%len(providers)]
		service.Replace(map[int64]BrokerSession{provider.id: provider})
	}
	wg.Wait()
}
