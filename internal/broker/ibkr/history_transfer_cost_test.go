package ibkr

import (
	"fmt"
	"strings"
	"testing"
)

func TestTransferCostFromCompleteOpenLots(t *testing.T) {
	transfer := `<Transfers><Transfer transactionID="X1" type="ACATS" direction="IN" assetCategory="STK" currency="USD" symbol="XYZ" conid="10" quantity="6" date="20260708" positionAmount="9999"/></Transfers>`
	lot := `<OpenPosition levelOfDetail="LOT" assetCategory="STK" currency="USD" symbol="XYZ" conid="10" position="6" costBasisMoney="900.125" openDateTime="20260101;120000" originatingOrderID="0" originatingTransactionID="0"/>`
	for _, tc := range []struct {
		name, rows, extra string
		want              bool
	}{
		{"full lot", lot, "", true},
		{"multiple lots", strings.ReplaceAll(strings.ReplaceAll(lot, `position="6"`, `position="2"`), `900.125`, `300.1`) + strings.ReplaceAll(strings.ReplaceAll(lot, `position="6"`, `position="4"`), `900.125`, `600.025`), "", true},
		{"zero cost", strings.ReplaceAll(lot, `900.125`, `0`), "", true},
		{"partial lot", strings.ReplaceAll(lot, `position="6"`, `position="5"`), "", false},
		{"summary is not transfer cost", strings.ReplaceAll(lot, `LOT`, `SUMMARY`), "", false},
		{"other account", strings.ReplaceAll(lot, `<OpenPosition`, `<OpenPosition accountId="U2"`), "", false},
		{"broker purchase", strings.ReplaceAll(lot, `originatingOrderID="0"`, `originatingOrderID="123"`), "", false},
		{"unknown acquisition", strings.ReplaceAll(lot, `20260101;120000`, ``), "", false},
		{"missing cost", strings.ReplaceAll(lot, `costBasisMoney="900.125"`, ``), "", false},
		{"ambiguous duplicate lot", lot + lot, "", false},
		{"two transfers", lot, strings.ReplaceAll(transfer, `X1`, `X2`), false},
		{"disposal", lot, `<Trades><Trade tradeID="SELL1" assetCategory="STK" currency="USD" symbol="XYZ" conid="10" quantity="1" buySell="SELL" tradeDate="20260801" tradePrice="200" proceeds="200" ibCommission="0"/></Trades>`, false},
		{"corporate action", lot, `<CorporateActions><CorporateAction transactionID="S1" type="FS" assetCategory="STK" currency="USD" symbol="XYZ" conid="10" quantity="6" reportDate="20260801"/></CorporateActions>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`<FlexStatement accountId="U1" fromDate="20260101" toDate="20260911"><Trades/><CorporateActions/>%s%s<OpenPositions>%s</OpenPositions></FlexStatement>`, transfer, tc.extra, tc.rows))
			records, err := ParseActivityXML(body)
			if err != nil {
				t.Fatal(err)
			}
			a := records[0].Activity
			if (len(a.CostAdjustments) > 0) != tc.want {
				t.Fatalf("cost attachment: %#v", a)
			}
			if a.Fill != nil || len(a.CashEffectsByCurrency) != 0 || a.Legs[0].QuantityDelta != "6" {
				t.Fatalf("cost changed trading effects: %#v", a)
			}
			if tc.want && hasWarning(a, "transferred_cost_basis_unknown") {
				t.Fatal("known cost still marked unknown")
			}
			if tc.name == "full lot" && a.CostAdjustments[0].BasisDelta != "900.125" {
				t.Fatalf("cost precision: %#v", a.CostAdjustments)
			}
		})
	}
}

func TestLotDetailsDoNotDoubleCountSnapshot(t *testing.T) {
	const summary = `<OpenPosition levelOfDetail="SUMMARY" assetCategory="STK" currency="USD" symbol="XYZ" conid="10" position="6" costBasisMoney="900"/>`
	const lot = `<OpenPosition levelOfDetail="LOT" assetCategory="STK" currency="USD" symbol="XYZ" conid="10" position="6" costBasisMoney="900" openDateTime="20260101;120000"/>`
	for _, tc := range []struct {
		rows, completeness string
		balances           int
	}{{summary + lot, "complete", 1}, {lot, "partial", 0}} {
		rows, err := ParseReconciliationSnapshots([]byte(`<FlexStatement accountId="U1" toDate="20260911"><CashReport/><OpenPositions>` + tc.rows + `</OpenPositions></FlexStatement>`))
		if err != nil || len(rows) != 1 || rows[0].Completeness != tc.completeness || len(rows[0].Balances) != tc.balances {
			t.Fatalf("snapshot: %#v %v", rows, err)
		}
	}
}
