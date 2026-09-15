package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nite/traio/internal/activity"
)

func TestResolveHistoryIssueConfirmDistinctPersistsTypedRowsAndSurvivesReplay(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			s := historyTestStore(t, driver)
			_, _ = seedHistoryAccount(t, s)
			ctx := context.Background()

			record := historyTrade("manual-distinct", "-1", "2026-09-02T10:00:00Z")
			record.Activity.Status = activity.StatusNeedsReview
			record.Activity.Warnings = []string{"ambiguous_duplicate"}
			record.Activity.CostAdjustments = []activity.CostAdjustment{{
				ExternalInstrumentID: "265598",
				Currency:             "USD",
				BasisDelta:           "25.00",
				AllocationMethod:     "broker_reported",
				Evidence:             "ibkr.flex.cost_basis",
			}}
			runHistoryRecords(t, s, record)
			issue := resolutionIssueByType(t, s, activity.StatusNeedsReview, "")
			if _, err := s.execContext(ctx, `UPDATE activity_reconciliation_issues SET details=? WHERE id=?`, `{"warnings":["ambiguous_duplicate","unknown_currency"]}`, issue.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.ResolveHistoryIssue(ctx, issue.ID, "confirm_distinct", "", 0); err == nil || !strings.Contains(err.Error(), "resolution_requires_evidence") {
				t.Fatalf("unsupported evidence accepted: %v", err)
			}
			if _, err := s.execContext(ctx, `UPDATE activity_reconciliation_issues SET details=? WHERE id=?`, `{"warnings":["ambiguous_duplicate"]}`, issue.ID); err != nil {
				t.Fatal(err)
			}

			if err := s.ResolveHistoryIssue(ctx, issue.ID, "confirm_distinct", "", 0); err != nil {
				t.Fatal(err)
			}
			resolved, err := s.GetActivity(ctx, issue.ActivityID)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Status != activity.StatusEffective || resolved.Revision != 2 || resolved.Fill == nil || len(resolved.CostAdjustments) != 1 {
				t.Fatalf("unexpected resolved activity %#v", resolved)
			}
			if resolved.CostAdjustments[0].InstrumentID == 0 || resolved.CostAdjustments[0].InstrumentID != resolved.Legs[0].InstrumentID {
				t.Fatalf("cost adjustment identity not resolved %#v", resolved)
			}
			assertResolutionTypedRows(t, s, issue.ActivityID, 1, 1)

			replay := historyTrade("manual-distinct", "-9", "2026-09-03T10:00:00Z")
			runHistoryRecords(t, s, replay)
			after, err := s.GetActivity(ctx, issue.ActivityID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Revision != 2 || after.Status != activity.StatusEffective || after.CashEffectsByCurrency["USD"] != "-2001" {
				t.Fatalf("replay overturned manual distinct decision %#v", after)
			}
		})
	}
}

func TestResolveHistoryIssueConfirmSameRequiresExactDateAndInstrument(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			s := historyTestStore(t, driver)
			_, _ = seedHistoryAccount(t, s)
			ctx := context.Background()

			exact := historyTrade("exact-target", "-1", "2026-09-02T10:00:00Z")
			wrongDate := historyTrade("date-target", "-1", "2026-09-02T10:00:00Z")
			wrongDate.Activity.TradeDate = "2026-09-02"
			for i := range wrongDate.Activity.Legs {
				wrongDate.Activity.Legs[i].EffectiveDate = "2026-09-02"
			}
			wrongAsset := historyTrade("asset-target", "-1", "2026-09-02T10:00:00Z")
			wrongAsset.Activity.Description = "Synthetic MSFT buy"
			wrongAsset.Activity.Legs[0].Symbol = "MSFT"
			wrongAsset.Activity.Legs[0].ExternalInstrumentID = "272093"
			runHistoryRecords(t, s, exact, wrongDate, wrongAsset)

			candidate := historyTrade("candidate-source", "-1", "2026-09-02T10:00:00Z")
			candidate.Activity.Status = activity.StatusNeedsReview
			candidate.Activity.Warnings = []string{"ambiguous_duplicate"}
			runHistoryRecords(t, s, candidate)
			issue := resolutionIssueByType(t, s, activity.StatusNeedsReview, "candidate-source")

			exactID := resolutionActivityByExecution(t, s, "exact-target")
			wrongDateID := resolutionActivityByExecution(t, s, "date-target")
			wrongAssetID := resolutionActivityByExecution(t, s, "asset-target")
			if err := s.ResolveHistoryIssue(ctx, issue.ID, "confirm_same", wrongDateID, 0); err == nil || !strings.Contains(err.Error(), "resolution_requires_evidence") {
				t.Fatalf("different date accepted: %v", err)
			}
			if err := s.ResolveHistoryIssue(ctx, issue.ID, "confirm_same", wrongAssetID, 0); err == nil || !strings.Contains(err.Error(), "duplicate_effect_mismatch") {
				t.Fatalf("different instrument accepted: %v", err)
			}
			if err := s.ResolveHistoryIssue(ctx, issue.ID, "confirm_same", exactID, 0); err != nil {
				t.Fatal(err)
			}
			resolved, err := s.GetActivity(ctx, issue.ActivityID)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Status != activity.StatusVoided || resolved.Revision != 2 {
				t.Fatalf("candidate not voided %#v", resolved)
			}
			assertResolutionTypedRows(t, s, issue.ActivityID, 1, 0)

			var identityActivity string
			if err = s.queryRowContext(ctx, `
				SELECT activity_id FROM broker_activity_identities
				WHERE account_id=? AND namespace='ibkr.execution' AND kind='execution' AND external_id=?`,
				issue.AccountID, "candidate-source").Scan(&identityActivity); err != nil {
				t.Fatal(err)
			}
			if identityActivity != issue.ActivityID {
				t.Fatalf("manual duplicate identity moved to replaceable target: %s", identityActivity)
			}

			replay := historyTrade("candidate-source", "-8", "2026-09-04T10:00:00Z")
			runHistoryRecords(t, s, replay)
			after, err := s.GetActivity(ctx, issue.ActivityID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Status != activity.StatusVoided || after.Revision != 2 || after.CashEffectsByCurrency["USD"] != "-2001" {
				t.Fatalf("replay revived confirmed duplicate %#v", after)
			}
			targetActivity, err := s.GetActivity(ctx, exactID)
			if err != nil || targetActivity.CashEffectsByCurrency["USD"] != "-2001" {
				t.Fatalf("replay replaced duplicate target %#v %v", targetActivity, err)
			}
		})
	}
}

func TestResolveHistoryIssueAcceptRevisionUsesRawEvidenceAndRejectsStaleIssue(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			s := historyTestStore(t, driver)
			_, _ = seedHistoryAccount(t, s)
			ctx := context.Background()

			base := historyTrade("unordered", "-1", "")
			runHistoryRecords(t, s, base)
			firstProposal := historyTrade("unordered", "-2", "")
			secondProposal := historyTrade("unordered", "-3", "")
			runHistoryRecords(t, s, firstProposal)
			runHistoryRecords(t, s, secondProposal)

			issues, err := s.ListHistoryIssues(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var acceptedIssue, staleIssue HistoryIssue
			for _, issue := range issues {
				if issue.Type != "source_revision_conflict" {
					continue
				}
				var details revisionConflictIssueDetails
				if err = json.Unmarshal(issue.Details, &details); err != nil {
					t.Fatal(err)
				}
				switch details.ProposedActivity.CashEffectsByCurrency["USD"] {
				case "-2002":
					acceptedIssue = issue
				case "-2003":
					staleIssue = issue
				}
			}
			if acceptedIssue.ID == "" || staleIssue.ID == "" {
				t.Fatalf("missing conflict issues %#v", issues)
			}
			if err = s.ResolveHistoryIssue(ctx, acceptedIssue.ID, "accept_revision", "", 0); err != nil {
				t.Fatal(err)
			}
			accepted, err := s.GetActivity(ctx, acceptedIssue.ActivityID)
			if err != nil {
				t.Fatal(err)
			}
			if accepted.Revision != 2 || accepted.CashEffectsByCurrency["USD"] != "-2002" {
				t.Fatalf("raw proposal was not accepted exactly %#v", accepted)
			}
			assertResolutionTypedRows(t, s, acceptedIssue.ActivityID, 1, 0)
			if err = s.ResolveHistoryIssue(ctx, staleIssue.ID, "accept_revision", "", 0); err == nil || !strings.Contains(err.Error(), "issue_revision_stale") {
				t.Fatalf("stale conflict issue accepted: %v", err)
			}
		})
	}
}

func resolutionIssueByType(t *testing.T, s *Store, issueType, sourceKey string) HistoryIssue {
	t.Helper()
	issues, err := s.ListHistoryIssues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.Type != issueType {
			continue
		}
		if sourceKey == "" {
			return issue
		}
		var key string
		if err = s.queryRowContext(context.Background(), `SELECT source_key FROM broker_raw_records WHERE id=?`, issue.RawRecordID).Scan(&key); err != nil {
			t.Fatal(err)
		}
		if key == sourceKey {
			return issue
		}
	}
	t.Fatalf("issue type %q source %q not found", issueType, sourceKey)
	return HistoryIssue{}
}

func resolutionActivityByExecution(t *testing.T, s *Store, executionID string) string {
	t.Helper()
	page, err := s.ListActivities(context.Background(), HistoryQuery{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.Fill != nil && item.Fill.ExecutionID == executionID {
			return item.ID
		}
	}
	t.Fatalf("activity for execution %q not found", executionID)
	return ""
}

func assertResolutionTypedRows(t *testing.T, s *Store, activityID string, wantFills, wantCosts int) {
	t.Helper()
	ctx := context.Background()
	var revisionID string
	if err := s.queryRowContext(ctx, `
		SELECT v.id FROM account_activities a
		JOIN activity_revisions v ON v.activity_id=a.id AND v.revision_no=a.current_revision
		WHERE a.id=?`, activityID).Scan(&revisionID); err != nil {
		t.Fatal(err)
	}
	var fills, costs int
	if err := s.queryRowContext(ctx, `SELECT COUNT(*) FROM broker_fills WHERE revision_id=?`, revisionID).Scan(&fills); err != nil {
		t.Fatal(err)
	}
	if err := s.queryRowContext(ctx, `SELECT COUNT(*) FROM activity_cost_adjustments WHERE revision_id=?`, revisionID).Scan(&costs); err != nil {
		t.Fatal(err)
	}
	if fills != wantFills || costs != wantCosts {
		t.Fatalf("typed rows fills=%d costs=%d, want fills=%d costs=%d", fills, costs, wantFills, wantCosts)
	}
	if wantFills > 0 {
		var executionID, quantity, payload string
		if err := s.queryRowContext(ctx, `SELECT execution_id,CAST(quantity AS TEXT),payload FROM broker_fills WHERE revision_id=?`, revisionID).Scan(&executionID, &quantity, &payload); err != nil {
			t.Fatal(err)
		}
		var fill activity.Fill
		if err := json.Unmarshal([]byte(payload), &fill); err != nil || fill.ExecutionID != executionID || !sameActivityDecimal(fill.Quantity, quantity) {
			t.Fatalf("invalid typed fill payload %q: %v", payload, err)
		}
	}
	if wantCosts > 0 {
		var instrumentID int64
		var currency, basisDelta, allocationMethod, evidence string
		if err := s.queryRowContext(ctx, `
			SELECT instrument_id,currency,CAST(basis_delta AS TEXT),allocation_method,evidence
			FROM activity_cost_adjustments WHERE revision_id=?`, revisionID).Scan(
			&instrumentID, &currency, &basisDelta, &allocationMethod, &evidence,
		); err != nil {
			t.Fatal(err)
		}
		resolved, err := s.GetActivity(ctx, activityID)
		if err != nil {
			t.Fatal(err)
		}
		cost := resolved.CostAdjustments[0]
		if instrumentID != cost.InstrumentID || currency != cost.Currency || !sameActivityDecimal(basisDelta, cost.BasisDelta) || allocationMethod != cost.AllocationMethod || evidence != cost.Evidence {
			t.Fatalf("typed cost row does not match revision payload: %#v", cost)
		}
	}
}
