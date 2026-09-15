package ibkr

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

// Client wraps the IBKR Client Portal API at a configured external Gateway.
type Client struct {
	cfg        config.IBKRConfig
	httpClient *http.Client
	flexClient *http.Client

	pnlMu        sync.Mutex
	pnlFetchedAt time.Time
	pnlSnapshot  map[string]dailyPnLEntry

	orderState liveOrderState
}

func (c *Client) AccountSummary(ctx context.Context) (broker.AccountSummary, error) {
	accountID, err := c.resolveAccountID(ctx)
	if err != nil {
		return broker.AccountSummary{}, fmt.Errorf("ibkr: resolve account: %w", err)
	}
	return c.accountSummary(ctx, accountID)
}

func (c *Client) accountSummary(ctx context.Context, accountID string) (broker.AccountSummary, error) {
	var raw map[string]any
	path := "/portfolio/" + url.PathEscape(accountID) + "/summary"
	if err := c.getGatewayJSON(ctx, path, "account summary", &raw); err != nil {
		return broker.AccountSummary{}, err
	}

	return broker.AccountSummary{
		AccountID:          accountID,
		Currency:           firstAccountCurrency(raw),
		NetLiquidation:     firstAccountFloat(raw, "NetLiquidation", "netliquidation", "netLiquidation", "NLV"),
		TotalCashValue:     firstAccountFloat(raw, "TotalCashValue", "totalcashvalue", "totalCashValue", "CashBalance"),
		GrossPositionValue: firstAccountFloat(raw, "GrossPositionValue", "grosspositionvalue", "grossPositionValue"),
		UnrealizedPnL:      firstAccountFloat(raw, "UnrealizedPnL", "unrealizedpnl", "unrealizedPnl", "UnrealizedPnL-S"),
		RealizedPnL:        firstAccountFloat(raw, "RealizedPnL", "realizedpnl", "realizedPnl"),
		BuyingPower:        firstAccountFloat(raw, "BuyingPower", "buyingpower", "buyingPower"),
		Broker:             "IBKR",
		AsOf:               time.Now().UTC().Format(time.RFC3339),
	}, nil
}

const flexEquityPeriodDays = 365 // IBKR Flex Web Service max per request

func (c *Client) HistoricalEquity(ctx context.Context) ([]broker.AccountEquityPoint, error) {
	token := strings.TrimSpace(c.cfg.FlexToken)
	queryID := strings.TrimSpace(c.cfg.FlexQueryID)
	if token == "" || queryID == "" {
		return []broker.AccountEquityPoint{}, nil
	}

	period := flexPeriod{days: flexEquityPeriodDays}
	refCode, err := c.flexSendRequest(ctx, token, queryID, period)
	if err != nil {
		return nil, err
	}
	body, err := c.flexGetStatement(ctx, token, refCode, period)
	if err != nil {
		return nil, err
	}
	points := parseFlexEquity(body)
	if len(points) == 0 && len(bytes.TrimSpace(body)) > 0 {
		return nil, fmt.Errorf("ibkr flex: query %s 未返回 NAV 数据，请勾选「以基础货币计的资产净值 (NAV)」区块（当前可能是持仓价值变动等错误区块）", queryID)
	}
	return points, nil
}

type flexPeriod struct {
	days     int // Flex "p" override: last N days (max 365)
	from, to string
}

func applyFlexPeriod(q url.Values, period flexPeriod) {
	if period.from != "" && period.to != "" {
		q.Set("fd", strings.ReplaceAll(period.from, "-", ""))
		q.Set("td", strings.ReplaceAll(period.to, "-", ""))
		return
	}
	if period.days <= 0 || period.days > 365 {
		return
	}
	q.Set("p", strconv.Itoa(period.days))
}

func (c *Client) flexSendRequest(ctx context.Context, token, queryID string, period flexPeriod) (string, error) {
	q := url.Values{}
	q.Set("t", token)
	q.Set("q", queryID)
	q.Set("v", "3")
	applyFlexPeriod(q, period)
	body, err := c.flexRequestBody(ctx, "SendRequest", q)
	if err != nil {
		return "", err
	}

	var parsed flexResponse
	if err := xml.NewDecoder(bytes.NewReader(body)).Decode(&parsed); err != nil {
		return "", errors.New("ibkr flex: invalid SendRequest response")
	}
	if strings.EqualFold(parsed.Status, "Fail") || parsed.ErrorCode != "" {
		return "", newFlexError(parsed.ErrorCode)
	}
	if strings.TrimSpace(parsed.ReferenceCode) == "" {
		return "", fmt.Errorf("ibkr flex: missing reference code")
	}
	return strings.TrimSpace(parsed.ReferenceCode), nil
}

func (c *Client) flexGetStatement(ctx context.Context, token, refCode string, period flexPeriod) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(attempt) * 2 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}

		body, err := c.flexGetStatementOnce(ctx, token, refCode, period)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !isRetryableFlexError(err) {
			break
		}
	}
	return nil, lastErr
}

func (c *Client) flexGetStatementOnce(ctx context.Context, token, refCode string, period flexPeriod) ([]byte, error) {
	q := url.Values{}
	q.Set("t", token)
	q.Set("q", refCode)
	q.Set("v", "3")
	// Keep the existing NAV request shape. IBKR binds the range to the reference
	// code, but has historically tolerated the same override on retrieval.
	applyFlexPeriod(q, period)
	body, err := c.flexRequestBody(ctx, "GetStatement", q)
	if err != nil {
		return nil, err
	}
	if flexErr := parseFlexError(body); flexErr != nil {
		return nil, flexErr
	}
	return body, nil
}

func New(cfg config.IBKRConfig) *Client {
	return &Client{
		cfg:        cfg,
		httpClient: newGatewayHTTPClient(cfg.GatewayURL, cfg.GatewayToken, 15*time.Second),
		flexClient: newFlexHTTPClient(),
	}
}

func (c *Client) SetConfig(cfg config.IBKRConfig) {
	c.cfg = cfg
	c.httpClient = newGatewayHTTPClient(cfg.GatewayURL, cfg.GatewayToken, 15*time.Second)
	c.flexClient = newFlexHTTPClient()
	c.pnlMu.Lock()
	c.pnlFetchedAt = time.Time{}
	c.pnlSnapshot = nil
	c.pnlMu.Unlock()
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

// GetCandles fetches OHLCV history via IBKR CPAPI /iserver/marketdata/history.
// period maps to IBKR "period" param (e.g. "1d", "1m", "1y").
// bar maps to IBKR "bar" param (e.g. "5min", "1h", "1d").
func (c *Client) GetCandles(ctx context.Context, conID int64, period, bar string) ([]broker.Candle, error) {
	u, err := url.Parse(c.cfg.GatewayURL + "/v1/api/iserver/marketdata/history")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("conid", strconv.FormatInt(conID, 10))
	q.Set("period", period)
	q.Set("bar", bar)
	q.Set("outsideRth", "false")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ibkr: history request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("ibkr: gateway not authenticated")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ibkr: history status %d: %s", resp.StatusCode, string(body))
	}

	// IBKR returns t as either a millisecond-epoch integer or a date string.
	// Use json.Number so we can handle both without a type mismatch.
	var raw struct {
		Data []struct {
			T json.Number `json:"t"`
			O float64     `json:"o"`
			H float64     `json:"h"`
			L float64     `json:"l"`
			C float64     `json:"c"`
			V float64     `json:"v"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("ibkr: decode history: %w", err)
	}

	candles := make([]broker.Candle, 0, len(raw.Data))
	for _, d := range raw.Data {
		ts := parseIBKRTime(d.T.String())
		candles = append(candles, broker.Candle{
			Time:   ts,
			Open:   d.O,
			High:   d.H,
			Low:    d.L,
			Close:  d.C,
			Volume: int64(d.V),
		})
	}
	return candles, nil
}

// parseIBKRTime parses IBKR time strings: epoch-ms integer string or "20240101 09:30:00".
func parseIBKRTime(s string) int64 {
	// IBKR sometimes returns millisecond epoch as a string
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		if ms > 1e12 {
			return ms / 1000
		}
		return ms
	}
	// Try "20060102 15:04:05" in Eastern time
	loc, _ := time.LoadLocation("America/New_York")
	if loc == nil {
		loc = time.UTC
	}
	for _, layout := range []string{"20060102 15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t.Unix()
		}
	}
	return 0
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

	// IBKR CPAPI sometimes returns 400/503 on the first request after session
	// establishment ("warming up"). Retry up to 3 times with a short back-off.
	var resp *http.Response
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 600 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		resp, err = c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("ibkr: instrument search request: %w", err)
		}
		if resp.StatusCode == http.StatusOK {
			break
		}
		resp.Body.Close()
		resp = nil
	}
	if resp == nil {
		return nil, fmt.Errorf("ibkr: instrument search failed after retries")
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

// ListAccountPositions fetches every page of positions for one account.
func (c *Client) ListAccountPositions(ctx context.Context, accountID string) ([]broker.Position, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("ibkr: account ID is required")
	}
	if broker.FreshPositionsRequested(ctx) {
		if err := c.invalidatePositions(ctx, accountID); err != nil {
			return nil, err
		}
	}

	out := []broker.Position{}
	for page := 0; page < 1000; page++ {
		path := fmt.Sprintf("/portfolio/%s/positions/%d", url.PathEscape(accountID), page)
		var raw []struct {
			ConID         int64    `json:"conid"`
			AccountID     string   `json:"acctId"`
			ContractDesc  string   `json:"contractDesc"`
			Ticker        string   `json:"ticker"`
			Name          string   `json:"name"`
			FullName      string   `json:"fullName"`
			AssetClass    string   `json:"assetClass"`
			Type          string   `json:"type"`
			Position      *float64 `json:"position"`
			MktPrice      float64  `json:"mktPrice"`
			MktValue      float64  `json:"mktValue"`
			AvgCost       float64  `json:"avgCost"`
			UnrealizedPnl float64  `json:"unrealizedPnl"`
			RealizedPnl   float64  `json:"realizedPnl"`
			Currency      string   `json:"currency"`
		}
		if err := c.getGatewayJSON(ctx, path, "positions", &raw); err != nil {
			return nil, err
		}
		if raw == nil {
			return nil, errors.New("ibkr: incomplete positions response")
		}

		for _, p := range raw {
			symbol := strings.TrimSpace(firstNonEmpty(p.Ticker, p.ContractDesc))
			if p.Position == nil {
				return nil, errors.New("ibkr: missing position quantity")
			}
			quantity := *p.Position
			if p.AccountID != "" && p.AccountID != accountID {
				return nil, errors.New("ibkr: position account mismatch")
			}
			if quantity != 0 && (symbol == "" || p.ConID <= 0 || p.Currency == "") {
				return nil, errors.New("ibkr: incomplete position identity")
			}
			if quantity == 0 {
				continue
			}
			out = append(out, broker.Position{
				ExternalID:  strconv.FormatInt(p.ConID, 10),
				AssetType:   ibkrPositionAssetType(p.AssetClass, p.Type),
				Market:      marketFromCurrency(p.Currency),
				Symbol:      symbol,
				Name:        strings.TrimSpace(firstNonEmpty(p.Name, p.FullName)),
				ConID:       p.ConID,
				Quantity:    quantity,
				AvgCost:     p.AvgCost,
				MarketPrice: p.MktPrice,
				MarketValue: p.MktValue,
				Unrealized:  p.UnrealizedPnl,
				Realized:    p.RealizedPnl,
				Currency:    p.Currency,
				Account:     firstNonEmpty(p.AccountID, accountID),
				Broker:      "IBKR",
			})
		}
		if len(raw) < 100 {
			return out, nil
		}
	}
	return nil, fmt.Errorf("ibkr: positions exceeded pagination limit")
}

func ibkrPositionAssetType(assetClass, positionType string) string {
	if strings.EqualFold(strings.TrimSpace(positionType), "ETF") {
		return "etf"
	}
	switch strings.ToUpper(strings.TrimSpace(assetClass)) {
	case "OPT", "OPTION":
		return "option"
	case "FUT", "FUTURE":
		return "future"
	case "BOND":
		return "bond"
	case "CASH":
		return "cash"
	default:
		return "stock"
	}
}

func marketFromCurrency(currency string) string {
	if strings.EqualFold(strings.TrimSpace(currency), "USD") {
		return "US"
	}
	return strings.ToUpper(strings.TrimSpace(currency))
}

// resolveAccountID returns the configured sub-account or fetches it from the gateway.
func (c *Client) resolveAccountID(ctx context.Context) (string, error) {
	if c.cfg.SubAccount != "" {
		return c.cfg.SubAccount, nil
	}
	accountIDs, err := c.listAccountIDs(ctx)
	if err != nil {
		return "", err
	}
	return accountIDs[0], nil
}

func (c *Client) listAccountIDs(ctx context.Context) ([]string, error) {
	accounts, err := c.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	accountIDs := make([]string, 0, len(accounts))
	for _, account := range accounts {
		accountIDs = append(accountIDs, account.ID)
	}
	return accountIDs, nil
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
		cleaned = strings.ReplaceAll(cleaned, "$", "")
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

type flexResponse struct {
	Status        string `xml:"Status"`
	ReferenceCode string `xml:"ReferenceCode"`
	ErrorCode     string `xml:"ErrorCode"`
	ErrorMessage  string `xml:"ErrorMessage"`
}

type flexError struct {
	code    string
	message string
}

func (e flexError) Error() string {
	if e.code == "" {
		return "ibkr flex: " + e.message
	}
	return "ibkr flex: " + e.code + " " + e.message
}

// newFlexError deliberately maps only documented codes. Flex credentials are
// query parameters, so reflecting an upstream error message could disclose a
// token or reference code through logs and API error responses.
func newFlexError(code string) flexError {
	code = strings.TrimSpace(code)
	messages := map[string]string{
		"1001": "statement could not be generated yet",
		"1003": "statement is not available",
		"1004": "statement is incomplete",
		"1005": "settlement data is not ready",
		"1006": "FIFO data is not ready",
		"1007": "mark-to-market data is not ready",
		"1008": "profit and loss data is not ready",
		"1009": "report service is busy",
		"1010": "legacy Flex query is unsupported",
		"1011": "Flex service account is inactive",
		"1012": "Flex token has expired",
		"1013": "Flex IP restriction rejected the request",
		"1014": "Flex query is invalid",
		"1015": "Flex token is invalid",
		"1016": "Flex account is invalid",
		"1017": "Flex reference code is invalid",
		"1018": "Flex request rate limit exceeded",
		"1019": "statement generation is in progress",
		"1020": "Flex request could not be validated",
		"1021": "statement could not be retrieved",
	}
	message := messages[code]
	if message == "" {
		message = "report service returned an error"
	}
	return flexError{code: code, message: message}
}

func isRetryableFlexError(err error) bool {
	var fe flexError
	if !errors.As(err, &fe) {
		return false
	}
	switch fe.code {
	case "1001", "1003", "1004", "1005", "1006", "1007", "1008", "1009", "1018", "1019", "1021":
		return true
	default:
		return false
	}
}

func parseFlexError(body []byte) error {
	var parsed flexResponse
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	if strings.EqualFold(parsed.Status, "Fail") || parsed.ErrorCode != "" {
		return newFlexError(parsed.ErrorCode)
	}
	return nil
}

func parseFlexEquity(body []byte) []broker.AccountEquityPoint {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '<' {
		return parseFlexEquityXML(body)
	}
	if points := parseFlexEquityCSV(body); len(points) > 0 {
		return points
	}
	return parseFlexEquityXML(body)
}

func parseFlexEquityXML(body []byte) []broker.AccountEquityPoint {
	decoder := xml.NewDecoder(strings.NewReader(string(body)))
	pointsByTime := map[string]broker.AccountEquityPoint{}

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if !isFlexEquityElement(start.Name.Local) {
			continue
		}
		attrs := lowerAttrs(start.Attr)
		date := firstAttr(attrs, "reportdate", "date", "todate", "statementdate", "periodenddate")
		if date == "" {
			continue
		}
		value := firstAttrFloat(attrs,
			"endingvalue",
			"endingnav",
			"endingnetassetvalue",
			"currentnav",
			"netassetvalue",
			"netliquidation",
			"equitywithloanvalue",
			"total",
		)
		if value == 0 {
			continue
		}
		key := normalizeFlexDate(date)
		if key == "" {
			continue
		}
		pointsByTime[key] = broker.AccountEquityPoint{
			Time:     key,
			Value:    value,
			Currency: firstAttr(attrs, "currency", "basecurrency"),
			Source:   "IBKR Flex",
		}
	}

	out := make([]broker.AccountEquityPoint, 0, len(pointsByTime))
	for _, point := range pointsByTime {
		out = append(out, point)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Time < out[j].Time
	})
	return out
}

func lowerAttrs(attrs []xml.Attr) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		out[strings.ToLower(attr.Name.Local)] = strings.TrimSpace(attr.Value)
	}
	return out
}

func isFlexEquityElement(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "netassetvalue") ||
		strings.Contains(name, "nav") ||
		strings.Contains(name, "equitywithloan") ||
		strings.Contains(name, "netliquidation")
}

func firstAttr(attrs map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(attrs[strings.ToLower(key)]); value != "" {
			return value
		}
	}
	return ""
}

func firstAttrFloat(attrs map[string]string, keys ...string) float64 {
	for _, key := range keys {
		if value := parseFloat(firstAttr(attrs, key)); value != 0 {
			return value
		}
	}
	return 0
}

func normalizeFlexDate(value string) string {
	value = strings.TrimSpace(value)
	formats := []string{"2006-01-02", "20060102", "2006/01/02", "01/02/2006", time.RFC3339}
	for _, layout := range formats {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return value
}

func firstAccountFloat(raw map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if value := accountValue(raw, key); value != nil {
			if n := parseFloat(value); n != 0 {
				return n
			}
		}
	}
	return 0
}

func firstAccountString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(asString(accountValue(raw, key))); value != "" {
			return value
		}
	}
	return ""
}

func firstAccountCurrency(raw map[string]any) string {
	if value := firstAccountString(raw, "Currency", "currency", "BaseCurrency", "baseCurrency"); value != "" {
		return value
	}
	for _, key := range []string{"NetLiquidation", "netliquidation", "TotalCashValue", "totalcashvalue"} {
		for k, v := range raw {
			if !strings.EqualFold(k, key) {
				continue
			}
			if typed, ok := v.(map[string]any); ok {
				if value := strings.TrimSpace(asString(typed["currency"])); value != "" {
					return value
				}
			}
		}
	}
	return ""
}

func accountValue(raw map[string]any, key string) any {
	for k, v := range raw {
		if !strings.EqualFold(k, key) {
			continue
		}
		switch typed := v.(type) {
		case map[string]any:
			if value, ok := typed["value"]; ok && !isAccountFieldEmpty(value) {
				return value
			}
			if amount, ok := typed["amount"]; ok {
				return amount
			}
		default:
			return v
		}
	}
	return nil
}

func isAccountFieldEmpty(value any) bool {
	if value == nil {
		return true
	}
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	return false
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

// FlexErrorCode preserves the numeric broker diagnostic without exposing its
// upstream message, credentials, reference code or request URL.
func FlexErrorCode(err error) string {
	var fe flexError
	if !errors.As(err, &fe) || len(fe.code) != 4 {
		return ""
	}
	for _, r := range fe.code {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return fe.code
}
