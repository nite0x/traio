package account

import (
	"context"

	"github.com/nite/traio/internal/broker"
)

// Source identifies one broker adapter that can supply account equity data.
type Source struct {
	Name     string
	Provider broker.AccountProvider
}

// Service loads account equity directly from broker APIs until a SQLite projection exists.
type Service struct {
	sources []Source
}

func New(sources ...Source) *Service {
	return &Service{sources: sources}
}

// Timeline returns historical equity and the best available realtime summary.
// Sources are tried in registration order; historical points prefer the longest series.
func (s *Service) Timeline(ctx context.Context) ([]broker.AccountEquityPoint, broker.AccountSummary, error) {
	if len(s.sources) == 0 {
		return []broker.AccountEquityPoint{}, broker.AccountSummary{}, nil
	}

	var points []broker.AccountEquityPoint
	var historicalErr error
	var summary broker.AccountSummary
	var summaryErr error

	for _, source := range s.sources {
		if source.Provider == nil {
			continue
		}
		got, err := source.Provider.HistoricalEquity(ctx)
		if err != nil {
			if historicalErr == nil {
				historicalErr = err
			}
			continue
		}
		if len(got) > len(points) {
			points = got
			historicalErr = nil
		}
	}

	for _, source := range s.sources {
		if source.Provider == nil {
			continue
		}
		got, err := source.Provider.AccountSummary(ctx)
		if err != nil {
			if summaryErr == nil {
				summaryErr = err
			}
			continue
		}
		if got.NetLiquidation != 0 {
			summary = got
			summaryErr = nil
			break
		}
	}

	if summaryErr == nil && summary.NetLiquidation != 0 {
		points = appendOrReplaceToday(points, broker.AccountEquityPoint{
			Time:     summary.AsOf,
			Value:    summary.NetLiquidation,
			Currency: summary.Currency,
			Source:   summary.Broker + " realtime",
		})
	}

	if historicalErr != nil && summaryErr != nil {
		return nil, broker.AccountSummary{}, historicalErr
	}
	if historicalErr != nil {
		return points, summary, historicalErr
	}
	if summaryErr != nil {
		return points, summary, summaryErr
	}
	return points, summary, nil
}

func appendOrReplaceToday(points []broker.AccountEquityPoint, realtime broker.AccountEquityPoint) []broker.AccountEquityPoint {
	if realtime.Time == "" {
		return points
	}
	realtimeDay := realtime.Time
	if len(realtimeDay) > 10 {
		realtimeDay = realtimeDay[:10]
	}
	for i, point := range points {
		if len(point.Time) >= 10 && point.Time[:10] == realtimeDay {
			points[i] = realtime
			return points
		}
	}
	return append(points, realtime)
}
