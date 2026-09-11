// Package yahoo adapts Yahoo Finance's public quote and chart endpoints.
// The authentication flow follows niteX's YahooFinanceProvider: cookie warmup,
// crumb acquisition, bounded refresh/retry, and query1/query2 failover.
package yahoo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/market"
	"golang.org/x/net/publicsuffix"
)

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

type cacheEntry[T any] struct {
	value   T
	expires time.Time
}

// Client keeps credentials in memory. A context-aware gate coalesces overlapping
// requests through the cache and prevents concurrent crumb refresh storms.
type Client struct {
	http       *http.Client
	hosts      []string
	cookieURL  string
	gate       chan struct{}
	crumbs     map[string]string
	warmed     bool
	quotes     map[string]cacheEntry[broker.Quote]
	charts     map[string]cacheEntry[[]broker.Candle]
	retryDelay time.Duration
}

var _ market.SymbolProvider = (*Client)(nil)

func New() *Client {
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return &Client{
		http:      &http.Client{Timeout: 8 * time.Second, Jar: jar},
		hosts:     []string{"https://query1.finance.yahoo.com", "https://query2.finance.yahoo.com"},
		cookieURL: "https://fc.yahoo.com/", gate: make(chan struct{}, 1),
		crumbs: map[string]string{}, quotes: map[string]cacheEntry[broker.Quote]{},
		charts: map[string]cacheEntry[[]broker.Candle]{}, retryDelay: 400 * time.Millisecond,
	}
}

func normalizeSymbol(symbol string) (string, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if len(symbol) == 0 || len(symbol) > 64 {
		return "", fmt.Errorf("%w: symbol must contain 1–64 characters", market.ErrInvalidRequest)
	}
	for _, c := range symbol {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(".-^=", c)) {
			return "", fmt.Errorf("%w: unsupported symbol format", market.ErrInvalidRequest)
		}
	}
	return symbol, nil
}

func (c *Client) lock(ctx context.Context) error {
	select {
	case c.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) GetQuote(ctx context.Context, symbol string) (*broker.Quote, error) {
	quotes, err := c.GetQuotes(ctx, []string{symbol})
	if err != nil {
		return nil, err
	}
	if len(quotes) == 0 {
		return nil, market.ErrNotFound
	}
	return &quotes[0], nil
}

func (c *Client) GetQuotes(ctx context.Context, symbols []string) ([]broker.Quote, error) {
	if len(symbols) > 100 {
		return nil, fmt.Errorf("%w: at most 100 symbols per request", market.ErrInvalidRequest)
	}
	ordered := []string{}
	seen := map[string]bool{}
	for _, symbol := range symbols {
		if strings.TrimSpace(symbol) == "" {
			continue
		}
		symbol, err := normalizeSymbol(symbol)
		if err != nil {
			return nil, err
		}
		if !seen[symbol] {
			ordered = append(ordered, symbol)
			seen[symbol] = true
		}
	}
	if len(ordered) == 0 {
		return []broker.Quote{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := c.lock(ctx); err != nil {
		return nil, err
	}
	defer func() { <-c.gate }()
	prune(c.quotes, 512)
	missing := []string{}
	for _, symbol := range ordered {
		if _, ok := c.quotes[symbol]; !ok {
			missing = append(missing, symbol)
		}
	}
	if len(missing) > 0 {
		body, err := c.fetch(ctx, "/v7/finance/quote", url.Values{"symbols": {strings.Join(missing, ",")}})
		if err != nil {
			return nil, err
		}
		quotes, err := parseQuotes(body)
		if err != nil {
			return nil, err
		}
		for _, quote := range quotes {
			if seen[quote.Symbol] {
				c.quotes[quote.Symbol] = cacheEntry[broker.Quote]{quote, time.Now().Add(2 * time.Second)}
			}
		}
	}
	out := []broker.Quote{}
	for _, symbol := range ordered {
		if entry, ok := c.quotes[symbol]; ok {
			out = append(out, entry.value)
		}
	}
	if len(out) == 0 {
		return nil, market.ErrNotFound
	}
	return out, nil
}

var ranges = map[string]string{"1d": "1d", "5d": "5d", "1m": "1mo", "3m": "3mo", "6m": "6mo", "1y": "1y", "2y": "2y", "5y": "5y"}
var intervals = map[string]string{"1min": "1m", "5min": "5m", "15min": "15m", "30min": "30m", "1h": "1h", "1d": "1d", "1w": "1wk"}

func (c *Client) GetHistory(ctx context.Context, symbol, period, bar string, refresh bool) ([]broker.Candle, error) {
	symbol, err := normalizeSymbol(symbol)
	if err != nil {
		return nil, err
	}
	rangeValue, rangeOK := ranges[period]
	interval, intervalOK := intervals[bar]
	if !rangeOK || !intervalOK {
		return nil, fmt.Errorf("%w: unsupported period or bar", market.ErrInvalidRequest)
	}
	// Yahoo restricts one-minute history to seven days, other minute bars to
	// sixty days, and hourly bars to 730 days. Reject impossible combinations.
	if bar == "1min" && period != "1d" && period != "5d" ||
		strings.HasSuffix(bar, "min") && period != "1d" && period != "5d" && period != "1m" ||
		bar == "1h" && (period == "2y" || period == "5y") {
		return nil, fmt.Errorf("%w: period exceeds Yahoo intraday history limit", market.ErrInvalidRequest)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := c.lock(ctx); err != nil {
		return nil, err
	}
	defer func() { <-c.gate }()
	prune(c.charts, 64)
	key := symbol + ":" + period + ":" + bar
	if cached, ok := c.charts[key]; ok && !refresh {
		return append([]broker.Candle{}, cached.value...), nil
	}
	body, err := c.fetch(ctx, "/v8/finance/chart/"+url.PathEscape(symbol), url.Values{"range": {rangeValue}, "interval": {interval}})
	if err != nil {
		return nil, err
	}
	bars, err := parseChart(body)
	if err != nil {
		return nil, err
	}
	c.charts[key] = cacheEntry[[]broker.Candle]{bars, time.Now().Add(30 * time.Second)}
	return append([]broker.Candle{}, bars...), nil
}

func prune[T any](cache map[string]cacheEntry[T], limit int) {
	for key, entry := range cache {
		if !time.Now().Before(entry.expires) {
			delete(cache, key)
		}
	}
	if len(cache) >= limit {
		clear(cache)
	}
}

type statusError int

func (e statusError) Error() string { return fmt.Sprintf("yahoo: upstream HTTP %d", int(e)) }

// request never includes URLs, crumbs, cookies or upstream bodies in errors.
func (c *Client) request(ctx context.Context, endpoint string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, errors.New("yahoo: invalid endpoint")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, errors.New("yahoo: network request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return nil, resp.StatusCode, errors.New("yahoo: response read failed")
	}
	if len(body) > 8<<20 {
		return nil, resp.StatusCode, errors.New("yahoo: response too large")
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, statusError(resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

func (c *Client) authenticate(ctx context.Context, host string) error {
	if !c.warmed {
		_, status, err := c.request(ctx, c.cookieURL)
		// fc.yahoo.com normally returns 404 while setting the cookie.
		if err != nil && status != http.StatusNotFound {
			return err
		}
		c.warmed = true
	}
	if c.crumbs[host] != "" {
		return nil
	}
	body, _, err := c.request(ctx, host+"/v1/test/getcrumb")
	if err != nil {
		return err
	}
	crumb := strings.TrimSpace(string(body))
	if crumb == "" || len(crumb) > 256 || strings.ContainsAny(crumb, " \t\r\n<>{}\"") {
		return errors.New("yahoo: invalid crumb response")
	}
	c.crumbs[host] = crumb
	return nil
}

func (c *Client) fetch(ctx context.Context, path string, query url.Values) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		host := c.hosts[attempt%len(c.hosts)]
		err := c.authenticate(ctx, host)
		var body []byte
		if err == nil {
			query.Set("crumb", c.crumbs[host])
			body, _, err = c.request(ctx, host+path+"?"+query.Encode())
		}
		if err == nil {
			return body, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var status statusError
		if errors.As(err, &status) {
			if status == 404 {
				return nil, market.ErrNotFound
			}
			if status != 401 && status != 403 && status != 429 && status < 500 {
				return nil, err
			}
			if status == 401 {
				clear(c.crumbs)
				c.warmed = false
			}
		}
		if attempt < 2 {
			timer := time.NewTimer(c.retryDelay << attempt)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, lastErr
}

// Decode envelope errors without exposing upstream error descriptions.
func decode(body []byte, target any) error {
	if err := json.Unmarshal(body, target); err != nil {
		return errors.New("yahoo: invalid JSON response")
	}
	return nil
}
