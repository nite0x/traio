package history

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nite/traio/internal/store"
)

const (
	gatewayCadence = 5 * time.Minute
	flexCadence    = 24 * time.Hour
)

func (s *Service) schedulerLoop(ctx context.Context) {
	s.schedule(ctx)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.schedule(ctx)
		}
	}
}

func (s *Service) schedule(ctx context.Context) {
	s.mu.Lock()
	enabled := s.autoSyncEnabled
	s.mu.Unlock()
	if !enabled {
		return
	}
	connections, err := s.repo.ListBrokerConnections(ctx)
	if err != nil {
		return
	}
	coverage, err := s.repo.ListHistoryCoverage(ctx)
	if err != nil {
		return
	}
	now := s.now().UTC()
	for _, connection := range connections {
		if !connection.Enabled || !strings.EqualFold(connection.ProviderCode, "IBKR") || !configBool(connection.Config, "activity_history_enabled") {
			continue
		}
		runtimeConnection, err := s.repo.GetBrokerConnectionRuntimeConfig(ctx, connection.ID)
		if err != nil {
			continue
		}
		accounts, err := s.repo.ListBrokerAccountsByConnection(ctx, connection.ID)
		if err != nil {
			continue
		}
		ids := make([]int64, 0, len(accounts))
		for _, account := range accounts {
			ids = append(ids, account.ID)
		}
		from, to := now.AddDate(0, 0, -7).Format("2006-01-02"), now.Format("2006-01-02")
		if len(ids) > 0 && validateConnectionSource(runtimeConnection, SourceGateway) == nil && s.due(connection.ID, SourceGateway, gatewayCadence, now) {
			if _, err := s.repo.CreateHistoryJob(ctx, store.HistoryRequest{AccountIDs: ids, ConnectionID: connection.ID, Source: SourceGateway, From: from, To: to}); err == nil {
				s.signalWorker()
			}
		}
		if validateConnectionSource(runtimeConnection, SourceFlex) != nil {
			continue
		}
		if s.due(connection.ID, "flex-daily", flexCadence, now) {
			s.enqueueScheduledWindow(ctx, connection.ID, ids, from, to, coverage, true)
		}
		if s.due(connection.ID, "flex-weekly", 7*flexCadence, now) {
			s.enqueueScheduledWindow(ctx, connection.ID, ids, now.AddDate(0, 0, -90).Format("2006-01-02"), now.AddDate(0, 0, -8).Format("2006-01-02"), coverage, true)
		}
		if s.due(connection.ID, "flex-monthly", 30*flexCadence, now) {
			start := configString(connection.Config, "activity_history_from")
			end := now.AddDate(0, 0, -91).Format("2006-01-02")
			for _, window := range historyWindows(start, end) {
				s.enqueueScheduledWindow(ctx, connection.ID, ids, window[0], window[1], coverage, true)
			}
		}
	}
}

func (s *Service) enqueueScheduledWindow(ctx context.Context, connectionID int64, accountIDs []int64, from, to string, coverage []store.HistoryCoverage, skipComplete bool) {
	if from == "" || to == "" || from > to {
		return
	}
	// Successful coverage is revisited on its audit cadence so later broker
	// corrections remain discoverable. Coverage is not a permanent skip marker.
	cadence := flexCadence
	now := s.now().UTC()
	if to < now.AddDate(0, 0, -90).Format("2006-01-02") {
		cadence = 30 * flexCadence
	} else if to < now.AddDate(0, 0, -7).Format("2006-01-02") {
		cadence = 7 * flexCadence
	}
	fresh := make([]store.HistoryCoverage, 0, len(coverage))
	for _, item := range coverage {
		checked, e := time.Parse(time.RFC3339Nano, item.LastCheckedAt)
		if e == nil && checked.After(now.Add(-cadence)) {
			fresh = append(fresh, item)
		}
	}
	if skipComplete && completelyCovered(accountIDs, from, to, fresh) {
		return
	}
	if _, err := s.repo.CreateHistoryJob(ctx, store.HistoryRequest{AccountIDs: accountIDs, ConnectionID: connectionID, Source: SourceFlex, From: from, To: to}); err == nil {
		s.signalWorker()
	}
}

func completelyCovered(accountIDs []int64, from, to string, coverage []store.HistoryCoverage) bool {
	for _, accountID := range accountIDs {
		intervals := [][2]time.Time{}
		for _, item := range coverage {
			if item.AccountID != accountID || item.Source != SourceFlex || item.DataType != "activities" || item.FetchStatus != "complete" || item.ScopeKey != "account" {
				continue
			}
			start, startErr := time.Parse("2006-01-02", item.From)
			end, endErr := time.Parse("2006-01-02", item.To)
			if startErr == nil && endErr == nil && !start.After(end) {
				intervals = append(intervals, [2]time.Time{start, end})
			}
		}
		if !intervalsCover(intervals, from, to) {
			return false
		}
	}
	return len(accountIDs) > 0
}

func intervalsCover(intervals [][2]time.Time, from, to string) bool {
	wantStart, startErr := time.Parse("2006-01-02", from)
	wantEnd, endErr := time.Parse("2006-01-02", to)
	if startErr != nil || endErr != nil || wantStart.After(wantEnd) {
		return false
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i][0].Before(intervals[j][0]) })
	coveredUntil := wantStart.AddDate(0, 0, -1)
	for _, interval := range intervals {
		if interval[1].Before(wantStart) || interval[0].After(wantEnd) {
			continue
		}
		if interval[0].After(coveredUntil.AddDate(0, 0, 1)) {
			return false
		}
		if interval[1].After(coveredUntil) {
			coveredUntil = interval[1]
		}
		if !coveredUntil.Before(wantEnd) {
			return true
		}
	}
	return false
}

func (s *Service) due(connectionID int64, source string, cadence time.Duration, now time.Time) bool {
	key := source + ":" + strconv.FormatInt(connectionID, 10)
	s.mu.Lock()
	defer s.mu.Unlock()
	if last := s.lastRun[key]; !last.IsZero() && now.Sub(last) < cadence {
		return false
	}
	s.lastRun[key] = now
	return true
}

func historyWindows(from, to string) [][2]string {
	start, err1 := time.Parse("2006-01-02", from)
	end, err2 := time.Parse("2006-01-02", to)
	if err1 != nil || err2 != nil || start.After(end) {
		return nil
	}
	out := [][2]string{}
	for !start.After(end) {
		windowEnd := start.AddDate(0, 0, 364)
		if windowEnd.After(end) {
			windowEnd = end
		}
		out = append(out, [2]string{start.Format("2006-01-02"), windowEnd.Format("2006-01-02")})
		start = windowEnd.AddDate(0, 0, 1)
	}
	return out
}
