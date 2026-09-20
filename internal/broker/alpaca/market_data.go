package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/market"
)

func (c *Client) GetQuote(ctx context.Context, symbol string) (*broker.Quote, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return nil, fmt.Errorf("%w: symbol is required", market.ErrInvalidRequest)
	}
	c.mu.RLock()
	keyID, secret, dataBaseURL := c.cfg.APIKey, c.cfg.APISecret, c.cfg.DataBaseURL
	c.mu.RUnlock()
	if keyID == "" || secret == "" {
		return nil, market.NewProviderError(market.ErrorNotConfigured, "alpaca", "quote", "API credentials are not configured", nil)
	}
	endpoint := strings.TrimRight(dataBaseURL, "/") + "/v2/stocks/" + url.PathEscape(symbol) + "/snapshot"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, market.NewProviderError(market.ErrorInvalidRequest, "alpaca", "quote", "cannot build request", err)
	}
	req.Header.Set("APCA-API-KEY-ID", keyID)
	req.Header.Set("APCA-API-SECRET-KEY", secret)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, market.NewProviderError(market.ErrorUpstreamUnavailable, "alpaca", "quote", "upstream request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, market.NewProviderError(market.ErrorAuthentication, "alpaca", "quote", "authentication failed", nil)
		case http.StatusForbidden:
			return nil, market.NewProviderError(market.ErrorPermission, "alpaca", "quote", "market data permission denied", nil)
		case http.StatusNotFound:
			return nil, market.NewProviderError(market.ErrorSecurityNotFound, "alpaca", "quote", "security not found", market.ErrNotFound)
		case http.StatusTooManyRequests:
			return nil, market.NewProviderError(market.ErrorRateLimited, "alpaca", "quote", "rate limited", nil)
		default:
			return nil, market.NewProviderError(market.ErrorUpstreamUnavailable, "alpaca", "quote", fmt.Sprintf("upstream returned HTTP %d", resp.StatusCode), nil)
		}
	}
	var raw struct {
		LatestTrade struct {
			Price float64 `json:"p"`
			Time  string  `json:"t"`
		} `json:"latestTrade"`
		LatestQuote struct {
			Ask  float64 `json:"ap"`
			Bid  float64 `json:"bp"`
			Time string  `json:"t"`
		} `json:"latestQuote"`
		DailyBar struct {
			High   float64 `json:"h"`
			Low    float64 `json:"l"`
			Volume int64   `json:"v"`
		} `json:"dailyBar"`
		PrevDailyBar struct {
			Close float64 `json:"c"`
		} `json:"prevDailyBar"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return nil, market.NewProviderError(market.ErrorUpstreamUnavailable, "alpaca", "quote", "invalid upstream response", err)
	}
	if raw.LatestTrade.Price <= 0 {
		return nil, market.NewProviderError(market.ErrorUpstreamUnavailable, "alpaca", "quote", "quote has no usable price", nil)
	}
	asOf := parseMarketTime(raw.LatestTrade.Time)
	if asOf == 0 {
		asOf = parseMarketTime(raw.LatestQuote.Time)
	}
	change, changePct := 0.0, 0.0
	if raw.PrevDailyBar.Close > 0 {
		change = raw.LatestTrade.Price - raw.PrevDailyBar.Close
		changePct = change / raw.PrevDailyBar.Close * 100
	}
	return &broker.Quote{Symbol: symbol, Last: raw.LatestTrade.Price, Bid: raw.LatestQuote.Bid, Ask: raw.LatestQuote.Ask,
		Change: change, ChangePct: changePct, High: raw.DailyBar.High, Low: raw.DailyBar.Low, Volume: raw.DailyBar.Volume,
		Source: "alpaca", Status: market.StatusOfficial, Currency: "USD", AsOf: asOf, AdjustmentType: market.AdjustmentNone}, nil
}

func parseMarketTime(value string) int64 {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0
	}
	return parsed.Unix()
}
