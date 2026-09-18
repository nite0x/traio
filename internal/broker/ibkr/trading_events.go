package ibkr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nite/traio/internal/broker"
)

const orderRequestInterval = 5 * time.Second

type liveOrderRow struct {
	fields map[string]any
	at     time.Time
}

type liveOrderState struct {
	mu          sync.Mutex
	rows        map[string]liveOrderRow
	executions  map[string]executionSeen
	readyAt     time.Time
	requestMu   sync.Mutex
	requestedAt time.Time
}

type executionSeen struct {
	hash [32]byte
	at   time.Time
}

type tradingWatchConfig struct {
	poll, ping, reconnect, maxReconnect, readTimeout time.Duration
}

var defaultTradingWatchConfig = tradingWatchConfig{
	poll: 30 * time.Second, ping: 25 * time.Second, reconnect: 5 * time.Second,
	maxReconnect: time.Minute, readTimeout: 90 * time.Second,
}

var _ broker.TradingEventProvider = (*Session)(nil)

func (s *Session) WatchTradingEvents(ctx context.Context, emit func(broker.TradingEvent)) error {
	s.eventMu.Lock()
	if s.closed || s.eventDone != nil {
		s.eventMu.Unlock()
		return errors.New("ibkr: event watcher already started or closed")
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.eventCancel, s.eventDone = cancel, done
	s.eventMu.Unlock()
	defer cancel()
	defer func() {
		s.eventMu.Lock()
		close(done)
		s.eventDone, s.eventCancel = nil, nil
		s.eventMu.Unlock()
	}()
	return s.client.watchTradingEvents(ctx, emit, defaultTradingWatchConfig)
}

// The websocket and fallback poll run independently: a slow REST call cannot
// prevent reading fills or sending heartbeats. Their shared state is serialized.
func (c *Client) watchTradingEvents(ctx context.Context, emit func(broker.TradingEvent), cfg tradingWatchConfig) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(cfg.poll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.pollTradingEvents(ctx, emit, false)
			}
		}
	}()
	defer workers.Wait()
	delay := cfg.reconnect
	for ctx.Err() == nil {
		started := time.Now()
		err := c.streamTradingEvents(ctx, emit, cfg)
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			// Never print websocket errors: they may contain the URL, cookie or headers.
			log.Print("ibkr: order stream unavailable; retrying with REST fallback")
		}
		if time.Since(started) >= cfg.readTimeout {
			delay = cfg.reconnect
		}
		if !waitTrading(ctx, delay) {
			break
		}
		delay = min(delay*2, cfg.maxReconnect)
	}
	return ctx.Err()
}

func waitTrading(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(max(delay, 0))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (c *Client) streamTradingEvents(ctx context.Context, emit func(broker.TradingEvent), cfg tradingWatchConfig) error {
	session, err := c.streamSession(ctx)
	if err != nil {
		return err
	}
	// Prime the brokerage account list and retrieve today's orders before sor.
	var accounts json.RawMessage
	if err := c.getTradingJSON(ctx, "/iserver/accounts", &accounts); err != nil {
		return err
	}
	c.pollTradingEvents(ctx, emit, true)
	u, err := url.Parse(c.BaseURL())
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("ibkr: invalid stream URL")
	}
	secure := u.Scheme == "https"
	u.Scheme = "ws"
	if secure {
		u.Scheme = "wss"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/api/ws"
	u.RawQuery, u.Fragment, u.User = "", "", nil
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if secure && isLoopbackHost(u.Hostname()) {
		dialer.TLSClientConfig.InsecureSkipVerify = true
	} //nolint:gosec
	headers := http.Header{}
	headers.Set("Cookie", (&http.Cookie{Name: "api", Value: session}).String())
	if token := strings.TrimSpace(c.cfg.GatewayToken); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	ws, resp, err := dialer.DialContext(ctx, u.String(), headers)
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return errors.New("ibkr: stream handshake failed")
	}
	defer ws.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = ws.Close() })
	defer stopClose()
	ws.SetReadLimit(8 << 20)
	write := func(message string) error {
		_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return ws.WriteMessage(websocket.TextMessage, []byte(message))
	}
	// No status filter: filtering only Submitted would miss fills and cancellations.
	if err := write(`sor+{}`); err != nil {
		return err
	}
	if err := write(`str+{"realtimeUpdatesOnly":false,"days":1}`); err != nil {
		return err
	}
	// Reconcile even when the gap contained a transfer rather than an execution.
	emit(broker.TradingEvent{Refresh: true})
	writerCtx, stopWriter := context.WithCancel(ctx)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		ticker := time.NewTicker(cfg.ping)
		defer ticker.Stop()
		for {
			select {
			case <-writerCtx.Done():
				return
			case <-ticker.C:
				if write("tic") != nil {
					_ = ws.Close()
					return
				}
			}
		}
	}()
	defer func() { stopWriter(); _ = ws.Close(); <-writerDone }()
	for {
		_ = ws.SetReadDeadline(time.Now().Add(cfg.readTimeout))
		_, data, err := ws.ReadMessage()
		if err != nil {
			return errors.New("ibkr: stream disconnected")
		}
		if err := c.consumeTradingMessage(data, emit); err != nil {
			return err
		}
	}
}

func (c *Client) streamSession(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL()+"/v1/api/tickle", strings.NewReader("{}"))
	if err != nil {
		return "", errors.New("ibkr: invalid session request")
	}
	req.Header.Set("Content-Type", "application/json")
	hc := *c.httpClient
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return "", errors.New("ibkr: session request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("ibkr: stream authentication required")
	}
	var payload map[string]any
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload) != nil || !tickleAuthenticated(payload) {
		return "", errors.New("ibkr: stream authentication required")
	}
	if server, ok := payload["iserver"].(map[string]any); ok {
		if status, ok := server["authStatus"].(map[string]any); ok {
			if status["competing"] == true || status["connected"] == false {
				return "", errors.New("ibkr: brokerage session unavailable")
			}
		}
	}
	token, _ := payload["session"].(string)
	if token == "" || strings.ContainsAny(token, "\r\n;\"\\ ") {
		return "", errors.New("ibkr: invalid stream session")
	}
	return token, nil
}

func (c *Client) pollTradingEvents(ctx context.Context, emit func(broker.TradingEvent), force bool) {
	started := time.Now()
	rows, err := c.fetchOrderRows(ctx, force)
	if err == nil {
		c.applyOrderRows(rows, started, true, emit)
	}
	// Also keep /tickle alive independently of websocket protocol heartbeats.
	if !force {
		_, _ = c.streamSession(ctx)
	}
	var trades json.RawMessage
	if c.getTradingJSON(ctx, "/iserver/account/trades?days=1", &trades) == nil {
		_ = c.applyExecutionRows(trades, emit)
	}
}

func (c *Client) fetchOrderRows(ctx context.Context, force bool) ([]map[string]any, error) {
	c.orderState.requestMu.Lock()
	defer c.orderState.requestMu.Unlock()
	if !waitTrading(ctx, time.Until(c.orderState.requestedAt.Add(orderRequestInterval))) {
		return nil, ctx.Err()
	}
	c.orderState.requestedAt = time.Now()
	path := "/iserver/account/orders"
	if force {
		path += "?force=true"
	}
	var raw struct {
		Orders   []map[string]any `json:"orders"`
		Snapshot *bool            `json:"snapshot"`
	}
	if err := c.getTradingJSON(ctx, path, &raw); err != nil {
		return nil, err
	}
	if raw.Orders == nil || (raw.Snapshot != nil && !*raw.Snapshot) {
		return nil, errors.New("ibkr: orders snapshot pending")
	}
	return raw.Orders, nil
}

func (c *Client) getTradingJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL()+"/v1/api"+path, nil)
	if err != nil {
		return errors.New("ibkr: invalid trading request")
	}
	hc := *c.httpClient
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return errors.New("ibkr: trading request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return gatewayUnauthorizedError(resp, strings.Split(path, "?")[0])
	}
	if resp.StatusCode != http.StatusOK {
		return errors.New("ibkr: trading request unsuccessful")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(body) > 8<<20 {
		return errors.New("ibkr: invalid trading response size")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(out) != nil {
		return errors.New("ibkr: invalid trading response")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("ibkr: invalid trailing trading response")
	}
	return nil
}

func (c *Client) consumeTradingMessage(data []byte, emit func(broker.TradingEvent)) error {
	var message struct {
		Topic string          `json:"topic"`
		Args  json.RawMessage `json:"args"`
	}
	if json.Unmarshal(data, &message) != nil {
		return errors.New("ibkr: invalid stream message")
	}
	switch message.Topic {
	case "sor":
		rows, err := tradingRows(message.Args)
		if err != nil {
			return err
		}
		c.applyOrderRows(rows, time.Now(), false, emit)
	case "str":
		return c.applyExecutionRows(message.Args, emit)
	case "sts":
		var status struct {
			Authenticated *bool `json:"authenticated"`
			Competing     bool  `json:"competing"`
		}
		if json.Unmarshal(message.Args, &status) == nil && ((status.Authenticated != nil && !*status.Authenticated) || status.Competing) {
			return errors.New("ibkr: stream authentication lost")
		}
	}
	return nil
}

func tradingRows(data []byte) ([]map[string]any, error) {
	var rows []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&rows) != nil || rows == nil {
		return nil, errors.New("ibkr: invalid trading rows")
	}
	return rows, nil
}

func (c *Client) applyOrderRows(rows []map[string]any, started time.Time, snapshot bool, emit func(broker.TradingEvent)) {
	state := &c.orderState
	state.mu.Lock()
	if state.rows == nil {
		state.rows = map[string]liveOrderRow{}
	}
	now := time.Now()
	for key, row := range state.rows {
		if now.Sub(row.at) > 24*time.Hour {
			delete(state.rows, key)
		}
	}
	events := []broker.TradingEvent{}
	for _, row := range rows {
		account, id := textValue(row["acct"]), textValue(row["orderId"])
		if account == "" || id == "" {
			continue
		}
		key := account + "/" + id
		old, exists := state.rows[key]
		// A REST response started before a websocket update must not roll it back.
		if snapshot && old.at.After(started) {
			continue
		}
		if !exists && len(state.rows) >= 10000 {
			continue
		}
		before := normalizeIBKROrder(account, old.fields)
		merged := map[string]any{}
		for k, v := range old.fields {
			merged[k] = v
		}
		for k, v := range row {
			if v != nil {
				merged[k] = v
			}
		}
		after := normalizeIBKROrder(account, merged)
		state.rows[key] = liveOrderRow{fields: merged, at: now}
		if !exists || before != after {
			refresh := after.FilledQuantity != before.FilledQuantity || (after.Status == "filled" && before.Status != "filled") || (after.Status == "canceled" && before.Status != "canceled")
			events = append(events, broker.TradingEvent{AccountID: account, Order: &after, Refresh: refresh})
		}
	}
	if snapshot {
		state.readyAt = now
	}
	state.mu.Unlock()
	for _, event := range events {
		emit(event)
	}
}

func (c *Client) applyExecutionRows(data []byte, emit func(broker.TradingEvent)) error {
	rows, err := tradingRows(data)
	if err != nil {
		return err
	}
	state := &c.orderState
	state.mu.Lock()
	if state.executions == nil {
		state.executions = map[string]executionSeen{}
	}
	now := time.Now()
	for key, seen := range state.executions {
		if now.Sub(seen.at) > 7*24*time.Hour {
			delete(state.executions, key)
		}
	}
	events := []broker.TradingEvent{}
	for _, row := range rows {
		account := firstNonEmpty(textValue(row["accountCode"]), textValue(row["account"]))
		id := textValue(row["execution_id"])
		if account == "" || id == "" {
			continue
		}
		key := account + "/" + id
		// Only execution economics affect refresh dedupe, not optional REST metadata.
		body, _ := json.Marshal([]string{textValue(row["size"]), textValue(row["price"]), textValue(row["side"]), textValue(row["conid"]), textValue(row["trade_time_r"])})
		hash := sha256.Sum256(body)
		if seen, ok := state.executions[key]; ok && seen.hash == hash {
			continue
		}
		if len(state.executions) >= 50000 {
			state.executions = map[string]executionSeen{}
		}
		state.executions[key] = executionSeen{hash: hash, at: now}
		events = append(events, broker.TradingEvent{AccountID: account, ExecutionID: id, Refresh: true})
	}
	state.mu.Unlock()
	for _, event := range events {
		emit(event)
	}
	return nil
}

func (c *Client) cachedOrders(q broker.OrderQuery) ([]broker.Order, bool) {
	state := &c.orderState
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.readyAt.IsZero() || time.Since(state.readyAt) > time.Minute {
		return nil, false
	}
	keys := make([]string, 0, len(state.rows))
	for key := range state.rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	orders := []broker.Order{}
	for _, key := range keys {
		row := state.rows[key]
		order := normalizeIBKROrder(textValue(row.fields["acct"]), row.fields)
		if (q.AccountID != "" && order.AccountID != q.AccountID) || !matchesOrderStatus(q.Status, order.Status) {
			continue
		}
		orders = append(orders, order)
		if q.Limit > 0 && len(orders) >= q.Limit {
			break
		}
	}
	return orders, true
}
