package ibkr

import "testing"

func TestParseReconciliationSnapshots(t *testing.T) {
	body := []byte(`<FlexQueryResponse><FlexStatements><FlexStatement accountId="U1" fromDate="20260901" toDate="20260903"><CashReport><CashReportCurrency currency="USD" endingCash="110.25"/></CashReport><OpenPositions><OpenPosition conid="265598" symbol="AAPL" assetCategory="STK" position="2" costBasisMoney="300"/></OpenPositions></FlexStatement><FlexStatement accountId="U2" toDate="20260903"><CashReport><CashReportCurrency currency="USD" endingCash="5"/></CashReport></FlexStatement></FlexStatements></FlexQueryResponse>`)
	snapshots, err := ParseReconciliationSnapshots(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("snapshots = %#v", snapshots)
	}
	if snapshots[0].ProviderAccountID != "U1" || snapshots[0].AsOfDate != "2026-09-03" || snapshots[0].Completeness != "complete" || len(snapshots[0].Balances) != 2 {
		t.Fatalf("complete snapshot = %#v", snapshots[0])
	}
	if snapshots[0].Balances[0].Currency != "USD" || snapshots[0].Balances[0].Value != "110.25" || snapshots[0].Balances[1].ExternalInstrumentID != "265598" {
		t.Fatalf("balances = %#v", snapshots[0].Balances)
	}
	if snapshots[1].Completeness != "partial" {
		t.Fatalf("partial snapshot = %#v", snapshots[1])
	}
}

func TestParseReconciliationSnapshotsRejectsInvalidDecimal(t *testing.T) {
	body := []byte(`<FlexStatement accountId="U1" toDate="20260903"><CashReport><CashReportCurrency currency="USD" endingCash="secret-value"/></CashReport><OpenPositions></OpenPositions></FlexStatement>`)
	if _, err := ParseReconciliationSnapshots(body); err == nil {
		t.Fatal("expected invalid decimal error")
	}
}

func TestSnapshotCannotTreatSettledCashOrLotsAsCompleteTradeDate(t *testing.T) {
	for _, row := range []string{`<CashReportCurrency currency="USD" endingSettledCash="10"/>`, `<CashReportCurrency currency="USD" endingCash="10"/>`} {
		positions := `<OpenPositions></OpenPositions>`
		if row == `<CashReportCurrency currency="USD" endingCash="10"/>` {
			positions = `<OpenPositions><OpenPosition levelOfDetail="LOT" conid="1" symbol="A" assetCategory="STK" position="3"/></OpenPositions>`
		}
		body := []byte(`<FlexStatement accountId="U1" toDate="20260903"><CashReport>` + row + `</CashReport>` + positions + `</FlexStatement>`)
		snapshots, e := ParseReconciliationSnapshots(body)
		if e != nil || len(snapshots) != 1 || snapshots[0].Completeness != "partial" {
			t.Fatalf("ambiguous snapshot became complete: %#v %v", snapshots, e)
		}
	}
}
