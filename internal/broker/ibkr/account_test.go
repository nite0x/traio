package ibkr

import "testing"

func TestAccountValuePrefersAmountWhenValueNull(t *testing.T) {
	raw := map[string]any{
		"netliquidation": map[string]any{
			"amount":   6437.18994140625,
			"value":    nil,
			"currency": "USD",
		},
	}

	got := accountValue(raw, "NetLiquidation")
	if got == nil {
		t.Fatal("expected amount, got nil")
	}
	if n := parseFloat(got); n != 6437.18994140625 {
		t.Fatalf("unexpected amount: %v", n)
	}
}

func TestFirstAccountFloatFromIBKRSummaryShape(t *testing.T) {
	raw := map[string]any{
		"netliquidation": map[string]any{
			"amount": 6437.19,
			"value":  nil,
		},
		"totalcashvalue": map[string]any{
			"amount": 102.39,
			"value":  nil,
		},
	}

	if got := firstAccountFloat(raw, "NetLiquidation", "netliquidation"); got != 6437.19 {
		t.Fatalf("net liquidation: got %v", got)
	}
	if got := firstAccountFloat(raw, "TotalCashValue", "totalcashvalue"); got != 102.39 {
		t.Fatalf("total cash: got %v", got)
	}
}
