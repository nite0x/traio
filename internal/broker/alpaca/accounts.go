package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nite/traio/internal/broker"
)

type alpacaAccount struct {
	ID               string `json:"id"`
	AccountNumber    string `json:"account_number"`
	Status           string `json:"status"`
	Currency         string `json:"currency"`
	Cash             string `json:"cash"`
	PortfolioValue   string `json:"portfolio_value"`
	Equity           string `json:"equity"`
	LongMarketValue  string `json:"long_market_value"`
	ShortMarketValue string `json:"short_market_value"`
	BuyingPower      string `json:"buying_power"`
	LastEquity       string `json:"last_equity"`
}

type alpacaPosition struct {
	Symbol         string `json:"symbol"`
	Qty            string `json:"qty"`
	AvgEntryPrice  string `json:"avg_entry_price"`
	MarketValue    string `json:"market_value"`
	CostBasis      string `json:"cost_basis"`
	UnrealizedPL   string `json:"unrealized_pl"`
	CurrentPrice   string `json:"current_price"`
	Side           string `json:"side"`
}

type portfolioHistory struct {
	Timestamp []int64   `json:"timestamp"`
	Equity    []float64 `json:"equity"`
}

func (c *Client) ListPositions(ctx context.Context) ([]broker.Position, error) {
	if !c.Configured() {
		return []broker.Position{}, nil
	}
	resp, err := c.Do(ctx, http.MethodGet, "positions", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw []alpacaPosition
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("alpaca: decode positions: %w", err)
	}

	account, err := c.fetchAccount(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	out := make([]broker.Position, 0, len(raw))
	for _, position := range raw {
		quantity, err := parseDecimal(position.Qty)
		if err != nil || quantity == 0 {
			continue
		}
		if strings.EqualFold(position.Side, "short") && quantity > 0 {
			quantity = -quantity
		}
		out = append(out, broker.Position{
			Symbol:      strings.ToUpper(position.Symbol),
			Quantity:    quantity,
			AvgCost:     parseDecimalOrZero(position.AvgEntryPrice),
			MarketPrice: parseDecimalOrZero(position.CurrentPrice),
			MarketValue: parseDecimalOrZero(position.MarketValue),
			Unrealized:  parseDecimalOrZero(position.UnrealizedPL),
			Currency:    firstNonEmpty(account.Currency, "USD"),
			Account:     firstNonEmpty(account.AccountNumber, account.ID),
			Broker:      "ALPACA",
			SyncedAt:    now,
		})
	}
	return out, nil
}

func (c *Client) AccountSummary(ctx context.Context) (broker.AccountSummary, error) {
	if !c.Configured() {
		return broker.AccountSummary{}, nil
	}
	account, err := c.fetchAccount(ctx)
	if err != nil {
		return broker.AccountSummary{}, err
	}
	return broker.AccountSummary{
		AccountID:          firstNonEmpty(account.AccountNumber, account.ID),
		Currency:           firstNonEmpty(account.Currency, "USD"),
		NetLiquidation:     firstNonZero(parseDecimalOrZero(account.Equity), parseDecimalOrZero(account.PortfolioValue)),
		TotalCashValue:     parseDecimalOrZero(account.Cash),
		GrossPositionValue: parseDecimalOrZero(account.LongMarketValue) + parseDecimalOrZero(account.ShortMarketValue),
		BuyingPower:        parseDecimalOrZero(account.BuyingPower),
		Broker:             "ALPACA",
		AsOf:               time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (c *Client) HistoricalEquity(ctx context.Context) ([]broker.AccountEquityPoint, error) {
	if !c.Configured() {
		return []broker.AccountEquityPoint{}, nil
	}
	query := url.Values{
		"period":    {"1A"},
		"timeframe": {"1D"},
	}
	resp, err := c.Do(ctx, http.MethodGet, "account/portfolio/history?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw portfolioHistory
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("alpaca: decode portfolio history: %w", err)
	}

	out := make([]broker.AccountEquityPoint, 0, len(raw.Timestamp))
	for i, ts := range raw.Timestamp {
		if i >= len(raw.Equity) || raw.Equity[i] == 0 {
			continue
		}
		out = append(out, broker.AccountEquityPoint{
			Time:   time.Unix(ts, 0).UTC().Format("2006-01-02"),
			Value:  raw.Equity[i],
			Source: "ALPACA",
		})
	}
	return out, nil
}

func (c *Client) PlaceOrder(context.Context, broker.OrderRequest) (string, error) {
	return "", fmt.Errorf("alpaca: order placement is not implemented")
}

func (c *Client) fetchAccount(ctx context.Context) (alpacaAccount, error) {
	resp, err := c.Do(ctx, http.MethodGet, "account", nil)
	if err != nil {
		return alpacaAccount{}, err
	}
	defer resp.Body.Close()

	var account alpacaAccount
	if err := json.NewDecoder(resp.Body).Decode(&account); err != nil {
		return alpacaAccount{}, fmt.Errorf("alpaca: decode account: %w", err)
	}
	return account, nil
}

func parseDecimal(value string) (float64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	return strconv.ParseFloat(value, 64)
}

func parseDecimalOrZero(value string) float64 {
	parsed, err := parseDecimal(value)
	if err != nil {
		return 0
	}
	return parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonZero(values ...float64) float64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
