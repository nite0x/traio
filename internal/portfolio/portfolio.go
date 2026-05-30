package portfolio

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nite/traio/internal/broker"
)

const (
	DefaultPositionsCacheTTL      = 15 * time.Second
	DefaultPositionsErrorCacheTTL = 15 * time.Second
)

// Service merges positions across Schwab (SnapTrade) and IBKR.
type Service struct {
	snaptrade broker.PortfolioProvider
	ibkr      broker.PortfolioProvider

	positionsCacheTTL      time.Duration
	positionsErrorCacheTTL time.Duration

	positionsMu           sync.Mutex
	positionsCache        []broker.Position
	positionsCacheErr     error
	positionsCacheExpires time.Time
	positionsCacheHasData bool
	positionsRefreshCh    chan struct{}
	positionsGeneration   uint64
}

func New(snaptrade, ibkr broker.PortfolioProvider) *Service {
	return &Service{
		snaptrade:              snaptrade,
		ibkr:                   ibkr,
		positionsCacheTTL:      DefaultPositionsCacheTTL,
		positionsErrorCacheTTL: DefaultPositionsErrorCacheTTL,
	}
}

func (s *Service) AllPositions(ctx context.Context) ([]broker.Position, error) {
	for {
		now := time.Now()

		s.positionsMu.Lock()
		if now.Before(s.positionsCacheExpires) {
			pos, err := clonePositions(s.positionsCache), s.positionsCacheErr
			s.positionsMu.Unlock()
			return pos, err
		}
		if s.positionsRefreshCh != nil {
			ch := s.positionsRefreshCh
			s.positionsMu.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		ch := make(chan struct{})
		s.positionsRefreshCh = ch
		generation := s.positionsGeneration
		s.positionsMu.Unlock()

		pos, err := s.fetchAllPositions(ctx)
		return s.finishPositionsRefresh(pos, err, ch, generation)
	}
}

func (s *Service) InvalidatePositions() {
	s.positionsMu.Lock()
	defer s.positionsMu.Unlock()

	s.positionsCache = nil
	s.positionsCacheErr = nil
	s.positionsCacheExpires = time.Time{}
	s.positionsCacheHasData = false
	s.positionsGeneration++
}

func (s *Service) finishPositionsRefresh(pos []broker.Position, err error, ch chan struct{}, generation uint64) ([]broker.Position, error) {
	s.positionsMu.Lock()
	defer s.positionsMu.Unlock()
	defer close(ch)

	if s.positionsRefreshCh == ch {
		s.positionsRefreshCh = nil
	}
	if s.positionsGeneration != generation {
		return clonePositions(pos), err
	}

	now := time.Now()
	if err != nil {
		s.positionsCacheExpires = now.Add(s.positionsErrorCacheTTL)
		if s.positionsCacheHasData {
			s.positionsCacheErr = nil
			return clonePositions(s.positionsCache), nil
		}
		s.positionsCache = nil
		s.positionsCacheErr = err
		return nil, err
	}

	s.positionsCache = clonePositions(pos)
	s.positionsCacheErr = nil
	s.positionsCacheExpires = now.Add(s.positionsCacheTTL)
	s.positionsCacheHasData = true
	return clonePositions(pos), nil
}

func (s *Service) fetchAllPositions(ctx context.Context) ([]broker.Position, error) {
	var out []broker.Position
	var errs []string
	succeeded := false

	if s.snaptrade != nil {
		pos, err := s.snaptrade.ListPositions(ctx)
		if err != nil {
			errs = append(errs, "snaptrade: "+err.Error())
		} else {
			succeeded = true
			out = append(out, pos...)
		}
	}
	if s.ibkr != nil {
		pos, err := s.ibkr.ListPositions(ctx)
		if err != nil {
			errs = append(errs, "ibkr: "+err.Error())
		} else {
			succeeded = true
			out = append(out, pos...)
		}
	}

	// Return data if at least one broker succeeded.
	if succeeded {
		return out, nil
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return out, nil
}

func clonePositions(in []broker.Position) []broker.Position {
	if in == nil {
		return nil
	}
	out := make([]broker.Position, len(in))
	copy(out, in)
	return out
}
