package store

import "github.com/nite/traio/internal/activity"

func transferCostOnlyEnrichment(old, next activity.Activity) bool {
	if old.Type != "security_transfer" || old.Status != activity.StatusEffective || len(old.CostAdjustments) != 0 || len(next.CostAdjustments) == 0 {
		return false
	}
	for _, cost := range next.CostAdjustments {
		if cost.AllocationMethod != "reported_transfer_lots" || cost.BasisDelta == "" || cost.Evidence == "" {
			return false
		}
	}
	next.Revision, next.Sources = old.Revision, old.Sources
	next.CostAdjustments = nil
	filtered := []string{}
	for _, warning := range old.Warnings {
		if warning != "transferred_cost_basis_unknown" {
			filtered = append(filtered, warning)
		}
	}
	old.Warnings = filtered
	return historyJSON(old) == historyJSON(next)
}
