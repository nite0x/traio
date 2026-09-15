package ibkr

import (
	"encoding/json"

	"github.com/nite/traio/internal/activity"
)

// Open lots report remaining basis, not original transfer basis. Only attach
// them when all transferred units remain, the report contains one unambiguous
// incoming transfer, and no disposal or corporate adjustment could change basis.
// Summary positions and residual portfolio-cost calculations are never used.
func attachTransferLotCosts(records []activity.RawRecord, lots []map[string]string, from, asOf string) {
	if from == "" || asOf == "" || len(lots) == 0 {
		return
	}
	type movementScope struct {
		transfers int
		blocked   bool
	}
	scopes := map[string]movementScope{}
	lotGroups := map[string][]map[string]string{}
	for _, r := range records {
		seen := map[string]bool{}
		for _, leg := range r.Activity.Legs {
			if leg.Kind != "position" || leg.ExternalInstrumentID == "" {
				continue
			}
			key := r.ProviderAccountID + "\x00" + leg.ExternalInstrumentID
			if seen[key] {
				continue
			}
			seen[key] = true
			scope := scopes[key]
			if r.Activity.Type == "security_transfer" {
				scope.transfers++
			} else if r.Activity.Type != "trade" || r.Activity.Fill == nil || r.Activity.Fill.Side != "buy" {
				scope.blocked = true
			}
			scopes[key] = scope
		}
	}
	for _, lot := range lots {
		key := lot["accountId"] + "\x00" + lot["conid"] + "\x00" + lot["currency"]
		lotGroups[key] = append(lotGroups[key], lot)
	}
	for i := range records {
		r := &records[i]
		a := &r.Activity
		if a.Type != "security_transfer" || a.Status != activity.StatusEffective || len(a.Legs) != 1 {
			continue
		}
		leg := a.Legs[0]
		cmp, err := activity.Compare(leg.QuantityDelta, "0")
		if err != nil || cmp <= 0 || leg.ExternalInstrumentID == "" || !activity.ValidCurrency(leg.Currency) || a.TradeDate < from || a.TradeDate > asOf {
			continue
		}
		key := r.ProviderAccountID + "\x00" + leg.ExternalInstrumentID
		scope := scopes[key]
		if scope.transfers != 1 || scope.blocked {
			continue
		}
		ambiguous := false
		quantity, cost := "0", "0"
		acquisitions := []string{}
		evidenceLots := []map[string]string{}
		for _, lot := range lotGroups[key+"\x00"+leg.Currency] {
			if lot["accountId"] != r.ProviderAccountID || lot["conid"] != leg.ExternalInstrumentID || lot["currency"] != leg.Currency {
				continue
			}
			// Broker trade lots cannot be assigned to the transfer.
			if id := lot["originatingOrderID"]; id != "" && id != "0" {
				continue
			}
			if id := lot["originatingTransactionID"]; id != "" && id != "0" {
				continue
			}
			date := activity.NormalizeDate(lot["openDateTime"])
			if date == "" || date > a.TradeDate || lot["costBasisMoney"] == "" {
				ambiguous = true
				break
			}
			q, e := activity.Compare(lot["position"], "0")
			c, ce := activity.Compare(lot["costBasisMoney"], "0")
			if e != nil || ce != nil || q <= 0 || c < 0 {
				ambiguous = true
				break
			}
			// Wash-sale adjusted lots cannot establish original transfer cost.
			if lot["holdingPeriodDateTime"] != "" && lot["holdingPeriodDateTime"] != lot["openDateTime"] {
				ambiguous = true
				break
			}
			quantity, err = activity.Add(quantity, lot["position"])
			if err != nil {
				ambiguous = true
				break
			}
			cost, err = activity.Add(cost, lot["costBasisMoney"])
			if err != nil {
				ambiguous = true
				break
			}
			acquisitions = append(acquisitions, date)
			evidenceLots = append(evidenceLots, map[string]string{"conid": lot["conid"], "currency": lot["currency"], "position": lot["position"], "costBasisMoney": lot["costBasisMoney"], "openDateTime": lot["openDateTime"]})
		}
		cmp, err = activity.Compare(quantity, leg.QuantityDelta)
		if ambiguous || err != nil || cmp != 0 || len(acquisitions) == 0 {
			continue
		}
		evidence, _ := json.Marshal(map[string]any{"source": "IBKR Flex Open Positions LOT", "as_of": asOf, "from": from, "lots": evidenceLots})
		a.CostAdjustments = []activity.CostAdjustment{{ExternalInstrumentID: leg.ExternalInstrumentID, Currency: leg.Currency, BasisDelta: cost, AllocationMethod: "reported_transfer_lots", Evidence: string(evidence)}}
		warnings := []string{}
		for _, warning := range a.Warnings {
			if warning != "transferred_cost_basis_unknown" {
				warnings = append(warnings, warning)
			}
		}
		a.Warnings = warnings
		finishHistoryRecord(r)
	}
}
