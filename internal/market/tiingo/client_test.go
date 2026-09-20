package tiingo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nite/traio/internal/market"
)

func TestClientKeepsTokenOutOfURLAndUsesAdjustedHistory(t *testing.T) {
	const token = "tiingo-test-token-never-log"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.String(), token) {
			t.Fatal("token leaked into URL")
		}
		if r.Header.Get("Authorization") != "Token "+token {
			t.Errorf("missing token header")
		}
		switch {
		case r.URL.Path == "/iex/AAPL":
			_, _ = w.Write([]byte(`[{"ticker":"AAPL","timestamp":"2026-09-20T10:00:00Z","tngoLast":210,"prevClose":200,"bidPrice":209,"askPrice":211,"volume":12,"high":212,"low":198}]`))
		case r.URL.Path == "/tiingo/daily/AAPL/prices":
			_, _ = w.Write([]byte(`[{"date":"2026-09-19T00:00:00Z","open":100,"high":110,"low":90,"close":105,"volume":10,"adjOpen":50,"adjHigh":55,"adjLow":45,"adjClose":52.5,"adjVolume":20}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(Config{APIToken: token, BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	quote, err := client.GetQuote(t.Context(), "aapl")
	if err != nil || quote.Last != 210 || quote.Source != "tiingo" || !quote.Delayed {
		t.Fatalf("quote=%+v err=%v", quote, err)
	}
	candles, err := client.GetHistory(t.Context(), "AAPL", "1y", "1d", false)
	if err != nil || len(candles) != 1 || candles[0].Open != 50 || candles[0].Close != 52.5 || candles[0].Volume != 20 {
		t.Fatalf("candles=%+v err=%v", candles, err)
	}
	valuation, err := client.GetValuationHistory(t.Context(), "AAPL", "1y", "1d", false)
	if err != nil || len(valuation) != 1 || valuation[0].Open != 100 || valuation[0].Close != 105 || valuation[0].Volume != 10 {
		t.Fatalf("valuation candles=%+v err=%v", valuation, err)
	}
}

func TestClientClassifiesErrorsWithoutLeakingToken(t *testing.T) {
	const token = "tiingo-secret-auth-token"
	for _, test := range []struct {
		status int
		code   market.ErrorCode
	}{
		{http.StatusUnauthorized, market.ErrorAuthentication},
		{http.StatusForbidden, market.ErrorPermission},
		{http.StatusNotFound, market.ErrorSecurityNotFound},
		{http.StatusTooManyRequests, market.ErrorRateLimited},
		{http.StatusServiceUnavailable, market.ErrorUpstreamUnavailable},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"detail":"token `+token+` invalid"}`, test.status)
			}))
			defer server.Close()
			client, err := New(Config{APIToken: token, BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.GetQuote(t.Context(), "AAPL")
			code, provider := market.ErrorDetails(err)
			if code != test.code || provider != "tiingo" || strings.Contains(err.Error(), token) {
				t.Fatalf("code=%s provider=%s err=%v", code, provider, err)
			}
		})
	}
	_, err := New(Config{})
	if code, provider := market.ErrorDetails(err); code != market.ErrorNotConfigured || provider != "tiingo" {
		t.Fatalf("unconfigured code=%s provider=%s err=%v", code, provider, err)
	}
}
