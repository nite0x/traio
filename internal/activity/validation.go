package activity

import (
	"fmt"
	"strings"
	"time"
)

// CashEffects sums explicit cash legs, never reported net cash. Callers choose
// whether the enclosing activity is booked/effective before aggregating.
func CashEffects(legs []Leg) (map[string]string, error) {
	result := map[string]string{}
	for _, l := range legs {
		if l.Kind != "cash" {
			continue
		}
		if !ValidCurrency(l.Currency) {
			return nil, fmt.Errorf("cash leg requires a currency")
		}
		old := result[l.Currency]
		if old == "" {
			old = "0"
		}
		n, e := Add(old, l.CashDelta)
		if e != nil {
			return nil, e
		}
		result[l.Currency] = n
	}
	return result, nil
}
func ValidCurrency(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}
func ValidateDate(s string) error {
	if s == "" {
		return nil
	}
	t, e := time.Parse("2006-01-02", s)
	if e != nil || t.Format("2006-01-02") != s {
		return fmt.Errorf("invalid activity date")
	}
	return nil
}

// Validate canonicalizes precise decimals and rejects ambiguous leg shapes.
// Unresolved instrument IDs may retain an explicit provider instrument ID;
// persistence must resolve that identity before inserting a position leg.
func Validate(a *Activity) error {
	if a == nil {
		return fmt.Errorf("activity is required")
	}
	if a.Type == "" || a.ProviderAccountID == "" {
		return fmt.Errorf("activity type and provider account are required")
	}
	switch a.Status {
	case StatusEffective, StatusVoided, StatusNeedsReview, StatusUnsupported:
	default:
		return fmt.Errorf("invalid activity status")
	}
	if a.BookingStatus != BookingBooked && a.BookingStatus != BookingProvisional {
		return fmt.Errorf("invalid booking status")
	}
	for _, d := range []string{a.TradeDate, a.SettlementDate} {
		if e := ValidateDate(d); e != nil {
			return e
		}
	}
	if a.OccurredAt != "" {
		if _, e := time.Parse(time.RFC3339Nano, a.OccurredAt); e != nil {
			return fmt.Errorf("occurred_at requires explicit timezone")
		}
	}
	if a.TimePrecision == "" {
		a.TimePrecision = "date"
	}
	switch a.TimePrecision {
	case "date", "second", "millisecond", "nanosecond", "unknown":
	default:
		return fmt.Errorf("invalid time precision")
	}
	if a.Status == StatusEffective && len(a.Legs) == 0 && len(a.CostAdjustments) == 0 {
		return fmt.Errorf("effective activity needs an explicit effect")
	}
	for i := range a.Legs {
		l := &a.Legs[i]
		if e := ValidateDate(l.EffectiveDate); e != nil {
			return e
		}
		if e := ValidateDate(l.SettlementDate); e != nil {
			return e
		}
		switch l.Kind {
		case "cash":
			if !ValidCurrency(l.Currency) || l.CashDelta == "" || l.QuantityDelta != "" {
				return fmt.Errorf("invalid cash leg")
			}
			n, e := CanonicalDecimal(l.CashDelta)
			if e != nil {
				return e
			}
			l.CashDelta = n
		case "position":
			if (l.InstrumentID == 0 && l.ExternalInstrumentID == "" && strings.TrimSpace(l.Symbol) == "") || l.QuantityDelta == "" || l.CashDelta != "" {
				return fmt.Errorf("invalid position leg")
			}
			n, e := CanonicalDecimal(l.QuantityDelta)
			if e != nil {
				return e
			}
			l.QuantityDelta = n
		default:
			return fmt.Errorf("invalid leg kind")
		}
	}
	if a.Fill != nil {
		f := a.Fill
		for _, p := range []*string{&f.Quantity, &f.Price, &f.Multiplier, &f.ReportedNetCash, &f.ReportedRealizedPnL} {
			if *p != "" {
				n, e := CanonicalDecimal(*p)
				if e != nil {
					return e
				}
				*p = n
			}
		}
	}
	for i := range a.CostAdjustments {
		c := &a.CostAdjustments[i]
		if c.InstrumentID == 0 && c.ExternalInstrumentID == "" {
			return fmt.Errorf("cost adjustment needs instrument")
		}
		if !ValidCurrency(c.Currency) || c.Evidence == "" || c.AllocationMethod == "" {
			return fmt.Errorf("cost adjustment needs currency and evidence")
		}
		if c.BasisDelta == "" && c.BasisBefore == "" && c.BasisAfter == "" {
			return fmt.Errorf("cost adjustment has no values")
		}
		for _, p := range []*string{&c.BasisDelta, &c.BasisBefore, &c.BasisAfter} {
			if *p != "" {
				n, e := CanonicalDecimal(*p)
				if e != nil {
					return e
				}
				*p = n
			}
		}
	}
	effects, e := CashEffects(a.Legs)
	if e != nil {
		return e
	}
	a.CashEffectsByCurrency = effects
	if a.Legs == nil {
		a.Legs = []Leg{}
	}
	if a.Sources == nil {
		a.Sources = []Source{}
	}
	if a.Warnings == nil {
		a.Warnings = []string{}
	}
	return nil
}

// Reconciliation compares only like units; missing baselines remain unknown.
type Balance struct {
	Kind         string `json:"kind"`
	Currency     string `json:"currency,omitempty"`
	InstrumentID int64  `json:"instrument_id,omitempty"`
	Value        string `json:"value"`
}
type Difference struct {
	Balance
	Expected   string `json:"expected"`
	Actual     string `json:"actual"`
	Difference string `json:"difference"`
	Status     string `json:"status"`
}

func Reconcile(opening *Balance, closing Balance, activities []Activity) (Difference, error) {
	d := Difference{Balance: closing, Actual: closing.Value, Status: "unknown"}
	if opening == nil {
		return d, nil
	}
	if opening.Kind != closing.Kind || opening.Currency != closing.Currency || opening.InstrumentID != closing.InstrumentID {
		return d, fmt.Errorf("reconciliation unit mismatch")
	}
	sum, e := CanonicalDecimal(opening.Value)
	if e != nil {
		return d, e
	}
	incomplete := false
	for _, a := range activities {
		if a.Status == StatusVoided {
			continue
		}
		if a.Status != StatusEffective || a.BookingStatus != BookingBooked {
			incomplete = true
			continue
		}
		for _, l := range a.Legs {
			v := ""
			if closing.Kind == "cash" && l.Kind == "cash" && l.Currency == closing.Currency {
				v = l.CashDelta
			}
			if closing.Kind == "position" && l.Kind == "position" && l.InstrumentID == closing.InstrumentID {
				v = l.QuantityDelta
			}
			if v != "" {
				sum, e = Add(sum, v)
				if e != nil {
					return d, e
				}
			}
		}
	}
	d.Expected = sum
	d.Difference, e = Sub(closing.Value, sum)
	if e != nil {
		return d, e
	}
	d.Status = "passed"
	if d.Difference != "0" {
		d.Status = "mismatch"
	}
	if incomplete {
		d.Status = "incomplete"
	}
	return d, nil
}

// NormalizeDate preserves date-only precision; time zone inference is forbidden.
func NormalizeDate(s string) string {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if t, e := time.Parse(layout, s); e == nil {
			return t.Format("2006-01-02")
		}
	}
	for _, sep := range []string{";", "T", " "} {
		if i := strings.Index(s, sep); i > 0 {
			return NormalizeDate(s[:i])
		}
	}
	return ""
}
