package schwab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nite/traio/internal/broker"
)

const equityStreamFields = "0,1,2,3,8,10,11,18,42"

type StreamStatus struct {
	Connected bool   `json:"connected"`
	Symbols   int    `json:"symbols"`
	Error     string `json:"error,omitempty"`
}

type streamSubscriber struct {
	symbols map[string]struct{}
	quotes  chan broker.Quote
}

type streamManager struct {
	client *Client

	mu          sync.RWMutex
	subscribers map[uint64]streamSubscriber
	nextID      uint64
	cache       map[string]broker.Quote
	status      StreamStatus
	wakeup      chan struct{}
	startOnce   sync.Once
}

func newStreamManager(client *Client) *streamManager {
	return &streamManager{
		client:      client,
		subscribers: make(map[uint64]streamSubscriber),
		cache:       make(map[string]broker.Quote),
		wakeup:      make(chan struct{}, 1),
	}
}

// SubscribeQuotes registers a set of equity symbols on the client's single
// Schwab Streamer connection. The returned cancel function must be called.
func (c *Client) SubscribeQuotes(symbols []string) (<-chan broker.Quote, func()) {
	return c.stream.subscribe(symbols)
}

func (c *Client) StreamStatus() StreamStatus {
	return c.stream.currentStatus()
}

func (m *streamManager) subscribe(symbols []string) (<-chan broker.Quote, func()) {
	set := symbolSet(symbols)
	out := make(chan broker.Quote, 256)
	if len(set) == 0 {
		close(out)
		return out, func() {}
	}

	m.mu.Lock()
	m.nextID++
	id := m.nextID
	m.subscribers[id] = streamSubscriber{symbols: set, quotes: out}
	for symbol := range set {
		if quote, ok := m.cache[symbol]; ok {
			select {
			case out <- quote:
			default:
			}
		}
	}
	m.mu.Unlock()

	m.startOnce.Do(func() { go m.run() })
	m.wake()

	var once sync.Once
	return out, func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.subscribers, id)
			close(out)
			m.mu.Unlock()
			m.wake()
		})
	}
}

func (m *streamManager) wake() {
	select {
	case m.wakeup <- struct{}{}:
	default:
	}
}

func (m *streamManager) currentStatus() StreamStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *streamManager) setStatus(connected bool, symbols int, err error) {
	m.mu.Lock()
	m.status.Connected = connected
	m.status.Symbols = symbols
	if err == nil {
		m.status.Error = ""
	} else {
		m.status.Error = err.Error()
	}
	m.mu.Unlock()
}

func (m *streamManager) desiredSymbols() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	set := make(map[string]struct{})
	for _, subscriber := range m.subscribers {
		for symbol := range subscriber.symbols {
			set[symbol] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for symbol := range set {
		out = append(out, symbol)
	}
	sort.Strings(out)
	return out
}

func (m *streamManager) run() {
	backoff := time.Second
	for {
		symbols := m.desiredSymbols()
		if len(symbols) == 0 {
			m.setStatus(false, 0, nil)
			<-m.wakeup
			continue
		}

		err := m.connect(symbols)
		m.setStatus(false, len(symbols), err)

		timer := time.NewTimer(backoff)
		select {
		case <-m.wakeup:
			if !timer.Stop() {
				<-timer.C
			}
			backoff = time.Second
		case <-timer.C:
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}
}

func (m *streamManager) connect(symbols []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	preference, err := m.client.userPreference(ctx)
	cancel()
	if err != nil {
		return err
	}
	accessToken, err := m.client.validAccessToken(context.Background())
	if err != nil {
		return err
	}

	conn, _, err := websocket.DefaultDialer.Dial(normalizeStreamerURL(preference.StreamerSocketURL), nil)
	if err != nil {
		return fmt.Errorf("schwab streamer: connect: %w", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(streamRequest{Requests: []streamCommand{{
		Service:                "ADMIN",
		Command:                "LOGIN",
		RequestID:              "1",
		SchwabClientCustomerID: preference.CustomerID,
		SchwabClientCorrelID:   preference.CorrelID,
		Parameters: map[string]string{
			"Authorization":          accessToken,
			"SchwabClientChannel":    preference.Channel,
			"SchwabClientFunctionId": preference.FunctionID,
		},
	}}}); err != nil {
		return fmt.Errorf("schwab streamer: login request: %w", err)
	}
	if err := waitForSuccess(conn, "1"); err != nil {
		return err
	}
	if err := conn.WriteJSON(streamRequest{Requests: []streamCommand{{
		Service:                "LEVELONE_EQUITIES",
		Command:                "SUBS",
		RequestID:              "2",
		SchwabClientCustomerID: preference.CustomerID,
		SchwabClientCorrelID:   preference.CorrelID,
		Parameters: map[string]string{
			"keys":   strings.Join(symbols, ","),
			"fields": equityStreamFields,
		},
	}}}); err != nil {
		return fmt.Errorf("schwab streamer: subscribe request: %w", err)
	}
	m.setStatus(true, len(symbols), nil)

	for {
		if current := m.desiredSymbols(); !sameStrings(current, symbols) {
			return nil
		}
		_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
		var message streamMessage
		if err := conn.ReadJSON(&message); err != nil {
			return fmt.Errorf("schwab streamer: read: %w", err)
		}
		for _, response := range message.Response {
			if response.Content.Code != 0 {
				return fmt.Errorf(
					"schwab streamer: request %s failed (%d): %s",
					response.RequestID,
					response.Content.Code,
					response.Content.Msg,
				)
			}
		}
		for _, data := range message.Data {
			if data.Service != "LEVELONE_EQUITIES" {
				continue
			}
			for _, content := range data.Content {
				if quote, ok := m.mergeEquity(content); ok {
					m.dispatch(quote)
				}
			}
		}
	}
}

type userPreferenceResponse struct {
	StreamerInfo []struct {
		StreamerSocketURL      string `json:"streamerSocketUrl"`
		SchwabClientCustomerID string `json:"schwabClientCustomerId"`
		SchwabClientCorrelID   string `json:"schwabClientCorrelId"`
		SchwabClientChannel    string `json:"schwabClientChannel"`
		SchwabClientFunctionID string `json:"schwabClientFunctionId"`
	} `json:"streamerInfo"`
}

type streamerPreference struct {
	StreamerSocketURL string
	CustomerID        string
	CorrelID          string
	Channel           string
	FunctionID        string
}

func (c *Client) userPreference(ctx context.Context) (streamerPreference, error) {
	c.mu.RLock()
	endpoint := c.traderURL + "/userPreference"
	c.mu.RUnlock()
	resp, err := c.Do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return streamerPreference{}, err
	}
	defer resp.Body.Close()
	var body userPreferenceResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return streamerPreference{}, fmt.Errorf("schwab: decode user preference: %w", err)
	}
	if len(body.StreamerInfo) == 0 {
		return streamerPreference{}, errors.New("schwab: user preference did not include streamer info")
	}
	info := body.StreamerInfo[0]
	return streamerPreference{
		StreamerSocketURL: info.StreamerSocketURL,
		CustomerID:        info.SchwabClientCustomerID,
		CorrelID:          info.SchwabClientCorrelID,
		Channel:           info.SchwabClientChannel,
		FunctionID:        info.SchwabClientFunctionID,
	}, nil
}

type streamRequest struct {
	Requests []streamCommand `json:"requests"`
}

type streamCommand struct {
	Service                string            `json:"service"`
	Command                string            `json:"command"`
	RequestID              string            `json:"requestid"`
	SchwabClientCustomerID string            `json:"SchwabClientCustomerId"`
	SchwabClientCorrelID   string            `json:"SchwabClientCorrelId"`
	Parameters             map[string]string `json:"parameters"`
}

type streamMessage struct {
	Response []struct {
		RequestID string `json:"requestid"`
		Content   struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		} `json:"content"`
	} `json:"response"`
	Data []struct {
		Service string           `json:"service"`
		Content []map[string]any `json:"content"`
	} `json:"data"`
}

func waitForSuccess(conn *websocket.Conn, requestID string) error {
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	for {
		var message streamMessage
		if err := conn.ReadJSON(&message); err != nil {
			return fmt.Errorf("schwab streamer: login response: %w", err)
		}
		for _, response := range message.Response {
			if response.RequestID != requestID {
				continue
			}
			if response.Content.Code != 0 {
				return fmt.Errorf("schwab streamer: login failed (%d): %s", response.Content.Code, response.Content.Msg)
			}
			return nil
		}
	}
}

func (m *streamManager) mergeEquity(content map[string]any) (broker.Quote, bool) {
	symbol := strings.ToUpper(stringField(content, "key"))
	if symbol == "" {
		symbol = strings.ToUpper(stringField(content, "0"))
	}
	if symbol == "" {
		return broker.Quote{}, false
	}

	m.mu.Lock()
	quote := m.cache[symbol]
	quote.Symbol = symbol
	setFloat(content, "1", &quote.Bid)
	setFloat(content, "2", &quote.Ask)
	setFloat(content, "3", &quote.Last)
	setInt64(content, "8", &quote.Volume)
	setFloat(content, "10", &quote.High)
	setFloat(content, "11", &quote.Low)
	setFloat(content, "18", &quote.Change)
	setFloat(content, "42", &quote.ChangePct)
	if delayed, ok := content["delayed"].(bool); ok {
		quote.Delayed = delayed
	}
	m.cache[symbol] = quote
	m.mu.Unlock()
	return quote, true
}

func (m *streamManager) dispatch(quote broker.Quote) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, subscriber := range m.subscribers {
		if _, ok := subscriber.symbols[quote.Symbol]; !ok {
			continue
		}
		select {
		case subscriber.quotes <- quote:
		default:
		}
	}
}

func symbolSet(symbols []string) map[string]struct{} {
	set := make(map[string]struct{}, len(symbols))
	for _, symbol := range symbols {
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		if symbol != "" {
			set[symbol] = struct{}{}
		}
	}
	return set
}

func normalizeStreamerURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" {
		return raw
	}
	return "wss://" + raw
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func setFloat(values map[string]any, key string, target *float64) {
	if value, ok := number(values[key]); ok {
		*target = value
	}
}

func setInt64(values map[string]any, key string, target *int64) {
	if value, ok := number(values[key]); ok {
		*target = int64(value)
	}
}

func number(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case json.Number:
		n, err := value.Float64()
		return n, err == nil
	case string:
		n, err := strconv.ParseFloat(value, 64)
		return n, err == nil
	default:
		return 0, false
	}
}
