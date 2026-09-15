package ibkr

import (
	"testing"

	"github.com/nite/traio/internal/activity"
)

func TestParseGatewayTradesPreservesDocumentedEvidence(t *testing.T) {
	body := []byte(`[{
  "execution_id":"0000e0d5.6576fd38.01.01", "symbol":"AAPL", "side":"S",
  "trade_time":"20231211-18:00:49", "size":5, "price":"192.26",
  "exchange":"ISLAND", "commission":"1.01", "net_amount":961.3,
  "account":"U1234567", "accountCode":"U1234567", "account_allocation_name":"private",
  "contract_description_1":"AAPL", "sec_type":"STK", "conid":265598,
  "order_id":17437897932, "submitter":"private"
}]`)
	records, err := ParseGatewayTrades(body)
	if err != nil {
		t.Fatalf("ParseGatewayTrades: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d", len(records))
	}
	r := records[0]
	if r.Key != "0000e0d5.6576fd38.01.01" || r.ProviderAccountID != "U1234567" {
		t.Fatalf("unexpected identity: %#v", r)
	}
	if r.Activity.OccurredAt != "2023-12-11T18:00:49Z" || r.Activity.SourceTimezone != "UTC" {
		t.Fatalf("documented UTC time lost: %#v", r.Activity)
	}
	if r.Activity.Fill == nil || r.Activity.Fill.OrderID != "17437897932" || r.Activity.Fill.Quantity != "5" || r.Activity.Fill.Side != "sell" {
		t.Fatalf("unexpected fill: %#v", r.Activity.Fill)
	}
	if r.Activity.Status != activity.StatusNeedsReview || len(r.Activity.CashEffectsByCurrency) != 0 {
		t.Fatalf("undocumented currency must not be guessed: %#v", r.Activity)
	}
}

func TestParseGatewayTradesUsesExplicitCurrencyAndRejectsMissingIdentity(t *testing.T) {
	body := []byte(`[{"execution_id":"E1","side":"B","trade_time_r":1702317649000,"size":"2","price":"10.5","accountCode":"U1","sec_type":"STK","conid":"7","currency":"EUR","commission":"0.25","commission_currency":"USD"}]`)
	records, err := ParseGatewayTrades(body)
	if err != nil {
		t.Fatalf("ParseGatewayTrades: %v", err)
	}
	a := records[0].Activity
	if a.CashEffectsByCurrency["EUR"] != "-21" || a.CashEffectsByCurrency["USD"] != "-0.25" {
		t.Fatalf("explicit multi-currency effects lost: %#v", a.CashEffectsByCurrency)
	}
	if _, err := ParseGatewayTrades([]byte(`[{"side":"B","size":1}]`)); err == nil {
		t.Fatal("missing execution identity should fail the page")
	}
	if _, err := ParseGatewayTrades([]byte(`[] {}`)); err == nil {
		t.Fatal("trailing JSON should fail")
	}
}
