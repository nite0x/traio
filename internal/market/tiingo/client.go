package tiingo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/market"
)

const DefaultBaseURL = "https://api.tiingo.com"

type Config struct {
	APIToken string
	BaseURL  string
}

type Option func(*Client)

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.http = client
		}
	}
}

// Client intentionally keeps only the decrypted token and parsed responses in
// memory. Raw Tiingo payloads never enter Traio's persistent candle cache.
type Client struct {
	token   string
	baseURL string
	http    *http.Client
}

func New(config Config, options ...Option) (*Client, error) {
	token := strings.TrimSpace(config.APIToken)
	if token == "" {
		return nil, market.NewProviderError(market.ErrorNotConfigured, "tiingo", "configure", "API token is not configured", nil)
	}
	baseURL, err := NormalizeBaseURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	client := &Client{token: token, baseURL: baseURL, http: &http.Client{Timeout: 12 * time.Second}}
	for _, option := range options {
		option(client)
	}
	return client, nil
}

func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultBaseURL
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: Tiingo base_url must be an HTTP(S) URL without credentials, query, or fragment", market.ErrInvalidRequest)
	}
	return strings.TrimRight(raw, "/"), nil
}

func (c *Client) GetQuote(ctx context.Context, symbol string) (*broker.Quote, error) {
	symbol, err := normalizeSymbol(symbol)
	if err != nil {
		return nil, err
	}
	endpoint := c.baseURL + "/iex/" + url.PathEscape(symbol)
	var raw []struct {
		Ticker            string   `json:"ticker"`
		Timestamp         string   `json:"timestamp"`
		LastSaleTimestamp string   `json:"lastSaleTimestamp"`
		TngoLast          *float64 `json:"tngoLast"`
		Last              *float64 `json:"last"`
		PrevClose         *float64 `json:"prevClose"`
		BidPrice          *float64 `json:"bidPrice"`
		AskPrice          *float64 `json:"askPrice"`
		High              *float64 `json:"high"`
		Low               *float64 `json:"low"`
		Volume            int64    `json:"volume"`
	}
	if err := c.getJSON(ctx, endpoint, nil, "quote", &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, market.NewProviderError(market.ErrorSecurityNotFound, "tiingo", "quote", "security not found", market.ErrNotFound)
	}
	item := raw[0]
	last := firstFloat(item.TngoLast, item.Last)
	if last <= 0 {
		return nil, market.NewProviderError(market.ErrorUpstreamUnavailable, "tiingo", "quote", "quote has no usable price", nil)
	}
	asOf := parseTime(item.LastSaleTimestamp)
	if asOf == 0 {
		asOf = parseTime(item.Timestamp)
	}
	change, changePct := 0.0, 0.0
	if item.PrevClose != nil && *item.PrevClose > 0 {
		change = last - *item.PrevClose
		changePct = change / *item.PrevClose * 100
	}
	return &broker.Quote{
		Symbol: strings.ToUpper(firstString(item.Ticker, symbol)), Last: last,
		Bid: floatValue(item.BidPrice), Ask: floatValue(item.AskPrice),
		Change: change, ChangePct: changePct, Volume: item.Volume,
		High: floatValue(item.High), Low: floatValue(item.Low), Delayed: true,
		Source: "tiingo", Status: market.StatusEstimated, Currency: "USD", AsOf: asOf,
		AdjustmentType: market.AdjustmentNone,
	}, nil
}

func (c *Client) GetQuotes(ctx context.Context, symbols []string) ([]broker.Quote, error) {
	if len(symbols) > 100 {
		return nil, fmt.Errorf("%w: at most 100 symbols per request", market.ErrInvalidRequest)
	}
	out := make([]broker.Quote, 0, len(symbols))
	seen := map[string]bool{}
	var lastErr error
	for _, symbol := range symbols {
		normalized, err := normalizeSymbol(symbol)
		if err != nil {
			return nil, err
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		quote, err := c.GetQuote(ctx, normalized)
		if err != nil {
			lastErr = err
			continue
		}
		out = append(out, *quote)
	}
	if len(out) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}

func (c *Client) GetHistory(ctx context.Context, symbol, period, bar string, _ bool) ([]broker.Candle, error) {
	return c.getHistory(ctx, symbol, period, bar, true)
}

func (c *Client) GetValuationHistory(ctx context.Context, symbol, period, bar string, _ bool) ([]broker.Candle, error) {
	return c.getHistory(ctx, symbol, period, bar, false)
}

func (c *Client) getHistory(ctx context.Context, symbol, period, bar string, adjusted bool) ([]broker.Candle, error) {
	symbol, err := normalizeSymbol(symbol)
	if err != nil {
		return nil, err
	}
	start, err := periodStart(period, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	resample, intraday, err := tiingoBar(bar)
	if err != nil {
		return nil, err
	}
	path := "/tiingo/daily/" + url.PathEscape(symbol) + "/prices"
	if intraday {
		path = "/iex/" + url.PathEscape(symbol) + "/prices"
	}
	query := url.Values{
		"startDate":    {start.Format("2006-01-02")},
		"resampleFreq": {resample},
	}
	if intraday {
		query.Set("columns", "open,high,low,close,volume")
	} else {
		query.Set("endDate", time.Now().UTC().Format("2006-01-02"))
		query.Set("columns", "open,high,low,close,volume,adjOpen,adjHigh,adjLow,adjClose,adjVolume")
	}
	var raw []struct {
		Date      string   `json:"date"`
		Open      *float64 `json:"open"`
		High      *float64 `json:"high"`
		Low       *float64 `json:"low"`
		Close     *float64 `json:"close"`
		Volume    *int64   `json:"volume"`
		AdjOpen   *float64 `json:"adjOpen"`
		AdjHigh   *float64 `json:"adjHigh"`
		AdjLow    *float64 `json:"adjLow"`
		AdjClose  *float64 `json:"adjClose"`
		AdjVolume *int64   `json:"adjVolume"`
	}
	if err := c.getJSON(ctx, c.baseURL+path, query, "history", &raw); err != nil {
		return nil, err
	}
	out := make([]broker.Candle, 0, len(raw))
	for _, item := range raw {
		timestamp := parseTime(item.Date)
		open, high, low, close, volume := item.Open, item.High, item.Low, item.Close, item.Volume
		if adjusted && !intraday && item.AdjOpen != nil && item.AdjHigh != nil && item.AdjLow != nil && item.AdjClose != nil {
			open, high, low, close = item.AdjOpen, item.AdjHigh, item.AdjLow, item.AdjClose
			if item.AdjVolume != nil {
				volume = item.AdjVolume
			}
		}
		if timestamp <= 0 || open == nil || high == nil || low == nil || close == nil {
			continue
		}
		bar := broker.Candle{Time: timestamp, Open: *open, High: *high, Low: *low, Close: *close}
		if volume != nil {
			bar.Volume = *volume
		}
		if len(out) == 0 || bar.Time > out[len(out)-1].Time {
			out = append(out, bar)
		}
	}
	if len(out) == 0 {
		return nil, market.NewProviderError(market.ErrorSecurityNotFound, "tiingo", "history", "security not found", market.ErrNotFound)
	}
	return out, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, query url.Values, op string, target any) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return market.NewProviderError(market.ErrorInvalidRequest, "tiingo", op, "invalid endpoint", err)
	}
	if query != nil {
		parsed.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return market.NewProviderError(market.ErrorInvalidRequest, "tiingo", op, "cannot build request", err)
	}
	// The token is kept out of the URL so access logs, proxy logs, and error
	// messages cannot capture it.
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return market.NewProviderError(market.ErrorUpstreamUnavailable, "tiingo", op, "upstream request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return statusError(resp.StatusCode, op)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 8<<20))
	if err := decoder.Decode(target); err != nil {
		return market.NewProviderError(market.ErrorUpstreamUnavailable, "tiingo", op, "invalid upstream response", err)
	}
	return nil
}

func statusError(status int, op string) error {
	switch status {
	case http.StatusUnauthorized:
		return market.NewProviderError(market.ErrorAuthentication, "tiingo", op, "authentication failed", nil)
	case http.StatusForbidden:
		return market.NewProviderError(market.ErrorPermission, "tiingo", op, "market data permission denied", nil)
	case http.StatusNotFound:
		return market.NewProviderError(market.ErrorSecurityNotFound, "tiingo", op, "security not found", market.ErrNotFound)
	case http.StatusTooManyRequests:
		return market.NewProviderError(market.ErrorRateLimited, "tiingo", op, "rate limited", nil)
	default:
		return market.NewProviderError(market.ErrorUpstreamUnavailable, "tiingo", op, "upstream returned HTTP "+strconv.Itoa(status), nil)
	}
}

func normalizeSymbol(symbol string) (string, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" || len(symbol) > 32 {
		return "", fmt.Errorf("%w: symbol must contain 1-32 characters", market.ErrInvalidRequest)
	}
	for _, r := range symbol {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(".-", r)) {
			return "", fmt.Errorf("%w: unsupported symbol format", market.ErrInvalidRequest)
		}
	}
	return symbol, nil
}

func periodStart(period string, now time.Time) (time.Time, error) {
	switch strings.ToLower(strings.TrimSpace(period)) {
	case "1d":
		return now.AddDate(0, 0, -2), nil
	case "5d":
		return now.AddDate(0, 0, -7), nil
	case "1m":
		return now.AddDate(0, -1, 0), nil
	case "3m":
		return now.AddDate(0, -3, 0), nil
	case "6m":
		return now.AddDate(0, -6, 0), nil
	case "1y":
		return now.AddDate(-1, 0, 0), nil
	case "2y":
		return now.AddDate(-2, 0, 0), nil
	case "5y":
		return now.AddDate(-5, 0, 0), nil
	case "max":
		return time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), nil
	default:
		return time.Time{}, fmt.Errorf("%w: unsupported period %q", market.ErrInvalidRequest, period)
	}
}

func tiingoBar(bar string) (string, bool, error) {
	switch strings.ToLower(strings.TrimSpace(bar)) {
	case "1min":
		return "1min", true, nil
	case "5min":
		return "5min", true, nil
	case "15min":
		return "15min", true, nil
	case "30min":
		return "30min", true, nil
	case "1h", "1hour":
		return "1hour", true, nil
	case "1d", "1day", "daily":
		return "daily", false, nil
	case "1w", "weekly":
		return "weekly", false, nil
	default:
		return "", false, fmt.Errorf("%w: unsupported bar %q", market.ErrInvalidRequest, bar)
	}
}

func parseTime(value string) int64 {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.Unix()
	}
	return 0
}

func firstFloat(values ...*float64) float64 {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return 0
}

func floatValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var _ market.SymbolProvider = (*Client)(nil)
