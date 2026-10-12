package alpaca

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

func TestOrderWorkflowRequiresPreviewAndSendsOnce(t *testing.T) {
	posts, deletes := 0, 0
	client := New(config.AlpacaConfig{APIKey: "fixture", APISecret: "fixture", BaseURL: "https://paper-api.alpaca.test"}, WithHTTPClient(&http.Client{Transport: alpacaHistoryTransport(func(r *http.Request) (*http.Response, error) {
		body, status := "", 200
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/account":
			body = `{"id":"uuid-1","account_number":"A1","status":"ACTIVE","currency":"USD"}`
		case r.Method == http.MethodGet && r.URL.Path == "/v2/assets/AAPL":
			body = `{"id":"asset-1","symbol":"AAPL","name":"Apple","exchange":"NASDAQ","class":"us_equity","status":"active","tradable":true}`
		case r.Method == http.MethodPost && r.URL.Path == "/v2/orders":
			posts++
			payload, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(payload), `"client_order_id":"client-1"`) || !strings.Contains(string(payload), `"symbol":"AAPL"`) {
				t.Fatalf("wrong order payload: %s", payload)
			}
			body, status = `{"id":"order-1","client_order_id":"client-1","symbol":"AAPL","side":"buy","type":"limit","time_in_force":"day","status":"new","qty":"2"}`, 201
		case r.Method == http.MethodGet && r.URL.Path == "/v2/orders/order-1":
			body = `{"id":"order-1","symbol":"AAPL","status":"new","qty":"2"}`
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/orders/order-1":
			deletes++
			status = 204
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}))
	ctx := context.Background()
	instruments, err := client.SearchOrderInstruments(ctx, "AAPL")
	if err != nil || len(instruments) != 1 {
		t.Fatalf("instruments=%v err=%v", instruments, err)
	}
	req := broker.OrderRequest{AccountID: "A1", Symbol: "AAPL", InstrumentID: strconv.FormatInt(instruments[0].ConID, 10), AssetClass: "equity", Currency: "USD", Side: "buy", OrderType: "limit", Quantity: 2, LimitPrice: 100, TimeInForce: "day", ClientOrderID: "client-1"}
	if _, err := client.PlaceOrder(ctx, req); err == nil || posts != 0 {
		t.Fatalf("order bypassed preview: err=%v posts=%d", err, posts)
	}
	preview, err := client.PreviewOrder(ctx, req)
	if err != nil || preview.Commission != "" || len(preview.Warnings) == 0 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	order, err := client.PlaceOrder(ctx, req)
	if err != nil || order.ID != "order-1" || order.Status != "open" || posts != 1 {
		t.Fatalf("order=%+v posts=%d err=%v", order, posts, err)
	}
	again, err := client.PlaceOrder(ctx, req)
	if err != nil || again.ID != order.ID || posts != 1 {
		t.Fatalf("duplicate resent: order=%+v posts=%d err=%v", again, posts, err)
	}
	if _, err := client.OrderAttempt(ctx, "A1", "client-1"); err != nil {
		t.Fatal(err)
	}
	if err := client.CancelOrder(ctx, "OTHER", "order-1"); err == nil || deletes != 0 {
		t.Fatalf("another account canceled: err=%v deletes=%d", err, deletes)
	}
	if err := client.CancelOrder(ctx, "A1", "order-1"); err != nil || deletes != 1 {
		t.Fatalf("cancel err=%v deletes=%d", err, deletes)
	}
}

func TestLiveConnectionUsesLiveAPIByDefault(t *testing.T) {
	session, err := NewFactory().Open(context.Background(), broker.ConnectionConfig{Environment: "live", Secrets: map[string]string{"api_key": "fixture", "api_secret": "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := session.(*Session).baseURL(); got != defaultLiveBaseURL {
		t.Fatalf("live connection points to %q", got)
	}
}

func TestMutatingRequestNeverFollowsRedirect(t *testing.T) {
	calls := 0
	client := New(config.AlpacaConfig{APIKey: "fixture", APISecret: "fixture", BaseURL: "https://paper-api.alpaca.test"}, WithHTTPClient(&http.Client{Transport: alpacaHistoryTransport(func(*http.Request) (*http.Response, error) {
		calls++
		header := make(http.Header)
		header.Set("Location", "https://elsewhere.test/orders")
		return &http.Response{StatusCode: 302, Header: header, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}))
	if _, err := client.Do(context.Background(), http.MethodPost, "orders", strings.NewReader(`{}`)); err == nil || calls != 1 {
		t.Fatalf("redirect followed: calls=%d err=%v", calls, err)
	}
}
