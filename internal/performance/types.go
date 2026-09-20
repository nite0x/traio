// Package performance rebuilds account state from the normalized activity
// ledger and calculates server-authoritative daily valuation and TWR points.
package performance

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nite/traio/internal/activity"
)

const CalculationVersion = "daily-performance-v1"

const (
	RetentionTransient           = "transient"
	RetentionLicensedPersistence = "licensed_persistence"
)

var (
	ErrAccountNotFound = errors.New("performance_account_not_found")
	ErrInvalidRequest  = errors.New("invalid_performance_request")
)

type RetentionMode string

func ParseRetentionMode(value string) (RetentionMode, error) {
	switch RetentionMode(strings.ToLower(strings.TrimSpace(value))) {
	case "", RetentionTransient:
		return RetentionTransient, nil
	case RetentionLicensedPersistence:
		return RetentionLicensedPersistence, nil
	default:
		return "", errors.New("unsupported performance retention mode")
	}
}

type Query struct {
	AccountID    int64
	From         string
	To           string
	BaseCurrency string
}

type Instrument struct {
	ID           int64  `json:"instrument_id"`
	ConnectionID int64  `json:"-"`
	Symbol       string `json:"symbol"`
	Currency     string `json:"currency"`
	ExternalID   string `json:"external_id,omitempty"`
}

type CoverageWindow struct {
	Source          string `json:"source"`
	From            string `json:"from"`
	To              string `json:"to"`
	FetchStatus     string `json:"fetch_status"`
	NormalizeStatus string `json:"normalize_status"`
	ReconcileStatus string `json:"reconcile_status"`
}

type Input struct {
	AccountID          int64
	ConnectionID       int64
	Provider           string
	BaseCurrency       string
	ActivityGeneration int64
	Activities         []activity.Activity
	Instruments        map[int64]Instrument
	Coverage           []CoverageWindow
}

type Price struct {
	Date    string
	Value   string
	Source  string
	Missing bool
}

type FXRate struct {
	Date   string
	From   string
	To     string
	Value  string
	Source string
}

type FXPair struct {
	From string
	To   string
}

// ValuationProvider deliberately exposes normalized daily values rather than
// provider OHLC payloads. Implementations may use CandleProvider, licensed
// feeds, or deterministic test fixtures.
type ValuationProvider interface {
	DailyPrices(context.Context, []Instrument, time.Time, time.Time) (map[int64][]Price, error)
	DailyFX(context.Context, []FXPair, time.Time, time.Time) (map[FXPair][]FXRate, error)
}

type Coverage struct {
	Status           string           `json:"status"`
	RequestedFrom    string           `json:"requested_from"`
	RequestedTo      string           `json:"requested_to"`
	ActivityFrom     string           `json:"activity_from,omitempty"`
	ActivityTo       string           `json:"activity_to,omitempty"`
	ActivityWindows  []CoverageWindow `json:"activity_windows"`
	RecalculatedFrom string           `json:"recalculated_from"`
}

type Point struct {
	Date               string   `json:"date"`
	TotalValue         *string  `json:"total_value"`
	CashValue          *string  `json:"cash_value"`
	HoldingsValue      *string  `json:"holdings_value"`
	ExternalCashFlow   *string  `json:"external_cash_flow"`
	DailyPnL           *string  `json:"daily_pnl"`
	DailyReturn        *string  `json:"daily_return"`
	CumulativeTWR      *string  `json:"cumulative_twr"`
	Quality            string   `json:"quality"`
	Finality           string   `json:"finality"`
	MissingPrices      []string `json:"missing_prices"`
	MissingFX          []string `json:"missing_fx"`
	MissingCostBasis   []string `json:"missing_cost_basis"`
	SourceMix          []string `json:"source_mix"`
	CalculationVersion string   `json:"calculation_version"`
}

type Series struct {
	AccountID          int64         `json:"account_id"`
	BaseCurrency       string        `json:"base_currency"`
	RetentionMode      RetentionMode `json:"retention_mode"`
	AsOf               string        `json:"as_of"`
	Coverage           Coverage      `json:"coverage"`
	Points             []Point       `json:"points"`
	CalculationVersion string        `json:"calculation_version"`
	ActivityGeneration int64         `json:"activity_generation"`
}

type Anchor struct {
	Date          string
	TotalValue    *string
	CumulativeTWR *string
}

type Repository interface {
	LoadPerformanceInput(context.Context, int64, string) (Input, error)
	PerformanceDirtyFrom(context.Context, int64) (string, error)
	LoadPerformanceAnchor(context.Context, int64, string, string) (Anchor, error)
	ReplacePerformanceSeries(context.Context, Series, string, bool) error
	InvalidatePerformanceSource(context.Context, string) error
}
