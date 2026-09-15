package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

func TestGatewayStreamRefreshesStoredPortfolioAndFollowsLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var quantity, invalidations, streams, active atomic.Int64
	quantity.Store(100)
	push := make(chan string, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-proxy" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/v1/api/tickle":
			fmt.Fprint(w, `{"session":"test-session","authenticated":true}`)
		case "/v1/api/iserver/accounts":
			fmt.Fprint(w, `{"accounts":["U1"]}`)
		case "/v1/api/iserver/account/orders":
			fmt.Fprint(w, `{"orders":[],"snapshot":true}`)
		case "/v1/api/iserver/account/trades":
			fmt.Fprint(w, `[]`)
		case "/v1/api/portfolio/accounts":
			fmt.Fprint(w, `[{"id":"U1","currency":"USD"}]`)
		case "/v1/api/portfolio/U1/meta":
			fmt.Fprint(w, `{"id":"U1","currency":"USD"}`)
		case "/v1/api/portfolio/U1/ledger":
			fmt.Fprint(w, `{"USD":{"currency":"USD","cashbalance":500,"settledcash":500,"exchangerate":1}}`)
		case "/v1/api/portfolio/U1/positions/invalidate":
			invalidations.Add(1)
			fmt.Fprint(w, `[]`)
		case "/v1/api/portfolio/U1/positions/0":
			fmt.Fprintf(w, `[{"acctId":"U1","conid":265598,"ticker":"AAPL","position":%d,"currency":"USD","mktPrice":10,"mktValue":%d}]`, quantity.Load(), quantity.Load()*10)
		case "/v1/api/portfolio/U1/summary":
			fmt.Fprint(w, `{"NetLiquidation":1500,"GrossPositionValue":1000}`)
		case "/v1/api/iserver/account/pnl/partitioned":
			fmt.Fprint(w, `{"upnl":{"U1.Core":{"dpl":10,"nl":1500,"mv":1000}}}`)
		case "/v1/api/ws":
			upgrader := websocket.Upgrader{}
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer ws.Close()
			active.Add(1)
			defer active.Add(-1)
			for i := 0; i < 2; i++ {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
			}
			streams.Add(1)
			closed := make(chan struct{})
			go func() {
				defer close(closed)
				for {
					if _, _, err := ws.ReadMessage(); err != nil {
						return
					}
				}
			}()
			for {
				select {
				case <-ctx.Done():
					return
				case <-closed:
					return
				case message := <-push:
					if ws.WriteMessage(websocket.TextMessage, []byte(message)) != nil {
						return
					}
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base := t.TempDir()
	st, err := store.Open(filepath.Join(base, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	connection, err := st.UpsertBrokerConnection(ctx, store.BrokerConnection{ProviderCode: "IBKR", ConnectionKey: "stream", Name: "Stream", Enabled: true, AuthType: "gateway", Config: map[string]any{"gateway_id": "one", "gateway_url": server.URL}, Secrets: map[string]string{"gateway_token": "test-proxy"}})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := BuildConnectionManager(config.Default(base), st, base)
	if err != nil {
		t.Fatal(err)
	}
	syncer := BuildBrokerSync(st, manager)
	if err := syncer.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	syncer.StartBackground(ctx, time.Hour)
	stop := manager.StartTradingEvents(ctx, syncer)
	defer stop()
	wait := func(label string, predicate func() bool) {
		t.Helper()
		deadline := time.NewTimer(4 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if predicate() {
				return
			}
			select {
			case <-deadline.C:
				t.Fatal(label)
			case <-ticker.C:
			}
		}
	}
	wait("initial stream reconciliation missing", func() bool { return invalidations.Load() > 0 && streams.Load() == 1 })
	push <- `{"topic":"sor","args":[{"acct":"U1","orderId":7,"ticker":"AAPL","status":"Submitted","filledQuantity":0,"totalSize":50}]}`
	wait("new order not visible through existing API service", func() bool {
		orders, err := manager.Trading.ListOrders(ctx, connection.ID, broker.OrderQuery{AccountID: "U1"})
		return err == nil && len(orders) == 1 && orders[0].Quantity == 50 && orders[0].FilledQuantity == 0
	})
	positions, _ := st.ListBrokerPositions(ctx)
	if len(positions) != 1 || positions[0].Quantity != 100 {
		t.Fatal("unfilled order changed holdings")
	}
	quantity.Store(120)
	push <- `{"topic":"str","args":[{"account":"U1","execution_id":"E1","size":20,"price":"10","side":"B","conid":265598}]}`
	wait("fill did not refresh persisted holdings", func() bool {
		rows, e := st.ListBrokerPositions(ctx)
		return e == nil && len(rows) == 1 && rows[0].Quantity == 120
	})
	// Disabling the synchronization switch stops the existing worker; enabling
	// it must restart the same Session rather than being blocked by its old run.
	syncer.SetSyncConfig(config.BrokerSyncConfig{Enabled: false})
	wait("disabled sync left stream active", func() bool { return active.Load() == 0 })
	syncer.SetSyncConfig(config.BrokerSyncConfig{Enabled: true})
	wait("reenabling sync did not restart stream", func() bool { return streams.Load() == 2 })
	if active.Load() != 1 {
		t.Fatal("duplicate stream workers")
	}
	if err := st.SetBrokerConnectionEnabled(ctx, connection.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	wait("disabled connection left stream active", func() bool { return active.Load() == 0 })
	stop()
	cancel()
	statuses, err := st.ListBrokerSyncStatuses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range statuses {
		if strings.Contains(status.LastError, "test-proxy") || strings.Contains(status.LastError, "test-session") {
			t.Fatal("secret in sync diagnostics")
		}
	}
}
