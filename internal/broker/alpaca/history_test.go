package alpaca

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/nite/traio/internal/activity"
	"github.com/nite/traio/internal/config"
)

type alpacaHistoryTransport func(*http.Request) (*http.Response, error)

func (f alpacaHistoryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchHistoryPaginatesAndRejectsWrongAccount(t *testing.T) {
	first := make([]map[string]string, 100)
	for i := range first {
		first[i] = map[string]string{"id": "activity-" + string(rune('A'+i)), "activity_type": "CSD", "date": "2026-09-10", "net_amount": "1"}
	}
	firstBody, _ := json.Marshal(first)
	requests := 0
	client := New(config.AlpacaConfig{APIKey: "fixture", APISecret: "fixture", BaseURL: "https://alpaca.test"}, WithHTTPClient(&http.Client{Transport: alpacaHistoryTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		body := `{"id":"id-1","account_number":"ACCOUNT","currency":"USD"}`
		if strings.HasSuffix(r.URL.Path, "/account/activities") {
			if got := r.URL.Query().Get("after"); got != "2026-09-09T23:59:59Z" {
				t.Fatalf("after = %q", got)
			}
			if got := r.URL.Query().Get("until"); got != "2026-09-11T00:00:00Z" {
				t.Fatalf("until = %q", got)
			}
			if r.URL.Query().Get("page_token") == "" {
				body = string(firstBody)
			} else {
				if r.URL.Query().Get("page_token") != first[99]["id"] {
					t.Fatalf("page token = %q", r.URL.Query().Get("page_token"))
				}
				body = `[{"id":"last","activity_type":"DIV","date":"2026-09-10","net_amount":"2"}]`
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}))
	records, err := client.FetchHistory(context.Background(), "ACCOUNT", "USD", "2026-09-10", "2026-09-10")
	if err != nil || len(records) != 101 || requests != 3 {
		t.Fatalf("records=%d requests=%d err=%v", len(records), requests, err)
	}
	if _, err := client.FetchHistory(context.Background(), "OTHER", "USD", "2026-09-10", "2026-09-10"); err == nil {
		t.Fatal("accepted a different account")
	}
}

func TestParseAlpacaActivityPreservesBookedCashAndUncertainTrades(t *testing.T) {
	deposit, err := parseAlpacaActivity(json.RawMessage(`{"id":"cash-1","activity_type":"CSD","date":"2026-09-10","net_amount":"100.25"}`), "ACC-1", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if deposit.Activity.Provider != "ALPACA" || deposit.Activity.Type != "deposit" || deposit.Activity.Status != activity.StatusEffective || deposit.Activity.Legs[0].CashDelta != "100.25" {
		t.Fatalf("deposit = %+v", deposit.Activity)
	}
	fill, err := parseAlpacaActivity(json.RawMessage(`{"id":"fill-1","activity_type":"FILL","transaction_time":"2026-09-10T14:30:00Z","symbol":"AAPL","side":"buy","qty":"2","price":"100","order_id":"order-1"}`), "ACC-1", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if fill.Activity.Type != "trade" || fill.Activity.Status != activity.StatusNeedsReview || fill.Activity.Fill == nil || fill.Activity.Legs[1].CashDelta != "-200" {
		t.Fatalf("fill = %+v", fill.Activity)
	}
	corporate, err := parseAlpacaActivity(json.RawMessage(`{"id":"corp-1","activity_type":"DIVROC","date":"2026-09-10","net_amount":"5"}`), "ACC-1", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if corporate.Activity.Type != "return_of_capital" || corporate.Activity.Status != activity.StatusUnsupported || len(corporate.Activity.Legs) != 1 || corporate.Activity.Legs[0].Component != "reported_unverified" {
		t.Fatalf("corporate = %+v", corporate.Activity)
	}
}
