package schwab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

const (
	defaultAuthorizeURL = "https://api.schwabapi.com/v1/oauth/authorize"
	defaultTokenURL     = "https://api.schwabapi.com/v1/oauth/token"
	defaultTraderURL    = "https://api.schwabapi.com/trader/v1"
	defaultMarketURL    = "https://api.schwabapi.com/marketdata/v1"
)

// Client wraps Schwab Market Data + OAuth APIs.
type Client struct {
	mu        sync.RWMutex
	refreshMu sync.Mutex
	cfg       config.SchwabConfig
	token     *Token

	httpClient   *http.Client
	authorizeURL string
	tokenURL     string
	traderURL    string
	marketURL    string
	onToken      func(Token)

	stream *streamManager
}

// Option customizes a Client. The URL options are primarily useful for tests
// and compatible Schwab API proxies.
type Option func(*Client)

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func WithOAuthURLs(authorizeURL, tokenURL string) Option {
	return func(c *Client) {
		if authorizeURL != "" {
			c.authorizeURL = authorizeURL
		}
		if tokenURL != "" {
			c.tokenURL = tokenURL
		}
	}
}

func WithAPIURLs(traderURL, marketURL string) Option {
	return func(c *Client) {
		if traderURL != "" {
			c.traderURL = strings.TrimRight(traderURL, "/")
		}
		if marketURL != "" {
			c.marketURL = strings.TrimRight(marketURL, "/")
		}
	}
}

func WithTokenHandler(fn func(Token)) Option {
	return func(c *Client) {
		c.onToken = fn
	}
}

func New(cfg config.SchwabConfig, opts ...Option) *Client {
	c := &Client{
		cfg:          cfg,
		httpClient:   http.DefaultClient,
		authorizeURL: defaultAuthorizeURL,
		tokenURL:     defaultTokenURL,
		traderURL:    defaultTraderURL,
		marketURL:    defaultMarketURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	c.stream = newStreamManager(c)
	return c
}

func (c *Client) SetConfig(cfg config.SchwabConfig) {
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
	c.stream.wake()
}

// SetToken restores a previously persisted token into the client.
func (c *Client) SetToken(token Token) {
	c.mu.Lock()
	c.token = cloneToken(&token)
	c.mu.Unlock()
	c.stream.wake()
}

// Token returns a copy of the current token.
func (c *Client) Token() (Token, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.token == nil {
		return Token{}, false
	}
	return *cloneToken(c.token), true
}

func (c *Client) GetQuote(ctx context.Context, symbol string) (*broker.Quote, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return nil, fmt.Errorf("schwab: symbol is required")
	}
	c.mu.RLock()
	marketURL := c.marketURL
	c.mu.RUnlock()
	endpoint := marketURL + "/quotes?" + url.Values{"symbols": {symbol}}.Encode()
	resp, err := c.Do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("schwab: read quote response: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("schwab: decode quote response: %w", err)
	}
	data, ok := raw[symbol]
	if !ok {
		for key, value := range raw {
			if strings.EqualFold(key, symbol) {
				data, ok = value, true
				break
			}
		}
	}
	if !ok {
		return nil, fmt.Errorf("schwab: quote not found (symbol=%s)", symbol)
	}
	var quote schwabQuoteResponse
	if err := json.Unmarshal(data, &quote); err != nil {
		return nil, fmt.Errorf("schwab: decode quote for %s: %w", symbol, err)
	}
	return quote.normalized(symbol), nil
}

func (c *Client) GetQuotes(ctx context.Context, symbols []string) ([]broker.Quote, error) {
	out := make([]broker.Quote, 0, len(symbols))
	var lastErr error
	seen := make(map[string]struct{})
	for _, symbol := range symbols {
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		if symbol == "" {
			continue
		}
		if _, ok := seen[symbol]; ok {
			continue
		}
		seen[symbol] = struct{}{}
		quote, err := c.GetQuote(ctx, symbol)
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

type schwabQuoteResponse struct {
	Symbol   string `json:"symbol"`
	Realtime bool   `json:"realtime"`
	Quote    struct {
		BidPrice         float64 `json:"bidPrice"`
		AskPrice         float64 `json:"askPrice"`
		LastPrice        float64 `json:"lastPrice"`
		HighPrice        float64 `json:"highPrice"`
		LowPrice         float64 `json:"lowPrice"`
		NetChange        float64 `json:"netChange"`
		NetPercentChange float64 `json:"netPercentChange"`
		TotalVolume      int64   `json:"totalVolume"`
	} `json:"quote"`
	Regular struct {
		LastPrice  float64 `json:"regularMarketLastPrice"`
		NetChange  float64 `json:"regularMarketNetChange"`
		NetPercent float64 `json:"regularMarketPercentChange"`
	} `json:"regular"`
}

func (q schwabQuoteResponse) normalized(fallbackSymbol string) *broker.Quote {
	symbol := q.Symbol
	if symbol == "" {
		symbol = fallbackSymbol
	}
	last := q.Quote.LastPrice
	if last == 0 {
		last = q.Regular.LastPrice
	}
	change := q.Quote.NetChange
	if change == 0 {
		change = q.Regular.NetChange
	}
	changePct := q.Quote.NetPercentChange
	if changePct == 0 {
		changePct = q.Regular.NetPercent
	}
	return &broker.Quote{
		Symbol:    symbol,
		Last:      last,
		Bid:       q.Quote.BidPrice,
		Ask:       q.Quote.AskPrice,
		Change:    change,
		ChangePct: changePct,
		Volume:    q.Quote.TotalVolume,
		High:      q.Quote.HighPrice,
		Low:       q.Quote.LowPrice,
		Delayed:   !q.Realtime,
	}
}
