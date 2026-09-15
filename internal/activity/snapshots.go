package activity

// ReconciliationSnapshot is a broker-reported end-of-day balance set. It is
// evidence independent from the activity ledger and must not be synthesized
// from current portfolio projections.
type ReconciliationSnapshot struct {
	ProviderAccountID string                  `json:"provider_account_id"`
	AsOfDate          string                  `json:"as_of_date"`
	Basis             string                  `json:"basis"`
	SourceRef         string                  `json:"source_ref"`
	Completeness      string                  `json:"completeness"`
	Balances          []ReconciliationBalance `json:"balances"`
}

type ReconciliationBalance struct {
	Balance
	ExternalInstrumentID string `json:"external_instrument_id,omitempty"`
	Symbol               string `json:"symbol,omitempty"`
	AssetType            string `json:"asset_type,omitempty"`
	ReportedCost         string `json:"reported_cost,omitempty"`
}
