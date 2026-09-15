package ibkr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/nite/traio/internal/activity"
)

// ParseGatewayTrades consumes only /iserver/account/trades execution rows.
// json.RawMessage prevents size/price/order ID precision loss. Missing currency,
// fees or multiplier are explicit gaps; symbol suffixes never imply currency.
func ParseGatewayTrades(body []byte) ([]activity.RawRecord, error) {
	if len(body) > MaxActivityXMLBytes {
		return nil, fmt.Errorf("trade response too large")
	}
	var rows []map[string]json.RawMessage
	d := json.NewDecoder(bytes.NewReader(body))
	if e := d.Decode(&rows); e != nil {
		return nil, fmt.Errorf("invalid gateway trade response")
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return nil, fmt.Errorf("invalid trailing gateway trade response")
	}
	records := make([]activity.RawRecord, 0, len(rows))
	if len(rows) > maxActivityRecords {
		return nil, fmt.Errorf("too many trade records")
	}
	for _, row := range rows {
		m := map[string]string{}
		for k, v := range row {
			if sensitiveActivityField(k) {
				continue
			}
			var s string
			if json.Unmarshal(v, &s) == nil {
				m[k] = s
			} else if string(v) != "null" {
				m[k] = string(v)
			}
		}
		account := firstHistory(m, "accountCode", "account")
		a := activity.Activity{Provider: "IBKR", ProviderAccountID: account, Type: "trade", Status: activity.StatusEffective, BookingStatus: activity.BookingProvisional, Description: firstHistory(m, "order_description", "contract_description_1", "symbol"), TimePrecision: "date"}
		if ms, e := strconv.ParseInt(m["trade_time_r"], 10, 64); e == nil && ms > 0 {
			t := time.UnixMilli(ms).UTC()
			a.OccurredAt = t.Format(time.RFC3339Nano)
			a.TradeDate = t.Format("2006-01-02")
			a.TimePrecision = "millisecond"
			a.SourceTimezone = "UTC"
		} else if t, e := time.Parse("20060102-15:04:05", m["trade_time"]); e == nil {
			t = t.UTC()
			a.TradeDate = t.Format("2006-01-02")
			a.OccurredAt = t.Format(time.RFC3339)
			a.TimePrecision = "second"
			a.SourceTimezone = "UTC"
		}
		r := activity.RawRecord{Source: "gateway", Namespace: "ibkr.execution", Key: m["execution_id"], RecordType: "execution", ProviderAccountID: account, Activity: a}
		if r.Key != "" {
			r.Identities = []activity.Identity{{Namespace: "ibkr.execution", Kind: "execution", ExternalID: r.Key}}
		} else {
			return nil, fmt.Errorf("gateway execution identity missing")
		}
		side := strings.ToUpper(m["side"])
		qty, e := activity.Abs(m["size"])
		if e != nil || (side != "B" && side != "BUY" && side != "S" && side != "SELL") {
			markHistoryUnsupported(&r.Activity, "invalid_execution_quantity_or_side")
		} else {
			signed := qty
			normalizedSide := "buy"
			if side == "S" || side == "SELL" {
				signed, _ = activity.Negate(qty)
				normalizedSide = "sell"
			}
			typ := strings.ToUpper(m["sec_type"])
			currency := strings.ToUpper(strings.TrimSpace(m["currency"]))
			commissionCurrency := strings.ToUpper(strings.TrimSpace(m["commission_currency"]))
			mult := m["multiplier"]
			if mult == "" && typ == "STK" {
				mult = "1"
			}
			r.Activity.Fill = &activity.Fill{ExecutionID: r.Key, OrderID: m["order_id"], Side: normalizedSide, Quantity: qty, Price: m["price"], PriceCurrency: currency, Multiplier: mult, Exchange: m["exchange"]}
			addHistoryPosition(&r.Activity, map[string]string{"conid": firstHistory(m, "conid", "conidEx"), "symbol": m["symbol"], "assetCategory": typ, "currency": currency}, signed, "execution")
			if typ != "STK" && typ != "OPT" && typ != "FUT" {
				markHistoryReview(&r.Activity, "gateway_asset_settlement_unverified")
			}
			if !activity.ValidCurrency(currency) {
				markHistoryReview(&r.Activity, "trade_currency_missing")
			} else if typ != "FUT" {
				if mult == "" {
					markHistoryReview(&r.Activity, "contract_multiplier_missing")
				} else {
					value, e := activity.Mul(signed, m["price"])
					if e == nil {
						value, e = activity.Mul(value, mult)
					}
					if e != nil {
						markHistoryUnsupported(&r.Activity, "invalid_trade_notional")
					} else {
						value, _ = activity.Negate(value)
						addHistoryCash(&r.Activity, currency, value, "principal")
					}
				}
			}
			// Gateway commission is an unsigned charge. Its denomination is not in the
			// documented schema, so retain unknown unless the response names it.
			if m["commission"] != "" {
				v, e := activity.Abs(m["commission"])
				if e != nil {
					markHistoryReview(&r.Activity, "commission_invalid")
				} else if v != "0" {
					if !activity.ValidCurrency(commissionCurrency) {
						markHistoryReview(&r.Activity, "commission_currency_missing")
					} else {
						v, _ = activity.Negate(v)
						addHistoryCash(&r.Activity, commissionCurrency, v, "commission")
					}
				}
			} else {
				markHistoryReview(&r.Activity, "commission_unknown")
			}
			// net_amount's documented sample equals gross proceeds, not net cash. Do not
			// treat it as a new cash leg or copy it into ReportedNetCash.
		}
		if r.Activity.TradeDate == "" {
			markHistoryReview(&r.Activity, "activity_date_missing")
		}
		payload, _ := json.Marshal(m)
		r.Payload = payload
		finishHistoryRecord(&r)
		records = append(records, r)
	}
	return records, nil
}
