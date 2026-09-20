package market

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nite/traio/internal/broker"
)

type BrokerRouter interface {
	GetQuoteForConnection(context.Context, int64, string) (*broker.Quote, error)
	SearchInstrumentsForConnection(context.Context, int64, string) ([]broker.Instrument, error)
	GetQuotesByConIDForConnection(context.Context, int64, []int64) ([]broker.Quote, error)
	ProviderCode(int64) (string, bool)
}

type Service struct {
	mu     sync.RWMutex
	broker BrokerRouter
	tiingo SymbolProvider
	yahoo  SymbolProvider
}

func NewService(brokerRouter BrokerRouter, tiingo, yahoo SymbolProvider) *Service {
	return &Service{broker: brokerRouter, tiingo: tiingo, yahoo: yahoo}
}

func (s *Service) SetTiingo(provider SymbolProvider) {
	s.mu.Lock()
	s.tiingo = provider
	s.mu.Unlock()
}

func (s *Service) GetQuote(ctx context.Context, symbol string) (*broker.Quote, error) {
	return s.GetQuoteFor(ctx, symbol, Request{Usage: UsageLiveQuote, Source: SourceAuto})
}

func (s *Service) GetQuotes(ctx context.Context, symbols []string) ([]broker.Quote, error) {
	return s.GetQuotesFor(ctx, symbols, Request{Usage: UsageLiveQuote, Source: SourceAuto})
}

func (s *Service) GetHistory(ctx context.Context, symbol, period, bar string, refresh bool) ([]broker.Candle, error) {
	result, err := s.GetHistoryFor(ctx, symbol, period, bar, refresh, Request{Usage: UsageHistoricalChart, Source: SourceAuto})
	return result.Candles, err
}

func (s *Service) GetQuoteFor(ctx context.Context, symbol string, request Request) (*broker.Quote, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return nil, fmt.Errorf("%w: symbol is required", ErrInvalidRequest)
	}
	request = normalizeRequest(request, UsageLiveQuote)
	sources, err := quoteSources(request)
	if err != nil {
		return nil, err
	}
	var attempts []error
	for _, source := range sources {
		quote, quoteErr := s.quoteFrom(ctx, source, symbol, request.ConnectionID)
		if quoteErr == nil {
			if source == SourceBroker {
				return s.normalizeBrokerQuote(*quote, request.ConnectionID), nil
			}
			return normalizeQuote(*quote, source), nil
		}
		if errors.Is(quoteErr, ErrInvalidRequest) {
			return nil, quoteErr
		}
		attempts = append(attempts, classifyProviderError(string(source), "quote", quoteErr))
	}
	return nil, combinedProviderError(attempts)
}

func (s *Service) GetQuotesFor(ctx context.Context, symbols []string, request Request) ([]broker.Quote, error) {
	if len(symbols) > 100 {
		return nil, fmt.Errorf("%w: at most 100 symbols per request", ErrInvalidRequest)
	}
	seen := map[string]bool{}
	result := make([]broker.Quote, 0, len(symbols))
	var lastErr error
	for _, value := range symbols {
		symbol := strings.ToUpper(strings.TrimSpace(value))
		if symbol == "" || seen[symbol] {
			continue
		}
		seen[symbol] = true
		quote, err := s.GetQuoteFor(ctx, symbol, request)
		if err != nil {
			lastErr = err
			continue
		}
		result = append(result, *quote)
	}
	if len(result) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return result, nil
}

func (s *Service) GetHistoryFor(ctx context.Context, symbol, period, bar string, refresh bool, request Request) (History, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return History{}, fmt.Errorf("%w: symbol is required", ErrInvalidRequest)
	}
	request = normalizeRequest(request, UsageHistoricalChart)
	if request.Usage != UsageHistoricalChart && request.Usage != UsagePortfolioValuation && request.Usage != UsageReconciliation {
		return History{}, fmt.Errorf("%w: unsupported history usage=%s", ErrInvalidRequest, request.Usage)
	}
	sources, err := historySources(request.Source)
	if err != nil {
		return History{}, err
	}
	var attempts []error
	for _, source := range sources {
		provider := s.symbolProvider(source)
		if provider == nil {
			attempts = append(attempts, NewProviderError(ErrorNotConfigured, string(source), "history", "provider is not configured", nil))
			continue
		}
		var candles []broker.Candle
		var historyErr error
		if request.Usage == UsagePortfolioValuation {
			if valuationProvider, ok := provider.(ValuationHistoryProvider); ok {
				candles, historyErr = valuationProvider.GetValuationHistory(ctx, symbol, period, bar, refresh)
			} else {
				candles, historyErr = provider.GetHistory(ctx, symbol, period, bar, refresh)
			}
		} else {
			candles, historyErr = provider.GetHistory(ctx, symbol, period, bar, refresh)
		}
		if historyErr != nil {
			if errors.Is(historyErr, ErrInvalidRequest) {
				return History{}, historyErr
			}
			attempts = append(attempts, classifyProviderError(string(source), "history", historyErr))
			continue
		}
		if len(candles) == 0 {
			attempts = append(attempts, NewProviderError(ErrorSecurityNotFound, string(source), "history", "security not found", ErrNotFound))
			continue
		}
		asOf := candles[len(candles)-1].Time
		adjustment := AdjustmentUnadjusted
		status := StatusProvisional
		if source == SourceTiingo {
			status = StatusOfficial
			if request.Usage != UsagePortfolioValuation && !isIntradayBar(bar) {
				adjustment = AdjustmentSplitDividendAdjusted
			}
		}
		return History{Symbol: symbol, Period: period, Bar: bar, Source: string(source), Status: status,
			AsOf: asOf, Delayed: true, Currency: "USD", AdjustmentType: adjustment,
			Attribution: historyAttribution(source), Candles: candles}, nil
	}
	return History{}, combinedProviderError(attempts)
}

func normalizeRequest(request Request, fallback Usage) Request {
	if request.Usage == "" {
		request.Usage = fallback
	}
	if request.Source == "" {
		request.Source = SourceAuto
	}
	return request
}

func quoteSources(request Request) ([]Source, error) {
	if request.Source != SourceAuto {
		if request.Source == SourceTiingo {
			return nil, fmt.Errorf("%w: Tiingo is historical-only", ErrInvalidRequest)
		}
		if request.Source == SourceBroker && request.ConnectionID <= 0 {
			return nil, fmt.Errorf("%w: connection_id is required for broker market data", ErrInvalidRequest)
		}
		return []Source{request.Source}, nil
	}
	sources := make([]Source, 0, 2)
	if request.ConnectionID > 0 {
		sources = append(sources, SourceBroker)
	}
	return append(sources, SourceYahoo), nil
}

func historyAttribution(source Source) string {
	switch source {
	case SourceTiingo:
		return "Data sourced by Tiingo"
	case SourceYahoo:
		return "Data sourced by Yahoo Finance"
	default:
		return ""
	}
}

func historySources(source Source) ([]Source, error) {
	if source == SourceAuto {
		return []Source{SourceTiingo, SourceYahoo}, nil
	}
	if source == SourceBroker {
		return nil, fmt.Errorf("%w: broker history uses the compatibility route", ErrInvalidRequest)
	}
	return []Source{source}, nil
}

func (s *Service) quoteFrom(ctx context.Context, source Source, symbol string, connectionID int64) (*broker.Quote, error) {
	if source != SourceBroker {
		provider := s.symbolProvider(source)
		if provider == nil {
			return nil, NewProviderError(ErrorNotConfigured, string(source), "quote", "provider is not configured", nil)
		}
		return provider.GetQuote(ctx, symbol)
	}
	if s.broker == nil {
		return nil, NewProviderError(ErrorNotConfigured, "broker", "quote", "broker market data is not configured", nil)
	}
	if quote, err := s.broker.GetQuoteForConnection(ctx, connectionID, symbol); err == nil {
		if !usableQuote(quote) {
			return nil, NewProviderError(ErrorPermission, "broker", "quote", "market data permission denied", nil)
		}
		return quote, nil
	} else if !errors.Is(err, broker.ErrCapabilityUnavailable) {
		return nil, err
	}
	instruments, err := s.broker.SearchInstrumentsForConnection(ctx, connectionID, symbol)
	if err != nil {
		return nil, err
	}
	instrument, ok := preferredInstrument(symbol, instruments)
	if !ok {
		return nil, ErrNotFound
	}
	quotes, err := s.broker.GetQuotesByConIDForConnection(ctx, connectionID, []int64{instrument.ConID})
	if err != nil {
		return nil, err
	}
	if len(quotes) == 0 {
		return nil, ErrNotFound
	}
	quote := quotes[0]
	if !usableQuote(&quote) {
		return nil, NewProviderError(ErrorPermission, "broker", "quote", "market data permission denied", nil)
	}
	if quote.Symbol == "" {
		quote.Symbol = instrument.Symbol
	}
	if quote.Currency == "" {
		quote.Currency = instrument.Currency
	}
	return &quote, nil
}

func usableQuote(quote *broker.Quote) bool {
	return quote != nil && (quote.Last > 0 || quote.Bid > 0 || quote.Ask > 0)
}

func (s *Service) symbolProvider(source Source) SymbolProvider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if source == SourceTiingo {
		return s.tiingo
	}
	if source == SourceYahoo {
		return s.yahoo
	}
	return nil
}

func (s *Service) brokerSource(connectionID int64) string {
	if s.broker != nil {
		if code, ok := s.broker.ProviderCode(connectionID); ok {
			return strings.ToLower(code)
		}
	}
	return "broker"
}

func (s *Service) normalizeBrokerQuote(quote broker.Quote, connectionID int64) *broker.Quote {
	if strings.TrimSpace(quote.Source) == "" || strings.EqualFold(quote.Source, "broker") {
		quote.Source = s.brokerSource(connectionID)
	}
	quote.Status = StatusOfficial
	quote.AdjustmentType = AdjustmentNone
	if quote.AsOf <= 0 {
		quote.AsOf = time.Now().Unix()
	}
	if quote.Currency == "" {
		quote.Currency = "USD"
	}
	return &quote
}

func normalizeQuote(quote broker.Quote, source Source) *broker.Quote {
	quote.Source = string(source)
	quote.AdjustmentType = AdjustmentNone
	if quote.AsOf <= 0 {
		quote.AsOf = time.Now().Unix()
	}
	if quote.Currency == "" {
		quote.Currency = "USD"
	}
	switch source {
	case SourceBroker:
		quote.Status = StatusOfficial
	case SourceTiingo:
		quote.Status = StatusEstimated
		quote.Delayed = true
	case SourceYahoo:
		quote.Status = StatusProvisional
	}
	return &quote
}

func preferredInstrument(symbol string, values []broker.Instrument) (broker.Instrument, bool) {
	for _, value := range values {
		if strings.EqualFold(value.Symbol, symbol) {
			return value, true
		}
	}
	if len(values) == 0 {
		return broker.Instrument{}, false
	}
	return values[0], true
}

func classifyProviderError(provider, op string, err error) error {
	var typed *ProviderError
	if errors.As(err, &typed) {
		return typed
	}
	lower := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, ErrNotFound), strings.Contains(lower, "not found"):
		return NewProviderError(ErrorSecurityNotFound, provider, op, "security not found", err)
	case strings.Contains(lower, "401"), strings.Contains(lower, "unauthenticated"), strings.Contains(lower, "not authenticated"), strings.Contains(lower, "unauthorized"):
		return NewProviderError(ErrorAuthentication, provider, op, "authentication failed", err)
	case strings.Contains(lower, "403"), strings.Contains(lower, "permission"), strings.Contains(lower, "subscription"):
		return NewProviderError(ErrorPermission, provider, op, "market data permission denied", err)
	case strings.Contains(lower, "429"), strings.Contains(lower, "rate limit"):
		return NewProviderError(ErrorRateLimited, provider, op, "rate limited", err)
	default:
		return NewProviderError(ErrorUpstreamUnavailable, provider, op, "upstream unavailable", err)
	}
}

func combinedProviderError(attempts []error) error {
	if len(attempts) == 0 {
		return NewProviderError(ErrorNotConfigured, "", "", "market data is not configured", nil)
	}
	if len(attempts) == 1 {
		return attempts[0]
	}
	allNotConfigured := true
	hasNotFound := false
	hasOperationalFailure := false
	for _, attempt := range attempts {
		code, _ := ErrorDetails(attempt)
		allNotConfigured = allNotConfigured && code == ErrorNotConfigured
		hasNotFound = hasNotFound || code == ErrorSecurityNotFound
		hasOperationalFailure = hasOperationalFailure || (code != ErrorNotConfigured && code != ErrorSecurityNotFound)
	}
	if allNotConfigured {
		return NewProviderError(ErrorNotConfigured, "", "", "market data is not configured", errors.Join(attempts...))
	}
	if hasNotFound && !hasOperationalFailure {
		return NewProviderError(ErrorSecurityNotFound, "", "", "security not found", ErrNotFound)
	}
	return NewProviderError(ErrorUpstreamUnavailable, "", "", "all eligible market data providers are unavailable", errors.Join(attempts...))
}

func isIntradayBar(bar string) bool {
	bar = strings.ToLower(strings.TrimSpace(bar))
	return strings.Contains(bar, "min") || strings.HasSuffix(bar, "h") || strings.Contains(bar, "hour")
}

var _ PolicyProvider = (*Service)(nil)
