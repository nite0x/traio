package schwab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nite/traio/internal/broker"
)

var _ broker.TradingProvider = (*Client)(nil)

type schwabOrder struct {
	OrderID                                                      int64  `json:"orderId"`
	AccountNumber                                                string `json:"accountNumber"`
	Status, EnteredTime, CloseTime, Duration, OrderType, Session string
	Quantity, FilledQuantity, Price, StopPrice                   float64
	Legs                                                         []struct {
		Instruction, PositionEffect string
		Instrument                  struct{ Symbol, AssetType string } `json:"instrument"`
	} `json:"orderLegCollection"`
}

func (c *Client) PlaceOrder(ctx context.Context, req broker.OrderRequest) (order broker.Order, err error) {
	if err = c.validateTradingOrder(ctx, req); err != nil {
		return broker.Order{}, err
	}
	if previous, exists, beginErr := c.orders.Begin(req); beginErr != nil {
		return broker.Order{}, beginErr
	} else if exists {
		if previous.Code != "" {
			return previous.Order, broker.NewOrderError(previous.Code, previous.Error)
		}
		return previous.Order, nil
	}
	defer func() { c.orders.Finish(req, order, err) }()
	hash, err := c.accountHash(ctx, req.AccountID)
	if err != nil {
		return broker.Order{}, err
	}
	body, _ := json.Marshal(schwabOrderPayload(req))
	endpoint := c.traderEndpoint("/accounts/" + url.PathEscape(hash) + "/orders")
	resp, err := c.Do(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 && apiErr.StatusCode != 429 {
			return broker.Order{}, broker.NewOrderError("order_rejected", err.Error())
		}
		return broker.Order{}, err
	}
	defer resp.Body.Close()
	id := pathBase(resp.Header.Get("Location"))
	parsedID, parseErr := strconv.ParseInt(id, 10, 64)
	if parseErr != nil || parsedID <= 0 {
		return broker.Order{}, fmt.Errorf("schwab: order response has no Location order id")
	}
	return broker.Order{ID: id, ClientOrderID: req.ClientOrderID, AccountID: req.AccountID, Symbol: req.Symbol, AssetClass: req.AssetClass, Side: req.Side, OrderType: req.OrderType, Quantity: req.Quantity, LimitPrice: req.LimitPrice, StopPrice: req.StopPrice, TimeInForce: req.TimeInForce, Status: "unknown", RawStatus: "SUBMITTED_STATUS_UNVERIFIED"}, nil
}

func (c *Client) GetOrder(ctx context.Context, accountID, orderID string) (broker.Order, error) {
	hash, err := c.accountHash(ctx, accountID)
	if err != nil {
		return broker.Order{}, err
	}
	resp, err := c.Do(ctx, http.MethodGet, c.traderEndpoint("/accounts/"+url.PathEscape(hash)+"/orders/"+url.PathEscape(orderID)), nil)
	if err != nil {
		return broker.Order{}, err
	}
	defer resp.Body.Close()
	var raw schwabOrder
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return broker.Order{}, fmt.Errorf("schwab: decode order: %w", err)
	}
	if raw.AccountNumber != "" && raw.AccountNumber != accountID {
		return broker.Order{}, broker.NewOrderError("invalid_order", "Schwab returned an order for another account")
	}
	return normalizeSchwabOrder(accountID, raw), nil
}
func (c *Client) ListOrders(ctx context.Context, q broker.OrderQuery) ([]broker.Order, error) {
	hash, err := c.accountHash(ctx, q.AccountID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	v := url.Values{"fromEnteredTime": {now.AddDate(0, -2, 0).Format(time.RFC3339)}, "toEnteredTime": {now.Format(time.RFC3339)}}
	if q.Limit > 0 {
		v.Set("maxResults", strconv.Itoa(q.Limit))
	}
	resp, err := c.Do(ctx, http.MethodGet, c.traderEndpoint("/accounts/"+url.PathEscape(hash)+"/orders?"+v.Encode()), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw []schwabOrder
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("schwab: decode orders: %w", err)
	}
	out := make([]broker.Order, 0, len(raw))
	for _, item := range raw {
		if item.AccountNumber != "" && item.AccountNumber != q.AccountID {
			return nil, broker.NewOrderError("invalid_order", "Schwab returned orders for another account")
		}
		o := normalizeSchwabOrder(q.AccountID, item)
		if q.Status == "" || q.Status == "all" || o.Status == q.Status {
			out = append(out, o)
		}
	}
	return out, nil
}
func (c *Client) CancelOrder(ctx context.Context, accountID, orderID string) error {
	order, err := c.GetOrder(ctx, accountID, orderID)
	if err != nil {
		return err
	}
	if order.ID != orderID || order.Status != "open" {
		return broker.NewOrderError("invalid_order", "order is not open for cancellation")
	}
	hash, err := c.accountHash(ctx, accountID)
	if err != nil {
		return err
	}
	resp, err := c.Do(ctx, http.MethodDelete, c.traderEndpoint("/accounts/"+url.PathEscape(hash)+"/orders/"+url.PathEscape(orderID)), nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (c *Client) accountHash(ctx context.Context, accountID string) (string, error) {
	resp, err := c.Do(ctx, http.MethodGet, c.traderEndpoint("/accounts/accountNumbers"), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var items []struct {
		AccountNumber string `json:"accountNumber"`
		HashValue     string `json:"hashValue"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return "", fmt.Errorf("schwab: decode account numbers: %w", err)
	}
	for _, item := range items {
		if item.AccountNumber == accountID && item.HashValue != "" {
			return item.HashValue, nil
		}
	}
	return "", fmt.Errorf("schwab: account %s not found", accountID)
}
func (c *Client) traderEndpoint(path string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.traderURL + path
}
func schwabOrderType(v string) string {
	return map[string]string{"market": "MARKET", "limit": "LIMIT", "stop": "STOP", "stop_limit": "STOP_LIMIT", "trailing_stop": "TRAILING_STOP"}[v]
}
func schwabAssetType(v string) string {
	switch v {
	case "option":
		return "OPTION"
	case "fund":
		return "MUTUAL_FUND"
	case "bond":
		return "FIXED_INCOME"
	default:
		return "EQUITY"
	}
}
func formatPrice(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
func pathBase(v string) string {
	u, err := url.Parse(v)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
func normalizeSchwabOrder(account string, o schwabOrder) broker.Order {
	symbol, asset, side := "", "", ""
	if len(o.Legs) > 0 {
		symbol = o.Legs[0].Instrument.Symbol
		asset = strings.ToLower(o.Legs[0].Instrument.AssetType)
		side = strings.ToLower(strings.Split(o.Legs[0].Instruction, "_")[0])
	}
	tif := strings.ToLower(o.Duration)
	switch o.Duration {
	case "GOOD_TILL_CANCEL":
		tif = "gtc"
	case "IMMEDIATE_OR_CANCEL":
		tif = "ioc"
	}
	return broker.Order{ID: strconv.FormatInt(o.OrderID, 10), AccountID: account, Symbol: symbol, AssetClass: asset, Side: side, OrderType: strings.ToLower(o.OrderType), Quantity: o.Quantity, FilledQuantity: o.FilledQuantity, LimitPrice: o.Price, StopPrice: o.StopPrice, TimeInForce: tif, Status: normalizeSchwabStatus(o.Status), RawStatus: o.Status, SubmittedAt: o.EnteredTime, UpdatedAt: o.CloseTime}
}
func normalizeSchwabStatus(s string) string {
	switch strings.ToUpper(s) {
	case "AWAITING_PARENT_ORDER", "AWAITING_CONDITION", "AWAITING_STOP_CONDITION", "AWAITING_MANUAL_REVIEW", "ACCEPTED", "AWAITING_UR_OUT", "PENDING_ACTIVATION", "QUEUED", "WORKING", "PENDING_ACKNOWLEDGEMENT", "PENDING_RECALL", "NEW", "AWAITING_RELEASE_TIME", "PENDING_REPLACE":
		return "open"
	case "PENDING_CANCEL":
		return "pending_cancel"
	case "FILLED":
		return "filled"
	case "CANCELED", "EXPIRED", "REPLACED":
		return "canceled"
	case "REJECTED":
		return "rejected"
	default:
		return strings.ToLower(s)
	}
}
