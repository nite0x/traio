package ibkr

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nite/traio/internal/activity"
)

func TestParseActivityXMLNormalizesEvidenceWithoutDoubleCounting(t *testing.T) {
	body := []byte(`<FlexQueryResponse><FlexStatements><FlexStatement accountId="U1" fromDate="20260901" toDate="20260903" whenGenerated="2026-09-04T01:02:03Z">
<Trades>
  <Trade levelOfDetail="EXECUTION" assetCategory="STK" currency="USD" symbol="AAPL" conid="265598" tradeID="T1" ibExecID="E1" ibOrderID="O1" tradeDate="20260901" settleDateTarget="20260903" dateTime="2026-09-01T14:30:00Z" buySell="BUY" quantity="10" tradePrice="200" multiplier="1" proceeds="-2000" ibCommission="-1" ibCommissionCurrency="USD" taxes="0" netCash="-2001" accountAlias="private" traderID="M-private"/>
  <Trade levelOfDetail="EXECUTION" assetCategory="STK" currency="USD" symbol="AAPL" conid="265598" tradeID="T2" ibExecID="E2" ibOrderID="O1" tradeDate="20260901" buySell="BUY" quantity="3" tradePrice="201" multiplier="1" proceeds="-603" ibCommission="-0.5" ibCommissionCurrency="USD" netCash="-603.5"/>
</Trades>
<CashTransactions>
  <CashTransaction transactionID="C1" type="Dividends" currency="USD" amount="100" dateTime="20260902;120000" symbol="AAPL" conid="265598"/>
  <CashTransaction transactionID="C2" type="Withholding Tax" currency="USD" amount="-15" dateTime="20260902;120000" symbol="AAPL" conid="265598"/>
</CashTransactions>
</FlexStatement></FlexStatements></FlexQueryResponse>`)

	records, err := ParseActivityXML(body)
	if err != nil {
		t.Fatalf("ParseActivityXML: %v", err)
	}
	if len(records) != 4 {
		t.Fatalf("records = %d, want 4", len(records))
	}
	trade := records[0]
	if trade.Key != "E1" || trade.ProviderAccountID != "U1" || trade.Activity.Status != activity.StatusEffective {
		t.Fatalf("unexpected trade identity/status: %#v", trade)
	}
	if trade.Activity.Fill == nil || trade.Activity.Fill.ExecutionID != "E1" || trade.Activity.Fill.OrderID != "O1" {
		t.Fatalf("unexpected fill: %#v", trade.Activity.Fill)
	}
	if got := trade.Activity.CashEffectsByCurrency["USD"]; got != "-2001" {
		t.Fatalf("cash effect = %q, want -2001 (reported net cash must not be another leg)", got)
	}
	if len(trade.Activity.Legs) != 3 || trade.Activity.Legs[0].QuantityDelta != "10" {
		t.Fatalf("unexpected trade legs: %#v", trade.Activity.Legs)
	}
	if records[1].Key != "E2" || records[1].Activity.Fill.OrderID != "O1" {
		t.Fatalf("partial execution identity lost: %#v", records[1])
	}
	if records[2].Activity.Type != "dividend" || records[2].Activity.CashEffectsByCurrency["USD"] != "100" {
		t.Fatalf("unexpected dividend: %#v", records[2].Activity)
	}
	if records[3].Activity.Type != "tax" || records[3].Activity.CashEffectsByCurrency["USD"] != "-15" {
		t.Fatalf("unexpected tax: %#v", records[3].Activity)
	}
	var payload map[string]string
	if err := json.Unmarshal(trade.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["accountAlias"] != "" || payload["traderID"] != "" {
		t.Fatalf("private aliases retained in payload: %#v", payload)
	}
}

func TestParseActivityXMLValidatesFXUsingReportedCurrencyLegs(t *testing.T) {
	body := []byte(`<FlexQueryResponse><FlexStatements><FlexStatement accountId="U1">
<Trades>
  <Trade levelOfDetail="EXECUTION" assetCategory="CASH" currency="HKD" symbol="USD.HKD" tradeID="FX1" ibExecID="FX1" tradeDate="20260722" buySell="BUY" quantity="4081.37" tradePrice="7.8405" multiplier="1" proceeds="-31999.981485" ibCommission="-2" ibCommissionCurrency="USD" taxes="0" netCash="0"/>
  <Trade levelOfDetail="EXECUTION" assetCategory="CASH" currency="USD" symbol="EUR.USD" tradeID="FX2" ibExecID="FX2" tradeDate="20260709" buySell="SELL" quantity="-104.98" tradePrice="1.1427" multiplier="1" proceeds="119.960646" ibCommission="-2" ibCommissionCurrency="USD" taxes="0" netCash="0"/>
</Trades>
</FlexStatement></FlexStatements></FlexQueryResponse>`)

	records, err := ParseActivityXML(body)
	if err != nil {
		t.Fatalf("ParseActivityXML: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	usdHKD := records[0].Activity
	if usdHKD.Status != activity.StatusEffective || hasWarning(usdHKD, "reported_net_cash_mismatch") {
		t.Fatalf("valid USD.HKD conversion requires review: %#v", usdHKD)
	}
	if usdHKD.CashEffectsByCurrency["USD"] != "4079.37" || usdHKD.CashEffectsByCurrency["HKD"] != "-31999.981485" {
		t.Fatalf("unexpected USD.HKD effects: %#v", usdHKD.CashEffectsByCurrency)
	}
	eurUSD := records[1].Activity
	if eurUSD.Status != activity.StatusEffective || hasWarning(eurUSD, "reported_net_cash_mismatch") {
		t.Fatalf("valid EUR.USD conversion requires review: %#v", eurUSD)
	}
	if eurUSD.CashEffectsByCurrency["EUR"] != "-104.98" || eurUSD.CashEffectsByCurrency["USD"] != "117.960646" {
		t.Fatalf("unexpected EUR.USD effects: %#v", eurUSD.CashEffectsByCurrency)
	}
}

func TestParseActivityXMLFlagsFXProceedsMismatchAndKeepsTradeNetCashCheck(t *testing.T) {
	body := []byte(`<FlexQueryResponse><FlexStatements><FlexStatement accountId="U1">
<Trades>
  <Trade levelOfDetail="EXECUTION" assetCategory="CASH" currency="HKD" symbol="USD.HKD" tradeID="FX1" ibExecID="FX1" tradeDate="20260722" buySell="BUY" quantity="4081.37" tradePrice="7.8405" multiplier="1" proceeds="-31999" ibCommission="0" ibCommissionCurrency="USD" taxes="0" netCash="0"/>
  <Trade levelOfDetail="EXECUTION" assetCategory="STK" currency="USD" symbol="AAPL" conid="265598" tradeID="T1" ibExecID="T1" tradeDate="20260722" buySell="BUY" quantity="1" tradePrice="200" multiplier="1" proceeds="-200" ibCommission="-1" ibCommissionCurrency="USD" taxes="0" netCash="0"/>
</Trades>
</FlexStatement></FlexStatements></FlexQueryResponse>`)

	records, err := ParseActivityXML(body)
	if err != nil {
		t.Fatalf("ParseActivityXML: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0].Activity.Status != activity.StatusNeedsReview || !hasWarning(records[0].Activity, "reported_fx_proceeds_mismatch") {
		t.Fatalf("FX proceeds mismatch was not retained: %#v", records[0].Activity)
	}
	if records[1].Activity.Status != activity.StatusNeedsReview || !hasWarning(records[1].Activity, "reported_net_cash_mismatch") {
		t.Fatalf("security net cash mismatch was not retained: %#v", records[1].Activity)
	}
}

func TestParseActivityXMLComplexEventsUseOnlyReportedEffects(t *testing.T) {
	body := []byte(`<FlexStatementResponse><FlexStatements><FlexStatement accountId="U2">
<Transfers><Transfer transactionID="X1" type="ACATS" direction="IN" assetCategory="STK" currency="USD" symbol="XYZ" conid="10" quantity="4" date="20260901" positionAmount="9999" transferAccount="private"/></Transfers>
<CorporateActions>
  <CorporateAction transactionID="S1" type="FS" assetCategory="STK" currency="USD" symbol="XYZ" conid="10" quantity="6" reportDate="20260901" amount="9999" value="9999"/>
  <CorporateAction transactionID="M1" type="TC" assetCategory="STK" currency="USD" symbol="NEW" conid="11" quantity="4" reportDate="20260901" proceeds="2.5"/>
  <CorporateAction transactionID="Q1" type="UE" assetCategory="STK" currency="USD" symbol="XYZ" conid="10" quantity="1" reportDate="20260901" amount="500"/>
</CorporateActions>
<OptionEAETransactions>
  <OptionEAETransaction tradeID="OE1" transactionType="Exercise" assetCategory="OPT" currency="USD" symbol="XYZ C" conid="12" quantity="-1" date="20260901" proceeds="-50" commissionsAndTax="-1"/>
  <OptionEAETransaction tradeID="OX1" transactionType="Expiration" assetCategory="OPT" currency="USD" symbol="XYZ P" conid="13" quantity="-2" date="20260901" proceeds="0" commissionsAndTax="0"/>
</OptionEAETransactions>
</FlexStatement></FlexStatements></FlexStatementResponse>`)

	records, err := ParseActivityXML(body)
	if err != nil {
		t.Fatalf("ParseActivityXML: %v", err)
	}
	if len(records) != 6 {
		t.Fatalf("records = %d, want 6", len(records))
	}
	transfer := records[0].Activity
	if transfer.Type != "security_transfer" || transfer.Legs[0].QuantityDelta != "4" || len(transfer.CashEffectsByCurrency) != 0 || !hasWarning(transfer, "transferred_cost_basis_unknown") {
		t.Fatalf("transfer used valuation as cash/cost: %#v", transfer)
	}
	split := records[1].Activity
	if split.Type != "split" || split.Status != activity.StatusEffective || len(split.Legs) != 1 || len(split.CashEffectsByCurrency) != 0 {
		t.Fatalf("split did not retain only reported quantity delta: %#v", split)
	}
	merger := records[2].Activity
	if merger.Type != "merger" || merger.Status != activity.StatusNeedsReview || merger.CashEffectsByCurrency["USD"] != "2.5" || !hasWarning(merger, "corporate_cost_allocation_missing") {
		t.Fatalf("merger evidence boundary incorrect: %#v", merger)
	}
	unknown := records[3].Activity
	if unknown.Status != activity.StatusUnsupported || len(unknown.Legs) != 0 || len(unknown.CashEffectsByCurrency) != 0 {
		t.Fatalf("unknown corporate action fabricated effects: %#v", unknown)
	}
	option := records[4].Activity
	if option.Type != "exercise" || option.Status != activity.StatusNeedsReview || !hasWarning(option, "option_execution_linkage_required") {
		t.Fatalf("option event must remain reviewable: %#v", option)
	}
	expiration := records[5].Activity
	if expiration.Type != "expiration" || expiration.Status != activity.StatusEffective || len(expiration.Legs) != 1 || expiration.Legs[0].QuantityDelta != "-2" {
		t.Fatalf("standalone expiration should use its explicit quantity only: %#v", expiration)
	}
	var payload map[string]string
	_ = json.Unmarshal(records[0].Payload, &payload)
	if payload["transferAccount"] != "" {
		t.Fatalf("contra account retained in payload: %#v", payload)
	}
}

func TestParseActivityXMLRejectsUnsafeOrInvalidDocuments(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{name: "empty", body: nil},
		{name: "wrong root", body: []byte(`<html/>`)},
		{name: "directive", body: []byte(`<!DOCTYPE x [<!ENTITY y SYSTEM "file:///etc/passwd">]><FlexStatement/>`)},
		{name: "entity", body: []byte(`<FlexStatement><CashTransaction transactionID="1" type="Dividend" currency="USD" amount="&y;"/></FlexStatement>`)},
		{name: "too deep", body: deepActivityXML()},
		{name: "too large", body: []byte(strings.Repeat("x", MaxActivityXMLBytes+1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseActivityXML(tt.body); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func deepActivityXML() []byte {
	return []byte(`<FlexStatement>` + strings.Repeat(`<Group>`, maxActivityXMLDepth) + strings.Repeat(`</Group>`, maxActivityXMLDepth) + `</FlexStatement>`)
}

func hasWarning(a activity.Activity, want string) bool {
	for _, warning := range a.Warnings {
		if warning == want {
			return true
		}
	}
	return false
}

func TestActivityTradeWithoutConidUsesSymbolIdentity(t *testing.T) {
	records, err := ParseActivityXML([]byte(`<FlexQueryResponse><FlexStatements><FlexStatement accountId="U1"><Trades><Trade levelOfDetail="EXECUTION" assetCategory="STK" currency="USD" symbol="AAPL" tradeID="T1" ibExecID="E1" tradeDate="20260901" buySell="BUY" quantity="2" tradePrice="200" multiplier="1" proceeds="-400" ibCommission="0" netCash="-400"/></Trades></FlexStatement></FlexStatements></FlexQueryResponse>`))
	if err != nil || len(records) != 1 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	a := records[0].Activity
	if a.Status != activity.StatusEffective || len(a.Legs) < 1 || a.Legs[0].Symbol != "AAPL" || a.Legs[0].AssetType != "stock" || a.Legs[0].QuantityDelta != "2" {
		t.Fatalf("symbol trade=%+v", a)
	}
}
