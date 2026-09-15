// Package activity defines exact, provider-neutral account history. It does not
// depend on storage, networking, order state, or current portfolio projections.
package activity

import "encoding/json"

const RuleVersion = "ibkr-activity-v1"
const (
	StatusEffective    = "effective"
	StatusVoided       = "voided"
	StatusNeedsReview  = "needs_review"
	StatusUnsupported  = "unsupported"
	BookingBooked      = "booked"
	BookingProvisional = "provisional"
)

type Activity struct {
	ID                    string            `json:"id"`
	AccountID             int64             `json:"account_id"`
	Provider              string            `json:"provider"`
	ProviderAccountID     string            `json:"provider_account_id"`
	Type                  string            `json:"type"`
	Subtype               string            `json:"subtype,omitempty"`
	Status                string            `json:"status"`
	BookingStatus         string            `json:"booking_status"`
	Description           string            `json:"description"`
	TradeDate             string            `json:"trade_date,omitempty"`
	SettlementDate        string            `json:"settlement_date,omitempty"`
	OccurredAt            string            `json:"occurred_at,omitempty"`
	SourceTimezone        string            `json:"source_timezone,omitempty"`
	TimePrecision         string            `json:"time_precision"`
	Revision              int               `json:"revision"`
	Legs                  []Leg             `json:"legs"`
	Fill                  *Fill             `json:"fill,omitempty"`
	Sources               []Source          `json:"sources"`
	Links                 []Link            `json:"links,omitempty"`
	CashEffectsByCurrency map[string]string `json:"cash_effects_by_currency"`
	Warnings              []string          `json:"warnings"`
	CostAdjustments       []CostAdjustment  `json:"cost_adjustments,omitempty"`
}
type Leg struct {
	InstrumentID         int64  `json:"instrument_id,omitempty"`
	Symbol               string `json:"symbol,omitempty"`
	AssetType            string `json:"asset_type,omitempty"`
	ExternalInstrumentID string `json:"external_instrument_id,omitempty"`
	Kind                 string `json:"kind"`
	Component            string `json:"component"`
	Currency             string `json:"currency,omitempty"`
	QuantityDelta        string `json:"quantity_delta,omitempty"`
	CashDelta            string `json:"cash_delta,omitempty"`
	EffectiveDate        string `json:"effective_date,omitempty"`
	SettlementDate       string `json:"settlement_date,omitempty"`
}
type Fill struct {
	ExecutionID         string `json:"execution_id,omitempty"`
	OrderID             string `json:"order_id,omitempty"`
	Side                string `json:"side"`
	OpenClose           string `json:"open_close,omitempty"`
	Quantity            string `json:"quantity"`
	Price               string `json:"price,omitempty"`
	PriceCurrency       string `json:"price_currency,omitempty"`
	Multiplier          string `json:"multiplier,omitempty"`
	Exchange            string `json:"exchange,omitempty"`
	ReportedNetCash     string `json:"reported_net_cash,omitempty"`
	ReportedRealizedPnL string `json:"reported_realized_pnl,omitempty"`
}
type Source struct {
	RawRecordID string `json:"raw_record_id,omitempty"`
	Source      string `json:"source"`
	Namespace   string `json:"namespace"`
	Key         string `json:"key"`
	Role        string `json:"role"`
	MatchMethod string `json:"match_method,omitempty"`
}
type Identity struct {
	Namespace  string `json:"namespace"`
	Kind       string `json:"kind"`
	ExternalID string `json:"external_id"`
}
type RawRecord struct {
	Source            string          `json:"source"`
	Namespace         string          `json:"namespace"`
	Key               string          `json:"key"`
	RecordType        string          `json:"record_type"`
	ProviderAccountID string          `json:"provider_account_id"`
	SourceUpdatedAt   string          `json:"source_updated_at,omitempty"`
	Payload           json.RawMessage `json:"payload"`
	Activity          Activity        `json:"activity"`
	Identities        []Identity      `json:"identities"`
}
type CostAdjustment struct {
	InstrumentID         int64  `json:"instrument_id,omitempty"`
	ExternalInstrumentID string `json:"external_instrument_id,omitempty"`
	Currency             string `json:"currency"`
	BasisDelta           string `json:"basis_delta,omitempty"`
	BasisBefore          string `json:"basis_before,omitempty"`
	BasisAfter           string `json:"basis_after,omitempty"`
	AllocationMethod     string `json:"allocation_method"`
	Evidence             string `json:"evidence"`
}

// Link describes an explicit, audited relationship between two activities.
type Link struct {
	FromActivityID string `json:"from_activity_id"`
	ToActivityID   string `json:"to_activity_id"`
	RelationType   string `json:"relation_type"`
	Status         string `json:"status"`
}
