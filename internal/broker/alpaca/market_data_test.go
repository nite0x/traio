package alpaca

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/market"
)

func TestGetQuoteUsesMarketDataSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/stocks/AAPL/snapshot" || r.Header.Get("APCA-API-KEY-ID") != "key" || r.Header.Get("APCA-API-SECRET-KEY") != "secret" {
			t.Fatalf("unexpected request path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"latestTrade":{"p":210,"t":"2026-09-20T10:00:00Z"},"latestQuote":{"bp":209,"ap":211,"t":"2026-09-20T10:00:00Z"},"dailyBar":{"h":212,"l":198,"v":1000},"prevDailyBar":{"c":200}}`))
	}))
	defer server.Close()
	client := New(config.AlpacaConfig{APIKey: "key", APISecret: "secret", BaseURL: server.URL, DataBaseURL: server.URL})
	quote, err := client.GetQuote(t.Context(), "aapl")
	if err != nil {
		t.Fatal(err)
	}
	if quote.Last != 210 || quote.Source != "alpaca" || quote.Status != market.StatusOfficial || quote.Change != 10 || quote.AsOf == 0 {
		t.Fatalf("quote=%+v", quote)
	}
}
