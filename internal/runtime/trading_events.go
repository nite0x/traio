package runtime

import (
	"context"
	"time"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/portfolio"
)

type tradingWorker struct {
	session broker.BrokerSession
	cancel  context.CancelFunc
	done    chan struct{}
}

// StartTradingEvents follows enabled sessions, including configuration reloads.
// stop waits for every websocket/poll worker before the store can be closed.
func (b *ConnectionManager) StartTradingEvents(ctx context.Context, syncer *portfolio.SyncService) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		workers := map[int64]tradingWorker{}
		defer func() {
			for _, worker := range workers {
				worker.cancel()
			}
			for _, worker := range workers {
				<-worker.done
			}
		}()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			current := map[int64]broker.BrokerSession{}
			if syncer.SyncEnabled() {
				b.mu.RLock()
				for id, session := range b.sessions {
					if _, ok := session.(broker.TradingEventProvider); ok {
						current[id] = session
					}
				}
				b.mu.RUnlock()
			}
			for id, worker := range workers {
				if current[id] != worker.session {
					worker.cancel()
					<-worker.done
					delete(workers, id)
				}
			}
			for id, session := range current {
				if _, ok := workers[id]; ok {
					continue
				}
				workerCtx, stop := context.WithCancel(ctx)
				worker := tradingWorker{session: session, cancel: stop, done: make(chan struct{})}
				workers[id] = worker
				go func() {
					defer close(worker.done)
					_ = session.(broker.TradingEventProvider).WatchTradingEvents(workerCtx, func(event broker.TradingEvent) {
						if workerCtx.Err() != nil || !event.Refresh || !syncer.SyncEnabled() {
							return
						}
						b.mu.RLock()
						defer b.mu.RUnlock()
						if b.sessions[id] == session {
							syncer.InvalidateAccount(id, event.AccountID)
						}
					})
				}()
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
