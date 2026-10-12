package schwab

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

func TestOrderWorkflowUsesHashPreviewAndSendsOnce(t *testing.T) {
	previews, posts, deletes := 0, 0, 0
	client := New(config.SchwabConfig{}, WithAPIURLs("https://schwab.test/trader/v1", "https://schwab.test/marketdata/v1"), WithHTTPClient(&http.Client{Transport: schwabHistoryTransport(func(r *http.Request) (*http.Response, error) {
		body, status := "", 200
		header := make(http.Header)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/trader/v1/accounts":
			body = `[{"securitiesAccount":{"accountNumber":"A1","type":"CASH"}}]`
		case r.Method == http.MethodGet && r.URL.Path == "/marketdata/v1/instruments":
			body = `{"instruments":[{"symbol":"AAPL","description":"Apple","assetType":"EQUITY","exchange":"NASDAQ"}]}`
		case r.Method == http.MethodGet && r.URL.Path == "/trader/v1/accounts/accountNumbers":
			body = `[{"accountNumber":"A1","hashValue":"HASH1"}]`
		case r.Method == http.MethodPost && r.URL.Path == "/trader/v1/accounts/HASH1/previewOrder":
			previews++
			if r.Header.Get("Content-Type") != "application/json" {
				t.Fatal("missing JSON content type")
			}
			body = `{"orderValidationResult":{},"commissionAndFee":{"commission":0.5}}`
		case r.Method == http.MethodPost && r.URL.Path == "/trader/v1/accounts/HASH1/orders":
			posts++
			payload, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(payload), `"orderStrategyType":"SINGLE"`) || !strings.Contains(string(payload), `"symbol":"AAPL"`) {
				t.Fatalf("wrong order payload: %s", payload)
			}
			header.Set("Location", "/trader/v1/accounts/HASH1/orders/123")
			status = 201
		case r.Method == http.MethodGet && r.URL.Path == "/trader/v1/accounts/HASH1/orders/123":
			body = `{"orderId":123,"accountNumber":"A1","status":"WORKING","orderType":"LIMIT","duration":"DAY"}`
		case r.Method == http.MethodDelete && r.URL.Path == "/trader/v1/accounts/HASH1/orders/123":
			deletes++
			status = 204
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: header}, nil
	})}))
	client.SetToken(Token{AccessToken: "fixture"})
	ctx := context.Background()
	instruments, err := client.SearchOrderInstruments(ctx, "AAPL")
	if err != nil || len(instruments) != 1 {
		t.Fatalf("instruments=%v err=%v", instruments, err)
	}
	req := broker.OrderRequest{AccountID: "A1", Symbol: "AAPL", InstrumentID: strconv.FormatInt(instruments[0].ConID, 10), AssetClass: "equity", Currency: "USD", Side: "buy", OrderType: "limit", Quantity: 2, LimitPrice: 100, TimeInForce: "day", ClientOrderID: "client-1"}
	if got := schwabOrderPayload(broker.OrderRequest{TimeInForce: "gtc"})["duration"]; got != "GOOD_TILL_CANCEL" {
		t.Fatalf("GTC duration = %v", got)
	}
	if _, err := client.PlaceOrder(ctx, req); err == nil || posts != 0 {
		t.Fatalf("order bypassed preview: err=%v posts=%d", err, posts)
	}
	preview, err := client.PreviewOrder(ctx, req)
	if err != nil || preview.Commission != "0.5" || previews != 1 {
		t.Fatalf("preview=%+v count=%d err=%v", preview, previews, err)
	}
	order, err := client.PlaceOrder(ctx, req)
	if err != nil || order.ID != "123" || order.Status != "unknown" || posts != 1 {
		t.Fatalf("order=%+v posts=%d err=%v", order, posts, err)
	}
	if _, err := client.PlaceOrder(ctx, req); err != nil || posts != 1 {
		t.Fatalf("duplicate resent: posts=%d err=%v", posts, err)
	}
	if err := client.CancelOrder(ctx, "A1", "123"); err != nil || deletes != 1 {
		t.Fatalf("cancel err=%v deletes=%d", err, deletes)
	}
}

func TestMutatingRequestNeverFollowsRedirect(t *testing.T) {
	calls := 0
	client := New(config.SchwabConfig{}, WithHTTPClient(&http.Client{Transport: schwabHistoryTransport(func(*http.Request) (*http.Response, error) {
		calls++
		header := make(http.Header)
		header.Set("Location", "https://elsewhere.test/orders")
		return &http.Response{StatusCode: 302, Header: header, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}))
	client.SetToken(Token{AccessToken: "fixture"})
	if _, err := client.Do(context.Background(), http.MethodPost, "https://schwab.test/trader/v1/accounts/HASH1/orders", strings.NewReader(`{}`)); err == nil || calls != 1 {
		t.Fatalf("redirect followed: calls=%d err=%v", calls, err)
	}
}
