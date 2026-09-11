package yahoo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nite/traio/internal/market"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const quoteFixture = `{"quoteResponse":{"error":null,"result":[
{"symbol":"AAPL","regularMarketPrice":210.5,"regularMarketPreviousClose":200,"regularMarketChange":0,"regularMarketChangePercent":0,"regularMarketTime":1788998400,"currency":"USD","exchangeDataDelayedBy":0,"marketState":"REGULAR","fiftyTwoWeekHigh":250,"fiftyTwoWeekLow":150},
{"symbol":"MSFT","regularMarketPrice":420,"regularMarketPreviousClose":400,"exchangeDataDelayedBy":15},
{"symbol":"MISSING","regularMarketPrice":null}]}}`
const chartFixture = `{"chart":{"error":null,"result":[{"timestamp":[100,200,300,400],"indicators":{"quote":[{"open":[10,null,12],"high":[12,null,14],"low":[9,null,11],"close":[11,null,13],"volume":[1000,null,null]}]}}]}}`

func fakeClient(t *testing.T, handle func(*http.Request) (int, string)) *Client {
	t.Helper()
	c := New()
	c.retryDelay = time.Millisecond
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing user agent")
		}
		status, body := handle(r)
		header := make(http.Header)
		if r.URL.Host == "fc.yahoo.com" {
			header.Set("Set-Cookie", "A3=test-cookie; Domain=.yahoo.com; Path=/; Secure")
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	return c
}

func TestQuotesAuthenticationNormalizationAndConcurrentCache(t *testing.T) {
	var requests atomic.Int32
	c := fakeClient(t, func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/":
			return 404, ""
		case "/v1/test/getcrumb":
			if cookie, err := r.Cookie("A3"); err != nil || cookie.Value != "test-cookie" {
				t.Error("crumb request missing cookie")
			}
			return 200, "test/crumb+"
		case "/v7/finance/quote":
			requests.Add(1)
			if r.URL.Query().Get("crumb") != "test/crumb+" || r.URL.Query().Get("symbols") != "MSFT,AAPL,MISSING" {
				t.Errorf("unexpected query: %v", r.URL.Query())
			}
			return 200, quoteFixture
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			return 500, ""
		}
	})
	quotes, err := c.GetQuotes(t.Context(), []string{" msft ", "aapl", "AAPL", "MISSING"})
	if err != nil || len(quotes) != 2 {
		t.Fatalf("quotes=%+v err=%v", quotes, err)
	}
	if quotes[0].Symbol != "MSFT" || quotes[0].Change != 20 || quotes[0].ChangePct != 5 || !quotes[0].Delayed {
		t.Fatalf("MSFT=%+v", quotes[0])
	}
	if quotes[1].Change != 0 || quotes[1].ChangePct != 0 || quotes[1].Delayed || quotes[1].Source != "yahoo" || quotes[1].AsOf != 1788998400 {
		t.Fatalf("AAPL=%+v", quotes[1])
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			q, err := c.GetQuote(t.Context(), "AAPL")
			if err != nil || q.Last != 210.5 {
				t.Errorf("cached quote=%v err=%v", q, err)
			}
		})
	}
	wg.Wait()
	if requests.Load() != 1 {
		t.Fatalf("cache requests=%d", requests.Load())
	}
	// Callers cannot mutate the cached result.
	quotes[1].Last = 1
	q, _ := c.GetQuote(t.Context(), "AAPL")
	if q.Last != 210.5 {
		t.Fatal("cache was mutated")
	}
}

func TestColdConcurrentRequestsCoalesce(t *testing.T) {
	var requests atomic.Int32
	c := fakeClient(t, func(r *http.Request) (int, string) {
		if r.URL.Path == "/" {
			return 404, ""
		}
		if r.URL.Path == "/v1/test/getcrumb" {
			return 200, "crumb"
		}
		requests.Add(1)
		return 200, quoteFixture
	})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			_, err := c.GetQuote(t.Context(), "AAPL")
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if requests.Load() != 1 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

func TestUnauthorizedRefreshAndHostFallback(t *testing.T) {
	var warm, quotes int
	c := fakeClient(t, func(r *http.Request) (int, string) {
		if r.URL.Path == "/" {
			warm++
			return 404, ""
		}
		if r.URL.Path == "/v1/test/getcrumb" {
			return 200, fmt.Sprintf("crumb%d", warm)
		}
		quotes++
		if quotes == 1 {
			return 401, `{"error":"invalid crumb"}`
		}
		if r.URL.Host != "query2.finance.yahoo.com" || r.URL.Query().Get("crumb") != "crumb2" {
			t.Error("credentials/host not refreshed")
		}
		return 200, quoteFixture
	})
	if _, err := c.GetQuote(t.Context(), "AAPL"); err != nil {
		t.Fatal(err)
	}
	if warm != 2 || quotes != 2 {
		t.Fatalf("warm=%d quotes=%d", warm, quotes)
	}
}

func TestRateLimitRetriesAndInvalidCrumbAreBounded(t *testing.T) {
	for _, status := range []int{403, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests int
			c := fakeClient(t, func(r *http.Request) (int, string) {
				if r.URL.Path == "/" {
					return 404, ""
				}
				if r.URL.Path == "/v1/test/getcrumb" {
					return 200, "crumb"
				}
				requests++
				return status, "secret upstream body"
			})
			_, err := c.GetQuote(t.Context(), "AAPL")
			if err == nil || requests != 3 || strings.Contains(err.Error(), "secret") {
				t.Fatalf("requests=%d err=%v", requests, err)
			}
		})
	}
	c := fakeClient(t, func(r *http.Request) (int, string) {
		if r.URL.Path == "/" {
			return 404, ""
		}
		if r.URL.Path == "/v1/test/getcrumb" {
			return 429, "Too Many Requests"
		}
		t.Error("bad crumb was used")
		return 200, quoteFixture
	})
	if _, err := c.GetQuote(t.Context(), "AAPL"); err == nil {
		t.Fatal("expected crumb error")
	}
}

func TestQuoteValidationAndMissingSymbol(t *testing.T) {
	c := fakeClient(t, func(r *http.Request) (int, string) {
		if r.URL.Path == "/" {
			return 404, ""
		}
		if r.URL.Path == "/v1/test/getcrumb" {
			return 200, "crumb"
		}
		return 200, `{"quoteResponse":{"result":[],"error":null}}`
	})
	if _, err := c.GetQuote(t.Context(), "NONE"); !errors.Is(err, market.ErrNotFound) {
		t.Fatal(err)
	}
	for _, symbol := range []string{"A/B", "..%2fA", "A?crumb=evil", strings.Repeat("A", 65)} {
		if _, err := c.GetQuote(t.Context(), symbol); !errors.Is(err, market.ErrInvalidRequest) {
			t.Fatalf("symbol=%s err=%v", symbol, err)
		}
	}
	if _, err := c.GetQuotes(t.Context(), make([]string, 101)); !errors.Is(err, market.ErrInvalidRequest) {
		t.Fatal(err)
	}
	for _, body := range []string{`{}`, `null`, `<html>`, `{"quoteResponse":{"result":[],"error":{"code":"Unauthorized"}}}`} {
		if _, err := parseQuotes([]byte(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestHistoryMappingGapsCacheAndRefresh(t *testing.T) {
	var requests int
	c := fakeClient(t, func(r *http.Request) (int, string) {
		if r.URL.Path == "/" {
			return 404, ""
		}
		if r.URL.Path == "/v1/test/getcrumb" {
			return 200, "crumb"
		}
		requests++
		if r.URL.Path != "/v8/finance/chart/0700.HK" || r.URL.Query().Get("range") != "1mo" || r.URL.Query().Get("interval") != "1h" {
			t.Errorf("unexpected history request: %s", r.URL.Path)
		}
		return 200, chartFixture
	})
	for _, refresh := range []bool{false, false, true} {
		bars, err := c.GetHistory(t.Context(), "0700.hk", "1m", "1h", refresh)
		if err != nil || len(bars) != 2 || bars[0].Time != 100 || bars[1].Close != 13 || bars[1].Volume != 0 {
			t.Fatalf("bars=%+v err=%v", bars, err)
		}
		bars[0].Close = 999
	}
	if requests != 2 {
		t.Fatalf("requests=%d", requests)
	}
	for _, pair := range [][2]string{{"invalid", "1d"}, {"1m", "1min"}, {"3m", "5min"}, {"5y", "1h"}} {
		if _, err := c.GetHistory(t.Context(), "AAPL", pair[0], pair[1], false); !errors.Is(err, market.ErrInvalidRequest) {
			t.Fatalf("pair=%v err=%v", pair, err)
		}
	}
}

func TestCanceledWaitDoesNotBlock(t *testing.T) {
	c := New()
	c.gate <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.GetQuote(ctx, "AAPL"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	<-c.gate
}

// Opt-in live smoke test; ordinary test runs never contact Yahoo.
func TestLiveYahoo(t *testing.T) {
	if os.Getenv("TRAIO_TEST_YAHOO_LIVE") != "1" {
		t.Skip("set TRAIO_TEST_YAHOO_LIVE=1 to contact Yahoo")
	}
	c := New()
	quotes, err := c.GetQuotes(t.Context(), []string{"AAPL", "MSFT", "0700.HK"})
	if err != nil {
		t.Fatal(err)
	}
	if len(quotes) != 3 {
		t.Fatalf("expected three quotes, got %d", len(quotes))
	}
	for _, q := range quotes {
		t.Logf("symbol=%s price=%g currency=%s source=%s as_of=%d delayed=%v", q.Symbol, q.Last, q.Currency, q.Source, q.AsOf, q.Delayed)
	}
	bars, err := c.GetHistory(t.Context(), "AAPL", "1m", "1d", false)
	if err != nil || len(bars) == 0 {
		t.Fatalf("bars=%d err=%v", len(bars), err)
	}
	t.Logf("AAPL candles=%d last_time=%s", len(bars), time.Unix(bars[len(bars)-1].Time, 0).UTC())
}
