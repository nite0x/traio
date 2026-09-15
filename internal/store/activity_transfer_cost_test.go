package store

import (
	"context"
	"testing"

	"github.com/nite/traio/internal/activity"
)

func TestTransferCostEvidenceUpdatesExistingTransfer(t *testing.T) {
	s := historyTestStore(t, "sqlite")
	seedHistoryAccount(t, s)
	r := historyTrade("transfer-cost", "0", "")
	r.RecordType = "Transfer"
	r.Activity.Type = "security_transfer"
	r.Activity.Fill = nil
	r.Activity.Legs = r.Activity.Legs[:1]
	r.Activity.Warnings = []string{"transferred_cost_basis_unknown"}
	runHistoryRecords(t, s, r)
	r.Activity.Warnings = nil
	r.Activity.CostAdjustments = []activity.CostAdjustment{{ExternalInstrumentID: "265598", Currency: "USD", BasisDelta: "1234.567", AllocationMethod: "reported_transfer_lots", Evidence: "test lot evidence"}}
	counts := runHistoryRecords(t, s, r)
	if counts.Updated != 1 {
		t.Fatalf("late evidence was discarded: %#v", counts)
	}
	counts = runHistoryRecords(t, s, r)
	if counts.Duplicates != 1 {
		t.Fatalf("repeated evidence not idempotent: %#v", counts)
	}
	page, err := s.ListActivities(context.Background(), HistoryQuery{Symbol: "AAPL"})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page: %#v %v", page, err)
	}
	a := page.Items[0]
	if len(a.CostAdjustments) != 1 || a.CostAdjustments[0].BasisDelta != "1234.567" || a.CostAdjustments[0].InstrumentID == 0 || a.Legs[0].QuantityDelta != "10" || len(a.CashEffectsByCurrency) != 0 {
		t.Fatalf("cost not preserved independently from cash and shares: %#v", a)
	}
}
