package alpaca

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/nite/traio/internal/broker"
)

type alpacaOrder struct {
	ID, ClientOrderID, Symbol, AssetClass, Side, Type, TimeInForce, Status string
	Qty, FilledQty, LimitPrice, StopPrice, FilledAvgPrice                  string
	SubmittedAt, UpdatedAt                                                 string
}

func (o *alpacaOrder) UnmarshalJSON(data []byte) error {
	type wire struct {
		ID             string `json:"id"`
		ClientOrderID  string `json:"client_order_id"`
		Symbol         string `json:"symbol"`
		AssetClass     string `json:"asset_class"`
		Side           string `json:"side"`
		Type           string `json:"type"`
		TimeInForce    string `json:"time_in_force"`
		Status         string `json:"status"`
		Qty            string `json:"qty"`
		FilledQty      string `json:"filled_qty"`
		LimitPrice     string `json:"limit_price"`
		StopPrice      string `json:"stop_price"`
		FilledAvgPrice string `json:"filled_avg_price"`
		SubmittedAt    string `json:"submitted_at"`
		UpdatedAt      string `json:"updated_at"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*o = alpacaOrder{w.ID, w.ClientOrderID, w.Symbol, w.AssetClass, w.Side, w.Type, w.TimeInForce, w.Status, w.Qty, w.FilledQty, w.LimitPrice, w.StopPrice, w.FilledAvgPrice, w.SubmittedAt, w.UpdatedAt}
	return nil
}

var _ broker.TradingProvider = (*Client)(nil)

func (c *Client) PlaceOrder(ctx context.Context, req broker.OrderRequest) (broker.Order, error) {
	if err := broker.ValidateOrder(req); err != nil {
		return broker.Order{}, fmt.Errorf("alpaca: %w", err)
	}
	payload := map[string]any{"symbol": strings.ToUpper(req.Symbol), "side": req.Side, "type": req.OrderType, "time_in_force": req.TimeInForce}
	if req.Quantity > 0 {
		payload["qty"] = decimal(req.Quantity)
	} else {
		payload["notional"] = decimal(req.Notional)
	}
	optionalPrice(payload, "limit_price", req.LimitPrice)
	optionalPrice(payload, "stop_price", req.StopPrice)
	optionalPrice(payload, "trail_price", req.TrailPrice)
	optionalPrice(payload, "trail_percent", req.TrailPercent)
	if req.ClientOrderID != "" {
		payload["client_order_id"] = req.ClientOrderID
	}
	if req.ExtendedHours {
		payload["extended_hours"] = true
	}
	body, _ := json.Marshal(payload)
	resp, err := c.Do(ctx, http.MethodPost, "orders", bytes.NewReader(body))
	if err != nil {
		return broker.Order{}, err
	}
	defer resp.Body.Close()
	var raw alpacaOrder
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return broker.Order{}, fmt.Errorf("alpaca: decode order: %w", err)
	}
	return normalizeAlpacaOrder(req.AccountID, raw), nil
}

func (c *Client) GetOrder(ctx context.Context, accountID, orderID string) (broker.Order, error) {
	resp, err := c.Do(ctx, http.MethodGet, "orders/"+url.PathEscape(orderID), nil)
	if err != nil {
		return broker.Order{}, err
	}
	defer resp.Body.Close()
	var raw alpacaOrder
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return broker.Order{}, fmt.Errorf("alpaca: decode order: %w", err)
	}
	return normalizeAlpacaOrder(accountID, raw), nil
}

func (c *Client) ListOrders(ctx context.Context, q broker.OrderQuery) ([]broker.Order, error) {
	values := url.Values{}
	if q.Status != "" {
		values.Set("status", q.Status)
	}
	if q.Limit > 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}
	path := "orders"
	if len(values) > 0 {
		path += "?" + values.Encode()
	}
	resp, err := c.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw []alpacaOrder
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("alpaca: decode orders: %w", err)
	}
	out := make([]broker.Order, len(raw))
	for i := range raw {
		out[i] = normalizeAlpacaOrder(q.AccountID, raw[i])
	}
	return out, nil
}

func (c *Client) CancelOrder(ctx context.Context, _ string, orderID string) error {
	resp, err := c.Do(ctx, http.MethodDelete, "orders/"+url.PathEscape(orderID), nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func decimal(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
func optionalPrice(m map[string]any, key string, value float64) {
	if value > 0 {
		m[key] = decimal(value)
	}
}
func normalizeAlpacaOrder(accountID string, o alpacaOrder) broker.Order {
	return broker.Order{ID: o.ID, ClientOrderID: o.ClientOrderID, AccountID: accountID, Symbol: o.Symbol, AssetClass: o.AssetClass, Side: o.Side, OrderType: o.Type, Quantity: parseDecimalOrZero(o.Qty), FilledQuantity: parseDecimalOrZero(o.FilledQty), LimitPrice: parseDecimalOrZero(o.LimitPrice), StopPrice: parseDecimalOrZero(o.StopPrice), AverageFillPrice: parseDecimalOrZero(o.FilledAvgPrice), TimeInForce: o.TimeInForce, Status: normalizeStatus(o.Status), RawStatus: o.Status, SubmittedAt: o.SubmittedAt, UpdatedAt: o.UpdatedAt}
}
func normalizeStatus(s string) string {
	switch strings.ToLower(s) {
	case "new", "accepted", "pending_new", "accepted_for_bidding", "partially_filled", "pending_replace":
		return "open"
	case "filled":
		return "filled"
	case "canceled", "expired", "replaced":
		return "canceled"
	case "rejected", "suspended", "stopped":
		return "rejected"
	default:
		return strings.ToLower(s)
	}
}
