package activity

import "testing"

func TestExactDecimalArithmeticAndLimits(t *testing.T) {
	tests := []struct {
		name string
		got  func() (string, error)
		want string
	}{
		{name: "canonical", got: func() (string, error) { return CanonicalDecimal("-00012.3400") }, want: "-12.34"},
		{name: "add", got: func() (string, error) { return Add("0.1", "0.2") }, want: "0.3"},
		{name: "subtract", got: func() (string, error) { return Sub("1", "1.000") }, want: "0"},
		{name: "multiply", got: func() (string, error) { return Mul("12.5", "0.08") }, want: "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.got()
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	for _, invalid := range []string{"", "NaN", "1e3", "1,000", "123456789012345678901", "0.1234567890123456789"} {
		if _, err := CanonicalDecimal(invalid); err == nil {
			t.Fatalf("CanonicalDecimal(%q) succeeded", invalid)
		}
	}
	if _, err := Mul("0.333333333333333333", "0.1"); err == nil {
		t.Fatal("inexact result beyond 18 fractional digits succeeded")
	}
}

func TestValidateAndCashEffectsKeepCurrenciesSeparate(t *testing.T) {
	a := Activity{
		Provider: "IBKR", ProviderAccountID: "U1", Type: "trade",
		Status: StatusEffective, BookingStatus: BookingBooked, TradeDate: "2026-09-01", TimePrecision: "date",
		Legs: []Leg{
			{Kind: "cash", Component: "principal", Currency: "USD", CashDelta: "-1000.00"},
			{Kind: "cash", Component: "commission", Currency: "HKD", CashDelta: "-7.8"},
			{Kind: "position", Component: "execution", ExternalInstrumentID: "123", QuantityDelta: "10.000"},
		},
	}
	if err := Validate(&a); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if a.CashEffectsByCurrency["USD"] != "-1000" || a.CashEffectsByCurrency["HKD"] != "-7.8" || len(a.CashEffectsByCurrency) != 2 {
		t.Fatalf("cash effects merged unlike currencies: %#v", a.CashEffectsByCurrency)
	}
	if a.Legs[2].QuantityDelta != "10" {
		t.Fatalf("quantity not canonical: %#v", a.Legs[2])
	}

	bad := a
	bad.Legs = []Leg{{Kind: "cash", Currency: "usd", CashDelta: "1"}}
	if err := Validate(&bad); err == nil {
		t.Fatal("invalid currency accepted")
	}
}

func TestReconcileDoesNotTreatUnknownAsPassed(t *testing.T) {
	closing := Balance{Kind: "cash", Currency: "USD", Value: "110"}
	if got, err := Reconcile(nil, closing, nil); err != nil || got.Status != "unknown" {
		t.Fatalf("missing baseline = %#v, %v", got, err)
	}
	opening := &Balance{Kind: "cash", Currency: "USD", Value: "100"}
	activities := []Activity{
		{Status: StatusEffective, BookingStatus: BookingBooked, Legs: []Leg{{Kind: "cash", Currency: "USD", CashDelta: "10"}}},
		{Status: StatusNeedsReview, BookingStatus: BookingBooked, Legs: []Leg{{Kind: "cash", Currency: "USD", CashDelta: "999"}}},
	}
	got, err := Reconcile(opening, closing, activities)
	if err != nil || got.Status != "incomplete" || got.Expected != "110" || got.Difference != "0" {
		t.Fatalf("reconciliation = %#v, %v", got, err)
	}
}

func TestValidateRequiresEvidenceForCostAdjustments(t *testing.T) {
	a := Activity{
		Provider: "IBKR", ProviderAccountID: "U1", Type: "return_of_capital",
		Status: StatusEffective, BookingStatus: BookingBooked, TradeDate: "2026-09-01",
		CostAdjustments: []CostAdjustment{{
			ExternalInstrumentID: "265598", Currency: "USD", BasisDelta: "-25.000",
			AllocationMethod: "provider_reported", Evidence: "ibkr.flex:transaction:C1",
		}},
	}
	if err := Validate(&a); err != nil {
		t.Fatalf("explicit cost adjustment rejected: %v", err)
	}
	if a.CostAdjustments[0].BasisDelta != "-25" {
		t.Fatalf("basis delta not canonicalized: %#v", a.CostAdjustments[0])
	}
	bad := a
	bad.CostAdjustments = append([]CostAdjustment(nil), a.CostAdjustments...)
	bad.CostAdjustments[0].Evidence = ""
	if err := Validate(&bad); err == nil {
		t.Fatal("cost adjustment without evidence accepted")
	}
}
