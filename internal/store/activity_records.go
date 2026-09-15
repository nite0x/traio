package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nite/traio/internal/activity"
)

var ErrHistoryCursorStale = errors.New("history_cursor_stale")
var ErrHistoryLeaseLost = errors.New("history_lease_lost")

type HistoryAccount struct {
	ID                int64  `json:"id"`
	Provider          string `json:"provider"`
	ProviderAccountID string `json:"provider_account_id"`
	Name              string `json:"name"`
	ArchivedAt        string `json:"archived_at,omitempty"`
}
type HistoryQuery struct {
	Symbol                                                            string
	AccountIDs                                                        []int64
	Provider, InstrumentID, Types, From, To, Status, Currency, Cursor string
	Limit                                                             int
}
type HistoryPage struct {
	Items      []activity.Activity `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
	Coverage   []HistoryCoverage   `json:"coverage"`
	Warnings   []string            `json:"warnings"`
	AsOf       string              `json:"as_of"`
}
type HistoryCounts struct {
	Records     int `json:"records"`
	Added       int `json:"added"`
	Updated     int `json:"updated"`
	Duplicates  int `json:"duplicates"`
	Unsupported int `json:"unsupported"`
	Conflicts   int `json:"conflicts"`
}
type HistoryCoverage struct {
	ID              string `json:"id"`
	AccountID       int64  `json:"account_id"`
	Source          string `json:"source"`
	DataType        string `json:"data_type"`
	ScopeKey        string `json:"scope_key"`
	From            string `json:"from"`
	To              string `json:"to"`
	FetchStatus     string `json:"fetch_status"`
	NormalizeStatus string `json:"normalize_status"`
	ReconcileStatus string `json:"reconcile_status"`
	LastCheckedAt   string `json:"last_checked_at"`
}
type HistoryIssue struct {
	ID          string          `json:"id"`
	AccountID   int64           `json:"account_id"`
	ActivityID  string          `json:"activity_id,omitempty"`
	RawRecordID string          `json:"-"`
	Type        string          `json:"type"`
	Status      string          `json:"status"`
	Details     json.RawMessage `json:"details"`
	Resolution  string          `json:"resolution,omitempty"`
	CreatedAt   string          `json:"created_at"`
}

type ActivityRepository interface {
	ListHistoryAccounts(context.Context) ([]HistoryAccount, error)
	SaveHistoryRaw(context.Context, HistoryJob, []activity.RawRecord) error
	NormalizeHistoryJob(context.Context, HistoryJob) (HistoryCounts, error)
	ListActivities(context.Context, HistoryQuery) (HistoryPage, error)
	GetActivity(context.Context, string) (activity.Activity, error)
	ActivityRevisions(context.Context, string) ([]activity.Activity, error)
	ListHistoryCoverage(context.Context) ([]HistoryCoverage, error)
	ListHistoryIssues(context.Context) ([]HistoryIssue, error)
	ResolveHistoryIssue(context.Context, string, string, string, int64) error
	CreateHistoryJob(context.Context, HistoryRequest) (HistoryJob, error)
	GetHistoryJob(context.Context, string) (HistoryJob, error)
	ClaimHistoryJob(context.Context, string) (HistoryJob, error)
	FinishHistoryJob(context.Context, HistoryJob, string, HistoryCounts, string) error
	RenewHistoryJob(context.Context, HistoryJob) error
	SaveHistoryCoverage(context.Context, HistoryJob, HistoryCounts) error
	CreateHistoryImport(context.Context, []activity.RawRecord, string, int64) (HistoryImport, error)
	SaveHistoryImportEvidence(context.Context, string, []HistoryCoverage, []activity.ReconciliationSnapshot) error
	GetHistoryImport(context.Context, string) (HistoryImport, error)
	CommitHistoryImport(context.Context, string) (HistoryJob, error)
	HistoryJobRecords(context.Context, string) ([]activity.RawRecord, error)
	SaveHistorySnapshots(context.Context, HistoryJob, []activity.ReconciliationSnapshot) error
	ReconcileHistoryJob(context.Context, HistoryJob) (map[int64]string, error)
}

func (s *Store) ListHistoryAccounts(ctx context.Context) ([]HistoryAccount, error) {
	rows, err := s.queryContext(ctx, `SELECT id,provider_code,provider_account_id,display_name,archived_at FROM broker_accounts ORDER BY provider_code,provider_account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryAccount{}
	for rows.Next() {
		var a HistoryAccount
		if err = rows.Scan(&a.ID, &a.Provider, &a.ProviderAccountID, &a.Name, &a.ArchivedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func historyJSON(v any) string    { b, _ := json.Marshal(v); return string(b) }
func historyHash(v string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(v))) }
func historyNullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func historyInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func (s *Store) checkHistoryLease(ctx context.Context, tx *sql.Tx, j HistoryJob) error {
	res, err := s.txExecContext(ctx, tx, `UPDATE broker_history_jobs SET updated_at=? WHERE id=? AND status='running' AND lease_owner=? AND lease_generation=? AND lease_until>?`, nowRFC3339(), j.ID, j.LeaseOwner, j.LeaseGeneration, nowRFC3339())
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrHistoryLeaseLost
	}
	return nil
}

// SaveHistoryRaw commits evidence before normalization. A crash after this
// transaction resumes the normalize phase without losing or re-fetching a page.
func (s *Store) SaveHistoryRaw(ctx context.Context, j HistoryJob, records []activity.RawRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.checkHistoryLease(ctx, tx, j); err != nil {
		return err
	}
	for _, r := range records {
		var accountID int64
		err = tx.QueryRowContext(ctx, s.bind(`SELECT id FROM broker_accounts WHERE provider_code='IBKR' AND provider_account_id=?`), r.ProviderAccountID).Scan(&accountID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("history_account_not_found")
		}
		if err != nil {
			return err
		}
		if len(j.Request.AccountIDs) > 0 && !containsHistoryID(j.Request.AccountIDs, accountID) {
			return ErrForbidden
		}
		if j.Request.ConnectionID > 0 {
			var n int
			err = tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM broker_account_connections WHERE account_id=? AND connection_id=?`), accountID, j.Request.ConnectionID).Scan(&n)
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrForbidden
			}
		}
		if r.Key == "" || r.Namespace == "" {
			return fmt.Errorf("history_record_identity_required")
		}
		if !json.Valid(r.Payload) {
			return fmt.Errorf("history_invalid_payload")
		}
		h := historyHash(string(r.Payload))
		// A later report can supply lot cost evidence while the transfer row itself
		// remains identical. Keep that evidence as a new immutable observation.
		if len(r.Activity.CostAdjustments) > 0 {
			h = historyHash(string(r.Payload) + historyJSON(r.Activity.CostAdjustments))
		}
		id := uuid.NewString()
		_, err = s.txExecContext(ctx, tx, `INSERT INTO broker_raw_records(id,account_id,source,namespace,record_type,source_key,payload_hash,payload,normalized,source_updated_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account_id,namespace,record_type,source_key,payload_hash) DO NOTHING`, id, accountID, r.Source, r.Namespace, r.RecordType, r.Key, h, string(r.Payload), historyJSON(r), r.SourceUpdatedAt, nowRFC3339())
		if err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, s.bind(`SELECT id FROM broker_raw_records WHERE account_id=? AND namespace=? AND record_type=? AND source_key=? AND payload_hash=?`), accountID, r.Namespace, r.RecordType, r.Key, h).Scan(&id)
		if err != nil {
			return err
		}
		if _, err = s.txExecContext(ctx, tx, `INSERT INTO broker_raw_observations(raw_record_id,job_id,observed_at) VALUES(?,?,?) ON CONFLICT(raw_record_id,job_id) DO NOTHING`, id, j.ID, nowRFC3339()); err != nil {
			return err
		}
	}
	if _, err = s.txExecContext(ctx, tx, `UPDATE broker_history_jobs SET phase='normalize',evidence=?,request=? WHERE id=?`, historyJSON(j.Coverage), historyJSON(j.Request), j.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func containsHistoryID(ids []int64, n int64) bool {
	for _, id := range ids {
		if id == n {
			return true
		}
	}
	return false
}

func (s *Store) NormalizeHistoryJob(ctx context.Context, j HistoryJob) (HistoryCounts, error) {
	counts := HistoryCounts{}
	rows, err := s.queryContext(ctx, `SELECT r.id,r.account_id,r.normalized,r.normalize_status FROM broker_raw_records r JOIN broker_raw_observations o ON o.raw_record_id=r.id WHERE o.job_id=? ORDER BY r.source_updated_at,r.id`, j.ID)
	if err != nil {
		return counts, err
	}
	type row struct {
		id      string
		account int64
		r       activity.RawRecord
		status  string
	}
	all := []row{}
	for rows.Next() {
		var x row
		var b string
		if err = rows.Scan(&x.id, &x.account, &b, &x.status); err != nil {
			rows.Close()
			return counts, err
		}
		if err = json.Unmarshal([]byte(b), &x.r); err != nil {
			rows.Close()
			return counts, err
		}
		all = append(all, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return counts, err
	}
	for _, x := range all {
		counts.Records++

		result, err := s.normalizeHistoryRecord(ctx, j, x.id, x.account, x.r)
		if err != nil {
			return counts, err
		}
		switch result {
		case "added":
			counts.Added++
		case "updated":
			counts.Updated++
		case "duplicate":
			counts.Duplicates++
		case "unsupported":
			counts.Unsupported++
		case "needs_review":
			counts.Conflicts++
		}
	}
	return counts, nil
}

func (s *Store) normalizeHistoryRecord(ctx context.Context, j HistoryJob, rawID string, accountID int64, r activity.RawRecord) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err = s.checkHistoryLease(ctx, tx, j); err != nil {
		return "", err
	}
	// Account row serializes identity matching across distinct source jobs.
	if _, err = s.txExecContext(ctx, tx, `UPDATE broker_accounts SET activity_generation=activity_generation WHERE id=?`, accountID); err != nil {
		return "", err
	}
	var already string
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT normalize_status FROM broker_raw_records WHERE id=?`), rawID).Scan(&already); err != nil {
		return "", err
	}
	var outcome string
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT outcome FROM broker_raw_observations WHERE raw_record_id=? AND job_id=?`), rawID, j.ID).Scan(&outcome); err != nil {
		return "", err
	}
	if outcome != "" {
		return outcome, tx.Commit()
	}
	if already != "pending" {
		outcome = "duplicate"
		if already == "needs_review" || already == "unsupported" {
			outcome = already
		}
		if _, err = s.txExecContext(ctx, tx, `UPDATE broker_raw_observations SET outcome=? WHERE raw_record_id=? AND job_id=?`, outcome, rawID, j.ID); err != nil {
			return "", err
		}
		return outcome, tx.Commit()
	}
	a := r.Activity
	a.AccountID = accountID
	a.Provider = "IBKR"
	a.ProviderAccountID = r.ProviderAccountID
	for i := range a.Legs {
		l := &a.Legs[i]
		if l.Kind != "position" {
			continue
		}
		if l.Symbol == "" {
			a.Status = activity.StatusNeedsReview
			a.Warnings = append(a.Warnings, "instrument_identity_unresolved")
			continue
		}
		asset, err := s.resolveInstrumentTx(ctx, tx, InstrumentIdentity{PreserveBrokerIdentity: true, ProviderCode: "IBKR", ExternalID: l.ExternalInstrumentID, AssetType: l.AssetType, Symbol: l.Symbol, Currency: l.Currency})
		if err != nil {
			return "", err
		}
		l.InstrumentID = asset.ID
	}
	// Keep valid reported effects visible for review; unresolved securities stay
	// in raw evidence and cannot enter the typed ledger with a missing identity.
	resolved := a.Legs[:0]
	for _, l := range a.Legs {
		if l.Kind != "position" || l.InstrumentID > 0 {
			resolved = append(resolved, l)
		}
	}
	a.Legs = resolved
	for i := range a.CostAdjustments {
		c := &a.CostAdjustments[i]
		for _, l := range a.Legs {
			if l.Kind == "position" && l.ExternalInstrumentID == c.ExternalInstrumentID {
				c.InstrumentID = l.InstrumentID
				break
			}
		}
		if c.InstrumentID == 0 {
			a.Status = activity.StatusNeedsReview
			a.Warnings = append(a.Warnings, "cost_instrument_unresolved")
			a.CostAdjustments = nil
			break
		}
	}
	if err = activity.Validate(&a); err != nil {
		a.Status = activity.StatusNeedsReview
		a.Warnings = append(a.Warnings, "record_validation_failed")
		a.Legs = []activity.Leg{}
		a.CashEffectsByCurrency = map[string]string{}
		a.Fill = nil
		a.CostAdjustments = nil
		if a.Type == "" {
			a.Type = "unknown"
		}
		if a.BookingStatus != activity.BookingBooked && a.BookingStatus != activity.BookingProvisional {
			a.BookingStatus = activity.BookingProvisional
		}
		if activity.ValidateDate(a.TradeDate) != nil {
			a.TradeDate = ""
		}
		if activity.ValidateDate(a.SettlementDate) != nil {
			a.SettlementDate = ""
		}
		a.OccurredAt = ""
		a.TimePrecision = "unknown"
		if err = activity.Validate(&a); err != nil {
			return "", fmt.Errorf("history_quarantine_invalid: %w", err)
		}
	}
	identities := append([]activity.Identity{{Namespace: r.Namespace, Kind: r.RecordType, ExternalID: r.Key}}, r.Identities...)
	id := ""
	match := "source_identity"
	for _, key := range identities {
		if key.ExternalID == "" {
			continue
		}
		var found string
		e := tx.QueryRowContext(ctx, s.bind(`SELECT activity_id FROM broker_activity_identities WHERE account_id=? AND namespace=? AND kind=? AND external_id=?`), accountID, key.Namespace, key.Kind, key.ExternalID).Scan(&found)
		if e != nil && e != sql.ErrNoRows {
			return "", e
		}
		if e == nil {
			if id != "" && id != found {
				return "", fmt.Errorf("history_identity_conflict")
			}
			id = found
			match = "external_id"
		}
	}
	priority := 10
	if r.Source == "flex" || r.Source == "xml" || strings.Contains(r.Source, "flex") {
		priority = 30
	}
	revision := 0
	oldPriority := 0
	oldTime := ""
	oldID := ""
	var old activity.Activity
	if id != "" {
		var b string
		err = tx.QueryRowContext(ctx, s.bind(`SELECT v.id,v.revision_no,v.payload,v.source_priority,v.source_updated_at FROM account_activities a JOIN activity_revisions v ON v.activity_id=a.id AND v.revision_no=a.current_revision WHERE a.id=?`), id).Scan(&oldID, &revision, &b, &oldPriority, &oldTime)
		if err != nil {
			return "", err
		}
		if err = json.Unmarshal([]byte(b), &old); err != nil {
			return "", err
		}
	}
	if id == "" {
		id = uuid.NewString()
		if _, err = s.txExecContext(ctx, tx, `INSERT INTO account_activities(id,account_id,created_at) VALUES(?,?,?)`, id, accountID, nowRFC3339()); err != nil {
			return "", err
		}
	}
	a.ID = id
	a.Revision = revision + 1
	// A late lower-quality observation is evidence, not a new booking.
	newer := false
	if next, e := time.Parse(time.RFC3339Nano, r.SourceUpdatedAt); e == nil {
		if prev, e := time.Parse(time.RFC3339Nano, oldTime); e == nil {
			newer = next.After(prev)
		}
	}
	replace := revision == 0 || priority > oldPriority || priority == oldPriority && newer
	unorderedConflict := false
	if revision > 0 && (priority == oldPriority && !newer || oldPriority == acceptedRevisionSourcePriority && priority >= 30) {
		comparable := a
		comparable.Revision = old.Revision
		comparable.Sources = old.Sources
		unorderedConflict = historyJSON(comparable) != historyJSON(old) && (oldTime == "" || r.SourceUpdatedAt == "" || oldTime == r.SourceUpdatedAt || oldPriority == acceptedRevisionSourcePriority && newer)
	}
	// Explicit duplicate resolutions keep their original lineage non-replaceable.
	if revision > 0 && oldPriority != acceptedRevisionSourcePriority && priority >= oldPriority && transferCostOnlyEnrichment(old, a) {
		// Flex timestamps may omit a timezone. A strictly additive cost-only
		// update does not require ordering two competing financial movements.
		replace, unorderedConflict = true, false
	}
	var manualDuplicate int
	if revision > 0 {
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM activity_links WHERE from_activity_id=? AND relation_type='duplicate_of'`), id).Scan(&manualDuplicate); err != nil {
			return "", err
		}
		if manualDuplicate > 0 {
			replace = false
			unorderedConflict = false
		}
	}
	if revision > 0 && replace {
		comparable := a
		comparable.Revision = old.Revision
		comparable.Sources = old.Sources
		if historyJSON(comparable) == historyJSON(old) {
			replace = false
		}
	}
	if !replace {
		if _, err = s.txExecContext(ctx, tx, `INSERT INTO activity_sources(revision_id,raw_record_id,role,match_method) VALUES(?,?,'corroboration',?) ON CONFLICT(revision_id,raw_record_id) DO NOTHING`, oldID, rawID, match); err != nil {
			return "", err
		}
	} else {
		// Weak cross-source lookalikes are quarantined rather than silently double booked.
		if revision == 0 && a.Status == activity.StatusEffective {
			var candidates int
			weakCrossSource := false
			candidateIDs := []string{}
			fingerprint := historyHash(historyJSON(a.Legs) + a.TradeDate + a.Type)
			candidateRows, e := s.txQueryContext(ctx, tx, `SELECT v.payload,x.id FROM account_activities x JOIN activity_revisions v ON v.activity_id=x.id AND v.revision_no=x.current_revision WHERE x.account_id=? AND x.id<>? AND v.activity_type=? AND v.sort_key LIKE ?`, accountID, id, a.Type, a.TradeDate+"%")
			if e != nil {
				return "", e
			}
			for candidateRows.Next() {
				var b, candidateID string
				if e = candidateRows.Scan(&b, &candidateID); e != nil {
					candidateRows.Close()
					return "", e
				}
				var c activity.Activity
				if json.Unmarshal([]byte(b), &c) == nil && historyHash(historyJSON(c.Legs)+c.TradeDate+c.Type) == fingerprint {
					candidates++
					candidateIDs = append(candidateIDs, candidateID)
				}
			}
			e = candidateRows.Err()
			candidateRows.Close()
			if e != nil {
				return "", e
			}
			for _, candidateID := range candidateIDs {
				var cross int
				family := r.Source
				if family == "xml" {
					family = "flex"
				}
				if e = tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM activity_sources x JOIN activity_revisions v ON v.id=x.revision_id JOIN broker_raw_records r ON r.id=x.raw_record_id WHERE v.activity_id=? AND x.role='principal' AND (CASE WHEN r.source='xml' THEN 'flex' ELSE r.source END)<>?`), candidateID, family).Scan(&cross); e != nil {
					return "", e
				}
				if cross == 0 {
					continue
				}
				comparableIDs := false
				for _, key := range r.Identities {
					var n int
					if e = tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM broker_activity_identities WHERE activity_id=? AND namespace=? AND kind=?`), candidateID, key.Namespace, key.Kind).Scan(&n); e != nil {
						return "", e
					}
					if n > 0 {
						comparableIDs = true
						break
					}
				}
				if !comparableIDs {
					weakCrossSource = true
				}
			}
			// Distinct IDs in a common execution namespace prove distinct fills.
			// Cross-source IDs with unrelated namespaces do not prove that.
			if candidates > 0 && (len(r.Identities) == 0 || weakCrossSource) {
				a.Status = activity.StatusNeedsReview
				a.Warnings = append(a.Warnings, "ambiguous_duplicate")
			}
		}
		revID := uuid.NewString()
		sortKey := a.TradeDate + "|" + a.OccurredAt
		a.Sources = []activity.Source{}
		if a.Legs == nil {
			a.Legs = []activity.Leg{}
		}
		if a.Warnings == nil {
			a.Warnings = []string{}
		}
		if _, err = s.txExecContext(ctx, tx, `INSERT INTO activity_revisions(id,activity_id,revision_no,activity_type,status,booking_status,sort_key,payload,source_priority,source_updated_at,rule_version,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, revID, id, a.Revision, a.Type, a.Status, a.BookingStatus, sortKey, historyJSON(a), priority, r.SourceUpdatedAt, activity.RuleVersion, nowRFC3339()); err != nil {
			return "", err
		}
		for i, l := range a.Legs {
			if _, err = s.txExecContext(ctx, tx, `INSERT INTO activity_legs(id,revision_id,ordinal,kind,component,instrument_id,currency,quantity_delta,cash_delta,effective_date,settlement_date) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), revID, i, l.Kind, l.Component, historyInt(l.InstrumentID), historyNullable(l.Currency), historyNullable(l.QuantityDelta), historyNullable(l.CashDelta), l.EffectiveDate, l.SettlementDate); err != nil {
				return "", err
			}
		}
		if a.Fill != nil && a.Fill.Quantity != "" {
			var orderID any
			if a.Fill.OrderID != "" {
				oid := uuid.NewString()
				if _, err = s.txExecContext(ctx, tx, `INSERT INTO broker_orders(id,account_id,namespace,external_id,payload) VALUES(?,?,'ibkr.order',?,?) ON CONFLICT(account_id,namespace,external_id) DO NOTHING`, oid, accountID, a.Fill.OrderID, `{"status":"unknown"}`); err != nil {
					return "", err
				}
				if err = tx.QueryRowContext(ctx, s.bind(`SELECT id FROM broker_orders WHERE account_id=? AND namespace='ibkr.order' AND external_id=?`), accountID, a.Fill.OrderID).Scan(&oid); err != nil {
					return "", err
				}
				orderID = oid
			}
			if _, err = s.txExecContext(ctx, tx, `INSERT INTO broker_fills(revision_id,order_id,execution_id,quantity,price,multiplier,payload) VALUES(?,?,?,?,?,?,?)`, revID, orderID, a.Fill.ExecutionID, a.Fill.Quantity, historyNullable(a.Fill.Price), historyNullable(a.Fill.Multiplier), historyJSON(a.Fill)); err != nil {
				return "", err
			}
		}
		for _, c := range a.CostAdjustments {
			if _, err = s.txExecContext(ctx, tx, `INSERT INTO activity_cost_adjustments(id,revision_id,instrument_id,currency,basis_delta,basis_before,basis_after,allocation_method,evidence) VALUES(?,?,?,?,?,?,?,?,?)`, uuid.NewString(), revID, historyInt(c.InstrumentID), c.Currency, historyNullable(c.BasisDelta), historyNullable(c.BasisBefore), historyNullable(c.BasisAfter), c.AllocationMethod, c.Evidence); err != nil {
				return "", err
			}
		}
		if _, err = s.txExecContext(ctx, tx, `INSERT INTO activity_sources(revision_id,raw_record_id,role,match_method) VALUES(?,?,'principal',?)`, revID, rawID, match); err != nil {
			return "", err
		}
		if revision > 0 {
			if _, err = s.txExecContext(ctx, tx, `INSERT INTO activity_sources(revision_id,raw_record_id,role,match_method) SELECT ?,raw_record_id,'corroboration',match_method FROM activity_sources WHERE revision_id=? ON CONFLICT(revision_id,raw_record_id) DO NOTHING`, revID, oldID); err != nil {
				return "", err
			}
		}
		if _, err = s.txExecContext(ctx, tx, `UPDATE account_activities SET current_revision=? WHERE id=?`, a.Revision, id); err != nil {
			return "", err
		}
		if _, err = s.txExecContext(ctx, tx, `UPDATE broker_accounts SET activity_generation=activity_generation+1 WHERE id=?`, accountID); err != nil {
			return "", err
		}
	}
	for _, key := range identities {
		if key.ExternalID == "" {
			continue
		}
		if _, err = s.txExecContext(ctx, tx, `INSERT INTO broker_activity_identities(account_id,namespace,kind,external_id,activity_id) VALUES(?,?,?,?,?) ON CONFLICT(account_id,namespace,kind,external_id) DO NOTHING`, accountID, key.Namespace, key.Kind, key.ExternalID, id); err != nil {
			return "", err
		}
	}
	result := "added"
	if revision > 0 {
		result = "updated"
	}
	if !replace {
		result = "duplicate"
	}
	status := "done"
	if unorderedConflict {
		status = "needs_review"
		result = status
		if _, err = s.txExecContext(ctx, tx, `INSERT INTO activity_reconciliation_issues(id,account_id,activity_id,raw_record_id,issue_type,status,details,created_at,updated_at) VALUES(?,?,?,?,?,'open',?,?,?)`, uuid.NewString(), accountID, id, rawID, "source_revision_conflict", historyJSON(map[string]any{"warnings": []string{"source_revision_order_unknown"}, "proposed_activity": a, "current_revision": old.Revision}), nowRFC3339(), nowRFC3339()); err != nil {
			return "", err
		}
	}
	if replace && (a.Status == activity.StatusUnsupported || a.Status == activity.StatusNeedsReview) {
		status = a.Status
		result = status
		if _, err = s.txExecContext(ctx, tx, `INSERT INTO activity_reconciliation_issues(id,account_id,activity_id,raw_record_id,issue_type,status,details,created_at,updated_at) VALUES(?,?,?,?,?,'open',?,?,?)`, uuid.NewString(), accountID, id, rawID, status, historyJSON(map[string]any{"warnings": a.Warnings}), nowRFC3339(), nowRFC3339()); err != nil {
			return "", err
		}
	}
	if _, err = s.txExecContext(ctx, tx, `UPDATE broker_raw_records SET normalize_status=? WHERE id=?`, status, rawID); err != nil {
		return "", err
	}
	if _, err = s.txExecContext(ctx, tx, `UPDATE broker_raw_observations SET outcome=? WHERE raw_record_id=? AND job_id=?`, result, rawID, j.ID); err != nil {
		return "", err
	}
	return result, tx.Commit()
}

type historyCursor struct {
	Generation string `json:"g"`
	Filter     string `json:"f"`
	Sort       string `json:"s"`
	ID         string `json:"i"`
}

func (s *Store) ListActivities(ctx context.Context, q HistoryQuery) (HistoryPage, error) {
	q.Symbol = NormalizeInstrumentSymbol(q.Symbol)
	out := HistoryPage{Items: []activity.Activity{}, Coverage: []HistoryCoverage{}, Warnings: []string{}, AsOf: nowRFC3339()}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	sort.Slice(q.AccountIDs, func(i, j int) bool { return q.AccountIDs[i] < q.AccountIDs[j] })
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := s.txQueryContext(ctx, tx, `SELECT id,activity_generation FROM broker_accounts ORDER BY id`)
	if err != nil {
		return out, err
	}
	gen := ""
	for rows.Next() {
		var id, g int64
		if err = rows.Scan(&id, &g); err != nil {
			rows.Close()
			return out, err
		}
		if len(q.AccountIDs) == 0 || containsHistoryID(q.AccountIDs, id) {
			gen += fmt.Sprintf("%d:%d;", id, g)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	generation := historyHash(gen)
	filterQ := q
	filterQ.Cursor = ""
	filterQ.Limit = 0
	filter := historyHash(historyJSON(filterQ))
	cur := historyCursor{}
	if q.Cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		if e != nil || json.Unmarshal(b, &cur) != nil {
			return out, fmt.Errorf("invalid_history_cursor")
		}
		if cur.Generation != generation || cur.Filter != filter {
			return out, ErrHistoryCursorStale
		}
	}
	where := ` WHERE 1=1`
	args := []any{}
	if len(q.AccountIDs) > 0 {
		where += ` AND a.account_id IN (` + strings.TrimRight(strings.Repeat("?,", len(q.AccountIDs)), ",") + `)`
		for _, id := range q.AccountIDs {
			args = append(args, id)
		}
	}
	if q.Provider != "" {
		where += ` AND b.provider_code=?`
		args = append(args, strings.ToUpper(q.Provider))
	}
	if q.Types != "" {
		types := strings.Split(q.Types, ",")
		where += ` AND v.activity_type IN (` + strings.TrimRight(strings.Repeat("?,", len(types)), ",") + `)`
		for _, t := range types {
			args = append(args, t)
		}
	}
	if q.From != "" {
		where += ` AND v.sort_key>=?`
		args = append(args, q.From)
	}
	if q.To != "" {
		where += ` AND v.sort_key<?`
		args = append(args, q.To+"~")
	}
	if q.Status != "" {
		where += ` AND v.status=?`
		args = append(args, q.Status)
	}
	if q.InstrumentID != "" {
		where += ` AND EXISTS(SELECT 1 FROM activity_legs l WHERE l.revision_id=v.id AND l.instrument_id=?)`
		args = append(args, q.InstrumentID)
	}
	if q.Symbol != "" {
		where += ` AND EXISTS(SELECT 1 FROM activity_legs l JOIN instruments i ON i.id=l.instrument_id WHERE l.revision_id=v.id AND i.normalized_symbol=?)`
		args = append(args, q.Symbol)
	}
	if q.Currency != "" {
		where += ` AND EXISTS(SELECT 1 FROM activity_legs l WHERE l.revision_id=v.id AND l.currency=?)`
		args = append(args, strings.ToUpper(q.Currency))
	}
	if cur.ID != "" {
		where += ` AND (v.sort_key<? OR (v.sort_key=? AND a.id<?))`
		args = append(args, cur.Sort, cur.Sort, cur.ID)
	}
	args = append(args, q.Limit+1)
	rows, err = s.txQueryContext(ctx, tx, `SELECT v.payload,v.sort_key FROM account_activities a JOIN activity_revisions v ON v.activity_id=a.id AND v.revision_no=a.current_revision JOIN broker_accounts b ON b.id=a.account_id`+where+` ORDER BY v.sort_key DESC,a.id DESC LIMIT ?`, args...)
	if err != nil {
		return out, err
	}
	keys := []string{}
	for rows.Next() {
		var b, key string
		if err = rows.Scan(&b, &key); err != nil {
			rows.Close()
			return out, err
		}
		var a activity.Activity
		if err = json.Unmarshal([]byte(b), &a); err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, a)
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Items) > q.Limit {
		out.Items = out.Items[:q.Limit]
		out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(historyJSON(historyCursor{generation, filter, keys[q.Limit-1], out.Items[q.Limit-1].ID})))
	}
	coverageWhere := " WHERE 1=1"
	coverageArgs := []any{}
	if len(q.AccountIDs) > 0 {
		coverageWhere += ` AND c.account_id IN (` + strings.TrimRight(strings.Repeat("?,", len(q.AccountIDs)), ",") + `)`
		for _, id := range q.AccountIDs {
			coverageArgs = append(coverageArgs, id)
		}
	}
	if q.Provider != "" {
		coverageWhere += " AND b.provider_code=?"
		coverageArgs = append(coverageArgs, strings.ToUpper(q.Provider))
	}
	if q.From != "" {
		coverageWhere += " AND c.to_date>=?"
		coverageArgs = append(coverageArgs, q.From)
	}
	if q.To != "" {
		coverageWhere += " AND c.from_date<=?"
		coverageArgs = append(coverageArgs, q.To)
	}
	rows, err = s.txQueryContext(ctx, tx, `SELECT c.id,c.account_id,c.source,c.data_type,c.scope_key,c.from_date,c.to_date,c.fetch_status,c.normalize_status,c.reconcile_status,c.last_checked_at FROM broker_history_coverage c JOIN broker_accounts b ON b.id=c.account_id`+coverageWhere+` ORDER BY c.last_checked_at DESC`, coverageArgs...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c HistoryCoverage
		if err = rows.Scan(&c.ID, &c.AccountID, &c.Source, &c.DataType, &c.ScopeKey, &c.From, &c.To, &c.FetchStatus, &c.NormalizeStatus, &c.ReconcileStatus, &c.LastCheckedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Coverage = append(out.Coverage, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	if len(out.Coverage) == 0 {
		out.Warnings = append(out.Warnings, "history_coverage_unknown")
	}
	return out, nil
}
func (s *Store) GetActivity(ctx context.Context, id string) (activity.Activity, error) {
	var a activity.Activity
	var b, rev string
	err := s.queryRowContext(ctx, `SELECT v.payload,v.id FROM account_activities a JOIN activity_revisions v ON v.activity_id=a.id AND v.revision_no=a.current_revision WHERE a.id=?`, id).Scan(&b, &rev)
	if err == sql.ErrNoRows {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	if err = json.Unmarshal([]byte(b), &a); err != nil {
		return a, err
	}
	rows, err := s.queryContext(ctx, `SELECT r.id,r.source,r.namespace,r.source_key,x.role,x.match_method FROM activity_sources x JOIN broker_raw_records r ON r.id=x.raw_record_id WHERE x.revision_id=? ORDER BY r.created_at,r.id`, rev)
	if err != nil {
		return a, err
	}
	a.Sources = []activity.Source{}
	for rows.Next() {
		var src activity.Source
		if err = rows.Scan(&src.RawRecordID, &src.Source, &src.Namespace, &src.Key, &src.Role, &src.MatchMethod); err != nil {
			rows.Close()
			return a, err
		}
		a.Sources = append(a.Sources, src)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return a, err
	}
	rows, err = s.queryContext(ctx, `SELECT from_activity_id,to_activity_id,relation_type FROM activity_links WHERE from_activity_id=? OR to_activity_id=? ORDER BY created_at,id`, id, id)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	for rows.Next() {
		var link activity.Link
		if err = rows.Scan(&link.FromActivityID, &link.ToActivityID, &link.RelationType); err != nil {
			return a, err
		}
		link.Status = "confirmed"
		a.Links = append(a.Links, link)
	}
	return a, rows.Err()
}
func (s *Store) ActivityRevisions(ctx context.Context, id string) ([]activity.Activity, error) {
	if _, err := s.GetActivity(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.queryContext(ctx, `SELECT id,payload FROM activity_revisions WHERE activity_id=? ORDER BY revision_no DESC`, id)
	if err != nil {
		return nil, err
	}
	out := []activity.Activity{}
	revs := []string{}
	for rows.Next() {
		var rev, b string
		if err = rows.Scan(&rev, &b); err != nil {
			rows.Close()
			return nil, err
		}
		var a activity.Activity
		if err = json.Unmarshal([]byte(b), &a); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, a)
		revs = append(revs, rev)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i, rev := range revs {
		rows, err = s.queryContext(ctx, `SELECT r.id,r.source,r.namespace,r.source_key,x.role,x.match_method FROM activity_sources x JOIN broker_raw_records r ON r.id=x.raw_record_id WHERE x.revision_id=? ORDER BY r.created_at,r.id`, rev)
		if err != nil {
			return nil, err
		}
		out[i].Sources = []activity.Source{}
		for rows.Next() {
			var src activity.Source
			if err = rows.Scan(&src.RawRecordID, &src.Source, &src.Namespace, &src.Key, &src.Role, &src.MatchMethod); err != nil {
				rows.Close()
				return nil, err
			}
			out[i].Sources = append(out[i].Sources, src)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) ListHistoryCoverage(ctx context.Context) ([]HistoryCoverage, error) {
	rows, err := s.queryContext(ctx, `SELECT id,account_id,source,data_type,scope_key,from_date,to_date,fetch_status,normalize_status,reconcile_status,last_checked_at FROM broker_history_coverage ORDER BY last_checked_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryCoverage{}
	for rows.Next() {
		var c HistoryCoverage
		if err = rows.Scan(&c.ID, &c.AccountID, &c.Source, &c.DataType, &c.ScopeKey, &c.From, &c.To, &c.FetchStatus, &c.NormalizeStatus, &c.ReconcileStatus, &c.LastCheckedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) ListHistoryIssues(ctx context.Context) ([]HistoryIssue, error) {
	rows, err := s.queryContext(ctx, `SELECT id,account_id,COALESCE(CAST(activity_id AS TEXT),''),COALESCE(CAST(raw_record_id AS TEXT),''),issue_type,status,details,resolution,created_at FROM activity_reconciliation_issues ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryIssue{}
	for rows.Next() {
		var i HistoryIssue
		var b string
		if err = rows.Scan(&i.ID, &i.AccountID, &i.ActivityID, &i.RawRecordID, &i.Type, &i.Status, &b, &i.Resolution, &i.CreatedAt); err != nil {
			return nil, err
		}
		i.Details = json.RawMessage(b)
		out = append(out, i)
	}
	return out, rows.Err()
}

// ResolveHistoryIssue only links explicit transfers or leaves an issue pending.
// Amount-changing duplicate resolutions require the evidence replay path.
func (s *Store) ResolveHistoryIssue(ctx context.Context, id, action, target string, userID int64) error {
	if action == "confirm_same" || action == "confirm_distinct" || action == "accept_revision" {
		return s.resolveHistoryDuplicate(ctx, id, action, target, userID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var accountID int64
	var aid, status string
	err = tx.QueryRowContext(ctx, s.bind(`SELECT account_id,COALESCE(CAST(activity_id AS TEXT),''),status FROM activity_reconciliation_issues WHERE id=?`), id).Scan(&accountID, &aid, &status)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if action == "keep_pending" {
		return tx.Commit()
	}
	if action != "link_transfer" {
		return fmt.Errorf("resolution_requires_evidence")
	}
	var b string
	err = tx.QueryRowContext(ctx, s.bind(`SELECT v.payload FROM account_activities a JOIN activity_revisions v ON v.activity_id=a.id AND v.revision_no=a.current_revision WHERE a.id=?`), target).Scan(&b)
	if err != nil {
		return ErrNotFound
	}
	var other activity.Activity
	if err = json.Unmarshal([]byte(b), &other); err != nil {
		return err
	}
	var original string
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT v.payload FROM account_activities a JOIN activity_revisions v ON v.activity_id=a.id AND v.revision_no=a.current_revision WHERE a.id=?`), aid).Scan(&original); err != nil {
		return err
	}
	var first activity.Activity
	if err = json.Unmarshal([]byte(original), &first); err != nil {
		return err
	}
	if aid == target || first.AccountID == other.AccountID || !strings.Contains(first.Type, "transfer") || first.Type != other.Type {
		return fmt.Errorf("invalid_transfer_pair")
	}
	if len(first.Legs) == 0 || len(first.Legs) != len(other.Legs) {
		return fmt.Errorf("transfer_effect_mismatch")
	}
	for i, l := range first.Legs {
		r := other.Legs[i]
		if l.Kind != r.Kind || l.Currency != r.Currency || l.InstrumentID != r.InstrumentID {
			return fmt.Errorf("transfer_effect_mismatch")
		}
		x, y := l.CashDelta, r.CashDelta
		if l.Kind == "position" {
			x, y = l.QuantityDelta, r.QuantityDelta
		}
		sum, e := activity.Add(x, y)
		if e != nil || sum != "0" {
			return fmt.Errorf("transfer_effect_mismatch")
		}
	}
	if _, err = s.txExecContext(ctx, tx, `INSERT INTO activity_links(id,from_activity_id,to_activity_id,relation_type,created_by,created_at) VALUES(?,?,?,'transfer_pair',?,?) ON CONFLICT(from_activity_id,to_activity_id,relation_type) DO NOTHING`, uuid.NewString(), aid, target, userID, nowRFC3339()); err != nil {
		return err
	}
	if _, err = s.txExecContext(ctx, tx, `UPDATE activity_reconciliation_issues SET status='resolved',resolution=?,updated_at=? WHERE id=?`, historyJSON(map[string]any{"action": action, "target": target, "user_id": userID}), nowRFC3339(), id); err != nil {
		return err
	}
	if _, err = s.txExecContext(ctx, tx, `UPDATE broker_accounts SET activity_generation=activity_generation+1 WHERE id IN (?,?)`, accountID, other.AccountID); err != nil {
		return err
	}
	return tx.Commit()
}

// ParseHistoryIDs rejects invalid scopes rather than silently expanding them.
func ParseHistoryIDs(value string) ([]int64, error) {
	ids := []int64{}
	if value == "" {
		return ids, nil
	}
	for _, p := range strings.Split(value, ",") {
		n, e := strconv.ParseInt(p, 10, 64)
		if e != nil || n <= 0 {
			return nil, fmt.Errorf("invalid_account_ids")
		}
		if !containsHistoryID(ids, n) {
			ids = append(ids, n)
		}
	}
	return ids, nil
}
func ValidateHistoryDates(from, to string) error {
	for _, s := range []string{from, to} {
		if s != "" {
			if _, e := time.Parse("2006-01-02", s); e != nil {
				return fmt.Errorf("invalid_history_date")
			}
		}
	}
	if from != "" && to != "" && from > to {
		return fmt.Errorf("invalid_history_range")
	}
	return nil
}
