package schwab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nite/traio/internal/activity"
	"github.com/nite/traio/internal/config"
)

type schwabHistoryTransport func(*http.Request) (*http.Response, error)

func (f schwabHistoryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchHistoryUsesHashedAccountAndDetectsResponseCap(t *testing.T) {
	var paths []string
	client := New(config.SchwabConfig{}, WithAPIURLs("https://schwab.test/trader/v1", ""), WithHTTPClient(&http.Client{Transport: schwabHistoryTransport(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		body := `[ {"accountNumber":"A1","hashValue":"HASH1"} ]`
		if strings.Contains(r.URL.Path, "/transactions") {
			if r.URL.Query().Get("types") == "" || r.URL.Query().Get("startDate") == "" || r.URL.Query().Get("endDate") == "" {
				t.Fatal("missing required transaction filters")
			}
			body = `[{"activityId":1,"accountNumber":"A1","type":"ACH_RECEIPT","status":"VALID","time":"2026-09-10T10:00:00Z","netAmount":1}]`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}))
	client.SetToken(Token{AccessToken: "fixture"})
	records, err := client.FetchHistory(context.Background(), "A1", "USD", "2026-09-10", "2026-09-10")
	if err != nil || len(records) != 1 || len(paths) != 2 || !strings.Contains(paths[1], "/accounts/HASH1/transactions") {
		t.Fatalf("records=%d paths=%v err=%v", len(records), paths, err)
	}
	full := make([]json.RawMessage, 3000)
	for i := range full {
		full[i] = json.RawMessage(`{}`)
	}
	tooMany, _ := json.Marshal(full)
	client = New(config.SchwabConfig{}, WithAPIURLs("https://schwab.test/trader/v1", ""), WithHTTPClient(&http.Client{Transport: schwabHistoryTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(tooMany))), Header: make(http.Header)}, nil
	})}))
	client.SetToken(Token{AccessToken: "fixture"})
	start, _ := time.Parse("2006-01-02", "2026-09-10")
	if _, err := client.fetchTransactionWindow(context.Background(), "HASH1", "A1", "USD", start, start.AddDate(0, 0, 1)); err == nil {
		t.Fatal("accepted a capped one-day response")
	}
}

func TestParseSchwabTransactionRequiresIdentityAndDoesNotBookAmbiguousTrades(t *testing.T) {
	deposit, err := parseSchwabTransaction(json.RawMessage(`{"activityId":123,"accountNumber":"A1","type":"ACH_RECEIPT","status":"VALID","time":"2026-09-10T14:30:00Z","netAmount":250.50}`), "A1", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if deposit.Key != "123" || deposit.Activity.Type != "deposit" || deposit.Activity.Status != activity.StatusEffective || deposit.Activity.Legs[0].CashDelta != "250.5" {
		t.Fatalf("deposit = %+v", deposit)
	}
	trade, err := parseSchwabTransaction(json.RawMessage(`{"activityId":124,"accountNumber":"A1","type":"TRADE","activityType":"EXECUTION","status":"VALID","time":"2026-09-10T14:30:00Z","netAmount":-200,"transferItems":[{"amount":2,"price":100,"instrument":{"symbol":"AAPL","assetType":"EQUITY","cusip":"C1"}}]}`), "A1", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if trade.Activity.Type != "trade" || trade.Activity.Status != activity.StatusNeedsReview || trade.Activity.Fill == nil || len(trade.Activity.Legs) != 2 {
		t.Fatalf("trade = %+v", trade.Activity)
	}
	if _, err := parseSchwabTransaction(json.RawMessage(`{"activityId":125,"accountNumber":"OTHER","type":"ACH_RECEIPT","status":"VALID","time":"2026-09-10T14:30:00Z","netAmount":1}`), "A1", "USD"); err == nil {
		t.Fatal("accepted another account's transaction")
	}
}
