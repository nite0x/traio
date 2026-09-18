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

func (c *Client) GetOrder(ctx context.Context, accountID, orderID string) (broker.Order, error) {
	orders, err := c.ListOrders(ctx, broker.OrderQuery{AccountID: accountID, Status: "all", Fresh: true})
	if err != nil {
		return broker.Order{}, err
	}
	for _, order := range orders {
		if order.ID == orderID {
			return order, nil
		}
	}
	return broker.Order{}, fmt.Errorf("ibkr: order not found in current session for this account")
}
func (c *Client) ListOrders(ctx context.Context, q broker.OrderQuery) ([]broker.Order, error) {
	if orders, ok := c.cachedOrders(q); ok && !q.Fresh {
		return orders, nil
	}
	if _, err := c.TradingAccounts(ctx); err != nil {
		return nil, err
	}
	rows, err := c.fetchOrderRows(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]broker.Order, 0, len(rows))
	for _, item := range rows {
		account := firstNonEmpty(textValue(item["acct"]), textValue(item["account"]))
		if q.AccountID != "" && account != q.AccountID {
			continue
		}
		order := normalizeIBKROrder(account, item)
		if !matchesOrderStatus(q.Status, order.Status) {
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
	c.orderFlow.mu.Lock()
	defer c.orderFlow.mu.Unlock()
	if c.orderFlow.pending != nil {
		return broker.NewOrderError("confirmation_pending", "resolve the pending IBKR order confirmation first")
	}
	id, err := strconv.ParseInt(orderID, 10, 64)
	if err != nil || id <= 0 {
		return invalidOrder("order_id must be a positive integer")
	}
	if _, err := c.tradingAccount(ctx, accountID); err != nil {
		return err
	}
	orders, err := c.ListOrders(ctx, broker.OrderQuery{AccountID: accountID, Status: "all", Fresh: true})
	if err != nil {
		return err
	}
	found := false
	for _, order := range orders {
		if order.ID == orderID {
			found = true
			if order.Status != "open" && order.Status != "pending_cancel" {
				return invalidOrder("order is no longer open")
			}
		}
	}
	if !found {
		return invalidOrder("order was not found in the selected account")
	}
	var result map[string]any
	if err := c.orderRequest(ctx, http.MethodDelete, "/iserver/account/"+url.PathEscape(accountID)+"/order/"+url.PathEscape(orderID), nil, &result); err != nil {
		return err
	}
	if !strings.EqualFold(textValue(result["msg"]), "Request was submitted") {
		return broker.NewOrderError("order_outcome_unknown", "IBKR cancellation result is unknown; refresh order status")
	}
	c.invalidateOrderCache()
	return nil
}

// Mutating requests are sent once, never followed across redirects or retried.
func (c *Client) orderRequest(ctx context.Context, method, path string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL()+"/v1/api"+path, reader)
	if err != nil {
		return fmt.Errorf("ibkr: invalid order request")
	}
	req.Header.Set("Content-Type", "application/json")
	hc := *c.httpClient
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return broker.NewOrderError("order_outcome_unknown", "IBKR request outcome is unknown; check orders before submitting again")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return gatewayUnauthorizedError(resp, strings.Split(path, "?")[0])
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return broker.NewOrderError("order_outcome_unknown", "IBKR response could not be read; check order status")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return broker.NewOrderError("order_outcome_unknown", fmt.Sprintf("IBKR order request returned HTTP %d; check order status before retrying", resp.StatusCode))
	}
	var payload any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&payload) != nil {
		return broker.NewOrderError("order_outcome_unknown", "IBKR returned an invalid response; check order status")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return broker.NewOrderError("order_outcome_unknown", "IBKR returned trailing response data; check order status")
	}
	if obj, ok := payload.(map[string]any); ok && textValue(obj["error"]) != "" {
		return broker.NewOrderError("order_rejected", c.safeOrderText(textValue(obj["error"])))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return broker.NewOrderError("order_outcome_unknown", "IBKR returned an unexpected response; check order status")
		}
	}
	return nil
}
func ibkrOrderType(v string) string {
	return map[string]string{"market": "MKT", "limit": "LMT", "stop": "STP", "stop_limit": "STP LMT"}[v]
}
func textValue(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
func floatValue(v any) float64 { f, _ := strconv.ParseFloat(textValue(v), 64); return f }
func normalizeIBKROrder(account string, m map[string]any) broker.Order {
	raw := firstNonEmpty(textValue(m["status"]), textValue(m["order_status"]))
	id := firstNonEmpty(textValue(m["orderId"]), textValue(m["order_id"]))
	kind := firstNonEmpty(textValue(m["orderType"]), textValue(m["order_type"]))
	orderType := map[string]string{"MKT": "market", "LMT": "limit", "STP": "stop", "STP LMT": "stop_limit", "STOP_LIMIT": "stop_limit", "TRAIL": "trailing_stop"}[strings.ToUpper(kind)]
	if orderType == "" {
		orderType = strings.ToLower(kind)
	}
	side := strings.ToLower(textValue(m["side"]))
	if side == "b" {
		side = "buy"
	}
	if side == "s" {
		side = "sell"
	}
	order := broker.Order{ID: id, AccountID: account, Currency: textValue(m["currency"]), ClientOrderID: firstNonEmpty(textValue(m["order_ref"]), textValue(m["cOID"])), Symbol: firstNonEmpty(textValue(m["ticker"]), textValue(m["symbol"])), InstrumentID: textValue(m["conid"]), Side: side, OrderType: orderType, Quantity: floatValue(firstNonEmpty(textValue(m["totalSize"]), textValue(m["total_size"]))), FilledQuantity: floatValue(firstNonEmpty(textValue(m["filledQuantity"]), textValue(m["cum_fill"]))), AverageFillPrice: floatValue(firstNonEmpty(textValue(m["avgPrice"]), textValue(m["avg_price"]))), TimeInForce: strings.ToLower(firstNonEmpty(textValue(m["timeInForce"]), textValue(m["tif"]))), Status: normalizeIBKRStatus(raw), RawStatus: raw}
	if orderType == "limit" || orderType == "stop_limit" {
		order.LimitPrice = floatValue(m["price"])
	}
	if orderType == "stop" {
		order.StopPrice = floatValue(m["price"])
	}
	if orderType == "stop_limit" {
		order.StopPrice = floatValue(m["auxPrice"])
	}
	return order
}
func normalizeIBKRStatus(s string) string {
	switch strings.ToLower(strings.ReplaceAll(s, " ", "")) {
	case "submitted", "presubmitted", "pendingsubmit", "partiallyfilled":
		return "open"
	case "pendingcancel":
		return "pending_cancel"
	case "filled":
		return "filled"
	case "cancelled", "canceled", "apicancelled":
		return "canceled"
	case "inactive", "rejected":
		return "rejected"
	default:
		return strings.ToLower(s)
	}
}
func matchesOrderStatus(filter, status string) bool {
	switch filter {
	case "", "all":
		return true
	case "open":
		return status == "open" || status == "pending_cancel"
	case "closed":
		return status == "filled" || status == "canceled" || status == "rejected" || status == "expired"
	default:
		return status == filter
	}
}
