package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/market"
)

type publicMarketStub struct {
	calls       int
	err         error
	period, bar string
	refresh     bool
}

func (s *publicMarketStub) GetQuote(context.Context, string) (*broker.Quote, error) {
	s.calls++
	return &broker.Quote{Symbol: "AAPL", Last: 210, Source: "yahoo", Currency: "USD"}, s.err
}
func (s *publicMarketStub) GetQuotes(context.Context, []string) ([]broker.Quote, error) {
	s.calls++
	return []broker.Quote{{Symbol: "AAPL", Last: 210, Source: "yahoo"}}, s.err
}
func (s *publicMarketStub) GetHistory(_ context.Context, _ string, period, bar string, refresh bool) ([]broker.Candle, error) {
	s.calls++
	s.period = period
	s.bar = bar
	s.refresh = refresh
	return []broker.Candle{{Time: 100, Open: 200, High: 211, Low: 199, Close: 210}}, s.err
}

func TestYahooRoutesWorkWithoutBrokerAndKeepBrokerOptIn(t *testing.T) {
	provider := &publicMarketStub{}
	router := NewRouter(Deps{PublicMarketData: provider}, ServerControl{})
	for _, path := range []string{"/api/v1/quotes/AAPL", "/api/v1/quotes/symbols?symbols=AAPL,MSFT", "/api/v1/quotes/AAPL/history?period=1m&refresh=1"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 200 || rec.Header().Get("X-Market-Data-Source") != "yahoo" {
			t.Fatalf("path=%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		if !json.Valid(rec.Body.Bytes()) {
			t.Fatal("invalid JSON")
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("market responses must not be cached by browsers or proxies")
		}
	}
	if provider.period != "1m" || provider.bar != "1h" || !provider.refresh {
		t.Fatalf("history options=%+v", provider)
	}
	for _, path := range []string{"/api/v1/quotes/AAPL?source=broker", "/api/v1/quotes/symbols?symbols=AAPL&source=broker", "/api/v1/quotes/AAPL/history?source=broker", "/api/v1/quotes?conids=265598"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 503 {
			t.Fatalf("broker path=%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	if provider.calls != 3 {
		t.Fatalf("broker requests unexpectedly called Yahoo: %d", provider.calls)
	}
}

func TestYahooRouteErrorsAndAuthentication(t *testing.T) {
	provider := &publicMarketStub{}
	router := NewRouter(Deps{PublicMarketData: provider, APIToken: "test-token"}, ServerControl{})
	request := func(path, token string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		router.ServeHTTP(rec, req)
		return rec
	}
	if rec := request("/api/v1/quotes/AAPL", ""); rec.Code != 401 {
		t.Fatalf("unauthenticated status=%d", rec.Code)
	}
	if provider.calls != 0 {
		t.Fatal("unauthenticated request reached provider")
	}
	if rec := request("/api/v1/quotes/AAPL?source=typo", "test-token"); rec.Code != 400 {
		t.Fatalf("bad source status=%d", rec.Code)
	}
	for _, test := range []struct {
		err    error
		status int
	}{{market.ErrInvalidRequest, 400}, {market.ErrNotFound, 404}, {errors.New("yahoo: upstream HTTP 429"), 502}} {
		provider.err = test.err
		if rec := request("/api/v1/quotes/AAPL?source=yahoo", "test-token"); rec.Code != test.status {
			t.Fatalf("error=%v status=%d", test.err, rec.Code)
		}
	}
}
