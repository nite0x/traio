package performance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nite/traio/internal/activity"
)

type Service struct {
	repo      Repository
	valuation ValuationProvider
	mode      RetentionMode
	now       func() time.Time
}

func New(repo Repository, valuation ValuationProvider, mode RetentionMode) *Service {
	if mode == "" {
		mode = RetentionTransient
	}
	return &Service{repo: repo, valuation: valuation, mode: mode, now: time.Now}
}

func (s *Service) Calculate(ctx context.Context, query Query) (Series, error) {
	return s.calculate(ctx, query, false)
}

func (s *Service) Recalculate(ctx context.Context, query Query) (Series, error) {
	return s.calculate(ctx, query, true)
}

// RefreshAccounts is called by the history worker after normalized activities
// and coverage are committed. Transient mode already records the durable dirty
// boundary but intentionally performs no provider fetch or derived persistence.
func (s *Service) RefreshAccounts(ctx context.Context, accountIDs []int64, from string) error {
	if s == nil || s.mode != RetentionLicensedPersistence {
		return nil
	}
	var refreshErrors []error
	seen := map[int64]bool{}
	for _, accountID := range accountIDs {
		if accountID <= 0 || seen[accountID] {
			continue
		}
		seen[accountID] = true
		if _, err := s.Recalculate(ctx, Query{AccountID: accountID, From: from}); err != nil {
			refreshErrors = append(refreshErrors, fmt.Errorf("account %d: %w", accountID, err))
		}
	}
	return errors.Join(refreshErrors...)
}

func (s *Service) calculate(ctx context.Context, query Query, useDirtyBoundary bool) (Series, error) {
	if s == nil || s.repo == nil || s.valuation == nil || query.AccountID <= 0 {
		return Series{}, ErrInvalidRequest
	}
	now := s.now().UTC()
	to, err := parseDateDefault(query.To, startOfDay(now))
	if err != nil || to.After(startOfDay(now)) {
		return Series{}, ErrInvalidRequest
	}
	input, err := s.repo.LoadPerformanceInput(ctx, query.AccountID, to.Format(time.DateOnly))
	if err != nil {
		return Series{}, err
	}
	base := strings.ToUpper(strings.TrimSpace(query.BaseCurrency))
	if base == "" {
		base = strings.ToUpper(strings.TrimSpace(input.BaseCurrency))
	}
	if len(base) != 3 {
		return Series{}, ErrInvalidRequest
	}

	activityFrom, activityTo := inputActivityRange(input.Activities)
	defaultFrom := to
	if activityFrom != "" {
		defaultFrom, _ = time.Parse(time.DateOnly, activityFrom)
	}
	from, err := parseDateDefault(query.From, defaultFrom)
	if err != nil || from.After(to) {
		return Series{}, ErrInvalidRequest
	}
	requestedFrom := from
	dirtyFrom := ""
	if s.mode == RetentionLicensedPersistence {
		dirtyFrom, err = s.repo.PerformanceDirtyFrom(ctx, query.AccountID)
		if err != nil {
			return Series{}, err
		}
		if dirtyFrom != "" {
			dirty, parseErr := time.Parse(time.DateOnly, dirtyFrom)
			if parseErr != nil {
				return Series{}, parseErr
			}
			if useDirtyBoundary || dirty.Before(from) {
				from = dirty
			}
		}
	}
	anchor := Anchor{}
	if s.mode == RetentionLicensedPersistence {
		anchor, err = s.repo.LoadPerformanceAnchor(ctx, query.AccountID, from.Format(time.DateOnly), CalculationVersion)
		if err != nil {
			return Series{}, err
		}
	}

	instruments := usedInstruments(input)
	pairs := usedFXPairs(input, base)
	marketFrom := from.AddDate(0, 0, -10)
	priceRows, err := s.valuation.DailyPrices(ctx, instruments, marketFrom, to)
	if err != nil {
		return Series{}, err
	}
	fxRows, err := s.valuation.DailyFX(ctx, pairs, marketFrom, to)
	if err != nil {
		return Series{}, err
	}
	prices, err := indexPrices(priceRows)
	if err != nil {
		return Series{}, err
	}
	fx, err := indexFX(fxRows)
	if err != nil {
		return Series{}, err
	}
	coverage := Coverage{
		Status:        summarizeCoverage(input.Coverage, requestedFrom.Format(time.DateOnly), to.Format(time.DateOnly)),
		RequestedFrom: requestedFrom.Format(time.DateOnly), RequestedTo: to.Format(time.DateOnly),
		ActivityFrom: activityFrom, ActivityTo: activityTo, ActivityWindows: input.Coverage,
		RecalculatedFrom: from.Format(time.DateOnly),
	}
	series, err := calculate(calculator{input: input, base: base, from: from, to: to, now: now, prices: prices, fx: fx, anchor: anchor, coverage: coverage})
	if err != nil {
		return Series{}, err
	}
	series.RetentionMode = s.mode
	if s.mode == RetentionLicensedPersistence {
		clearDirty := !to.Before(startOfDay(now))
		if err := s.repo.ReplacePerformanceSeries(ctx, series, from.Format(time.DateOnly), clearDirty); err != nil {
			return Series{}, err
		}
	}
	if requestedFrom.After(from) {
		filtered := series.Points[:0]
		for _, point := range series.Points {
			if point.Date >= requestedFrom.Format(time.DateOnly) {
				filtered = append(filtered, point)
			}
		}
		series.Points = filtered
	}
	return series, nil
}

func summarizeCoverage(windows []CoverageWindow, from, to string) string {
	if len(windows) == 0 {
		return "unknown"
	}
	cursor := from
	reconciled := true
	for cursor <= to {
		coveredTo := ""
		for _, window := range windows {
			if window.From <= cursor && cursor <= window.To && window.FetchStatus == "complete" && window.NormalizeStatus == "complete" {
				if window.To > coveredTo {
					coveredTo = window.To
				}
				if window.ReconcileStatus != "passed" {
					reconciled = false
				}
			}
		}
		if coveredTo == "" {
			return "partial"
		}
		if coveredTo >= to {
			if reconciled {
				return "complete"
			}
			return "normalized"
		}
		date, err := time.Parse(time.DateOnly, coveredTo)
		if err != nil {
			return "partial"
		}
		cursor = date.AddDate(0, 0, 1).Format(time.DateOnly)
	}
	return "partial"
}

func parseDateDefault(value string, fallback time.Time) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	return time.Parse(time.DateOnly, strings.TrimSpace(value))
}

func inputActivityRange(activities []activity.Activity) (from, to string) {
	for _, item := range activities {
		dates := []string{}
		for _, leg := range item.Legs {
			if date := legDate(item, leg); date != "" {
				dates = append(dates, date)
			}
		}
		if len(dates) == 0 {
			dates = append(dates, activityDate(item))
		}
		for _, date := range dates {
			if date == "" {
				continue
			}
			if from == "" || date < from {
				from = date
			}
			if date > to {
				to = date
			}
		}
	}
	return
}

func usedInstruments(input Input) []Instrument {
	ids := map[int64]struct{}{}
	for _, item := range input.Activities {
		for _, leg := range item.Legs {
			if leg.Kind == "position" && leg.InstrumentID > 0 {
				ids[leg.InstrumentID] = struct{}{}
			}
		}
	}
	out := make([]Instrument, 0, len(ids))
	for id := range ids {
		out = append(out, input.Instruments[id])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func usedFXPairs(input Input, base string) []FXPair {
	pairs := map[FXPair]struct{}{}
	add := func(currency string) {
		currency = strings.ToUpper(currency)
		if currency != "" && currency != base {
			pairs[FXPair{From: currency, To: base}] = struct{}{}
		}
	}
	for _, instrument := range input.Instruments {
		add(instrument.Currency)
	}
	for _, item := range input.Activities {
		for _, leg := range item.Legs {
			if leg.Kind == "cash" {
				add(leg.Currency)
			}
		}
	}
	out := make([]FXPair, 0, len(pairs))
	for pair := range pairs {
		out = append(out, pair)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From == out[j].From {
			return out[i].To < out[j].To
		}
		return out[i].From < out[j].From
	})
	return out
}

func indexPrices(rows map[int64][]Price) (map[int64]map[string]Price, error) {
	out := map[int64]map[string]Price{}
	for id, values := range rows {
		out[id] = map[string]Price{}
		for _, value := range values {
			if _, err := time.Parse(time.DateOnly, value.Date); err != nil {
				return nil, err
			}
			if !value.Missing {
				if _, err := parseRat(value.Value); err != nil {
					return nil, err
				}
			}
			out[id][value.Date] = value
		}
	}
	return out, nil
}

func indexFX(rows map[FXPair][]FXRate) (map[FXPair]map[string]FXRate, error) {
	out := map[FXPair]map[string]FXRate{}
	for pair, values := range rows {
		pair = FXPair{From: strings.ToUpper(pair.From), To: strings.ToUpper(pair.To)}
		out[pair] = map[string]FXRate{}
		for _, value := range values {
			if _, err := time.Parse(time.DateOnly, value.Date); err != nil {
				return nil, err
			}
			if _, err := parseRat(value.Value); err != nil {
				return nil, err
			}
			out[pair][value.Date] = value
		}
	}
	return out, nil
}
