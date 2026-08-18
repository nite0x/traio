package schwab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nite/traio/internal/broker"
)

type accountEnvelope struct {
	SecuritiesAccount securitiesAccount `json:"securitiesAccount"`
}

type securitiesAccount struct {
	AccountNumber   string           `json:"accountNumber"`
	Type            string           `json:"type"`
	Positions       []schwabPosition `json:"positions"`
	CurrentBalances currentBalances  `json:"currentBalances"`
}

type schwabPosition struct {
	ShortQuantity           float64          `json:"shortQuantity"`
	AveragePrice            float64          `json:"averagePrice"`
	CurrentDayProfitLoss    *float64         `json:"currentDayProfitLoss"`
	CurrentDayProfitLossPct *float64         `json:"currentDayProfitLossPercentage"`
	LongQuantity            float64          `json:"longQuantity"`
	SettledLongQuantity     float64          `json:"settledLongQuantity"`
	SettledShortQuantity    float64          `json:"settledShortQuantity"`
	Instrument              schwabInstrument `json:"instrument"`
	MarketValue             float64          `json:"marketValue"`
	LongOpenProfitLoss      float64          `json:"longOpenProfitLoss"`
	ShortOpenProfitLoss     float64          `json:"shortOpenProfitLoss"`
	TaxLotAverageLongPrice  float64          `json:"taxLotAverageLongPrice"`
	TaxLotAverageShortPrice float64          `json:"taxLotAverageShortPrice"`
	PreviousSessionLongQty  float64          `json:"previousSessionLongQuantity"`
	PreviousSessionShortQty float64          `json:"previousSessionShortQuantity"`
	CurrentDayCost          float64          `json:"currentDayCost"`
	MaintenanceRequirement  float64          `json:"maintenanceRequirement"`
	AverageLongPrice        float64          `json:"averageLongPrice"`
	AverageShortPrice       float64          `json:"averageShortPrice"`
}

type schwabInstrument struct {
	AssetType    string `json:"assetType"`
	CUSIP        string `json:"cusip"`
	Symbol       string `json:"symbol"`
	Description  string `json:"description"`
	InstrumentID int64  `json:"instrumentId"`
}

type currentBalances struct {
	CashBalance             float64 `json:"cashBalance"`
	LiquidationValue        float64 `json:"liquidationValue"`
	LongMarketValue         float64 `json:"longMarketValue"`
	ShortMarketValue        float64 `json:"shortMarketValue"`
	Equity                  float64 `json:"equity"`
	MoneyMarketFund         float64 `json:"moneyMarketFund"`
	MutualFundValue         float64 `json:"mutualFundValue"`
	AvailableFunds          float64 `json:"availableFunds"`
	BuyingPower             float64 `json:"buyingPower"`
	CashAvailableForTrading float64 `json:"cashAvailableForTrading"`
	MaintenanceRequirement  float64 `json:"maintenanceRequirement"`
	RegTCall                float64 `json:"regTCall"`
	DayTradingBuyingPower   float64 `json:"dayTradingBuyingPower"`
	UnrealizedProfitLoss    float64 `json:"unrealizedProfitLoss"`
}

var _ broker.Broker = (*Client)(nil)

func (c *Client) BeginLogin(context.Context) (broker.LoginAction, error) {
	_, authenticated := c.Token()
	return broker.LoginAction{URL: c.AuthURL(""), Authenticated: authenticated}, nil
}

func (c *Client) ListAccounts(ctx context.Context) ([]broker.Account, error) {
	accounts, err := c.accounts(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]broker.Account, 0, len(accounts))
	for _, envelope := range accounts {
		account := envelope.SecuritiesAccount
		if strings.TrimSpace(account.AccountNumber) == "" {
			continue
		}
		out = append(out, broker.Account{
			ID: account.AccountNumber, Broker: "SCHWAB", DisplayName: account.AccountNumber,
			AccountType: account.Type, Status: "open", BaseCurrency: "USD",
		})
	}
	return out, nil
}

func (c *Client) GetAccount(ctx context.Context, accountID string) (broker.Account, error) {
	accounts, err := c.ListAccounts(ctx)
	if err != nil {
		return broker.Account{}, err
	}
	for _, account := range accounts {
		if account.ID == accountID {
			return account, nil
		}
	}
	return broker.Account{}, fmt.Errorf("schwab: account %s not found", accountID)
}

func (c *Client) GetCashBalances(ctx context.Context, accountID string) ([]broker.CashBalance, error) {
	account, err := c.findAccount(ctx, accountID, false)
	if err != nil {
		return nil, err
	}
	return []broker.CashBalance{{
		AccountID: accountID, Currency: "USD",
		Total:   account.CurrentBalances.CashBalance + account.CurrentBalances.MoneyMarketFund,
		Settled: account.CurrentBalances.CashBalance, ExchangeRate: 1,
		IsBaseCurrency: true, AsOf: time.Now().UTC().Format(time.RFC3339),
	}}, nil
}

func (c *Client) ListAccountPositions(ctx context.Context, accountID string) ([]broker.Position, error) {
	positions, err := c.ListPositions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]broker.Position, 0, len(positions))
	for _, position := range positions {
		if position.Account == accountID {
			out = append(out, position)
		}
	}
	return out, nil
}

func (c *Client) GetDailyPerformance(ctx context.Context, accountID string) (broker.DailyPerformance, error) {
	account, err := c.findAccount(ctx, accountID, true)
	if err != nil {
		return broker.DailyPerformance{}, err
	}
	performance := broker.DailyPerformance{
		AccountID:       accountID,
		NetLiquidation:  firstNonZero(account.CurrentBalances.LiquidationValue, account.CurrentBalances.Equity),
		UnrealizedPnL:   account.CurrentBalances.UnrealizedProfitLoss,
		ExcessLiquidity: account.CurrentBalances.AvailableFunds,
		MarketValue:     account.CurrentBalances.LongMarketValue + account.CurrentBalances.ShortMarketValue,
		AsOf:            time.Now().UTC().Format(time.RFC3339),
	}
	for _, position := range account.Positions {
		if position.CurrentDayProfitLoss != nil {
			performance.DailyPnL += *position.CurrentDayProfitLoss
		}
	}
	return performance, nil
}

func (c *Client) findAccount(ctx context.Context, accountID string, positions bool) (securitiesAccount, error) {
	accounts, err := c.accounts(ctx, positions)
	if err != nil {
		return securitiesAccount{}, err
	}
	for _, envelope := range accounts {
		if envelope.SecuritiesAccount.AccountNumber == accountID {
			return envelope.SecuritiesAccount, nil
		}
	}
	return securitiesAccount{}, fmt.Errorf("schwab: account %s not found", accountID)
}

func (c *Client) accounts(ctx context.Context, positions bool) ([]accountEnvelope, error) {
	c.mu.RLock()
	endpoint := c.traderURL + "/accounts"
	c.mu.RUnlock()
	if positions {
		endpoint += "?" + url.Values{"fields": {"positions"}}.Encode()
	}
	resp, err := c.Do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var accounts []accountEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&accounts); err != nil {
		return nil, fmt.Errorf("schwab: decode accounts: %w", err)
	}
	return accounts, nil
}

func (c *Client) ListPositions(ctx context.Context) ([]broker.Position, error) {
	accounts, err := c.accounts(ctx, true)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	out := make([]broker.Position, 0)
	for _, envelope := range accounts {
		account := envelope.SecuritiesAccount
		for _, position := range account.Positions {
			quantity := position.LongQuantity - position.ShortQuantity
			if quantity == 0 {
				continue
			}
			averagePrice := position.AveragePrice
			if averagePrice == 0 {
				if quantity > 0 {
					averagePrice = firstNonZero(position.AverageLongPrice, position.TaxLotAverageLongPrice)
				} else {
					averagePrice = firstNonZero(position.AverageShortPrice, position.TaxLotAverageShortPrice)
				}
			}
			marketPrice := 0.0
			if quantity != 0 {
				marketPrice = position.MarketValue / quantity
			}
			out = append(out, broker.Position{
				ExternalID:  schwabInstrumentExternalID(position.Instrument),
				AssetType:   strings.ToLower(strings.TrimSpace(position.Instrument.AssetType)),
				Market:      "US",
				Symbol:      strings.ToUpper(position.Instrument.Symbol),
				Name:        strings.TrimSpace(position.Instrument.Description),
				ConID:       position.Instrument.InstrumentID,
				Quantity:    quantity,
				AvgCost:     averagePrice,
				MarketPrice: marketPrice,
				MarketValue: position.MarketValue,
				Unrealized:  position.LongOpenProfitLoss + position.ShortOpenProfitLoss,
				DailyPnL:    position.CurrentDayProfitLoss,
				DailyPnLPct: position.CurrentDayProfitLossPct,
				Currency:    "USD",
				Account:     account.AccountNumber,
				Broker:      "SCHWAB",
				SyncedAt:    now,
			})
		}
	}
	return out, nil
}

func schwabInstrumentExternalID(instrument schwabInstrument) string {
	if cusip := strings.TrimSpace(instrument.CUSIP); cusip != "" {
		return cusip
	}
	if instrument.InstrumentID != 0 {
		return fmt.Sprintf("%d", instrument.InstrumentID)
	}
	return ""
}

func (c *Client) AccountSummary(ctx context.Context) (broker.AccountSummary, error) {
	accounts, err := c.accounts(ctx, false)
	if err != nil {
		return broker.AccountSummary{}, err
	}
	summary := broker.AccountSummary{
		Currency: "USD",
		Broker:   "SCHWAB",
		AsOf:     time.Now().UTC().Format(time.RFC3339),
	}
	for i, envelope := range accounts {
		account := envelope.SecuritiesAccount
		balances := account.CurrentBalances
		if i == 0 {
			summary.AccountID = account.AccountNumber
		}
		summary.NetLiquidation += firstNonZero(balances.LiquidationValue, balances.Equity)
		summary.TotalCashValue += balances.CashBalance + balances.MoneyMarketFund
		summary.GrossPositionValue += balances.LongMarketValue + balances.ShortMarketValue
		summary.UnrealizedPnL += balances.UnrealizedProfitLoss
		summary.BuyingPower += firstNonZero(balances.BuyingPower, balances.CashAvailableForTrading, balances.AvailableFunds)
	}
	return summary, nil
}

func (c *Client) HistoricalEquity(context.Context) ([]broker.AccountEquityPoint, error) {
	return []broker.AccountEquityPoint{}, nil
}

func firstNonZero(values ...float64) float64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
