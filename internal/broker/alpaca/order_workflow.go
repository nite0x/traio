package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/nite/traio/internal/broker"
)

var _ broker.OrderWorkflowProvider = (*Client)(nil)
var _ broker.OrderQuoteProvider = (*Client)(nil)

var equitySymbol = regexp.MustCompile(`^[A-Z][A-Z0-9.\-]{0,14}$`)

type tradingAsset struct {
	ID           string `json:"id"`
	Symbol       string `json:"symbol"`
	Name         string `json:"name"`
	Exchange     string `json:"exchange"`
	Class        string `json:"class"`
	Status       string `json:"status"`
	Tradable     bool   `json:"tradable"`
	Fractionable bool   `json:"fractionable"`
}

func (c *Client) TradingAccounts(ctx context.Context) ([]broker.TradingAccount, error) {
	account, err := c.fetchAccount(ctx)
	if err != nil {
		return nil, err
	}
	id := alpacaAccountID(account)
	if id == "" {
		return nil, broker.NewOrderError("invalid_order", "Alpaca account has no identifier")
	}
	if !strings.EqualFold(account.Status, "ACTIVE") || account.TradingBlocked || account.AccountBlocked || account.TradeSuspendedByUser {
		return []broker.TradingAccount{}, nil
	}
	var paper *bool
	switch officialEnvironment(c.baseURL()) {
	case "paper":
		value := true
		paper = &value
	case "live":
		value := false
		paper = &value
	}
	return []broker.TradingAccount{{ID: id, Name: id, IsPaper: paper, SupportsFractions: true}}, nil
}

func (c *Client) tradingAsset(ctx context.Context, symbol string) (tradingAsset, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if !equitySymbol.MatchString(symbol) {
		return tradingAsset{}, broker.NewOrderError("invalid_order", "enter a valid US stock or ETF symbol")
	}
	resp, err := c.Do(ctx, http.MethodGet, "assets/"+url.PathEscape(symbol), nil)
	if err != nil {
		return tradingAsset{}, err
	}
	defer resp.Body.Close()
	var asset tradingAsset
	if err := json.NewDecoder(resp.Body).Decode(&asset); err != nil {
		return tradingAsset{}, fmt.Errorf("alpaca: decode trading asset: %w", err)
	}
	if !strings.EqualFold(asset.Symbol, symbol) || !strings.EqualFold(asset.Status, "active") || !asset.Tradable || (asset.Class != "us_equity" && asset.Class != "equity") {
		return tradingAsset{}, broker.NewOrderError("invalid_order", "selected asset is not an active tradable US stock or ETF")
	}
	return asset, nil
}

func (c *Client) SearchOrderInstruments(ctx context.Context, query string) ([]broker.Instrument, error) {
	query = strings.ToUpper(strings.TrimSpace(query))
	if query == "" {
		return []broker.Instrument{}, nil
	}
	asset, err := c.tradingAsset(ctx, query)
	if err != nil {
		return nil, err
	}
	instrument := c.orders.RememberInstrument(broker.Instrument{Symbol: asset.Symbol, Name: asset.Name, SecType: "STK", Exchange: asset.Exchange, Currency: "USD"})
	return []broker.Instrument{instrument}, nil
}

func (c *Client) OrderInstrument(_ context.Context, id string) (broker.Instrument, error) {
	return c.orders.Instrument(id)
}

func (c *Client) OrderQuote(_ context.Context, id string) (broker.OrderQuote, error) {
	instrument, err := c.orders.Instrument(id)
	if err != nil {
		return broker.OrderQuote{}, err
	}
	return broker.OrderQuote{ConID: instrument.ConID, Availability: "unavailable", UpdatedAt: time.Now().UTC().Format(time.RFC3339)}, nil
}

func (c *Client) validateTradingOrder(ctx context.Context, req broker.OrderRequest) (tradingAsset, error) {
	if err := broker.ValidateOrder(req); err != nil {
		return tradingAsset{}, broker.NewOrderError("invalid_order", err.Error())
	}
	if err := broker.ValidateClientOrderID(req.ClientOrderID); err != nil {
		return tradingAsset{}, err
	}
	instrument, err := c.orders.Instrument(req.InstrumentID)
	if err != nil {
		return tradingAsset{}, err
	}
	if !strings.EqualFold(instrument.Symbol, req.Symbol) || req.AssetClass != "equity" || !strings.EqualFold(req.Currency, "USD") {
		return tradingAsset{}, broker.NewOrderError("invalid_order", "selected instrument does not match order details")
	}
	accounts, err := c.TradingAccounts(ctx)
	if err != nil {
		return tradingAsset{}, err
	}
	if len(accounts) != 1 || accounts[0].ID != req.AccountID {
		return tradingAsset{}, broker.NewOrderError("invalid_order", "Alpaca account is not available for trading")
	}
	asset, err := c.tradingAsset(ctx, instrument.Symbol)
	if err != nil {
		return tradingAsset{}, err
	}
	if req.Notional != 0 || req.PositionEffect != "" || req.TrailPrice != 0 || req.TrailPercent != 0 {
		return tradingAsset{}, broker.NewOrderError("invalid_order", "this order mode is not available")
	}
	if req.OrderType != "market" && req.OrderType != "limit" && req.OrderType != "stop" && req.OrderType != "stop_limit" {
		return tradingAsset{}, broker.NewOrderError("invalid_order", "unsupported order type")
	}
	if req.TimeInForce != "day" && req.TimeInForce != "gtc" && !(req.TimeInForce == "ioc" && req.OrderType == "limit") {
		return tradingAsset{}, broker.NewOrderError("invalid_order", "unsupported time in force")
	}
	if req.ExtendedHours && (req.OrderType != "limit" || req.TimeInForce == "ioc") {
		return tradingAsset{}, broker.NewOrderError("invalid_order", "extended hours requires a DAY or GTC limit order")
	}
	if req.Quantity != float64(int64(req.Quantity)) && (!asset.Fractionable || req.OrderType != "market" || req.TimeInForce != "day") {
		return tradingAsset{}, broker.NewOrderError("invalid_order", "fractional quantity is unavailable for this asset or order type")
	}
	return asset, nil
}

func (c *Client) PreviewOrder(ctx context.Context, req broker.OrderRequest) (broker.OrderPreview, error) {
	if _, err := c.validateTradingOrder(ctx, req); err != nil {
		return broker.OrderPreview{}, err
	}
	preview := broker.OrderPreview{Warnings: []string{"Alpaca 不提供此类订单的券商预估费用；提交前无法确认最终成交价。"}}
	if req.OrderType == "market" {
		preview.Warnings = append(preview.Warnings, "市价单可能以不同于当前行情的价格成交。")
	} else if req.LimitPrice > 0 {
		preview.Amount = fmt.Sprintf("%.2f USD（数量 × 限价，未含费用）", req.Quantity*req.LimitPrice)
	}
	c.orders.RememberPreview(req)
	return preview, nil
}

func (c *Client) ReplyOrder(context.Context, string, string, bool) (broker.Order, error) {
	return broker.Order{}, broker.NewOrderError("invalid_order", "Alpaca does not use broker risk reply IDs")
}

func (c *Client) OrderAttempt(_ context.Context, accountID, clientID string) (broker.OrderAttemptState, error) {
	return c.orders.Attempt(accountID, clientID)
}
