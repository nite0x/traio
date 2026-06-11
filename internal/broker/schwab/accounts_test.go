package schwab

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestListPositionsAndAccountSummary(t *testing.T) {
	client := testClient(func(r *http.Request) *http.Response {
		return jsonResponse(http.StatusOK, `[{
			"securitiesAccount": {
				"accountNumber": "123456",
				"type": "MARGIN",
				"positions": [{
					"longQuantity": 10,
					"averagePrice": 100,
					"marketValue": 1250,
					"longOpenProfitLoss": 250,
					"instrument": {"symbol": "AAPL", "assetType": "EQUITY"}
				}],
				"currentBalances": {
					"cashBalance": 500,
					"liquidationValue": 1750,
					"longMarketValue": 1250,
					"buyingPower": 1000,
					"unrealizedProfitLoss": 250
				}
			}
		}]`)
	})
	client.traderURL = "https://api.test/trader"
	client.SetToken(Token{AccessToken: "access", ExpiresAt: time.Now().Add(time.Hour)})

	positions, err := client.ListPositions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 || positions[0].Symbol != "AAPL" ||
		positions[0].MarketPrice != 125 || positions[0].Broker != "SCHWAB" {
		t.Fatalf("unexpected positions: %+v", positions)
	}

	summary, err := client.AccountSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.AccountID != "123456" || summary.NetLiquidation != 1750 ||
		summary.UnrealizedPnL != 250 || summary.Broker != "SCHWAB" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}
