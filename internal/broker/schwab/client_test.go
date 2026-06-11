package schwab

import (
	"context"
	"net/http"
	"testing"
)

func TestGetQuote(t *testing.T) {
	client := testClient(func(r *http.Request) *http.Response {
		if r.URL.Path != "/market/quotes" || r.URL.Query().Get("symbols") != "AAPL" {
			t.Fatalf("unexpected quote request: %s", r.URL)
		}
		return jsonResponse(http.StatusOK, `{
			"AAPL": {
				"symbol": "AAPL",
				"realtime": true,
				"quote": {
					"bidPrice": 201.1,
					"askPrice": 201.2,
					"lastPrice": 201.15,
					"highPrice": 205,
					"lowPrice": 198,
					"netChange": 1.25,
					"netPercentChange": 0.625,
					"totalVolume": 12345
				}
			}
		}`)
	})
	client.marketURL = "https://api.test/market"
	client.SetToken(Token{AccessToken: "access"})

	quote, err := client.GetQuote(context.Background(), "aapl")
	if err != nil {
		t.Fatal(err)
	}
	if quote.Symbol != "AAPL" || quote.Last != 201.15 || quote.High != 205 ||
		quote.ChangePct != 0.625 || quote.Volume != 12345 || quote.Delayed {
		t.Fatalf("unexpected quote: %+v", quote)
	}
}
