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
					"currentDayProfitLoss": 50,
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

	accounts, err := client.ListAccounts(context.Background())
	if err != nil || len(accounts) != 1 || accounts[0].ID != "123456" {
		t.Fatalf("unexpected accounts: accounts=%+v err=%v", accounts, err)
	}
	balances, err := client.GetCashBalances(context.Background(), "123456")
	if err != nil || len(balances) != 1 || balances[0].Total != 500 {
		t.Fatalf("unexpected cash balances: balances=%+v err=%v", balances, err)
	}
	accountPositions, err := client.ListAccountPositions(context.Background(), "123456")
	if err != nil || len(accountPositions) != 1 || accountPositions[0].Symbol != "AAPL" {
		t.Fatalf("unexpected account positions: positions=%+v err=%v", accountPositions, err)
	}
	performance, err := client.GetDailyPerformance(context.Background(), "123456")
	if err != nil || performance.DailyPnL != 50 || performance.NetLiquidation != 1750 {
		t.Fatalf("unexpected daily performance: performance=%+v err=%v", performance, err)
	}
}
