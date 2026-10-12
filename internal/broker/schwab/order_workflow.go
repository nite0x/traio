package schwab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/nite/traio/internal/broker"
)

var _ broker.OrderWorkflowProvider = (*Client)(nil)
var _ broker.OrderQuoteProvider = (*Client)(nil)

var orderEquitySymbol = regexp.MustCompile(`^[A-Z][A-Z0-9.\-]{0,14}$`)

func (c *Client) TradingAccounts(ctx context.Context) ([]broker.TradingAccount, error) {
	accounts, err := c.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]broker.TradingAccount, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, broker.TradingAccount{ID: account.ID, Name: account.DisplayName, SupportsFractions: false})
	}
	return out, nil
}

func (c *Client) SearchOrderInstruments(ctx context.Context, query string) ([]broker.Instrument, error) {
	query = strings.ToUpper(strings.TrimSpace(query))
	if query == "" {
		return []broker.Instrument{}, nil
	}
	if !orderEquitySymbol.MatchString(query) {
		return nil, broker.NewOrderError("invalid_order", "enter a valid US stock or ETF symbol")
	}
	c.mu.RLock()
	endpoint := c.marketURL + "/instruments?" + url.Values{"symbol": {query}, "projection": {"symbol-search"}}.Encode()
	c.mu.RUnlock()
	resp, err := c.Do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var data struct {
		Instruments []struct {
			Symbol, Description, Exchange, AssetType string
		} `json:"instruments"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data); err != nil {
		return nil, fmt.Errorf("schwab: decode instrument search: %w", err)
	}
	out := []broker.Instrument{}
	for _, item := range data.Instruments {
		if !strings.EqualFold(item.Symbol, query) || !strings.EqualFold(item.AssetType, "EQUITY") {
			continue
		}
		out = append(out, c.orders.RememberInstrument(broker.Instrument{Symbol: strings.ToUpper(item.Symbol), Name: item.Description, Exchange: item.Exchange, SecType: "STK", Currency: "USD"}))
	}
	return out, nil
}

func (c *Client) OrderInstrument(_ context.Context, id string) (broker.Instrument, error) {
	return c.orders.Instrument(id)
}

func (c *Client) OrderQuote(ctx context.Context, id string) (broker.OrderQuote, error) {
	instrument, err := c.orders.Instrument(id)
	if err != nil {
		return broker.OrderQuote{}, err
	}
	quote, err := c.GetQuote(ctx, instrument.Symbol)
	if err != nil {
		return broker.OrderQuote{}, err
	}
	value := broker.OrderQuote{ConID: instrument.ConID, Availability: "realtime", LastKind: "last"}
	if quote.Delayed {
		value.Availability = "delayed"
	}
	if quote.Last > 0 {
		value.Last = &quote.Last
	}
	if quote.Bid > 0 {
		value.Bid = &quote.Bid
	}
	if quote.Ask > 0 {
		value.Ask = &quote.Ask
	}
	if quote.AsOf > 0 {
		value.UpdatedAt = time.Unix(quote.AsOf, 0).UTC().Format(time.RFC3339)
	}
	return value, nil
}

func (c *Client) validateTradingOrder(ctx context.Context, req broker.OrderRequest) error {
	if err := broker.ValidateOrder(req); err != nil {
		return broker.NewOrderError("invalid_order", err.Error())
	}
	if err := broker.ValidateClientOrderID(req.ClientOrderID); err != nil {
		return err
	}
	instrument, err := c.orders.Instrument(req.InstrumentID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(instrument.Symbol, req.Symbol) || req.AssetClass != "equity" || !strings.EqualFold(req.Currency, "USD") {
		return broker.NewOrderError("invalid_order", "selected instrument does not match order details")
	}
	if req.Notional != 0 || req.PositionEffect != "" || req.TrailPrice != 0 || req.TrailPercent != 0 || req.ExtendedHours {
		return broker.NewOrderError("invalid_order", "this Schwab order mode is not available")
	}
	if req.Quantity != float64(int64(req.Quantity)) || req.Quantity <= 0 {
		return broker.NewOrderError("invalid_order", "Schwab stock orders require whole shares")
	}
	if req.OrderType != "market" && req.OrderType != "limit" && req.OrderType != "stop" && req.OrderType != "stop_limit" {
		return broker.NewOrderError("invalid_order", "unsupported order type")
	}
	if req.TimeInForce != "day" && req.TimeInForce != "gtc" && !(req.TimeInForce == "ioc" && req.OrderType == "limit") {
		return broker.NewOrderError("invalid_order", "unsupported time in force")
	}
	accounts, err := c.TradingAccounts(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, account := range accounts {
		found = found || account.ID == req.AccountID
	}
	if !found {
		return broker.NewOrderError("invalid_order", "Schwab account is not available for trading")
	}
	current, err := c.SearchOrderInstruments(ctx, instrument.Symbol)
	if err != nil {
		return err
	}
	if len(current) == 0 {
		return broker.NewOrderError("invalid_order", "selected Schwab instrument is no longer available")
	}
	return nil
}

func (c *Client) PreviewOrder(ctx context.Context, req broker.OrderRequest) (broker.OrderPreview, error) {
	if err := c.validateTradingOrder(ctx, req); err != nil {
		return broker.OrderPreview{}, err
	}
	hash, err := c.accountHash(ctx, req.AccountID)
	if err != nil {
		return broker.OrderPreview{}, err
	}
	body, _ := json.Marshal(schwabOrderPayload(req))
	resp, err := c.Do(ctx, http.MethodPost, c.traderEndpoint("/accounts/"+url.PathEscape(hash)+"/previewOrder"), bytes.NewReader(body))
	if err != nil {
		return broker.OrderPreview{}, err
	}
	defer resp.Body.Close()
	var result struct {
		OrderValidationResult json.RawMessage `json:"orderValidationResult"`
		CommissionAndFee      json.RawMessage `json:"commissionAndFee"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return broker.OrderPreview{}, fmt.Errorf("schwab: decode order preview: %w", err)
	}
	if len(result.OrderValidationResult) > 0 {
		var validation map[string]json.RawMessage
		if json.Unmarshal(result.OrderValidationResult, &validation) != nil {
			return broker.OrderPreview{}, broker.NewOrderError("order_rejected", "Schwab preview returned an unknown validation result")
		}
		if validation != nil {
			for _, key := range []string{"valid", "isValid"} {
				if string(validation[key]) == "false" {
					return broker.OrderPreview{}, broker.NewOrderError("order_rejected", "Schwab preview did not validate this order")
				}
			}
			for _, key := range []string{"orderRejects", "validationErrors", "errors"} {
				if raw := validation[key]; len(raw) > 0 && string(raw) != "null" && string(raw) != "[]" {
					return broker.OrderPreview{}, broker.NewOrderError("order_rejected", "Schwab preview reported validation errors; inspect the order in Schwab")
				}
			}
		}
	}
	preview := broker.OrderPreview{Warnings: []string{"嘉信预览只是估算；提交后仍可能被拒绝或以不同价格成交。"}}
	if req.LimitPrice > 0 {
		preview.Amount = fmt.Sprintf("%.2f USD（数量 × 限价，未含费用）", req.Quantity*req.LimitPrice)
	}
	var fees map[string]any
	if json.Unmarshal(result.CommissionAndFee, &fees) == nil {
		for _, key := range []string{"totalCommissionAndFee", "totalCommission", "commission"} {
			if value, ok := fees[key]; ok {
				preview.Commission = fmt.Sprint(value)
				break
			}
		}
	}
	c.orders.RememberPreview(req)
	return preview, nil
}

func (c *Client) ReplyOrder(context.Context, string, string, bool) (broker.Order, error) {
	return broker.Order{}, broker.NewOrderError("invalid_order", "Schwab does not use separate order reply IDs")
}

func (c *Client) OrderAttempt(_ context.Context, accountID, clientID string) (broker.OrderAttemptState, error) {
	return c.orders.Attempt(accountID, clientID)
}

func schwabOrderPayload(req broker.OrderRequest) map[string]any {
	duration := map[string]string{"day": "DAY", "gtc": "GOOD_TILL_CANCEL", "ioc": "IMMEDIATE_OR_CANCEL"}[req.TimeInForce]
	payload := map[string]any{"session": "NORMAL", "duration": duration, "orderType": schwabOrderType(req.OrderType), "orderStrategyType": "SINGLE", "orderLegCollection": []any{map[string]any{"instruction": strings.ToUpper(req.Side), "quantity": req.Quantity, "instrument": map[string]any{"symbol": strings.ToUpper(req.Symbol), "assetType": "EQUITY"}}}}
	if req.LimitPrice > 0 {
		payload["price"] = formatPrice(req.LimitPrice)
	}
	if req.StopPrice > 0 {
		payload["stopPrice"] = formatPrice(req.StopPrice)
	}
	return payload
}
