package market

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nite/traio/internal/broker"
)

var (
	ErrInvalidRequest = errors.New("invalid market data request")
	ErrNotFound       = errors.New("market data not found")
)

type Usage string

const (
	UsageLiveQuote          Usage = "live_quote"
	UsageHistoricalChart    Usage = "historical_chart"
	UsagePortfolioValuation Usage = "portfolio_valuation"
	UsageReconciliation     Usage = "reconciliation"
)

func ParseUsage(value string, fallback Usage) (Usage, error) {
	usage := Usage(strings.ToLower(strings.TrimSpace(value)))
	if usage == "" {
		usage = fallback
	}
	switch usage {
	case UsageLiveQuote, UsageHistoricalChart, UsagePortfolioValuation, UsageReconciliation:
		return usage, nil
	default:
		return "", fmt.Errorf("%w: unsupported market data usage %q", ErrInvalidRequest, value)
	}
}

type Source string

const (
	SourceAuto   Source = "auto"
	SourceBroker Source = "broker"
	SourceTiingo Source = "tiingo"
	SourceYahoo  Source = "yahoo"
)

func ParseSource(value string) (Source, error) {
	source := Source(strings.ToLower(strings.TrimSpace(value)))
	if source == "" {
		return SourceAuto, nil
	}
	switch source {
	case SourceAuto, SourceBroker, SourceTiingo, SourceYahoo:
		return source, nil
	default:
		return "", fmt.Errorf("%w: source must be auto, broker, tiingo, or yahoo", ErrInvalidRequest)
	}
}

const (
	StatusOfficial    = "official"
	StatusProvisional = "provisional"
	StatusEstimated   = "estimated"

	AdjustmentNone                  = "none"
	AdjustmentUnadjusted            = "unadjusted"
	AdjustmentSplitDividendAdjusted = "split_dividend_adjusted"
)

type ErrorCode string

const (
	ErrorNotConfigured       ErrorCode = "not_configured"
	ErrorAuthentication      ErrorCode = "authentication_failed"
	ErrorPermission          ErrorCode = "market_data_permission_denied"
	ErrorRateLimited         ErrorCode = "rate_limited"
	ErrorUpstreamUnavailable ErrorCode = "upstream_unavailable"
	ErrorSecurityNotFound    ErrorCode = "security_not_found"
	ErrorInvalidRequest      ErrorCode = "invalid_request"
)

type ProviderError struct {
	Code     ErrorCode
	Provider string
	Op       string
	Message  string
	Cause    error
}

func (e *ProviderError) Error() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = string(e.Code)
	}
	if e.Provider == "" {
		return message
	}
	return e.Provider + ": " + message
}

func (e *ProviderError) Unwrap() error { return e.Cause }

func NewProviderError(code ErrorCode, provider, op, message string, cause error) error {
	return &ProviderError{Code: code, Provider: provider, Op: op, Message: message, Cause: cause}
}

func ErrorDetails(err error) (ErrorCode, string) {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Code, providerErr.Provider
	}
	if errors.Is(err, ErrInvalidRequest) {
		return ErrorInvalidRequest, ""
	}
	if errors.Is(err, ErrNotFound) {
		return ErrorSecurityNotFound, ""
	}
	return ErrorUpstreamUnavailable, ""
}

type Request struct {
	Usage        Usage
	Source       Source
	ConnectionID int64
}

type History struct {
	Symbol         string          `json:"symbol"`
	Period         string          `json:"period"`
	Bar            string          `json:"bar"`
	Source         string          `json:"source"`
	Status         string          `json:"status"`
	AsOf           int64           `json:"as_of"`
	Delayed        bool            `json:"delayed"`
	Currency       string          `json:"currency"`
	AdjustmentType string          `json:"adjustment_type"`
	Attribution    string          `json:"attribution"`
	Candles        []broker.Candle `json:"candles"`
}

// SymbolProvider provides public market data without a brokerage account or conid.
type SymbolProvider interface {
	GetQuote(context.Context, string) (*broker.Quote, error)
	GetQuotes(context.Context, []string) ([]broker.Quote, error)
	GetHistory(ctx context.Context, symbol, period, bar string, refresh bool) ([]broker.Candle, error)
}

// ValuationHistoryProvider supplies unadjusted historical prices for replaying
// actual position quantities. Adjusted OHLC would double count split/dividend
// activities already present in the ledger.
type ValuationHistoryProvider interface {
	GetValuationHistory(context.Context, string, string, string, bool) ([]broker.Candle, error)
}

// PolicyProvider routes a request according to its declared use and, when
// present, the exact broker connection that owns the account/holding.
type PolicyProvider interface {
	SymbolProvider
	GetQuoteFor(context.Context, string, Request) (*broker.Quote, error)
	GetQuotesFor(context.Context, []string, Request) ([]broker.Quote, error)
	GetHistoryFor(context.Context, string, string, string, bool, Request) (History, error)
}
