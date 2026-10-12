package alpaca

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

// FetchHistory reads every page of Alpaca Trading API account activities. A
// partial page failure fails the whole job, so no partial range is imported.
func (c *Client) FetchHistory(ctx context.Context, accountID, baseCurrency, from, to string) ([]activity.RawRecord, error) {
	if _, err := c.GetAccount(ctx, accountID); err != nil {
		return nil, err
	}
	end, err := time.Parse("2006-01-02", to)
	if err != nil {
		return nil, fmt.Errorf("alpaca history: invalid end date")
	}
	start, err := time.Parse("2006-01-02", from)
	if err != nil || start.After(end) {
		return nil, fmt.Errorf("alpaca history: invalid start date")
	}
	// Alpaca's after/until filters are strict. Move after back one second so
	// an activity created exactly at midnight on the requested first day stays in.
	q := url.Values{"after": {start.Add(-time.Second).Format(time.RFC3339)}, "until": {end.AddDate(0, 0, 1).Format(time.RFC3339)}, "direction": {"asc"}, "page_size": {"100"}}
	var records []activity.RawRecord
	seenPages := map[string]bool{}
	for {
		resp, err := c.Do(ctx, http.MethodGet, "account/activities?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
		resp.Body.Close()
		if err != nil || len(body) > 8<<20 {
			return nil, fmt.Errorf("alpaca history: response too large or unreadable")
		}
		var page []json.RawMessage
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("alpaca history: invalid activities response")
		}
		if len(page) > 100 {
			return nil, fmt.Errorf("alpaca history: page limit exceeded")
		}
		for _, raw := range page {
			record, err := parseAlpacaActivity(raw, accountID, baseCurrency)
			if err != nil {
				return nil, err
			}
			records = append(records, record)
		}
		if len(records) > 100000 {
			return nil, fmt.Errorf("alpaca history: range too large")
		}
		if len(page) < 100 {
			break
		}
		last := records[len(records)-1].Key
		if seenPages[last] {
			return nil, fmt.Errorf("alpaca history: pagination did not advance")
		}
		seenPages[last] = true
		q.Set("page_token", last)
	}
	return records, nil
}

func parseAlpacaActivity(raw json.RawMessage, accountID, baseCurrency string) (activity.RawRecord, error) {
	var row map[string]json.RawMessage
	if err := json.Unmarshal(raw, &row); err != nil {
		return activity.RawRecord{}, fmt.Errorf("alpaca history: invalid row")
	}
	get := func(key string) string {
		var value string
		if json.Unmarshal(row[key], &value) == nil {
			return strings.TrimSpace(value)
		}
		return strings.TrimSpace(string(row[key]))
	}
	id, kind := get("id"), strings.ToUpper(get("activity_type"))
	if id == "" || kind == "" {
		return activity.RawRecord{}, fmt.Errorf("alpaca history: missing activity identity")
	}
	date := get("date")
	if date == "" {
		date = get("transaction_time")
	}
	if len(date) < 10 || activity.ValidateDate(date[:10]) != nil {
		return activity.RawRecord{}, fmt.Errorf("alpaca history: invalid activity date")
	}
	currency := strings.ToUpper(get("currency"))
	if currency == "" {
		currency = strings.ToUpper(baseCurrency)
	}
	a := activity.Activity{Provider: "ALPACA", ProviderAccountID: accountID, Type: "unknown", Subtype: kind, Status: activity.StatusUnsupported, BookingStatus: activity.BookingBooked, TradeDate: date[:10], Description: kind, TimePrecision: "date", Warnings: []string{"unmapped_alpaca_activity_type"}}
	if strings.Contains(date, "T") {
		if t, err := time.Parse(time.RFC3339Nano, date); err == nil {
			a.OccurredAt, a.SourceTimezone, a.TimePrecision = t.Format(time.RFC3339Nano), "UTC", "nanosecond"
		}
	}
	r := activity.RawRecord{Source: "alpaca", Namespace: "alpaca.activity", Key: id, RecordType: kind, ProviderAccountID: accountID, Payload: raw, Activity: a, Identities: []activity.Identity{{Namespace: "alpaca.activity", Kind: kind, ExternalID: id}}}
	amount := get("net_amount")
	if kind == "FILL" {
		qty, price, side, symbol := get("qty"), get("price"), strings.ToLower(get("side")), get("symbol")
		if symbol == "" || (side != "buy" && side != "sell") {
			return r, nil
		}
		q, err := activity.Abs(qty)
		if err != nil {
			return r, nil
		}
		value, err := activity.Mul(q, price)
		if err != nil {
			return r, nil
		}
		signed := q
		if side == "sell" {
			signed, _ = activity.Negate(q)
		} else {
			value, _ = activity.Negate(value)
		}
		a.Type, a.Status, a.Description = "trade", activity.StatusNeedsReview, side+" "+symbol
		a.Warnings = []string{"trade_fees_unverified"}
		assetType := strings.ToLower(get("asset_class"))
		if assetType == "" {
			assetType = "unknown"
			a.Warnings = append(a.Warnings, "asset_class_unverified")
		}
		a.Fill = &activity.Fill{ExecutionID: id, OrderID: get("order_id"), Side: side, Quantity: q, Price: price, PriceCurrency: currency, Multiplier: "1"}
		a.Legs = []activity.Leg{{Kind: "position", Component: "execution", Symbol: symbol, AssetType: assetType, ExternalInstrumentID: get("asset_id"), Currency: currency, QuantityDelta: signed, EffectiveDate: a.TradeDate}, {Kind: "cash", Component: "principal", Currency: currency, CashDelta: value, EffectiveDate: a.TradeDate}}
		if !activity.ValidCurrency(currency) {
			a.Status = activity.StatusNeedsReview
			a.Legs = a.Legs[:1]
			a.Warnings = append(a.Warnings, "currency_missing")
		}
		r.Activity = a
		return r, nil
	}
	cashType := map[string]string{"CSD": "deposit", "CSW": "withdrawal", "DIV": "dividend", "DIVCGL": "dividend", "DIVCGS": "dividend", "DIVTXEX": "dividend", "CGD": "dividend", "INT": "interest", "INTNRA": "tax", "INTTW": "tax", "DIVFT": "tax", "DIVNRA": "tax", "DIVTW": "tax", "DIVFEE": "fee", "FEE": "fee", "CFEE": "fee", "PTC": "fee", "PTR": "refund", "ACATC": "cash_transfer", "JNLC": "cash_transfer"}[kind]
	if cashType != "" {
		a.Type, a.Description, a.Warnings = cashType, kind, nil
		if amount != "" && activity.ValidCurrency(currency) {
			if normalized, err := activity.CanonicalDecimal(amount); err == nil {
				a.Status = activity.StatusEffective
				a.Legs = []activity.Leg{{Kind: "cash", Component: "principal", Currency: currency, CashDelta: normalized, EffectiveDate: a.TradeDate}}
			}
		}
		if a.Status != activity.StatusEffective {
			a.Status = activity.StatusNeedsReview
			a.Warnings = []string{"cash_amount_or_currency_missing"}
		}
	}
	if kind == "ACATS" || kind == "FOPT" || kind == "JNLS" {
		a.Type, a.Status, a.Warnings = "security_transfer", activity.StatusNeedsReview, []string{"transfer_cost_or_direction_unverified"}
	}
	if kind == "DIVROC" {
		a.Type, a.Warnings = "return_of_capital", []string{"cost_allocation_required"}
	}
	if kind == "SSP" {
		a.Type, a.Warnings = "split", []string{"corporate_action_requires_review"}
	}
	if kind == "SSO" {
		a.Type, a.Warnings = "spinoff", []string{"corporate_action_requires_review"}
	}
	if kind == "MA" {
		a.Type, a.Warnings = "merger", []string{"corporate_action_requires_review"}
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
