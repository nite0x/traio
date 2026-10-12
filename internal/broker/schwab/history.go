package schwab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nite/traio/internal/activity"
)

const schwabTransactionTypes = "TRADE,RECEIVE_AND_DELIVER,DIVIDEND_OR_INTEREST,ACH_RECEIPT,ACH_DISBURSEMENT,CASH_RECEIPT,CASH_DISBURSEMENT,ELECTRONIC_FUND,WIRE_OUT,WIRE_IN,JOURNAL,MEMORANDUM,MARGIN_CALL,MONEY_MARKET,SMA_ADJUSTMENT"

// FetchHistory uses the hashed account path and splits the requested interval
// so Schwab's 3,000-transaction response cap never silently truncates a job.
func (c *Client) FetchHistory(ctx context.Context, accountID, baseCurrency, from, to string) ([]activity.RawRecord, error) {
	hash, err := c.accountHash(ctx, accountID)
	if err != nil {
		return nil, err
	}
	start, err := time.Parse("2006-01-02", from)
	if err != nil {
		return nil, fmt.Errorf("schwab history: invalid start date")
	}
	end, err := time.Parse("2006-01-02", to)
	if err != nil || start.After(end) {
		return nil, fmt.Errorf("schwab history: invalid end date")
	}
	var records []activity.RawRecord
	for !start.After(end) {
		windowEnd := start.AddDate(0, 0, 29)
		if windowEnd.After(end) {
			windowEnd = end
		}
		page, err := c.fetchTransactionWindow(ctx, hash, accountID, baseCurrency, start, windowEnd.AddDate(0, 0, 1))
		if err != nil {
			return nil, err
		}
		records = append(records, page...)
		if len(records) > 100000 {
			return nil, fmt.Errorf("schwab history: range too large")
		}
		start = windowEnd.AddDate(0, 0, 1)
	}
	return records, nil
}

func (c *Client) fetchTransactionWindow(ctx context.Context, hash, accountID, currency string, start, endExclusive time.Time) ([]activity.RawRecord, error) {
	q := url.Values{"startDate": {start.UTC().Format(time.RFC3339)}, "endDate": {endExclusive.UTC().Add(-time.Second).Format(time.RFC3339)}, "types": {schwabTransactionTypes}}
	endpoint := c.traderEndpoint("/accounts/" + url.PathEscape(hash) + "/transactions?" + q.Encode())
	resp, err := c.Do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	resp.Body.Close()
	if err != nil || len(body) > 8<<20 {
		return nil, fmt.Errorf("schwab history: response too large or unreadable")
	}
	var page []json.RawMessage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("schwab history: invalid transactions response")
	}
	if len(page) >= 3000 {
		// The upstream limit makes the result ambiguous. Retry smaller windows;
		// even a saturated single day is an error, never complete coverage.
		if endExclusive.Sub(start) <= 24*time.Hour {
			return nil, fmt.Errorf("schwab history: daily response cap reached")
		}
		mid := start.AddDate(0, 0, int(endExclusive.Sub(start).Hours()/48))
		left, err := c.fetchTransactionWindow(ctx, hash, accountID, currency, start, mid)
		if err != nil {
			return nil, err
		}
		right, err := c.fetchTransactionWindow(ctx, hash, accountID, currency, mid, endExclusive)
		if err != nil {
			return nil, err
		}
		return append(left, right...), nil
	}
	records := make([]activity.RawRecord, 0, len(page))
	for _, raw := range page {
		record, err := parseSchwabTransaction(raw, accountID, currency)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func parseSchwabTransaction(raw json.RawMessage, accountID, baseCurrency string) (activity.RawRecord, error) {
	var row map[string]json.RawMessage
	if err := json.Unmarshal(raw, &row); err != nil {
		return activity.RawRecord{}, fmt.Errorf("schwab history: invalid row")
	}
	get := func(key string) string { return schwabJSONText(row[key]) }
	id, kind := get("activityId"), strings.ToUpper(get("type"))
	if id == "" || kind == "" || id == "null" {
		return activity.RawRecord{}, fmt.Errorf("schwab history: missing transaction identity")
	}
	if reported := get("accountNumber"); reported != "" && reported != accountID {
		return activity.RawRecord{}, fmt.Errorf("schwab history: account mismatch")
	}
	date := get("tradeDate")
	if date == "" {
		date = get("time")
	}
	if len(date) < 10 || activity.ValidateDate(date[:10]) != nil {
		return activity.RawRecord{}, fmt.Errorf("schwab history: invalid transaction date")
	}
	currency := strings.ToUpper(get("currency"))
	if currency == "" {
		currency = strings.ToUpper(baseCurrency)
	}
	a := activity.Activity{Provider: "SCHWAB", ProviderAccountID: accountID, Type: "unknown", Subtype: kind, Status: activity.StatusUnsupported, BookingStatus: activity.BookingBooked, Description: get("description"), TradeDate: date[:10], SettlementDate: activity.NormalizeDate(get("settlementDate")), TimePrecision: "date", Warnings: []string{"unmapped_schwab_transaction_type"}}
	if t, err := time.Parse(time.RFC3339Nano, get("time")); err == nil {
		a.OccurredAt, a.TimePrecision = t.Format(time.RFC3339Nano), "nanosecond"
	}
	if a.Description == "" {
		a.Description = kind
	}
	r := activity.RawRecord{Source: "schwab", Namespace: "schwab.transaction", Key: id, RecordType: kind, ProviderAccountID: accountID, Payload: raw, Activity: a, Identities: []activity.Identity{{Namespace: "schwab.transaction", Kind: kind, ExternalID: id}}}
	if strings.ToUpper(get("status")) != "VALID" {
		r.Activity.Warnings = []string{"schwab_transaction_not_valid"}
		return r, nil
	}
	amount := get("netAmount")
	if kind == "TRADE" && strings.ToUpper(get("activityType")) == "EXECUTION" {
		var items []map[string]json.RawMessage
		_ = json.Unmarshal(row["transferItems"], &items)
		if len(items) != 1 {
			r.Activity.Type, r.Activity.Status, r.Activity.Warnings = "trade", activity.StatusNeedsReview, []string{"trade_legs_ambiguous"}
			return r, nil
		}
		item := items[0]
		var instrument map[string]json.RawMessage
		_ = json.Unmarshal(item["instrument"], &instrument)
		symbol, qty := schwabJSONText(instrument["symbol"]), schwabJSONText(item["amount"])
		if symbol == "" || qty == "" || !activity.ValidCurrency(currency) {
			r.Activity.Type, r.Activity.Status, r.Activity.Warnings = "trade", activity.StatusNeedsReview, []string{"trade_identity_or_currency_missing"}
			return r, nil
		}
		qty, err := activity.CanonicalDecimal(qty)
		if err != nil {
			return r, nil
		}
		net, err := activity.CanonicalDecimal(amount)
		if err != nil {
			r.Activity.Type, r.Activity.Status, r.Activity.Warnings = "trade", activity.StatusNeedsReview, []string{"trade_net_amount_missing"}
			return r, nil
		}
		side := "buy"
		if strings.HasPrefix(net, "-") {
			side = "buy"
		} else if net != "0" {
			side = "sell"
		} else {
			r.Activity.Type, r.Activity.Status, r.Activity.Warnings = "trade", activity.StatusNeedsReview, []string{"trade_direction_unverified"}
			return r, nil
		}
		absQty, _ := activity.Abs(qty)
		qty = absQty
		if side == "sell" {
			qty, _ = activity.Negate(absQty)
		}
		a.Type, a.Status, a.Warnings = "trade", activity.StatusNeedsReview, []string{"trade_fee_split_unverified"}
		a.Fill = &activity.Fill{ExecutionID: id, OrderID: get("orderId"), Side: side, Quantity: absQty, Price: schwabJSONText(item["price"]), PriceCurrency: currency, Multiplier: "1", ReportedNetCash: net}
		a.Legs = []activity.Leg{{Kind: "position", Component: "execution", Symbol: symbol, AssetType: strings.ToLower(schwabJSONText(instrument["assetType"])), ExternalInstrumentID: schwabJSONText(instrument["cusip"]), Currency: currency, QuantityDelta: qty, EffectiveDate: a.TradeDate}, {Kind: "cash", Component: "principal", Currency: currency, CashDelta: net, EffectiveDate: a.TradeDate}}
		r.Activity = a
		return r, nil
	}
	cashType := map[string]string{"ACH_RECEIPT": "deposit", "CASH_RECEIPT": "deposit", "WIRE_IN": "deposit", "ACH_DISBURSEMENT": "withdrawal", "CASH_DISBURSEMENT": "withdrawal", "WIRE_OUT": "withdrawal", "ELECTRONIC_FUND": "cash_transfer"}[kind]
	if kind == "DIVIDEND_OR_INTEREST" {
		if strings.Contains(strings.ToLower(a.Description), "dividend") {
			cashType = "dividend"
		}
		if strings.Contains(strings.ToLower(a.Description), "interest") {
			cashType = "interest"
		}
	}
	if cashType != "" {
		a.Type, a.Warnings = cashType, nil
		if net, err := activity.CanonicalDecimal(amount); err == nil && activity.ValidCurrency(currency) {
			a.Status = activity.StatusEffective
			a.Legs = []activity.Leg{{Kind: "cash", Component: "principal", Currency: currency, CashDelta: net, EffectiveDate: a.TradeDate}}
		} else {
			a.Status, a.Warnings = activity.StatusNeedsReview, []string{"cash_amount_or_currency_missing"}
		}
	}
	if kind == "RECEIVE_AND_DELIVER" {
		a.Type, a.Status, a.Warnings = "security_transfer", activity.StatusNeedsReview, []string{"transfer_cost_or_direction_unverified"}
	}
	if kind == "JOURNAL" {
		a.Type, a.Status, a.Warnings = "cash_transfer", activity.StatusNeedsReview, []string{"journal_effects_unverified"}
	}
	if kind == "DIVIDEND_OR_INTEREST" && cashType == "" {
		a.Type, a.Status, a.Warnings = "unknown", activity.StatusNeedsReview, []string{"income_type_unverified"}
	}
	if a.Status != activity.StatusEffective && len(a.Legs) == 0 && activity.ValidCurrency(currency) {
		if reported, err := activity.CanonicalDecimal(amount); err == nil {
			a.Legs = []activity.Leg{{Kind: "cash", Component: "reported_unverified", Currency: currency, CashDelta: reported, EffectiveDate: a.TradeDate}}
			a.Warnings = append(a.Warnings, "reported_cash_unverified")
		}
	}
	r.Activity = a
	return r, nil
}

func schwabJSONText(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(string(raw))
}
