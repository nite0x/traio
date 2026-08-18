package ibkr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/nite/traio/internal/broker"
)

var _ broker.TradingProvider = (*Client)(nil)

func (c *Client) PlaceOrder(ctx context.Context, req broker.OrderRequest) (broker.Order, error) {
	if err := broker.ValidateOrder(req); err != nil {
		return broker.Order{}, fmt.Errorf("ibkr: %w", err)
	}
	if req.Notional > 0 {
		return broker.Order{}, fmt.Errorf("ibkr: notional orders are not supported by this adapter")
	}
	conid, err := strconv.ParseInt(strings.TrimSpace(req.InstrumentID), 10, 64)
	if err != nil || conid <= 0 {
		return broker.Order{}, fmt.Errorf("ibkr: instrument_id must be a numeric conid")
	}
	if req.AccountID == "" {
		req.AccountID, err = c.resolveAccountID(ctx)
		if err != nil {
			return broker.Order{}, err
		}
	}
	payload := map[string]any{"acctId": req.AccountID, "conid": conid, "side": strings.ToUpper(req.Side), "orderType": ibkrOrderType(req.OrderType), "quantity": req.Quantity, "tif": strings.ToUpper(req.TimeInForce)}
	if req.LimitPrice > 0 {
		payload["price"] = req.LimitPrice
	}
	if req.StopPrice > 0 {
		payload["auxPrice"] = req.StopPrice
	}
	if req.ClientOrderID != "" {
		payload["cOID"] = req.ClientOrderID
	}
	if req.ExtendedHours {
		payload["outsideRTH"] = true
	}
	body, _ := json.Marshal(map[string]any{"orders": []any{payload}})
	var response []map[string]any
	if err := c.orderRequest(ctx, http.MethodPost, "/iserver/account/"+url.PathEscape(req.AccountID)+"/orders", body, &response); err != nil {
		return broker.Order{}, err
	}
	if len(response) == 0 {
		return broker.Order{}, fmt.Errorf("ibkr: empty order response")
	}
	if replyID := textValue(response[0]["id"]); replyID != "" && textValue(response[0]["order_id"]) == "" {
		return broker.Order{}, fmt.Errorf("ibkr: order requires confirmation (reply_id=%s): %s", replyID, textValue(response[0]["message"]))
	}
	return normalizeIBKROrder(req.AccountID, response[0]), nil
}

func (c *Client) GetOrder(ctx context.Context, accountID, orderID string) (broker.Order, error) {
	orders, err := c.ListOrders(ctx, broker.OrderQuery{AccountID: accountID, Status: "all"})
	if err != nil {
		return broker.Order{}, err
	}
	for _, order := range orders {
		if order.ID == orderID {
			return order, nil
		}
	}
	return broker.Order{}, fmt.Errorf("ibkr: order %s not found", orderID)
}

func (c *Client) ListOrders(ctx context.Context, q broker.OrderQuery) ([]broker.Order, error) {
	var raw struct {
		Orders []map[string]any `json:"orders"`
	}
	if err := c.orderRequest(ctx, http.MethodGet, "/iserver/account/orders?force=true", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]broker.Order, 0, len(raw.Orders))
	for _, item := range raw.Orders {
		account := textValue(item["acct"])
		if q.AccountID != "" && account != q.AccountID {
			continue
		}
		order := normalizeIBKROrder(account, item)
		if q.Status != "" && q.Status != "all" && order.Status != q.Status {
			continue
		}
		out = append(out, order)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

func (c *Client) CancelOrder(ctx context.Context, accountID, orderID string) error {
	return c.orderRequest(ctx, http.MethodDelete, "/iserver/account/"+url.PathEscape(accountID)+"/order/"+url.PathEscape(orderID), nil, nil)
}

func (c *Client) orderRequest(ctx context.Context, method, path string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.GatewayURL, "/")+"/v1/api"+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ibkr: order request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ibkr: order request status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("ibkr: decode order response: %w", err)
		}
	}
	return nil
}
func ibkrOrderType(v string) string {
	return map[string]string{"market": "MKT", "limit": "LMT", "stop": "STP", "stop_limit": "STOP_LIMIT", "trailing_stop": "TRAIL"}[v]
}
func textValue(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
func floatValue(v any) float64 { f, _ := strconv.ParseFloat(textValue(v), 64); return f }
func normalizeIBKROrder(account string, m map[string]any) broker.Order {
	raw := textValue(m["status"])
	id := textValue(m["orderId"])
	if id == "" {
		id = textValue(m["order_id"])
	}
	return broker.Order{ID: id, AccountID: account, Symbol: textValue(m["ticker"]), InstrumentID: textValue(m["conid"]), Side: strings.ToLower(textValue(m["side"])), OrderType: strings.ToLower(textValue(m["orderType"])), Quantity: floatValue(m["totalSize"]), FilledQuantity: floatValue(m["filledQuantity"]), LimitPrice: floatValue(m["price"]), AverageFillPrice: floatValue(m["avgPrice"]), TimeInForce: strings.ToLower(textValue(m["tif"])), Status: normalizeIBKRStatus(raw), RawStatus: raw}
}
func normalizeIBKRStatus(s string) string {
	switch strings.ToLower(strings.ReplaceAll(s, " ", "")) {
	case "submitted", "presubmitted", "pendingsubmit", "pendingcancel", "partiallyfilled":
		return "open"
	case "filled":
		return "filled"
	case "cancelled", "apicancelled", "inactive":
		return "canceled"
	default:
		return strings.ToLower(s)
	}
}
