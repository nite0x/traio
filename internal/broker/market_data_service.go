package broker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrCapabilityUnavailable is returned when no enabled connection exposes a
// requested optional capability, or when the selected connection does not
// expose it.
var ErrCapabilityUnavailable = errors.New("broker capability unavailable")

// MarketDataCapability distinguishes the independently routable market-data
// contracts. CapabilityMarketData intentionally remains the provider catalog
// flag for both single-symbol and batch quote implementations.
type MarketDataCapability string

const (
	MarketDataCapabilityInstruments MarketDataCapability = "instruments"
	MarketDataCapabilityQuotes      MarketDataCapability = "quotes"
	MarketDataCapabilityBatchQuotes MarketDataCapability = "batch_quotes"
	MarketDataCapabilityCandles     MarketDataCapability = "candles"
)

// CapabilityUnavailableError identifies the connection and capability that
// could not be routed. It unwraps to ErrCapabilityUnavailable for stable error
// handling at API boundaries.
type CapabilityUnavailableError struct {
	ConnectionID int64
	Capability   MarketDataCapability
}

func (e *CapabilityUnavailableError) Error() string {
	if e.ConnectionID > 0 {
		return fmt.Sprintf("broker connection %d does not provide %s", e.ConnectionID, e.Capability)
	}
	return fmt.Sprintf("no enabled broker connection provides %s", e.Capability)
}

func (*CapabilityUnavailableError) Unwrap() error { return ErrCapabilityUnavailable }

// MarketDataService routes every market-data operation to an enabled broker
// session. Replace swaps all capability indexes together. Calls hold a read
// lease for their duration so a runtime can safely close removed sessions as
// soon as Replace returns.
type MarketDataService struct {
	mu sync.RWMutex

	preferredConnectionID int64
	instruments           map[int64]InstrumentProvider
	quotes                map[int64]MarketDataProvider
	batchQuotes           map[int64]BatchMarketDataProvider
	candles               map[int64]CandleProvider
}

func NewMarketDataService() *MarketDataService {
	return &MarketDataService{
		instruments: map[int64]InstrumentProvider{},
		quotes:      map[int64]MarketDataProvider{},
		batchQuotes: map[int64]BatchMarketDataProvider{},
		candles:     map[int64]CandleProvider{},
	}
}

// Replace rebuilds the capability indexes from runtime sessions. The input map
// is not retained, so callers may reuse it after this method returns.
func (s *MarketDataService) Replace(sessions map[int64]BrokerSession) {
	instruments := make(map[int64]InstrumentProvider)
	quotes := make(map[int64]MarketDataProvider)
	batchQuotes := make(map[int64]BatchMarketDataProvider)
	candles := make(map[int64]CandleProvider)
	for connectionID, session := range sessions {
		if session == nil {
			continue
		}
		if provider, ok := session.(InstrumentProvider); ok {
			instruments[connectionID] = provider
		}
		if provider, ok := session.(MarketDataProvider); ok {
			quotes[connectionID] = provider
		}
		if provider, ok := session.(BatchMarketDataProvider); ok {
			batchQuotes[connectionID] = provider
		}
		if provider, ok := session.(CandleProvider); ok {
			candles[connectionID] = provider
		}
	}

	s.mu.Lock()
	s.instruments = instruments
	s.quotes = quotes
	s.batchQuotes = batchQuotes
	s.candles = candles
	s.mu.Unlock()
}

// SetDefaultConnectionID prefers one connection whenever it implements the
// requested capability. A missing capability deterministically falls back to
// the lowest enabled connection ID that implements it. Passing zero clears the
// preference.
func (s *MarketDataService) SetDefaultConnectionID(connectionID int64) {
	s.mu.Lock()
	s.preferredConnectionID = connectionID
	s.mu.Unlock()
}

// Supports reports whether at least one enabled connection currently exposes
// the requested market-data capability.
func (s *MarketDataService) Supports(capability MarketDataCapability) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch capability {
	case MarketDataCapabilityInstruments:
		return len(s.instruments) != 0
	case MarketDataCapabilityQuotes:
		return len(s.quotes) != 0
	case MarketDataCapabilityBatchQuotes:
		return len(s.batchQuotes) != 0
	case MarketDataCapabilityCandles:
		return len(s.candles) != 0
	default:
		return false
	}
}

// DefaultConnectionID returns the connection selected for a capability.
func (s *MarketDataService) DefaultConnectionID(capability MarketDataCapability) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch capability {
	case MarketDataCapabilityInstruments:
		return selectedProviderID(s.instruments, s.preferredConnectionID, capability)
	case MarketDataCapabilityQuotes:
		return selectedProviderID(s.quotes, s.preferredConnectionID, capability)
	case MarketDataCapabilityBatchQuotes:
		return selectedProviderID(s.batchQuotes, s.preferredConnectionID, capability)
	case MarketDataCapabilityCandles:
		return selectedProviderID(s.candles, s.preferredConnectionID, capability)
	default:
		return 0, &CapabilityUnavailableError{Capability: capability}
	}
}

func (s *MarketDataService) SearchInstruments(ctx context.Context, query string) ([]Instrument, error) {
	return s.SearchInstrumentsForConnection(ctx, 0, query)
}

func (s *MarketDataService) SearchInstrumentsForConnection(ctx context.Context, connectionID int64, query string) ([]Instrument, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	provider, err := selectedProvider(s.instruments, s.preferredConnectionID, connectionID, MarketDataCapabilityInstruments)
	if err != nil {
		return nil, err
	}
	return provider.SearchInstruments(ctx, query)
}

func (s *MarketDataService) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	return s.GetQuoteForConnection(ctx, 0, symbol)
}

func (s *MarketDataService) GetQuoteForConnection(ctx context.Context, connectionID int64, symbol string) (*Quote, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	provider, err := selectedProvider(s.quotes, s.preferredConnectionID, connectionID, MarketDataCapabilityQuotes)
	if err != nil {
		return nil, err
	}
	return provider.GetQuote(ctx, symbol)
}

func (s *MarketDataService) GetQuotesByConID(ctx context.Context, conIDs []int64) ([]Quote, error) {
	return s.GetQuotesByConIDForConnection(ctx, 0, conIDs)
}

func (s *MarketDataService) GetQuotesByConIDForConnection(ctx context.Context, connectionID int64, conIDs []int64) ([]Quote, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	provider, err := selectedProvider(s.batchQuotes, s.preferredConnectionID, connectionID, MarketDataCapabilityBatchQuotes)
	if err != nil {
		return nil, err
	}
	return provider.GetQuotesByConID(ctx, conIDs)
}

func (s *MarketDataService) GetCandles(ctx context.Context, conID int64, period, bar string) ([]Candle, error) {
	return s.GetCandlesForConnection(ctx, 0, conID, period, bar)
}

func (s *MarketDataService) GetCandlesForConnection(ctx context.Context, connectionID, conID int64, period, bar string) ([]Candle, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	provider, err := selectedProvider(s.candles, s.preferredConnectionID, connectionID, MarketDataCapabilityCandles)
	if err != nil {
		return nil, err
	}
	return provider.GetCandles(ctx, conID, period, bar)
}

func selectedProvider[T any](providers map[int64]T, preferredConnectionID, requestedConnectionID int64, capability MarketDataCapability) (T, error) {
	var zero T
	if requestedConnectionID > 0 {
		provider, ok := providers[requestedConnectionID]
		if !ok {
			return zero, &CapabilityUnavailableError{ConnectionID: requestedConnectionID, Capability: capability}
		}
		return provider, nil
	}
	connectionID, err := selectedProviderID(providers, preferredConnectionID, capability)
	if err != nil {
		return zero, err
	}
	return providers[connectionID], nil
}

func selectedProviderID[T any](providers map[int64]T, preferredConnectionID int64, capability MarketDataCapability) (int64, error) {
	if _, ok := providers[preferredConnectionID]; preferredConnectionID > 0 && ok {
		return preferredConnectionID, nil
	}
	if len(providers) == 0 {
		return 0, &CapabilityUnavailableError{Capability: capability}
	}
	ids := make([]int64, 0, len(providers))
	for id := range providers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids[0], nil
}

var _ InstrumentProvider = (*MarketDataService)(nil)
var _ MarketDataProvider = (*MarketDataService)(nil)
var _ BatchMarketDataProvider = (*MarketDataService)(nil)
var _ CandleProvider = (*MarketDataService)(nil)
