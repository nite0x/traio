package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/nite/traio/internal/activity"
)

func (s *Store) SaveHistorySnapshots(ctx context.Context, job HistoryJob, snapshots []activity.ReconciliationSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.checkHistoryLease(ctx, tx, job); err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		if err := validateReconciliationSnapshot(&snapshot); err != nil {
			return err
		}
		var accountID int64
		err = tx.QueryRowContext(ctx, s.bind(`SELECT id FROM broker_accounts WHERE provider_code='IBKR' AND provider_account_id=?`), snapshot.ProviderAccountID).Scan(&accountID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if len(job.Request.AccountIDs) > 0 && !containsHistoryID(job.Request.AccountIDs, accountID) {
			return ErrForbidden
		}
		if job.Request.ConnectionID > 0 {
			var count int
			if err := tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM broker_account_connections WHERE account_id=? AND connection_id=?`), accountID, job.Request.ConnectionID).Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				return ErrForbidden
			}
		}
		if _, err := s.txExecContext(ctx, tx, `UPDATE broker_accounts SET activity_generation=activity_generation WHERE id=?`, accountID); err != nil {
			return err
		}
		var existing string
		err = tx.QueryRowContext(ctx, s.bind(`SELECT id FROM account_reconciliation_snapshots WHERE account_id=? AND as_of_date=? AND basis=? AND source_ref=? ORDER BY created_at DESC LIMIT 1`), accountID, snapshot.AsOfDate, snapshot.Basis, snapshot.SourceRef).Scan(&existing)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		snapshotID := uuid.NewString()
		if _, err := s.txExecContext(ctx, tx, `INSERT INTO account_reconciliation_snapshots(id,account_id,as_of_date,basis,source_ref,completeness,created_at) VALUES(?,?,?,?,?,?,?)`, snapshotID, accountID, snapshot.AsOfDate, snapshot.Basis, snapshot.SourceRef, snapshot.Completeness, nowRFC3339()); err != nil {
			return err
		}
		for _, balance := range snapshot.Balances {
			var instrumentID any
			if balance.Kind == "position" {
				instrument, err := s.resolveInstrumentTx(ctx, tx, InstrumentIdentity{PreserveBrokerIdentity: true, ProviderCode: "IBKR", ExternalID: balance.ExternalInstrumentID, AssetType: balance.AssetType, Symbol: balance.Symbol, Currency: balance.Currency})
				if err != nil {
					return err
				}
				instrumentID = instrument.ID
			} else if balance.Kind != "cash" {
				return fmt.Errorf("invalid_reconciliation_balance")
			}
			amount, quantity := any(nil), any(nil)
			if balance.Kind == "cash" {
				amount = balance.Value
			} else {
				quantity = balance.Value
			}
			if _, err := s.txExecContext(ctx, tx, `INSERT INTO account_reconciliation_balances(id,snapshot_id,kind,currency,instrument_id,amount,quantity,reported_cost) VALUES(?,?,?,?,?,?,?,?)`, uuid.NewString(), snapshotID, balance.Kind, historyNullable(balance.Currency), instrumentID, amount, quantity, historyNullable(balance.ReportedCost)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func validateReconciliationSnapshot(snapshot *activity.ReconciliationSnapshot) error {
	if snapshot.ProviderAccountID == "" || snapshot.AsOfDate == "" || snapshot.SourceRef == "" {
		return fmt.Errorf("invalid_reconciliation_snapshot")
	}
	if err := ValidateHistoryDates(snapshot.AsOfDate, snapshot.AsOfDate); err != nil {
		return err
	}
	if snapshot.Basis == "" {
		snapshot.Basis = "trade_date"
	}
	if snapshot.Basis != "trade_date" && snapshot.Basis != "settled" {
		return fmt.Errorf("invalid_reconciliation_basis")
	}
	if snapshot.Completeness != "complete" && snapshot.Completeness != "partial" {
		return fmt.Errorf("invalid_reconciliation_completeness")
	}
	seen := map[string]bool{}
	for i := range snapshot.Balances {
		balance := &snapshot.Balances[i]
		value, err := activity.CanonicalDecimal(balance.Value)
		if err != nil {
			return fmt.Errorf("invalid_reconciliation_balance")
		}
		balance.Value = value
		if balance.ReportedCost != "" {
			balance.ReportedCost, err = activity.CanonicalDecimal(balance.ReportedCost)
			if err != nil {
				return fmt.Errorf("invalid_reconciliation_balance")
			}
		}
		key := ""
		switch balance.Kind {
		case "cash":
			if !activity.ValidCurrency(balance.Currency) || balance.InstrumentID != 0 || balance.ExternalInstrumentID != "" {
				return fmt.Errorf("invalid_reconciliation_balance")
			}
			key = "cash|" + balance.Currency
		case "position":
			if balance.InstrumentID != 0 || balance.ExternalInstrumentID == "" || balance.Symbol == "" || balance.AssetType == "" || (balance.Currency != "" && !activity.ValidCurrency(balance.Currency)) {
				return fmt.Errorf("invalid_reconciliation_balance")
			}
			key = "position|" + balance.ExternalInstrumentID
		default:
			return fmt.Errorf("invalid_reconciliation_balance")
		}
		if seen[key] {
			return fmt.Errorf("duplicate_reconciliation_balance")
		}
		seen[key] = true
	}
	return nil
}

type storedSnapshot struct {
	id, date, basis, completeness string
	balances                      map[string]activity.Balance
}

func (s *Store) ReconcileHistoryJob(ctx context.Context, job HistoryJob) (map[int64]string, error) {
	result := map[int64]string{}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err := s.checkHistoryLease(ctx, tx, job); err != nil {
		return result, err
	}
	accountIDs := append([]int64{}, job.Request.AccountIDs...)
	rows, err := s.txQueryContext(ctx, tx, `SELECT DISTINCT r.account_id FROM broker_raw_records r JOIN broker_raw_observations o ON o.raw_record_id=r.id WHERE o.job_id=?`, job.ID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var accountID int64
		if err := rows.Scan(&accountID); err != nil {
			rows.Close()
			return result, err
		}
		if !containsHistoryID(accountIDs, accountID) {
			accountIDs = append(accountIDs, accountID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	sort.Slice(accountIDs, func(i, k int) bool { return accountIDs[i] < accountIDs[k] })
	accountIDs = uniqueHistoryIDs(accountIDs)
	// Account rows serialize reconciliation with normalizers and issue
	// resolutions, so all snapshots, revisions and coverage below form one
	// consistent view in PostgreSQL as well as SQLite.
	for _, accountID := range accountIDs {
		if _, err := s.txExecContext(ctx, tx, `UPDATE broker_accounts SET activity_generation=activity_generation WHERE id=?`, accountID); err != nil {
			return result, err
		}
	}
	for _, accountID := range accountIDs {
		closing, err := s.historySnapshot(ctx, tx, accountID, job.Request.From, job.Request.To)
		if errors.Is(err, sql.ErrNoRows) {
			result[accountID] = "unknown"
			continue
		}
		if err != nil {
			return result, err
		}
		opening, err := s.historySnapshot(ctx, tx, accountID, "", previousDate(closing.date))
		if errors.Is(err, sql.ErrNoRows) {
			result[accountID] = "unknown"
			continue
		}
		if err != nil {
			return result, err
		}
		activities, err := s.reconciliationActivities(ctx, tx, accountID, opening.date, closing.date)
		if err != nil {
			return result, err
		}
		covered, err := s.reconciliationCoverageComplete(ctx, tx, job, accountID, nextDate(opening.date), closing.date)
		if err != nil {
			return result, err
		}
		currentRecordsComplete, err := s.reconciliationJobRecordsComplete(ctx, tx, job.ID, accountID)
		if err != nil {
			return result, err
		}
		ledgerComplete := true
		for _, item := range activities {
			if item.Status != activity.StatusVoided && (item.Status != activity.StatusEffective || item.BookingStatus != activity.BookingBooked) {
				ledgerComplete = false
				break
			}
		}
		preconditionsComplete := opening.completeness == "complete" && closing.completeness == "complete" && opening.basis == closing.basis && covered && currentRecordsComplete && ledgerComplete
		status := "incomplete"
		if preconditionsComplete {
			status = "passed"
		}
		keys := map[string]bool{}
		templates := map[string]activity.Balance{}
		for key := range opening.balances {
			keys[key] = true
			templates[key] = opening.balances[key]
		}
		for key := range closing.balances {
			keys[key] = true
			templates[key] = closing.balances[key]
		}
		for _, item := range activities {
			for _, leg := range item.Legs {
				var balance activity.Balance
				switch leg.Kind {
				case "cash":
					balance = activity.Balance{Kind: "cash", Currency: leg.Currency, Value: "0"}
				case "position":
					balance = activity.Balance{Kind: "position", InstrumentID: leg.InstrumentID, Value: "0"}
				default:
					continue
				}
				key := balanceKey(balance)
				keys[key] = true
				if _, ok := templates[key]; !ok {
					templates[key] = balance
				}
			}
		}
		for key := range keys {
			open, openOK := opening.balances[key]
			close, closeOK := closing.balances[key]
			if !closeOK {
				if !preconditionsComplete {
					continue
				}
				close = templates[key]
				close.Value = "0"
			}
			var openingPtr *activity.Balance
			if openOK {
				openingPtr = &open
			} else if preconditionsComplete {
				zero := templates[key]
				zero.Value = "0"
				openingPtr = &zero
			}
			difference, err := activity.Reconcile(openingPtr, close, activities)
			if err != nil {
				return result, err
			}
			if difference.Status == "mismatch" && preconditionsComplete {
				status = "mismatch"
				if err := s.saveReconciliationIssue(ctx, tx, accountID, opening, closing, difference); err != nil {
					return result, err
				}
			} else if preconditionsComplete {
				status = reconciliationWorse(status, difference.Status)
			}
		}
		result[accountID] = status
	}
	return result, tx.Commit()
}

func (s *Store) reconciliationJobRecordsComplete(ctx context.Context, tx *sql.Tx, jobID string, accountID int64) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM broker_raw_observations o JOIN broker_raw_records r ON r.id=o.raw_record_id WHERE o.job_id=? AND r.account_id=? AND (o.outcome='' OR o.outcome IN ('unsupported','needs_review'))`), jobID, accountID).Scan(&count)
	return count == 0, err
}

func (s *Store) reconciliationCoverageComplete(ctx context.Context, tx *sql.Tx, job HistoryJob, accountID int64, from, to string) (bool, error) {
	if from == "" || to == "" || from > to {
		return false, nil
	}
	type interval struct{ from, to time.Time }
	intervals := []interval{}
	rows, err := s.txQueryContext(ctx, tx, `SELECT from_date,to_date FROM broker_history_coverage WHERE account_id=? AND data_type='activities' AND scope_key='account' AND fetch_status='complete' AND normalize_status='complete'`, accountID)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var start, end string
		if err := rows.Scan(&start, &end); err != nil {
			rows.Close()
			return false, err
		}
		startDate, startErr := time.Parse("2006-01-02", start)
		endDate, endErr := time.Parse("2006-01-02", end)
		if startErr == nil && endErr == nil && !startDate.After(endDate) {
			intervals = append(intervals, interval{from: startDate, to: endDate})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()
	for _, proof := range job.Coverage {
		if proof.AccountID != accountID || proof.DataType != "activities" || proof.ScopeKey != "account" || proof.FetchStatus != "complete" {
			continue
		}
		startDate, startErr := time.Parse("2006-01-02", proof.From)
		endDate, endErr := time.Parse("2006-01-02", proof.To)
		if startErr == nil && endErr == nil && !startDate.After(endDate) {
			intervals = append(intervals, interval{from: startDate, to: endDate})
		}
	}
	wantFrom, err := time.Parse("2006-01-02", from)
	if err != nil {
		return false, nil
	}
	wantTo, err := time.Parse("2006-01-02", to)
	if err != nil {
		return false, nil
	}
	sort.Slice(intervals, func(i, k int) bool { return intervals[i].from.Before(intervals[k].from) })
	coveredUntil := wantFrom.AddDate(0, 0, -1)
	for _, candidate := range intervals {
		if candidate.to.Before(wantFrom) || candidate.from.After(wantTo) {
			continue
		}
		if candidate.from.After(coveredUntil.AddDate(0, 0, 1)) {
			return false, nil
		}
		if candidate.to.After(coveredUntil) {
			coveredUntil = candidate.to
		}
		if !coveredUntil.Before(wantTo) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) historySnapshot(ctx context.Context, tx *sql.Tx, accountID int64, from, to string) (storedSnapshot, error) {
	query := `SELECT id,as_of_date,basis,completeness FROM account_reconciliation_snapshots WHERE account_id=?`
	args := []any{accountID}
	if from != "" {
		query += ` AND as_of_date>=?`
		args = append(args, from)
	}
	if to != "" {
		query += ` AND as_of_date<=?`
		args = append(args, to)
	}
	query += ` ORDER BY as_of_date DESC,created_at DESC,id DESC LIMIT 1`
	var snapshot storedSnapshot
	if err := tx.QueryRowContext(ctx, s.bind(query), args...).Scan(&snapshot.id, &snapshot.date, &snapshot.basis, &snapshot.completeness); err != nil {
		return snapshot, err
	}
	snapshot.balances = map[string]activity.Balance{}
	rows, err := s.txQueryContext(ctx, tx, `SELECT kind,COALESCE(currency,''),COALESCE(instrument_id,0),COALESCE(CAST(amount AS TEXT),''),COALESCE(CAST(quantity AS TEXT),'') FROM account_reconciliation_balances WHERE snapshot_id=?`, snapshot.id)
	if err != nil {
		return snapshot, err
	}
	defer rows.Close()
	for rows.Next() {
		var balance activity.Balance
		var amount, quantity string
		if err := rows.Scan(&balance.Kind, &balance.Currency, &balance.InstrumentID, &amount, &quantity); err != nil {
			return snapshot, err
		}
		if balance.Kind == "cash" {
			balance.Value = amount
		} else {
			balance.Value = quantity
			balance.Currency = ""
		}
		snapshot.balances[balanceKey(balance)] = balance
	}
	return snapshot, rows.Err()
}

func (s *Store) reconciliationActivities(ctx context.Context, tx *sql.Tx, accountID int64, from, to string) ([]activity.Activity, error) {
	rows, err := s.txQueryContext(ctx, tx, `SELECT v.payload FROM account_activities a JOIN activity_revisions v ON v.activity_id=a.id AND v.revision_no=a.current_revision WHERE a.account_id=? AND v.sort_key>? AND v.sort_key<=? ORDER BY v.sort_key,a.id`, accountID, from+"~", to+"~")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []activity.Activity{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var item activity.Activity
		if err := json.Unmarshal([]byte(payload), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) saveReconciliationIssue(ctx context.Context, tx *sql.Tx, accountID int64, opening, closing storedSnapshot, difference activity.Difference) error {
	details := historyJSON(map[string]any{"opening_snapshot_id": opening.id, "closing_snapshot_id": closing.id, "balance": difference.Balance, "expected": difference.Expected, "actual": difference.Actual, "difference": difference.Difference})
	var count int
	if err := tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM activity_reconciliation_issues WHERE account_id=? AND issue_type='balance_mismatch' AND details=? AND status='open'`), accountID, details).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := s.txExecContext(ctx, tx, `INSERT INTO activity_reconciliation_issues(id,account_id,issue_type,status,details,created_at,updated_at) VALUES(?,?,'balance_mismatch','open',?,?,?)`, uuid.NewString(), accountID, details, nowRFC3339(), nowRFC3339())
	return err
}

func balanceKey(balance activity.Balance) string {
	if balance.Kind == "position" {
		return balance.Kind + "||" + strconv.FormatInt(balance.InstrumentID, 10)
	}
	return balance.Kind + "|" + balance.Currency + "|0"
}
func previousDate(value string) string {
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		return ""
	}
	return date.AddDate(0, 0, -1).Format("2006-01-02")
}
func nextDate(value string) string {
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		return ""
	}
	return date.AddDate(0, 0, 1).Format("2006-01-02")
}

func uniqueHistoryIDs(ids []int64) []int64 {
	out := ids[:0]
	for _, id := range ids {
		if len(out) == 0 || out[len(out)-1] != id {
			out = append(out, id)
		}
	}
	return out
}
func reconciliationWorse(current, candidate string) string {
	rank := map[string]int{"passed": 0, "unknown": 1, "incomplete": 2, "mismatch": 3}
	if rank[candidate] > rank[current] {
		return candidate
	}
	return current
}
