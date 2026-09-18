package portfolio

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type accountRefreshKey struct {
	connectionID int64
	accountID    string
}
type accountRefresh struct {
	due     time.Time
	attempt int
}

func (s *SyncService) SyncEnabled() bool {
	cfg := s.syncConfig()
	return cfg.Enabled || cfg.AutomaticEnabled("IBKR")
}

// InvalidateAccount coalesces fills without delaying the first pending refresh.
// Later passes account for IBKR's independently updated positions/cash feeds.
func (s *SyncService) InvalidateAccount(connectionID int64, accountID string) {
	if connectionID <= 0 {
		return
	}
	key := accountRefreshKey{connectionID, strings.TrimSpace(accountID)}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.pending == nil {
		s.pending = map[accountRefreshKey]accountRefresh{}
	}
	if old, ok := s.pending[key]; ok {
		old.attempt = 0
		old.due = minTime(old.due, time.Now().Add(750*time.Millisecond))
		s.pending[key] = old
	} else {
		s.pending[key] = accountRefresh{due: time.Now().Add(750 * time.Millisecond)}
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// SyncAccount discovers the full account list but resolves resources only for
// the affected account. Empty accountID requests a connection-wide reconciliation.
func (s *SyncService) SyncAccount(ctx context.Context, connectionID int64, accountID string) error {
	for _, source := range s.brokerSources() {
		if source.ConnectionID == connectionID {
			return s.syncSources(ctx, []Source{source}, strings.TrimSpace(accountID))
		}
	}
	return fmt.Errorf("broker connection %d does not support account synchronization", connectionID)
}

func (s *SyncService) refreshPendingAccounts(ctx context.Context) {
	s.pendingMu.Lock()
	if !s.SyncEnabled() {
		clear(s.pending)
		s.pendingMu.Unlock()
		return
	}
	ready := map[accountRefreshKey]accountRefresh{}
	for key, pending := range s.pending {
		if !pending.due.After(time.Now()) {
			ready[key] = pending
			delete(s.pending, key)
		}
	}
	s.pendingMu.Unlock()
	for key, pending := range ready {
		if ctx.Err() != nil {
			return
		}
		requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = s.SyncAccount(requestCtx, key.connectionID, key.accountID)
		cancel()
		if pending.attempt >= 2 {
			continue
		} // ordinary periodic sync is the final fallback
		delay := 5 * time.Second
		if pending.attempt == 1 {
			delay = 15 * time.Second
		}
		pending.attempt++
		pending.due = time.Now().Add(delay)
		s.pendingMu.Lock()
		if _, exists := s.pending[key]; !exists {
			s.pending[key] = pending
		}
		s.pendingMu.Unlock()
	}
}
