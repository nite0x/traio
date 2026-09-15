package ibkr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

func TestTradingMessagesOrdersFillsAndDeduplication(t *testing.T) {
	c := New(config.IBKRConfig{})
	var events []broker.TradingEvent
	emit := func(e broker.TradingEvent) { events = append(events, e) }
	consume := func(body string) {
		t.Helper()
		if err := c.consumeTradingMessage([]byte(body), emit); err != nil {
			t.Fatal(err)
		}
	}
	consume(`{"topic":"sor","args":[{"acct":"U1","orderId":9007199254740993,"ticker":"AAPL","conid":265598,"status":"Submitted","totalSize":50,"filledQuantity":0,"timeInForce":"DAY"}]}`)
	if len(events) != 1 || events[0].Refresh || events[0].Order.ID != "9007199254740993" {
		t.Fatalf("new order: %+v", events)
	}
	consume(`{"topic":"sor","args":[{"acct":"U1","orderId":9007199254740993,"status":"Submitted","filledQuantity":20}]}`)
	if len(events) != 2 || !events[1].Refresh || events[1].Order.Symbol != "AAPL" || events[1].Order.Quantity != 50 {
		t.Fatalf("partial fill/merge: %+v", events)
	}
	consume(`{"topic":"sor","args":[{"acct":"U1","orderId":9007199254740993,"status":"Submitted","filledQuantity":20}]}`)
	if len(events) != 2 {
		t.Fatal("duplicate order update emitted")
	}
	fill := `{"topic":"str","args":[{"account":"U1","execution_id":"E1","size":20,"price":"10","side":"B","conid":265598}]}`
	consume(fill)
	consume(fill)
	if len(events) != 3 || events[2].ExecutionID != "E1" || !events[2].Refresh {
		t.Fatalf("fill dedupe: %+v", events)
	}
	consume(strings.Replace(fill, `"U1"`, `"U2"`, 1))
	if len(events) != 4 {
		t.Fatal("execution identity must be account scoped")
	}
	consume(strings.Replace(fill, `"10"`, `"11"`, 1))
	if len(events) != 5 {
		t.Fatal("execution correction must refresh")
	}
	consume(`{"topic":"sor","args":[{"acct":"U1","orderId":9007199254740993,"status":"Filled","filledQuantity":50}]}`)
	if events[len(events)-1].Order.Status != "filled" || !events[len(events)-1].Refresh {
		t.Fatal("full fill not detected")
	}
	consume(`{"topic":"sts","args":{"authenticated":true}}`)
	if c.consumeTradingMessage([]byte(`{"topic":"sts","args":{"authenticated":false}}`), emit) == nil {
		t.Fatal("lost authentication not detected")
	}
}

func TestOrderSnapshotCannotRollbackNewerStreamUpdate(t *testing.T) {
	c := New(config.IBKRConfig{})
	start := time.Now().Add(-time.Second)
	emit := func(broker.TradingEvent) {}
	c.applyOrderRows([]map[string]any{{"acct": "U1", "orderId": json.Number("1"), "filledQuantity": json.Number("20"), "status": "Submitted"}}, time.Now(), false, emit)
	c.applyOrderRows([]map[string]any{{"acct": "U1", "orderId": json.Number("1"), "filledQuantity": json.Number("0"), "status": "Submitted"}}, start, true, emit)
	orders, ok := c.cachedOrders(broker.OrderQuery{AccountID: "U1"})
	if !ok || len(orders) != 1 || orders[0].FilledQuantity != 20 {
		t.Fatalf("stale REST replaced stream: %+v", orders)
	}
	if other, _ := c.cachedOrders(broker.OrderQuery{AccountID: "U2"}); len(other) != 0 {
		t.Fatal("account isolation failed")
	}
}

func TestStreamHandshakeSubscriptionsAndShutdown(t *testing.T) {
	var polled atomic.Bool
	messages := make(chan string, 10)
	wsClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer proxy-secret" {
			t.Error("missing proxy authorization")
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/v1/api/tickle":
			fmt.Fprint(w, `{"session":"session-secret","iserver":{"authStatus":{"authenticated":true,"connected":true}}}`)
		case "/v1/api/iserver/accounts":
			fmt.Fprint(w, `{"accounts":["U1"]}`)
		case "/v1/api/iserver/account/orders":
			polled.Store(true)
			fmt.Fprint(w, `{"orders":[],"snapshot":true}`)
		case "/v1/api/iserver/account/trades":
			fmt.Fprint(w, `[]`)
		case "/v1/api/ws":
			if !polled.Load() {
				t.Error("subscribed before initial REST orders")
			}
			cookie, err := r.Cookie("api")
			if err != nil || cookie.Value != "session-secret" {
				t.Error("missing IBKR session cookie")
			}
			upgrader := websocket.Upgrader{}
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer ws.Close()
			defer close(wsClosed)
			for i := 0; i < 2; i++ {
				_, body, err := ws.ReadMessage()
				if err != nil {
					return
				}
				messages <- string(body)
			}
			_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"topic":"str","args":[{"account":"U1","execution_id":"E1","size":2,"price":"10","side":"B"}]}`))
			for {
				_, body, err := ws.ReadMessage()
				if err != nil {
					return
				}
				messages <- string(body)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := New(config.IBKRConfig{GatewayURL: server.URL, GatewayToken: "proxy-secret"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan broker.TradingEvent, 10)
	done := make(chan error, 1)
	cfg := defaultTradingWatchConfig
	cfg.ping = 20 * time.Millisecond
	go func() { done <- c.watchTradingEvents(ctx, func(e broker.TradingEvent) { events <- e }, cfg) }()
	for _, want := range []string{`sor+{}`, `str+{"realtimeUpdatesOnly":false,"days":1}`, "tic"} {
		select {
		case got := <-messages:
			if got != want {
				t.Fatalf("message %q want %q", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("subscription timeout")
		}
	}
	for {
		select {
		case e := <-events:
			if e.ExecutionID == "E1" {
				cancel()
				goto stopped
			}
		case <-time.After(3 * time.Second):
			t.Fatal("fill timeout")
		}
	}
stopped:
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop")
	}
	select {
	case <-wsClosed:
	case <-time.After(time.Second):
		t.Fatal("websocket leaked")
	}
}

func TestPollingDetectsFillsWhenWebsocketUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api/tickle":
			fmt.Fprint(w, `{"session":"test","authenticated":true}`)
		case "/v1/api/iserver/accounts":
			fmt.Fprint(w, `{"accounts":["U1"]}`)
		case "/v1/api/iserver/account/orders":
			fmt.Fprint(w, `{"orders":[{"acct":"U1","orderId":1,"status":"Submitted","filledQuantity":2}],"snapshot":true}`)
		case "/v1/api/iserver/account/trades":
			fmt.Fprint(w, `[{"account":"U1","execution_id":"missed","size":2,"price":"10","side":"B"}]`)
		default:
			http.Error(w, "websocket unavailable", http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	c := New(config.IBKRConfig{GatewayURL: server.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan broker.TradingEvent, 10)
	done := make(chan error, 1)
	go func() {
		done <- c.watchTradingEvents(ctx, func(e broker.TradingEvent) { events <- e }, defaultTradingWatchConfig)
	}()
	for {
		select {
		case e := <-events:
			if e.ExecutionID == "missed" {
				cancel()
				goto stopped
			}
		case <-time.After(3 * time.Second):
			t.Fatal("REST fallback did not detect fill")
		}
	}
stopped:
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("REST fallback worker leaked")
	}
}

func TestTradingHTTPRejectsRedirectAndDoesNotExposeSecrets(t *testing.T) {
	var reached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL+"?token=secret", 302) }))
	defer server.Close()
	c := New(config.IBKRConfig{GatewayURL: server.URL, GatewayToken: "proxy-secret"})
	var out any
	err := c.getTradingJSON(context.Background(), "/iserver/accounts", &out)
	if err == nil || strings.Contains(err.Error(), "secret") || reached.Load() {
		t.Fatalf("unsafe redirect handling: %v", err)
	}
}

func TestReconnectReplaysExecutionsWithoutDuplicateFill(t *testing.T) {
	var streams, orderRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api/tickle":
			fmt.Fprint(w, `{"session":"session","authenticated":true}`)
		case "/v1/api/iserver/accounts":
			fmt.Fprint(w, `{"accounts":["U1"]}`)
		case "/v1/api/iserver/account/orders":
			orderRequests.Add(1)
			fmt.Fprint(w, `{"orders":[],"snapshot":true}`)
		case "/v1/api/iserver/account/trades":
			fmt.Fprint(w, `[]`)
		case "/v1/api/ws":
			upgrader := websocket.Upgrader{}
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer ws.Close()
			for i := 0; i < 2; i++ {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
			}
			run := streams.Add(1)
			_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"topic":"str","args":[{"account":"U1","execution_id":"E1","size":1,"price":10,"side":"B"}]}`))
			if run == 1 {
				return
			}
			_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"topic":"str","args":[{"account":"U1","execution_id":"E2","size":1,"price":10,"side":"B"}]}`))
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := New(config.IBKRConfig{GatewayURL: server.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan broker.TradingEvent, 20)
	done := make(chan error, 1)
	cfg := defaultTradingWatchConfig
	cfg.reconnect = 10 * time.Millisecond
	go func() { done <- c.watchTradingEvents(ctx, func(e broker.TradingEvent) { events <- e }, cfg) }()
	fills := map[string]int{}
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if event.ExecutionID == "" {
				continue
			}
			fills[event.ExecutionID]++
			if event.ExecutionID == "E2" {
				cancel()
				goto stopped
			}
		case <-deadline.C:
			t.Fatal("stream did not reconnect and recover")
		}
	}
stopped:
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconnected worker leaked")
	}
	if fills["E1"] != 1 || fills["E2"] != 1 || orderRequests.Load() != 2 {
		t.Fatalf("replay or recovery incorrect: fills=%v snapshots=%d", fills, orderRequests.Load())
	}
}
