package ibkr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nite/traio/internal/broker"
)

type orderAttempt struct {
	request broker.OrderRequest
	order   broker.Order
	err     error
}
type replyAttempt struct {
	account   string
	confirmed bool
	order     broker.Order
	err       error
}
type orderWorkflowState struct {
	mu       sync.Mutex
	attempts map[string]*orderAttempt
	replies  map[string]replyAttempt
	pending  *orderAttempt
}

var orderReferencePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func invalidOrder(message string) error { return broker.NewOrderError("invalid_order", message) }

func (c *Client) TradingAccounts(ctx context.Context) ([]broker.TradingAccount, error) {
	var raw struct {
		Accounts []string          `json:"accounts"`
		Aliases  map[string]string `json:"aliases"`
		IsPaper  *bool             `json:"isPaper"`
		Props    map[string]struct {
			SupportsFractions bool `json:"supportsFractions"`
			AllowCustomerTime bool `json:"allowCustomerTime"`
		} `json:"acctProps"`
	}
	if err := c.getTradingJSON(ctx, "/iserver/accounts", &raw); err != nil {
		return nil, err
	}
	if raw.Accounts == nil {
		return nil, fmt.Errorf("ibkr: brokerage session is not ready; sign in to Gateway")
	}
	out := make([]broker.TradingAccount, 0, len(raw.Accounts))
	for _, id := range raw.Accounts {
		out = append(out, broker.TradingAccount{ID: id, Name: firstNonEmpty(raw.Aliases[id], id), IsPaper: raw.IsPaper, SupportsFractions: raw.Props[id].SupportsFractions, AllowCustomerTime: raw.Props[id].AllowCustomerTime})
	}
	return out, nil
}
func (c *Client) tradingAccount(ctx context.Context, id string) (broker.TradingAccount, error) {
	if strings.TrimSpace(id) == "" {
		return broker.TradingAccount{}, invalidOrder("account_id is required")
	}
	accounts, err := c.TradingAccounts(ctx)
	if err != nil {
		return broker.TradingAccount{}, err
	}
	for _, a := range accounts {
		if a.ID == id {
			return a, nil
		}
	}
	return broker.TradingAccount{}, invalidOrder("selected account is not available for trading on this Gateway")
}
func (c *Client) SearchOrderInstruments(ctx context.Context, query string) ([]broker.Instrument, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []broker.Instrument{}, nil
	}
	// Explicitly scope the search to equities; option-chain roots must not become stock orders.
	var rows []struct {
		ConID       any    `json:"conid"`
		Symbol      string `json:"symbol"`
		Name        string `json:"companyName"`
		Header      string `json:"companyHeader"`
		Description string `json:"description"`
		Exchange    string `json:"listingExchange"`
		SecType     string `json:"secType"`
		Currency    string `json:"currency"`
		Sections    []struct {
			SecType string `json:"secType"`
		} `json:"sections"`
	}
	if err := c.getTradingJSON(ctx, "/iserver/secdef/search?secType=STK&symbol="+url.QueryEscape(query), &rows); err != nil {
		return nil, err
	}
	out := []broker.Instrument{}
	for _, r := range rows {
		stock := r.SecType == "STK"
		for _, s := range r.Sections {
			stock = stock || s.SecType == "STK"
		}
		conid, err := strconv.ParseInt(textValue(r.ConID), 10, 64)
		if !stock || err != nil || conid <= 0 {
			continue
		}
		out = append(out, broker.Instrument{ConID: conid, Symbol: r.Symbol, Name: firstNonEmpty(r.Name, r.Header), SecType: "STK", Exchange: firstNonEmpty(r.Exchange, r.Description), Currency: r.Currency})
	}
	return out, nil
}
func (c *Client) OrderInstrument(ctx context.Context, id string) (broker.Instrument, error) {
	conid, err := strconv.ParseInt(id, 10, 64)
	if err != nil || conid <= 0 {
		return broker.Instrument{}, invalidOrder("instrument_id must be a positive numeric conid")
	}
	var raw map[string]any
	if err := c.getTradingJSON(ctx, "/iserver/contract/"+strconv.FormatInt(conid, 10)+"/info", &raw); err != nil {
		return broker.Instrument{}, err
	}
	// Contract info uses instrument_type, unlike secdef/search's secType.
	secType := textValue(raw["instrument_type"])
	if secType == "" {
		return broker.Instrument{}, fmt.Errorf("IBKR 合约详情缺少证券类型，请重新选择合约")
	}
	if secType != "STK" {
		return broker.Instrument{}, invalidOrder("当前仅支持 IBKR 股票和 ETF，该合约类型为 " + secType)
	}
	returnedConID, err := strconv.ParseInt(textValue(raw["con_id"]), 10, 64)
	if err != nil || returnedConID != conid {
		return broker.Instrument{}, fmt.Errorf("IBKR 返回的合约编号与所选合约不一致，请重新选择合约")
	}
	symbol, currency := textValue(raw["symbol"]), textValue(raw["currency"])
	if symbol == "" || currency == "" {
		return broker.Instrument{}, fmt.Errorf("ibkr: contract details are incomplete")
	}
	return broker.Instrument{ConID: conid, Symbol: symbol, Name: firstNonEmpty(textValue(raw["company_name"]), textValue(raw["instrument_name"]), symbol), SecType: secType, Exchange: firstNonEmpty(textValue(raw["listing_exchange"]), textValue(raw["exchange"])), Currency: currency}, nil
}
func ibkrOrderPayload(req broker.OrderRequest) (map[string]any, error) {
	if err := broker.ValidateOrder(req); err != nil {
		return nil, invalidOrder(err.Error())
	}
	if req.Notional != 0 {
		return nil, invalidOrder("IBKR order entry requires a share quantity")
	}
	if req.OrderType != "market" && req.OrderType != "limit" && req.OrderType != "stop" && req.OrderType != "stop_limit" {
		return nil, invalidOrder("unsupported IBKR order type")
	}
	if req.TimeInForce != "day" && req.TimeInForce != "gtc" && req.TimeInForce != "ioc" {
		return nil, invalidOrder("unsupported IBKR time in force")
	}
	if req.TimeInForce == "ioc" && req.OrderType != "limit" {
		return nil, invalidOrder("IOC is supported only for limit orders")
	}
	if req.ExtendedHours && req.OrderType != "limit" {
		return nil, invalidOrder("extended hours is supported only for limit orders")
	}
	if (req.OrderType == "market" && (req.LimitPrice != 0 || req.StopPrice != 0)) || (req.OrderType == "limit" && req.StopPrice != 0) || (req.OrderType == "stop" && req.LimitPrice != 0) || req.TrailPrice != 0 || req.TrailPercent != 0 {
		return nil, invalidOrder("unexpected price fields for this order type")
	}
	if req.AssetClass != "" && req.AssetClass != "equity" {
		return nil, invalidOrder("only equity orders are supported")
	}
	if req.PositionEffect != "" {
		return nil, invalidOrder("position_effect is not supported for stock orders")
	}
	if !orderReferencePattern.MatchString(req.ClientOrderID) {
		return nil, invalidOrder("client_order_id must contain 1-64 letters, digits, underscores or hyphens")
	}
	conid, err := strconv.ParseInt(req.InstrumentID, 10, 64)
	if err != nil || conid <= 0 {
		return nil, invalidOrder("instrument_id must be a positive numeric conid")
	}
	payload := map[string]any{"acctId": req.AccountID, "conid": conid, "side": strings.ToUpper(req.Side), "orderType": ibkrOrderType(req.OrderType), "quantity": req.Quantity, "tif": strings.ToUpper(req.TimeInForce), "cOID": req.ClientOrderID, "outsideRTH": req.ExtendedHours}
	switch req.OrderType {
	case "limit":
		payload["price"] = req.LimitPrice
	case "stop":
		payload["price"] = req.StopPrice
	case "stop_limit":
		payload["price"] = req.LimitPrice
		payload["auxPrice"] = req.StopPrice
	}
	return payload, nil
}
func (c *Client) prepareOrder(ctx context.Context, req broker.OrderRequest) ([]byte, error) {
	payload, err := ibkrOrderPayload(req)
	if err != nil {
		return nil, err
	}
	account, err := c.tradingAccount(ctx, req.AccountID)
	if err != nil {
		return nil, err
	}
	if req.Quantity != math.Trunc(req.Quantity) && !account.SupportsFractions {
		return nil, invalidOrder("this IBKR account does not support fractional shares")
	}
	instrument, err := c.OrderInstrument(ctx, req.InstrumentID)
	if err != nil {
		return nil, err
	}
	if req.Symbol != "" && !strings.EqualFold(req.Symbol, instrument.Symbol) {
		return nil, invalidOrder("symbol does not match the selected IBKR contract")
	}
	if req.Currency != "" && req.Currency != instrument.Currency {
		return nil, invalidOrder("currency does not match the selected IBKR contract")
	}
	if account.AllowCustomerTime {
		return nil, invalidOrder("accounts requiring a manual order timestamp are not supported by this order ticket")
	}
	return json.Marshal(map[string]any{"orders": []any{payload}})
}
func (c *Client) PreviewOrder(ctx context.Context, req broker.OrderRequest) (broker.OrderPreview, error) {
	c.orderFlow.mu.Lock()
	defer c.orderFlow.mu.Unlock()
	if c.orderFlow.pending != nil {
		return broker.OrderPreview{}, broker.NewOrderError("confirmation_pending", "resolve the pending IBKR order confirmation first")
	}
	body, err := c.prepareOrder(ctx, req)
	if err != nil {
		return broker.OrderPreview{}, err
	}
	// IBKR requires a snapshot request before what-if. Never infer a commission locally.
	var snapshot json.RawMessage
	if err := c.getTradingJSON(ctx, "/iserver/marketdata/snapshot?conids="+url.QueryEscape(req.InstrumentID)+"&fields=31,84,86", &snapshot); err != nil {
		return broker.OrderPreview{}, err
	}
	var raw map[string]json.RawMessage
	if err := c.orderRequest(ctx, http.MethodPost, "/iserver/account/"+url.PathEscape(req.AccountID)+"/orders/whatif", body, &raw); err != nil {
		return broker.OrderPreview{}, err
	}
	var amount struct {
		Amount     string `json:"amount"`
		Commission string `json:"commission"`
		Total      string `json:"total"`
	}
	if json.Unmarshal(raw["amount"], &amount) != nil {
		return broker.OrderPreview{}, fmt.Errorf("ibkr: preview is unavailable; no order was submitted")
	}
	preview := broker.OrderPreview{Amount: amount.Amount, Commission: amount.Commission, Total: amount.Total, Warnings: []string{}}
	for key, dst := range map[string]*broker.OrderImpact{"equity": &preview.Equity, "initial": &preview.InitialMargin, "maintenance": &preview.MaintenanceMargin, "position": &preview.Position} {
		if len(raw[key]) > 0 {
			_ = json.Unmarshal(raw[key], dst)
		}
	}
	var warn any
	_ = json.Unmarshal(raw["warn"], &warn)
	preview.Warnings = c.orderMessages(warn)
	return preview, nil
}
func (c *Client) PlaceOrder(ctx context.Context, req broker.OrderRequest) (broker.Order, error) {
	state := &c.orderFlow
	state.mu.Lock()
	defer state.mu.Unlock()
	if _, err := ibkrOrderPayload(req); err != nil {
		return broker.Order{}, err
	}
	if prior := state.attempts[req.ClientOrderID]; prior != nil {
		if prior.request != req {
			return broker.Order{}, broker.NewOrderError("duplicate_order", "client_order_id was already used with different order details")
		}
		return prior.order, prior.err
	}
	if state.pending != nil {
		return broker.Order{}, broker.NewOrderError("confirmation_pending", "resolve the pending IBKR order confirmation first")
	}
	// Retain attempts for the lifetime of the session. Fail closed instead of evicting IDs.
	if len(state.attempts) >= 10000 {
		return broker.Order{}, fmt.Errorf("ibkr: order entry session capacity reached")
	}
	body, err := c.prepareOrder(ctx, req)
	if err != nil {
		return broker.Order{}, err
	}
	if state.attempts == nil {
		state.attempts = map[string]*orderAttempt{}
	}
	attempt := &orderAttempt{request: req}
	state.attempts[req.ClientOrderID] = attempt
	var response json.RawMessage
	err = c.orderRequest(ctx, http.MethodPost, "/iserver/account/"+url.PathEscape(req.AccountID)+"/orders", body, &response)
	if err == nil {
		attempt.order, err = c.parseOrderResponse(req, response)
	}
	attempt.err = err
	if attempt.order.Confirmation != nil {
		state.pending = attempt
	}
	c.invalidateOrderCache()
	return attempt.order, attempt.err
}
func (c *Client) ReplyOrder(ctx context.Context, account, reply string, confirmed bool) (broker.Order, error) {
	state := &c.orderFlow
	state.mu.Lock()
	defer state.mu.Unlock()
	if prior, ok := state.replies[reply]; ok {
		if prior.account != account || prior.confirmed != confirmed {
			return broker.Order{}, invalidOrder("reply was already resolved with another decision")
		}
		return prior.order, prior.err
	}
	attempt := state.pending
	if attempt == nil || attempt.request.AccountID != account || attempt.order.Confirmation == nil || attempt.order.Confirmation.ReplyID != reply {
		return broker.Order{}, invalidOrder("confirmation does not belong to this account or is no longer active")
	}
	// Do not perform unrelated preflight requests between submission and its reply.
	body, _ := json.Marshal(map[string]bool{"confirmed": confirmed})
	var response json.RawMessage
	err := c.orderRequest(ctx, http.MethodPost, "/iserver/reply/"+url.PathEscape(reply), body, &response)
	order := broker.Order{}
	if err == nil {
		if confirmed {
			order, err = c.parseOrderResponse(attempt.request, response)
		} else {
			order = orderFromRequest(attempt.request)
			order.Status = "not_submitted"
		}
	}
	if state.replies == nil {
		state.replies = map[string]replyAttempt{}
	}
	state.replies[reply] = replyAttempt{account: account, confirmed: confirmed, order: order, err: err}
	attempt.order, attempt.err = order, err
	// Retire the reply on any error. Its outcome is retained above, so this ID
	// can never transmit again. The UI requires reconciliation before a new order.
	if err != nil || order.Confirmation == nil {
		state.pending = nil
	}
	c.invalidateOrderCache()
	return order, err
}
func orderFromRequest(r broker.OrderRequest) broker.Order {
	return broker.Order{ClientOrderID: r.ClientOrderID, AccountID: r.AccountID, Currency: r.Currency, Symbol: r.Symbol, InstrumentID: r.InstrumentID, AssetClass: r.AssetClass, Side: r.Side, OrderType: r.OrderType, Quantity: r.Quantity, LimitPrice: r.LimitPrice, StopPrice: r.StopPrice, TimeInForce: r.TimeInForce}
}
func (c *Client) parseOrderResponse(req broker.OrderRequest, data []byte) (broker.Order, error) {
	var rows []map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if decoder.Decode(&rows) != nil || len(rows) != 1 {
		return broker.Order{}, broker.NewOrderError("order_outcome_unknown", "IBKR returned an unexpected order response; check orders before submitting again")
	}
	row := rows[0]
	if message := textValue(row["error"]); message != "" {
		return broker.Order{}, broker.NewOrderError("order_rejected", c.safeOrderText(message))
	}
	order := orderFromRequest(req)
	order.ID = firstNonEmpty(textValue(row["order_id"]), textValue(row["orderId"]))
	if order.ID != "" {
		order.RawStatus = firstNonEmpty(textValue(row["order_status"]), textValue(row["status"]))
		order.Status = normalizeIBKRStatus(order.RawStatus)
		if order.Status == "" {
			order.Status = "unknown"
		}
		order.SubmittedAt = time.Now().UTC().Format(time.RFC3339)
		return order, nil
	}
	if reply := textValue(row["id"]); reply != "" {
		messages := c.orderMessages(row["message"])
		if len(messages) == 0 {
			return broker.Order{}, broker.NewOrderError("order_outcome_unknown", "IBKR confirmation did not contain a message; check the order in Gateway")
		}
		order.Status = "confirmation_required"
		order.Confirmation = &broker.OrderConfirmation{ReplyID: reply, Messages: messages, MessageIDs: c.orderMessages(row["messageIds"])}
		return order, nil
	}
	return broker.Order{}, broker.NewOrderError("order_outcome_unknown", "IBKR did not return an order ID; check orders before submitting again")
}
func (c *Client) orderMessages(value any) []string {
	result := []string{}
	if values, ok := value.([]any); ok {
		for _, item := range values {
			if s := textValue(item); s != "" {
				result = append(result, c.safeOrderText(s))
			}
		}
	} else if s := textValue(value); s != "" {
		result = append(result, c.safeOrderText(s))
	}
	return result
}
func (c *Client) safeOrderText(s string) string {
	for _, secret := range []string{c.cfg.GatewayToken, c.cfg.FlexToken, c.cfg.GatewayURL} {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	if len(s) > 4096 {
		s = s[:4096]
	}
	return s
}
func (c *Client) invalidateOrderCache() {
	c.orderState.mu.Lock()
	defer c.orderState.mu.Unlock()
	c.orderState.readyAt = time.Time{}
}

func (c *Client) OrderAttempt(_ context.Context, account, clientID string) (broker.OrderAttemptState, error) {
	c.orderFlow.mu.Lock()
	defer c.orderFlow.mu.Unlock()
	attempt := c.orderFlow.attempts[clientID]
	if attempt == nil || attempt.request.AccountID != account {
		return broker.OrderAttemptState{}, broker.NewOrderError("order_outcome_unknown", "order attempt is unavailable in this service session; check IBKR before submitting again")
	}
	state := broker.OrderAttemptState{Request: attempt.request, Order: attempt.order}
	if attempt.err != nil {
		state.Error = attempt.err.Error()
		var oe *broker.OrderError
		if errors.As(attempt.err, &oe) {
			state.Code = oe.Code
		}
	}
	return state, nil
}
