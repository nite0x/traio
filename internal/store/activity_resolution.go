package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/nite/traio/internal/activity"
)

const manualReviewSourcePriority = 1000
const acceptedRevisionSourcePriority = 100

type duplicateIssueDetails struct {
	Warnings []string `json:"warnings"`
}

type revisionConflictIssueDetails struct {
	Warnings         []string          `json:"warnings"`
	CurrentRevision  int               `json:"current_revision"`
	ProposedActivity activity.Activity `json:"proposed_activity"`
}

// resolveHistoryDuplicate never accepts edited amounts. It either proves a
// source is already represented, or books its validated original effects after
// an explicit duplicate/fee-overlap decision.
func (s *Store) resolveHistoryDuplicate(ctx context.Context, issueID, action, target string, userID int64) error {
	if action == "accept_revision" {
		return s.resolveHistoryRevisionConflict(ctx, issueID, target, userID)
	}
	if userID < 0 {
		return fmt.Errorf("resolution_requires_evidence")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var activityID, rawID, issueType, issueStatus, detailText string
	var accountID int64
	err = tx.QueryRowContext(ctx, s.bind(`
		SELECT account_id, COALESCE(CAST(activity_id AS TEXT), ''),
			COALESCE(CAST(raw_record_id AS TEXT), ''), issue_type, status, details
		FROM activity_reconciliation_issues WHERE id=?`), issueID).Scan(
		&accountID, &activityID, &rawID, &issueType, &issueStatus, &detailText,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if issueStatus == "resolved" {
		return fmt.Errorf("issue_already_resolved")
	}
	if issueStatus != "open" || activityID == "" || rawID == "" || !hasDuplicateResolutionEvidence(issueType, detailText) {
		return fmt.Errorf("resolution_requires_evidence")
	}
	if _, err = s.txExecContext(ctx, tx, `UPDATE broker_accounts SET activity_generation=activity_generation WHERE id=?`, accountID); err != nil {
		return err
	}

	var rawText string
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT normalized FROM broker_raw_records WHERE id=? AND account_id=?`), rawID, accountID).Scan(&rawText); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var raw activity.RawRecord
	if err = json.Unmarshal([]byte(rawText), &raw); err != nil {
		return err
	}

	var current activity.Activity
	var oldPayload, oldRevisionID, sourceUpdatedAt string
	err = tx.QueryRowContext(ctx, s.bind(`
		SELECT v.id, v.payload, v.source_updated_at
		FROM account_activities a
		JOIN activity_revisions v ON v.activity_id=a.id AND v.revision_no=a.current_revision
		WHERE a.id=? AND a.account_id=?`), activityID, accountID).Scan(&oldRevisionID, &oldPayload, &sourceUpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(oldPayload), &current); err != nil {
		return err
	}
	if current.Status != activity.StatusNeedsReview {
		return fmt.Errorf("issue_revision_stale")
	}
	var sourceRole string
	err = tx.QueryRowContext(ctx, s.bind(`SELECT role FROM activity_sources WHERE revision_id=? AND raw_record_id=?`), oldRevisionID, rawID).Scan(&sourceRole)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("issue_revision_stale")
	}
	if err != nil {
		return err
	}
	if sourceRole != "principal" {
		return fmt.Errorf("issue_revision_stale")
	}
	if raw.ProviderAccountID != current.ProviderAccountID || (raw.Activity.ProviderAccountID != "" && raw.Activity.ProviderAccountID != current.ProviderAccountID) {
		return fmt.Errorf("resolution_requires_evidence")
	}

	reviewed := current
	matchMethod := "manual_distinct"
	if action == "confirm_distinct" {
		if target != "" {
			return fmt.Errorf("invalid_duplicate_target")
		}
		reviewed = raw.Activity
		reviewed.ID = activityID
		reviewed.AccountID = accountID
		reviewed.Provider = current.Provider
		reviewed.ProviderAccountID = current.ProviderAccountID
		reviewed.Revision = current.Revision + 1
		if reviewed.Status == activity.StatusUnsupported {
			return fmt.Errorf("resolution_requires_evidence")
		}
		reviewed.Status = activity.StatusEffective
		reviewed.Warnings = []string{}
		if reviewed.TradeDate == "" {
			return fmt.Errorf("resolution_requires_evidence")
		}
		if err = s.resolveReviewInstruments(ctx, tx, &reviewed); err != nil {
			return fmt.Errorf("resolution_requires_evidence: %w", err)
		}
		if err = activity.Validate(&reviewed); err != nil {
			return fmt.Errorf("resolution_requires_evidence: %w", err)
		}
	} else if action == "confirm_same" {
		if target == "" || target == activityID {
			return fmt.Errorf("invalid_duplicate_target")
		}
		candidate := raw.Activity
		candidate.AccountID = accountID
		candidate.Provider = current.Provider
		candidate.ProviderAccountID = current.ProviderAccountID
		if candidate.Status != activity.StatusEffective && candidate.Status != activity.StatusNeedsReview {
			return fmt.Errorf("resolution_requires_evidence")
		}
		if err = s.resolveReviewInstruments(ctx, tx, &candidate); err != nil {
			return fmt.Errorf("resolution_requires_evidence: %w", err)
		}
		if err = activity.Validate(&candidate); err != nil {
			return fmt.Errorf("resolution_requires_evidence: %w", err)
		}

		var targetPayload, targetRevisionID string
		err = tx.QueryRowContext(ctx, s.bind(`
			SELECT v.payload, v.id
			FROM account_activities a
			JOIN activity_revisions v ON v.activity_id=a.id AND v.revision_no=a.current_revision
			WHERE a.id=? AND a.account_id=?`), target, accountID).Scan(&targetPayload, &targetRevisionID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var other activity.Activity
		if err = json.Unmarshal([]byte(targetPayload), &other); err != nil {
			return err
		}
		if other.AccountID != accountID || other.Status != activity.StatusEffective || !duplicateDatesMatch(candidate, other) {
			return fmt.Errorf("resolution_requires_evidence")
		}
		if !activityEffectsContained(candidate, other) {
			return fmt.Errorf("duplicate_effect_mismatch")
		}
		if _, err = s.txExecContext(ctx, tx, `
			INSERT INTO activity_sources(revision_id,raw_record_id,role,match_method)
			VALUES(?,?,'corroboration','manual_exact_effect')
			ON CONFLICT(revision_id,raw_record_id) DO NOTHING`, targetRevisionID, rawID); err != nil {
			return err
		}
		// Keep the source identity on this voided lineage. Replays then encounter
		// the duplicate_of link and cannot replace either side of the decision.
		if _, err = s.txExecContext(ctx, tx, `
			INSERT INTO activity_links(id,from_activity_id,to_activity_id,relation_type,created_by,created_at)
			VALUES(?,?,?,'duplicate_of',?,?)
			ON CONFLICT(from_activity_id,to_activity_id,relation_type) DO NOTHING`,
			uuid.NewString(), activityID, target, userID, nowRFC3339()); err != nil {
			return err
		}
		reviewed.Revision++
		reviewed.Status = activity.StatusVoided
		reviewed.Warnings = []string{"duplicate_source_confirmed"}
		matchMethod = "manual_same"
	} else {
		return fmt.Errorf("resolution_requires_evidence")
	}

	reviewed.Sources = []activity.Source{}
	revisionID := uuid.NewString()
	if _, err = s.txExecContext(ctx, tx, `
		INSERT INTO activity_revisions(
			id,activity_id,revision_no,activity_type,status,booking_status,sort_key,payload,
			source_priority,source_updated_at,rule_version,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		revisionID, activityID, reviewed.Revision, reviewed.Type, reviewed.Status,
		reviewed.BookingStatus, reviewed.TradeDate+"|"+reviewed.OccurredAt, historyJSON(reviewed),
		manualReviewSourcePriority, sourceUpdatedAt, "manual-review-v1", nowRFC3339()); err != nil {
		return err
	}
	if err = s.insertReviewedActivityRows(ctx, tx, accountID, revisionID, reviewed); err != nil {
		return err
	}
	if _, err = s.txExecContext(ctx, tx, `
		INSERT INTO activity_sources(revision_id,raw_record_id,role,match_method)
		SELECT ?,raw_record_id,role,match_method FROM activity_sources WHERE revision_id=?
		ON CONFLICT(revision_id,raw_record_id) DO NOTHING`, revisionID, oldRevisionID); err != nil {
		return err
	}
	if _, err = s.txExecContext(ctx, tx, `UPDATE account_activities SET current_revision=? WHERE id=?`, reviewed.Revision, activityID); err != nil {
		return err
	}
	if _, err = s.txExecContext(ctx, tx, `UPDATE broker_raw_records SET normalize_status='done' WHERE id=?`, rawID); err != nil {
		return err
	}
	if _, err = s.txExecContext(ctx, tx, `
		UPDATE activity_reconciliation_issues SET status='resolved',resolution=?,updated_at=? WHERE id=?`,
		historyJSON(map[string]any{
			"action":             action,
			"target_activity_id": target,
			"user_id":            userID,
			"match_method":       matchMethod,
		}), nowRFC3339(), issueID); err != nil {
		return err
	}
	if _, err = s.txExecContext(ctx, tx, `UPDATE broker_accounts SET activity_generation=activity_generation+1 WHERE id=?`, accountID); err != nil {
		return err
	}
	return tx.Commit()
}

// resolveHistoryRevisionConflict accepts the immutable raw record attached to
// an ordering conflict. It never accepts amounts or effects from the request.
func (s *Store) resolveHistoryRevisionConflict(ctx context.Context, issueID, target string, userID int64) error {
	if userID < 0 || target != "" {
		return fmt.Errorf("resolution_requires_evidence")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var accountID int64
	var activityID, rawID, issueType, issueStatus, detailText string
	err = tx.QueryRowContext(ctx, s.bind(`
		SELECT account_id, COALESCE(CAST(activity_id AS TEXT), ''),
			COALESCE(CAST(raw_record_id AS TEXT), ''), issue_type, status, details
		FROM activity_reconciliation_issues WHERE id=?`), issueID).Scan(
		&accountID, &activityID, &rawID, &issueType, &issueStatus, &detailText,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if issueStatus == "resolved" {
		return fmt.Errorf("issue_already_resolved")
	}
	var details revisionConflictIssueDetails
	if issueStatus != "open" || issueType != "source_revision_conflict" ||
		activityID == "" || rawID == "" || json.Unmarshal([]byte(detailText), &details) != nil ||
		details.CurrentRevision <= 0 || len(details.Warnings) != 1 || details.Warnings[0] != "source_revision_order_unknown" {
		return fmt.Errorf("resolution_requires_evidence")
	}
	if _, err = s.txExecContext(ctx, tx, `UPDATE broker_accounts SET activity_generation=activity_generation WHERE id=?`, accountID); err != nil {
		return err
	}

	var rawText string
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT normalized FROM broker_raw_records WHERE id=? AND account_id=?`), rawID, accountID).Scan(&rawText); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var raw activity.RawRecord
	if err = json.Unmarshal([]byte(rawText), &raw); err != nil {
		return err
	}

	var current activity.Activity
	var oldRevisionID, oldPayload string
	err = tx.QueryRowContext(ctx, s.bind(`
		SELECT v.id, v.payload
		FROM account_activities a
		JOIN activity_revisions v ON v.activity_id=a.id AND v.revision_no=a.current_revision
		WHERE a.id=? AND a.account_id=?`), activityID, accountID).Scan(&oldRevisionID, &oldPayload)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(oldPayload), &current); err != nil {
		return err
	}
	if current.Revision != details.CurrentRevision {
		return fmt.Errorf("issue_revision_stale")
	}
	var sourceRole string
	err = tx.QueryRowContext(ctx, s.bind(`SELECT role FROM activity_sources WHERE revision_id=? AND raw_record_id=?`), oldRevisionID, rawID).Scan(&sourceRole)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("issue_revision_stale")
	}
	if err != nil {
		return err
	}
	if sourceRole != "corroboration" {
		return fmt.Errorf("issue_revision_stale")
	}
	if raw.ProviderAccountID != current.ProviderAccountID || (raw.Activity.ProviderAccountID != "" && raw.Activity.ProviderAccountID != current.ProviderAccountID) {
		return fmt.Errorf("resolution_requires_evidence")
	}

	accepted := raw.Activity
	accepted.ID = activityID
	accepted.AccountID = accountID
	accepted.Provider = current.Provider
	accepted.ProviderAccountID = current.ProviderAccountID
	accepted.Revision = current.Revision + 1
	accepted.Sources = []activity.Source{}
	if accepted.Status != activity.StatusEffective && accepted.Status != activity.StatusVoided {
		return fmt.Errorf("resolution_requires_evidence")
	}
	if err = s.resolveReviewInstruments(ctx, tx, &accepted); err != nil {
		return fmt.Errorf("resolution_requires_evidence: %w", err)
	}
	if err = activity.Validate(&accepted); err != nil {
		return fmt.Errorf("resolution_requires_evidence: %w", err)
	}
	proposed := details.ProposedActivity
	proposed.Sources = []activity.Source{}
	if proposed.ID == "" || historyJSON(proposed) != historyJSON(accepted) {
		return fmt.Errorf("resolution_requires_evidence")
	}

	revisionID := uuid.NewString()
	if _, err = s.txExecContext(ctx, tx, `
		INSERT INTO activity_revisions(
			id,activity_id,revision_no,activity_type,status,booking_status,sort_key,payload,
			source_priority,source_updated_at,rule_version,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		revisionID, activityID, accepted.Revision, accepted.Type, accepted.Status,
		accepted.BookingStatus, accepted.TradeDate+"|"+accepted.OccurredAt, historyJSON(accepted),
		acceptedRevisionSourcePriority, raw.SourceUpdatedAt, "manual-review-v1", nowRFC3339()); err != nil {
		return err
	}
	if err = s.insertReviewedActivityRows(ctx, tx, accountID, revisionID, accepted); err != nil {
		return err
	}
	if _, err = s.txExecContext(ctx, tx, `
		INSERT INTO activity_sources(revision_id,raw_record_id,role,match_method)
		SELECT ?,raw_record_id,
			CASE WHEN raw_record_id=? THEN 'principal' ELSE 'corroboration' END,
			CASE WHEN raw_record_id=? THEN 'manual_accept_revision' ELSE match_method END
		FROM activity_sources WHERE revision_id=?
		ON CONFLICT(revision_id,raw_record_id) DO NOTHING`,
		revisionID, rawID, rawID, oldRevisionID); err != nil {
		return err
	}
	if _, err = s.txExecContext(ctx, tx, `UPDATE account_activities SET current_revision=? WHERE id=?`, accepted.Revision, activityID); err != nil {
		return err
	}
	if _, err = s.txExecContext(ctx, tx, `UPDATE broker_raw_records SET normalize_status='done' WHERE id=?`, rawID); err != nil {
		return err
	}
	if _, err = s.txExecContext(ctx, tx, `
		UPDATE activity_reconciliation_issues SET status='resolved',resolution=?,updated_at=? WHERE id=?`,
		historyJSON(map[string]any{
			"action":        "accept_revision",
			"user_id":       userID,
			"raw_record_id": rawID,
			"match_method":  "manual_accept_revision",
		}), nowRFC3339(), issueID); err != nil {
		return err
	}
	if _, err = s.txExecContext(ctx, tx, `UPDATE broker_accounts SET activity_generation=activity_generation+1 WHERE id=?`, accountID); err != nil {
		return err
	}
	return tx.Commit()
}

func hasDuplicateResolutionEvidence(issueType, detailText string) bool {
	if issueType != activity.StatusNeedsReview {
		return false
	}
	var details duplicateIssueDetails
	if json.Unmarshal([]byte(detailText), &details) != nil || len(details.Warnings) == 0 {
		return false
	}
	found := false
	for _, warning := range details.Warnings {
		switch warning {
		case "ambiguous_duplicate", "potential_trade_fee_overlap":
			found = true
		default:
			return false
		}
	}
	return found
}

func (s *Store) resolveReviewInstruments(ctx context.Context, tx *sql.Tx, reviewed *activity.Activity) error {
	resolved := make(map[string]int64)
	for i := range reviewed.Legs {
		leg := &reviewed.Legs[i]
		if leg.Kind != "position" {
			continue
		}
		if leg.Symbol == "" {
			return fmt.Errorf("instrument identity is incomplete")
		}
		instrument, err := s.resolveInstrumentTx(ctx, tx, InstrumentIdentity{
			ProviderCode: reviewed.Provider,
			ExternalID:   leg.ExternalInstrumentID,
			AssetType:    leg.AssetType,
			Symbol:       leg.Symbol,
			Currency:     leg.Currency,
		})
		if err != nil {
			return err
		}
		if leg.InstrumentID != 0 && leg.InstrumentID != instrument.ID {
			return fmt.Errorf("instrument identity mismatch")
		}
		leg.InstrumentID = instrument.ID
		if leg.ExternalInstrumentID != "" {
			resolved[leg.ExternalInstrumentID] = instrument.ID
		}
	}
	for i := range reviewed.CostAdjustments {
		adjustment := &reviewed.CostAdjustments[i]
		instrumentID := resolved[adjustment.ExternalInstrumentID]
		if instrumentID == 0 && adjustment.ExternalInstrumentID != "" {
			err := tx.QueryRowContext(ctx, s.bind(`
				SELECT instrument_id FROM broker_instruments
				WHERE provider_code=? AND external_id=?`), reviewed.Provider, adjustment.ExternalInstrumentID).Scan(&instrumentID)
			if err != nil {
				return fmt.Errorf("cost instrument identity is unresolved")
			}
		}
		if instrumentID == 0 || (adjustment.InstrumentID != 0 && adjustment.InstrumentID != instrumentID) {
			return fmt.Errorf("cost instrument identity mismatch")
		}
		adjustment.InstrumentID = instrumentID
	}
	return nil
}

func duplicateDatesMatch(candidate, target activity.Activity) bool {
	if candidate.TradeDate == "" || candidate.TradeDate != target.TradeDate {
		return false
	}
	if candidate.SettlementDate != "" && candidate.SettlementDate != target.SettlementDate {
		return false
	}
	if candidate.OccurredAt != "" && candidate.OccurredAt != target.OccurredAt {
		return false
	}
	return true
}

func activityEffectsContained(candidate, target activity.Activity) bool {
	if len(candidate.Legs) == 0 && len(candidate.CostAdjustments) == 0 {
		return false
	}
	usedLegs := make(map[int]bool)
	for _, candidateLeg := range candidate.Legs {
		found := false
		for i, targetLeg := range target.Legs {
			if usedLegs[i] || !sameActivityLeg(candidateLeg, targetLeg) {
				continue
			}
			usedLegs[i] = true
			found = true
			break
		}
		if !found {
			return false
		}
	}
	usedCosts := make(map[int]bool)
	for _, candidateCost := range candidate.CostAdjustments {
		found := false
		for i, targetCost := range target.CostAdjustments {
			if usedCosts[i] || !sameCostAdjustment(candidateCost, targetCost) {
				continue
			}
			usedCosts[i] = true
			found = true
			break
		}
		if !found {
			return false
		}
	}
	return true
}

func sameActivityLeg(left, right activity.Leg) bool {
	if left.Kind != right.Kind || left.Component != right.Component || left.EffectiveDate == "" || left.EffectiveDate != right.EffectiveDate {
		return false
	}
	if left.SettlementDate != "" && left.SettlementDate != right.SettlementDate {
		return false
	}
	if left.Kind == "cash" {
		return left.Currency == right.Currency && sameActivityDecimal(left.CashDelta, right.CashDelta)
	}
	return left.InstrumentID != 0 && left.InstrumentID == right.InstrumentID && sameActivityDecimal(left.QuantityDelta, right.QuantityDelta)
}

func sameCostAdjustment(left, right activity.CostAdjustment) bool {
	return left.InstrumentID != 0 && left.InstrumentID == right.InstrumentID &&
		left.Currency == right.Currency && left.AllocationMethod == right.AllocationMethod && left.Evidence == right.Evidence &&
		sameOptionalActivityDecimal(left.BasisDelta, right.BasisDelta) &&
		sameOptionalActivityDecimal(left.BasisBefore, right.BasisBefore) &&
		sameOptionalActivityDecimal(left.BasisAfter, right.BasisAfter)
}

func sameOptionalActivityDecimal(left, right string) bool {
	if left == "" || right == "" {
		return left == right
	}
	return sameActivityDecimal(left, right)
}

func sameActivityDecimal(left, right string) bool {
	comparison, err := activity.Compare(left, right)
	return err == nil && comparison == 0
}

func (s *Store) insertReviewedActivityRows(ctx context.Context, tx *sql.Tx, accountID int64, revisionID string, reviewed activity.Activity) error {
	for i, leg := range reviewed.Legs {
		if _, err := s.txExecContext(ctx, tx, `
			INSERT INTO activity_legs(
				id,revision_id,ordinal,kind,component,instrument_id,currency,quantity_delta,cash_delta,effective_date,settlement_date
			) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			uuid.NewString(), revisionID, i, leg.Kind, leg.Component, historyInt(leg.InstrumentID),
			historyNullable(leg.Currency), historyNullable(leg.QuantityDelta), historyNullable(leg.CashDelta),
			leg.EffectiveDate, leg.SettlementDate); err != nil {
			return err
		}
	}
	if reviewed.Fill != nil && reviewed.Fill.Quantity != "" {
		var orderID any
		if reviewed.Fill.OrderID != "" {
			storedOrderID := uuid.NewString()
			if _, err := s.txExecContext(ctx, tx, `
				INSERT INTO broker_orders(id,account_id,namespace,external_id,payload)
				VALUES(?,?,'ibkr.order',?,?)
				ON CONFLICT(account_id,namespace,external_id) DO NOTHING`,
				storedOrderID, accountID, reviewed.Fill.OrderID, `{"status":"unknown"}`); err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, s.bind(`
				SELECT id FROM broker_orders
				WHERE account_id=? AND namespace='ibkr.order' AND external_id=?`),
				accountID, reviewed.Fill.OrderID).Scan(&storedOrderID); err != nil {
				return err
			}
			orderID = storedOrderID
		}
		if _, err := s.txExecContext(ctx, tx, `
			INSERT INTO broker_fills(revision_id,order_id,execution_id,quantity,price,multiplier,payload)
			VALUES(?,?,?,?,?,?,?)`,
			revisionID, orderID, reviewed.Fill.ExecutionID, reviewed.Fill.Quantity,
			historyNullable(reviewed.Fill.Price), historyNullable(reviewed.Fill.Multiplier), historyJSON(reviewed.Fill)); err != nil {
			return err
		}
	}
	for _, adjustment := range reviewed.CostAdjustments {
		if _, err := s.txExecContext(ctx, tx, `
			INSERT INTO activity_cost_adjustments(
				id,revision_id,instrument_id,currency,basis_delta,basis_before,basis_after,allocation_method,evidence
			) VALUES(?,?,?,?,?,?,?,?,?)`,
			uuid.NewString(), revisionID, historyInt(adjustment.InstrumentID), adjustment.Currency,
			historyNullable(adjustment.BasisDelta), historyNullable(adjustment.BasisBefore),
			historyNullable(adjustment.BasisAfter), adjustment.AllocationMethod, adjustment.Evidence); err != nil {
			return err
		}
	}
	return nil
}
