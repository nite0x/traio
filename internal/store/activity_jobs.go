package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/nite/traio/internal/activity"
)

type HistoryRequest struct {
	UseQueryPeriod bool    `json:"use_query_period,omitempty"`
	AccountIDs     []int64 `json:"account_ids,omitempty"`
	ConnectionID   int64   `json:"connection_id,omitempty"`
	Source         string  `json:"source"`
	From           string  `json:"from,omitempty"`
	To             string  `json:"to,omitempty"`
	ImportID       string  `json:"import_id,omitempty"`
}
type HistoryJob struct {
	ID              string            `json:"id"`
	Status          string            `json:"status"`
	Phase           string            `json:"phase"`
	LeaseOwner      string            `json:"-"`
	LeaseGeneration int64             `json:"-"`
	LeaseUntil      string            `json:"-"`
	Attempt         int               `json:"attempt"`
	Request         HistoryRequest    `json:"request"`
	Counts          HistoryCounts     `json:"counts"`
	Coverage        []HistoryCoverage `json:"-"`
	Error           string            `json:"error,omitempty"`
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
}
type HistoryImport struct {
	ID          string                            `json:"id"`
	Status      string                            `json:"status"`
	RecordCount int                               `json:"record_count"`
	Accounts    []string                          `json:"accounts"`
	Counts      HistoryCounts                     `json:"counts"`
	Warnings    []string                          `json:"warnings"`
	From        string                            `json:"from"`
	To          string                            `json:"to"`
	Records     []activity.RawRecord              `json:"-"`
	Coverage    []HistoryCoverage                 `json:"-"`
	Snapshots   []activity.ReconciliationSnapshot `json:"-"`
	CreatedBy   int64                             `json:"-"`
	ExpiresAt   string                            `json:"expires_at"`
}

func (s *Store) CreateHistoryJob(ctx context.Context, r HistoryRequest) (HistoryJob, error) {
	if err := ValidateHistoryDates(r.From, r.To); err != nil {
		return HistoryJob{}, err
	}
	if r.Source != "gateway" && r.Source != "flex" && r.Source != "xml" {
		return HistoryJob{}, fmt.Errorf("invalid_history_source")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HistoryJob{}, err
	}
	defer tx.Rollback()
	if s.dialect == dialectPostgres {
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(78134122)`); err != nil {
			return HistoryJob{}, err
		}
	}
	j, err := s.createHistoryJobTx(ctx, tx, r)
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}
func (s *Store) createHistoryJobTx(ctx context.Context, tx *sql.Tx, r HistoryRequest) (HistoryJob, error) {
	// Identical active work is coalesced. Completed imports have an additional
	// permanent identity on the import row, handled by CommitHistoryImport.
	ids := append([]int64(nil), r.AccountIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	r.AccountIDs = nil
	for _, id := range ids {
		if id <= 0 {
			return HistoryJob{}, fmt.Errorf("invalid_account_ids")
		}
		if len(r.AccountIDs) == 0 || r.AccountIDs[len(r.AccountIDs)-1] != id {
			r.AccountIDs = append(r.AccountIDs, id)
		}
	}
	request := historyJSON(r)
	var existing string
	err := tx.QueryRowContext(ctx, s.bind(`SELECT id FROM broker_history_jobs WHERE request=? AND status IN ('queued','running','retry_wait') ORDER BY created_at LIMIT 1`), request).Scan(&existing)
	if err == nil {
		return scanHistoryJob(tx.QueryRowContext(ctx, s.bind(historyJobSelect+` WHERE id=?`), existing))
	}
	if err != sql.ErrNoRows {
		return HistoryJob{}, err
	}
	j := HistoryJob{ID: uuid.NewString(), Status: "queued", Phase: "fetch", Request: r, CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339()}
	_, err = s.txExecContext(ctx, tx, `INSERT INTO broker_history_jobs(id,connection_id,source,request,status,phase,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, j.ID, historyInt(r.ConnectionID), r.Source, request, j.Status, j.Phase, j.CreatedAt, j.UpdatedAt)
	return j, err
}

const historyJobSelect = `SELECT id,status,phase,lease_owner,lease_generation,lease_until,attempt,request,counts,evidence,error,created_at,updated_at FROM broker_history_jobs`

type historyScanner interface{ Scan(...any) error }

func scanHistoryJob(row historyScanner) (HistoryJob, error) {
	var j HistoryJob
	var req, counts, evidence string
	err := row.Scan(&j.ID, &j.Status, &j.Phase, &j.LeaseOwner, &j.LeaseGeneration, &j.LeaseUntil, &j.Attempt, &req, &counts, &evidence, &j.Error, &j.CreatedAt, &j.UpdatedAt)
	if err == sql.ErrNoRows {
		return j, ErrNotFound
	}
	if err != nil {
		return j, err
	}
	if err = json.Unmarshal([]byte(req), &j.Request); err != nil {
		return j, err
	}
	if err = json.Unmarshal([]byte(counts), &j.Counts); err != nil {
		return j, err
	}
	err = json.Unmarshal([]byte(evidence), &j.Coverage)
	return j, err
}
func (s *Store) GetHistoryJob(ctx context.Context, id string) (HistoryJob, error) {
	return scanHistoryJob(s.queryRowContext(ctx, historyJobSelect+` WHERE id=?`, id))
}
func (s *Store) ClaimHistoryJob(ctx context.Context, owner string) (HistoryJob, error) {
	if owner == "" {
		return HistoryJob{}, fmt.Errorf("worker_owner_required")
	}
	now := nowRFC3339()
	// Preview-only business evidence expires after 24 hours. Keep the summary
	// and expired status for the UI, while committed evidence remains durable.
	if _, err := s.execContext(ctx, `UPDATE broker_history_imports SET records='[]',evidence='[]',snapshots='[]',status='expired' WHERE status IN ('preview','needs_action') AND expires_at<?`, now); err != nil {
		return HistoryJob{}, err
	}
	until := time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339Nano)
	// A single atomic UPDATE works in both databases. PostgreSQL additionally
	// skips rows leased by another worker. One live job per connection/source.
	pick := `SELECT id FROM broker_history_jobs j WHERE ((j.status IN ('queued','retry_wait') AND j.next_attempt_at<=?) OR (j.status='running' AND j.lease_until<?)) AND NOT EXISTS (SELECT 1 FROM broker_history_jobs busy WHERE busy.id<>j.id AND busy.status='running' AND busy.lease_until>? AND busy.source=j.source AND (busy.connection_id=j.connection_id OR (busy.connection_id IS NULL AND j.connection_id IS NULL))) ORDER BY j.created_at LIMIT 1`
	if s.dialect == dialectPostgres {
		pick += ` FOR UPDATE SKIP LOCKED`
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HistoryJob{}, err
	}
	defer tx.Rollback()
	// Serialize the short claim decision across processes, including distinct
	// queued jobs sharing a source/connection. Row locks alone do not protect
	// the NOT EXISTS scope predicate against another concurrent claim.
	if s.dialect == dialectPostgres {
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(78134123)`); err != nil {
			return HistoryJob{}, err
		}
	}
	row := tx.QueryRowContext(ctx, s.bind(`UPDATE broker_history_jobs SET status='running',lease_owner=?,lease_generation=lease_generation+1,lease_until=?,attempt=attempt+1,updated_at=? WHERE id=(`+pick+`) RETURNING id,status,phase,lease_owner,lease_generation,lease_until,attempt,request,counts,evidence,error,created_at,updated_at`), owner, until, now, now, now, now)
	j, err := scanHistoryJob(row)
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}
func (s *Store) RenewHistoryJob(ctx context.Context, j HistoryJob) error {
	res, err := s.execContext(ctx, `UPDATE broker_history_jobs SET lease_until=?,updated_at=? WHERE id=? AND status='running' AND lease_owner=? AND lease_generation=? AND lease_until>?`, time.Now().UTC().Add(2*time.Minute).Format(time.RFC3339Nano), nowRFC3339(), j.ID, j.LeaseOwner, j.LeaseGeneration, nowRFC3339())
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrHistoryLeaseLost
	}
	return nil
}
func (s *Store) FinishHistoryJob(ctx context.Context, j HistoryJob, status string, counts HistoryCounts, message string) error {
	switch status {
	case "succeeded", "partial", "retry_wait", "needs_action", "failed", "cancelled":
	default:
		return fmt.Errorf("invalid_job_status")
	}
	next := ""
	if status == "retry_wait" {
		delay := time.Duration(j.Attempt*j.Attempt) * time.Minute
		if delay > time.Hour {
			delay = time.Hour
		}
		next = time.Now().UTC().Add(delay).Format(time.RFC3339Nano)
	}
	res, err := s.execContext(ctx, `UPDATE broker_history_jobs SET status=?,counts=?,error=?,next_attempt_at=?,lease_until='',updated_at=? WHERE id=? AND status='running' AND lease_owner=? AND lease_generation=? AND lease_until>?`, status, historyJSON(counts), message, next, nowRFC3339(), j.ID, j.LeaseOwner, j.LeaseGeneration, nowRFC3339())
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrHistoryLeaseLost
	}
	return nil
}
func (s *Store) SaveHistoryCoverage(ctx context.Context, j HistoryJob, c HistoryCounts) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.checkHistoryLease(ctx, tx, j); err != nil {
		return err
	}
	ids := append([]int64{}, j.Request.AccountIDs...)
	rows, err := s.txQueryContext(ctx, tx, `SELECT DISTINCT r.account_id FROM broker_raw_records r JOIN broker_raw_observations o ON o.raw_record_id=r.id WHERE o.job_id=?`, j.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !containsHistoryID(ids, id) {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	norm := "complete"
	if c.Unsupported > 0 || c.Conflicts > 0 {
		norm = "partial"
	}
	kind := "activities"
	if j.Request.Source == "gateway" {
		kind = "trade"
	}
	proofs := append([]HistoryCoverage{}, j.Coverage...)
	if len(proofs) == 0 {
		for _, id := range ids {
			proofs = append(proofs, HistoryCoverage{AccountID: id, Source: j.Request.Source, DataType: kind, ScopeKey: "account", From: j.Request.From, To: j.Request.To, FetchStatus: "observed"})
		}
	}
	// Retry after a crash replaces only this job's coverage, never other ranges.
	if _, err = s.txExecContext(ctx, tx, `DELETE FROM broker_history_coverage WHERE job_id=?`, j.ID); err != nil {
		return err
	}
	for _, proof := range proofs {
		if err = ValidateHistoryDates(proof.From, proof.To); err != nil {
			return err
		}
		if len(j.Request.AccountIDs) > 0 && !containsHistoryID(j.Request.AccountIDs, proof.AccountID) {
			return ErrForbidden
		}
		if proof.FetchStatus != "complete" {
			proof.FetchStatus = "observed"
		}
		switch proof.ReconcileStatus {
		case "passed", "mismatch", "incomplete", "unknown":
		default:
			proof.ReconcileStatus = "unknown"
		}
		if _, err = s.txExecContext(ctx, tx, `INSERT INTO broker_history_coverage(id,account_id,source,data_type,scope_key,from_date,to_date,fetch_status,normalize_status,reconcile_status,job_id,last_checked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), proof.AccountID, proof.Source, proof.DataType, proof.ScopeKey, proof.From, proof.To, proof.FetchStatus, norm, proof.ReconcileStatus, j.ID, nowRFC3339()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) CreateHistoryImport(ctx context.Context, records []activity.RawRecord, hash string, userID int64) (HistoryImport, error) {
	out := HistoryImport{ID: uuid.NewString(), Status: "preview", RecordCount: len(records), Accounts: []string{}, Warnings: []string{}, Records: records, CreatedBy: userID, ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)}
	known, err := s.ListHistoryAccounts(ctx)
	if err != nil {
		return out, err
	}
	accounts := map[string]bool{}
	for _, a := range known {
		if a.Provider == "IBKR" {
			accounts[a.ProviderAccountID] = true
		}
	}
	seen := map[string]bool{}
	out.Counts.Records = len(records)
	for _, r := range records {
		if !seen[r.ProviderAccountID] {
			seen[r.ProviderAccountID] = true
			out.Accounts = append(out.Accounts, r.ProviderAccountID)
			if !accounts[r.ProviderAccountID] {
				out.Warnings = append(out.Warnings, "unknown_account:"+r.ProviderAccountID)
				out.Status = "needs_action"
			}
		}
		date := r.Activity.TradeDate
		if date != "" && (out.From == "" || date < out.From) {
			out.From = date
		}
		if date > out.To {
			out.To = date
		}
		if r.Activity.Status == activity.StatusUnsupported {
			out.Counts.Unsupported++
		}
		if r.Activity.Status == activity.StatusNeedsReview {
			out.Counts.Conflicts++
		}
		var n int
		err = s.queryRowContext(ctx, `SELECT COUNT(*) FROM broker_raw_records r JOIN broker_accounts a ON a.id=r.account_id WHERE a.provider_code='IBKR' AND a.provider_account_id=? AND r.namespace=? AND r.record_type=? AND r.source_key=? AND r.payload_hash=?`, r.ProviderAccountID, r.Namespace, r.RecordType, r.Key, historyHash(string(r.Payload))).Scan(&n)
		if err != nil {
			return out, err
		}
		if n > 0 {
			out.Counts.Duplicates++
		}
	}
	sort.Strings(out.Accounts)
	if out.Counts.Unsupported > 0 {
		out.Warnings = append(out.Warnings, "unsupported_records")
	}
	if out.Counts.Conflicts > 0 {
		out.Warnings = append(out.Warnings, "records_need_review")
	}
	_, err = s.execContext(ctx, `INSERT INTO broker_history_imports(id,provider_code,created_by,file_hash,status,records,preview,created_at,expires_at) VALUES(?,'IBKR',?,?,?,?,?,?,?)`, out.ID, userID, hash, out.Status, historyJSON(records), historyJSON(out), nowRFC3339(), out.ExpiresAt)
	return out, err
}
func (s *Store) GetHistoryImport(ctx context.Context, id string) (HistoryImport, error) {
	var out HistoryImport
	var preview, records, evidence, snapshots, status string
	var user int64
	err := s.queryRowContext(ctx, `SELECT preview,records,evidence,snapshots,status,created_by FROM broker_history_imports WHERE id=?`, id).Scan(&preview, &records, &evidence, &snapshots, &status, &user)
	if err == sql.ErrNoRows {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(preview), &out); err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(records), &out.Records); err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(evidence), &out.Coverage); err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(snapshots), &out.Snapshots); err != nil {
		return out, err
	}
	out.Status = status
	out.CreatedBy = user
	return out, nil
}

func (s *Store) SaveHistoryImportEvidence(ctx context.Context, id string, coverage []HistoryCoverage, snapshots []activity.ReconciliationSnapshot) error {
	res, err := s.execContext(ctx, `UPDATE broker_history_imports SET evidence=?,snapshots=? WHERE id=? AND status='preview'`, historyJSON(coverage), historyJSON(snapshots), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) CommitHistoryImport(ctx context.Context, id string) (HistoryJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HistoryJob{}, err
	}
	defer tx.Rollback()
	// Lock even an existing preview before checking job_id. Concurrent commits
	// return the same durable job, including after the job completes.
	if _, err = s.txExecContext(ctx, tx, `UPDATE broker_history_imports SET status=status WHERE id=?`, id); err != nil {
		return HistoryJob{}, err
	}
	var jobID sql.NullString
	var preview, status, expires string
	err = tx.QueryRowContext(ctx, s.bind(`SELECT job_id,preview,status,expires_at FROM broker_history_imports WHERE id=?`), id).Scan(&jobID, &preview, &status, &expires)
	if err == sql.ErrNoRows {
		return HistoryJob{}, ErrNotFound
	}
	if err != nil {
		return HistoryJob{}, err
	}
	if jobID.Valid {
		j, e := scanHistoryJob(tx.QueryRowContext(ctx, s.bind(historyJobSelect+` WHERE id=?`), jobID.String))
		if e != nil {
			return j, e
		}
		return j, tx.Commit()
	}
	if expires < nowRFC3339() {
		return HistoryJob{}, fmt.Errorf("import_expired")
	}
	if status != "preview" {
		return HistoryJob{}, fmt.Errorf("import_needs_action")
	}
	var p HistoryImport
	if err = json.Unmarshal([]byte(preview), &p); err != nil {
		return HistoryJob{}, err
	}
	j, err := s.createHistoryJobTx(ctx, tx, HistoryRequest{Source: "xml", ImportID: id, From: p.From, To: p.To})
	if err != nil {
		return j, err
	}
	if _, err = s.txExecContext(ctx, tx, `UPDATE broker_history_imports SET status='committed',job_id=? WHERE id=?`, j.ID, id); err != nil {
		return j, err
	}
	return j, tx.Commit()
}
func (s *Store) HistoryJobRecords(ctx context.Context, importID string) ([]activity.RawRecord, error) {
	p, err := s.GetHistoryImport(ctx, importID)
	return p.Records, err
}

// RetryHistoryConnectionFetches wakes failed fetches after the user saves a
// corrected configuration. Never interrupt a running lease or replay writes.
func (s *Store) RetryHistoryConnectionFetches(ctx context.Context, connectionID int64) error {
	_, err := s.execContext(ctx, `UPDATE broker_history_jobs SET status='queued',attempt=0,error='',next_attempt_at='',updated_at=?
 WHERE connection_id=? AND source='flex' AND phase='fetch' AND status IN ('retry_wait','needs_action')`, nowRFC3339(), connectionID)
	return err
}
