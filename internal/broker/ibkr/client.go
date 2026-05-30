package ibkr

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

// Client wraps IBKR Client Portal API (local Gateway).
type Client struct {
	cfg        config.IBKRConfig
	httpClient *http.Client
}

func New(cfg config.IBKRConfig) *Client {
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		},
	}
}

func (c *Client) SetConfig(cfg config.IBKRConfig) {
	c.cfg = cfg
}

// GetQuotesByConID fetches quote snapshots for multiple IBKR contracts.
func (c *Client) GetQuotesByConID(ctx context.Context, conIDs []int64) ([]broker.Quote, error) {
	conIDs = compactConIDs(conIDs)
	if len(conIDs) == 0 {
		return []broker.Quote{}, nil
	}

	var last []broker.Quote
	for attempt := 0; attempt < 3; attempt++ {
		quotes, err := c.getQuoteSnapshot(ctx, conIDs)
		if err != nil {
			return nil, err
		}
		last = quotes
		if hasQuotePrices(quotes) || attempt == 2 {
			return quotes, nil
		}
		timer := time.NewTimer(600 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return last, nil
}

func (c *Client) getQuoteSnapshot(ctx context.Context, conIDs []int64) ([]broker.Quote, error) {
	u, err := url.Parse(c.cfg.GatewayURL + "/v1/api/iserver/marketdata/snapshot")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("conids", joinConIDs(conIDs))
	q.Set("fields", "31,84,86,82,83,87")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ibkr: quote snapshot request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("ibkr: gateway not authenticated")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ibkr: quote snapshot status %d", resp.StatusCode)
	}

	var raw []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("ibkr: decode quote snapshot: %w", err)
	}

	out := make([]broker.Quote, 0, len(raw))
	for _, r := range raw {
		out = append(out, broker.Quote{
			ConID:     parseConID(firstRaw(r, "conid", "conidEx")),
			Symbol:    strings.TrimSpace(asString(firstRaw(r, "symbol", "55"))),
			Last:      parseFloat(firstRaw(r, "31", "last_price")),
			Bid:       parseFloat(firstRaw(r, "84", "bid_price")),
			Ask:       parseFloat(firstRaw(r, "86", "ask_price")),
			Change:    parseFloat(firstRaw(r, "82", "change_price")),
			ChangePct: parseFloat(firstRaw(r, "83", "change_percent")),
			Volume:    parseInt64(firstRaw(r, "87_raw", "87", "volume")),
		})
	}
	return out, nil
}

// SearchInstruments resolves user-entered symbols/names through IBKR Gateway.
func (c *Client) SearchInstruments(ctx context.Context, query string) ([]broker.Instrument, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []broker.Instrument{}, nil
	}

	u, err := url.Parse(c.cfg.GatewayURL + "/v1/api/iserver/secdef/search")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("symbol", query)
	q.Set("name", "true")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ibkr: instrument search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("ibkr: gateway not authenticated")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ibkr: instrument search status %d", resp.StatusCode)
	}

	var raw []struct {
		ConID           any    `json:"conid"`
		CompanyHeader   string `json:"companyHeader"`
		CompanyName     string `json:"companyName"`
		Description     string `json:"description"`
		Symbol          string `json:"symbol"`
		SecType         string `json:"secType"`
		Exchange        string `json:"exchange"`
		ListingExchange string `json:"listingExchange"`
		Currency        string `json:"currency"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("ibkr: decode instrument search: %w", err)
	}

	out := make([]broker.Instrument, 0, len(raw))
	for _, r := range raw {
		conID := parseConID(r.ConID)
		symbol := strings.TrimSpace(r.Symbol)
		if symbol == "" {
			symbol = firstToken(r.CompanyHeader)
		}
		if symbol == "" || conID <= 0 {
			continue
		}
		name := firstNonEmpty(r.CompanyName, r.Description, r.CompanyHeader)
		exchange := firstNonEmpty(r.ListingExchange, r.Exchange, exchangeFromHeader(r.CompanyHeader))
		out = append(out, broker.Instrument{
			ConID:    conID,
			Symbol:   symbol,
			Name:     strings.TrimSpace(name),
			SecType:  strings.TrimSpace(r.SecType),
			Exchange: strings.TrimSpace(exchange),
			Currency: strings.TrimSpace(r.Currency),
		})
	}
	return out, nil
}

// ListPositions fetches all positions from the IBKR gateway.
// It first resolves the account ID from the gateway, then fetches positions.
func (c *Client) ListPositions(ctx context.Context) ([]broker.Position, error) {
	accountID, err := c.resolveAccountID(ctx)
	if err != nil {
		return nil, fmt.Errorf("ibkr: resolve account: %w", err)
	}

	url := fmt.Sprintf("%s/v1/api/portfolio/%s/positions/0", c.cfg.GatewayURL, accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ibkr: positions request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("ibkr: gateway not authenticated")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ibkr: positions status %d", resp.StatusCode)
	}

	var raw []struct {
		ContractDesc  string  `json:"contractDesc"`
		Position      float64 `json:"position"`
		MktPrice      float64 `json:"mktPrice"`
		MktValue      float64 `json:"mktValue"`
		AvgCost       float64 `json:"avgCost"`
		UnrealizedPnl float64 `json:"unrealizedPnl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("ibkr: decode positions: %w", err)
	}

	out := make([]broker.Position, 0, len(raw))
	for _, p := range raw {
		if p.ContractDesc == "" || p.Position == 0 {
			continue
		}
		out = append(out, broker.Position{
			Symbol:      p.ContractDesc,
			Quantity:    p.Position,
			AvgCost:     p.AvgCost,
			MarketValue: p.MktValue,
			Unrealized:  p.UnrealizedPnl,
			Broker:      "IBKR",
		})
	}
	return out, nil
}

// resolveAccountID returns the configured sub-account or fetches it from the gateway.
func (c *Client) resolveAccountID(ctx context.Context) (string, error) {
	if c.cfg.SubAccount != "" {
		return c.cfg.SubAccount, nil
	}

	url := c.cfg.GatewayURL + "/v1/api/portfolio/accounts"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var accounts []struct {
		AccountID string `json:"accountId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&accounts); err != nil {
		return "", err
	}
	if len(accounts) == 0 {
		return "", fmt.Errorf("no accounts returned")
	}
	return accounts[0].AccountID, nil
}

func (c *Client) PlaceOrder(ctx context.Context, req broker.OrderRequest) (string, error) {
	_ = ctx
	_ = req
	return "", fmt.Errorf("ibkr: PlaceOrder not implemented")
}

func parseConID(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case string:
		if idx := strings.IndexByte(v, '@'); idx >= 0 {
			v = v[:idx]
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n
	default:
		return 0
	}
}

func compactConIDs(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, v := range values {
		if v <= 0 {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func joinConIDs(values []int64) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, strconv.FormatInt(v, 10))
	}
	return strings.Join(parts, ",")
}

func firstRaw(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := values[key]; ok {
			return v
		}
	}
	return nil
}

func asString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		if math.Trunc(v) == v {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

func parseFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	case string:
		cleaned := strings.TrimSpace(strings.TrimSuffix(v, "%"))
		cleaned = strings.ReplaceAll(cleaned, ",", "")
		n, _ := strconv.ParseFloat(cleaned, 64)
		return n
	default:
		return 0
	}
}

func parseInt64(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case string:
		cleaned := strings.ReplaceAll(strings.TrimSpace(v), ",", "")
		n, _ := strconv.ParseInt(cleaned, 10, 64)
		if n == 0 {
			f, _ := strconv.ParseFloat(strings.TrimRight(cleaned, "BKM%"), 64)
			n = int64(f)
		}
		return n
	default:
		return 0
	}
}

func hasQuotePrices(quotes []broker.Quote) bool {
	for _, quote := range quotes {
		if quote.Last != 0 || quote.Bid != 0 || quote.Ask != 0 {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstToken(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func exchangeFromHeader(value string) string {
	start := strings.LastIndex(value, "(")
	end := strings.LastIndex(value, ")")
	if start < 0 || end <= start {
		return ""
	}
	return strings.TrimSpace(value[start+1 : end])
}
