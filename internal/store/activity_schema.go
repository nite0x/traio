package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
)

// migrateActivities is additive. Historical records must never be recreated by
// a portfolio refresh or a connection lifecycle change.
func (s *Store) migrateActivities() error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.dialect == dialectPostgres {
		if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(78134121)`); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS activity_schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	schema := strings.NewReplacer("@ID@", "TEXT", "@ACCOUNT@", "INTEGER", "@DECIMAL@", "TEXT").Replace(activitySchema)
	if s.dialect == dialectPostgres {
		schema = strings.NewReplacer("@ID@", "UUID", "@ACCOUNT@", "BIGINT", "@DECIMAL@", "NUMERIC(38,18)").Replace(activitySchema)
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(schema)))
	var existing string
	err = tx.QueryRow(`SELECT checksum FROM activity_schema_migrations WHERE version=1`).Scan(&existing)
	if err == nil {
		if existing != checksum {
			return fmt.Errorf("activity migration checksum mismatch")
		}
	} else if err != sql.ErrNoRows {
		return err
	} else {
		for _, column := range []string{"archived_at TEXT NOT NULL DEFAULT ''", "archive_reason TEXT NOT NULL DEFAULT ''", "activity_generation BIGINT NOT NULL DEFAULT 0"} {
			if _, err = tx.Exec(`ALTER TABLE broker_accounts ADD COLUMN ` + column); err != nil {
				return fmt.Errorf("activity account migration: %w", err)
			}
		}
		for _, stmt := range strings.Split(schema, ";") {
			if strings.TrimSpace(stmt) != "" {
				if _, err = tx.Exec(stmt); err != nil {
					return fmt.Errorf("activity schema: %w", err)
				}
			}
		}
		if _, err = s.txExecContext(ctx, tx, `INSERT INTO activity_schema_migrations(version,checksum,applied_at) VALUES(1,?,?)`, checksum, nowRFC3339()); err != nil {
			return err
		}
	}
	if err = s.migrateActivityNormalizationVersion(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) migrateActivityNormalizationVersion(ctx context.Context, tx *sql.Tx) error {
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(activityNormalizationVersionSchema)))
	var existing string
	err := tx.QueryRow(`SELECT checksum FROM activity_schema_migrations WHERE version=2`).Scan(&existing)
	if err == nil {
		if existing != checksum {
			return fmt.Errorf("activity normalization migration checksum mismatch")
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	if _, err = tx.Exec(activityNormalizationVersionSchema); err != nil {
		return fmt.Errorf("activity normalization migration: %w", err)
	}
	_, err = s.txExecContext(ctx, tx, `INSERT INTO activity_schema_migrations(version,checksum,applied_at) VALUES(2,?,?)`, checksum, nowRFC3339())
	return err
}

const activityNormalizationVersionSchema = `ALTER TABLE broker_raw_records ADD COLUMN normalization_rule_version TEXT NOT NULL DEFAULT 'ibkr-activity-v1'`

const activitySchema = `
CREATE TABLE account_activities (
 id @ID@ PRIMARY KEY, account_id @ACCOUNT@ NOT NULL REFERENCES broker_accounts(id) ON DELETE RESTRICT,
 current_revision INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL
);
CREATE TABLE activity_revisions (
 id @ID@ PRIMARY KEY, activity_id @ID@ NOT NULL REFERENCES account_activities(id), revision_no INTEGER NOT NULL,
 activity_type TEXT NOT NULL CHECK(activity_type<>''), status TEXT NOT NULL CHECK(status IN ('effective','voided','needs_review','unsupported')), booking_status TEXT NOT NULL CHECK(booking_status IN ('booked','provisional')),
 sort_key TEXT NOT NULL, payload TEXT NOT NULL, source_priority INTEGER NOT NULL, source_updated_at TEXT NOT NULL,
 rule_version TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(activity_id,revision_no)
);
CREATE INDEX idx_activity_sort ON activity_revisions(sort_key DESC,activity_id DESC);
CREATE INDEX idx_activity_account ON account_activities(account_id,id);
CREATE TABLE activity_legs (
 id @ID@ PRIMARY KEY, revision_id @ID@ NOT NULL REFERENCES activity_revisions(id), ordinal INTEGER NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('cash','position')), component TEXT NOT NULL,
 instrument_id @ACCOUNT@ REFERENCES instruments(id) ON DELETE RESTRICT,
 currency TEXT, quantity_delta @DECIMAL@, cash_delta @DECIMAL@,
 effective_date TEXT NOT NULL, settlement_date TEXT NOT NULL,
 CHECK((kind='cash' AND currency IS NOT NULL AND cash_delta IS NOT NULL AND quantity_delta IS NULL)
 OR (kind='position' AND instrument_id IS NOT NULL AND quantity_delta IS NOT NULL AND cash_delta IS NULL)),
 UNIQUE(revision_id,ordinal)
);
CREATE INDEX idx_activity_instrument ON activity_legs(instrument_id,revision_id);
CREATE INDEX idx_activity_currency ON activity_legs(currency,revision_id);
CREATE TABLE broker_history_imports (
 id @ID@ PRIMARY KEY, provider_code TEXT NOT NULL, created_by BIGINT NOT NULL,
 file_hash TEXT NOT NULL, status TEXT NOT NULL, records TEXT NOT NULL, preview TEXT NOT NULL,
 evidence TEXT NOT NULL DEFAULT '[]', snapshots TEXT NOT NULL DEFAULT '[]',
 job_id @ID@, created_at TEXT NOT NULL, expires_at TEXT NOT NULL
);
CREATE INDEX idx_history_preview_expiry ON broker_history_imports(status,expires_at);
CREATE TABLE broker_history_jobs (
 id @ID@ PRIMARY KEY, connection_id @ACCOUNT@ REFERENCES broker_connections(id) ON DELETE SET NULL,
 source TEXT NOT NULL, request TEXT NOT NULL, status TEXT NOT NULL, phase TEXT NOT NULL,
 attempt INTEGER NOT NULL DEFAULT 0, lease_owner TEXT NOT NULL DEFAULT '', lease_generation BIGINT NOT NULL DEFAULT 0,
 lease_until TEXT NOT NULL DEFAULT '', next_attempt_at TEXT NOT NULL DEFAULT '',
 counts TEXT NOT NULL DEFAULT '{}', evidence TEXT NOT NULL DEFAULT '[]', error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX idx_history_jobs_ready ON broker_history_jobs(status,next_attempt_at,lease_until);
CREATE TABLE broker_raw_records (
 id @ID@ PRIMARY KEY, account_id @ACCOUNT@ NOT NULL REFERENCES broker_accounts(id) ON DELETE RESTRICT,
 source TEXT NOT NULL, namespace TEXT NOT NULL, record_type TEXT NOT NULL, source_key TEXT NOT NULL,
 payload_hash TEXT NOT NULL, payload TEXT NOT NULL, normalized TEXT NOT NULL, source_updated_at TEXT NOT NULL,
 normalize_status TEXT NOT NULL DEFAULT 'pending', created_at TEXT NOT NULL,
 UNIQUE(account_id,namespace,record_type,source_key,payload_hash)
);
CREATE TABLE broker_raw_observations (
 raw_record_id @ID@ NOT NULL REFERENCES broker_raw_records(id), job_id @ID@ NOT NULL REFERENCES broker_history_jobs(id),
 observed_at TEXT NOT NULL, outcome TEXT NOT NULL DEFAULT '', PRIMARY KEY(raw_record_id,job_id)
);
CREATE INDEX idx_history_observations_job ON broker_raw_observations(job_id,raw_record_id);
CREATE TABLE broker_activity_identities (
 account_id @ACCOUNT@ NOT NULL REFERENCES broker_accounts(id) ON DELETE RESTRICT,
 namespace TEXT NOT NULL, kind TEXT NOT NULL, external_id TEXT NOT NULL,
 activity_id @ID@ NOT NULL REFERENCES account_activities(id),
 PRIMARY KEY(account_id,namespace,kind,external_id)
);
CREATE INDEX idx_history_identity_activity ON broker_activity_identities(activity_id);
CREATE TABLE activity_sources (
 revision_id @ID@ NOT NULL REFERENCES activity_revisions(id), raw_record_id @ID@ NOT NULL REFERENCES broker_raw_records(id),
 role TEXT NOT NULL, match_method TEXT NOT NULL, PRIMARY KEY(revision_id,raw_record_id)
);
CREATE INDEX idx_history_source_raw ON activity_sources(raw_record_id,revision_id);
CREATE TABLE activity_links (
 id @ID@ PRIMARY KEY, from_activity_id @ID@ NOT NULL REFERENCES account_activities(id),
 to_activity_id @ID@ NOT NULL REFERENCES account_activities(id), relation_type TEXT NOT NULL,
 created_by BIGINT NOT NULL, created_at TEXT NOT NULL,
 UNIQUE(from_activity_id,to_activity_id,relation_type), CHECK(from_activity_id<>to_activity_id)
);
CREATE INDEX idx_activity_link_target ON activity_links(to_activity_id);
CREATE TABLE broker_orders (
 id @ID@ PRIMARY KEY, account_id @ACCOUNT@ NOT NULL REFERENCES broker_accounts(id),
 namespace TEXT NOT NULL, external_id TEXT NOT NULL, payload TEXT NOT NULL,
 UNIQUE(account_id,namespace,external_id)
);
CREATE TABLE broker_fills (
 revision_id @ID@ PRIMARY KEY REFERENCES activity_revisions(id), order_id @ID@ REFERENCES broker_orders(id),
 execution_id TEXT NOT NULL, quantity @DECIMAL@ NOT NULL, price @DECIMAL@, multiplier @DECIMAL@,
 payload TEXT NOT NULL
);
CREATE TABLE broker_history_coverage (
 id @ID@ PRIMARY KEY, account_id @ACCOUNT@ NOT NULL REFERENCES broker_accounts(id),
 source TEXT NOT NULL, data_type TEXT NOT NULL, scope_key TEXT NOT NULL, from_date TEXT NOT NULL,to_date TEXT NOT NULL,
 fetch_status TEXT NOT NULL, normalize_status TEXT NOT NULL,reconcile_status TEXT NOT NULL DEFAULT 'unknown',
 job_id @ID@ REFERENCES broker_history_jobs(id), last_checked_at TEXT NOT NULL
);
CREATE INDEX idx_history_coverage_scope ON broker_history_coverage(account_id,source,data_type,scope_key,from_date,to_date);
CREATE INDEX idx_history_coverage_job ON broker_history_coverage(job_id);
CREATE TABLE activity_reconciliation_issues (
 id @ID@ PRIMARY KEY, account_id @ACCOUNT@ NOT NULL REFERENCES broker_accounts(id), activity_id @ID@ REFERENCES account_activities(id),
 raw_record_id @ID@ REFERENCES broker_raw_records(id), issue_type TEXT NOT NULL,status TEXT NOT NULL,
 details TEXT NOT NULL, resolution TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL
);
CREATE INDEX idx_history_issues_account ON activity_reconciliation_issues(account_id,status);
CREATE TABLE account_reconciliation_snapshots (
 id @ID@ PRIMARY KEY,account_id @ACCOUNT@ NOT NULL REFERENCES broker_accounts(id),as_of_date TEXT NOT NULL,
 basis TEXT NOT NULL,source_ref TEXT NOT NULL,completeness TEXT NOT NULL,created_at TEXT NOT NULL
);
CREATE INDEX idx_history_snapshot_account ON account_reconciliation_snapshots(account_id,as_of_date,basis);
CREATE TABLE account_reconciliation_balances (
 id @ID@ PRIMARY KEY,snapshot_id @ID@ NOT NULL REFERENCES account_reconciliation_snapshots(id),kind TEXT NOT NULL,
 currency TEXT,instrument_id @ACCOUNT@ REFERENCES instruments(id),amount @DECIMAL@,quantity @DECIMAL@,reported_cost @DECIMAL@
);
CREATE INDEX idx_history_snapshot_balances ON account_reconciliation_balances(snapshot_id);
CREATE TABLE activity_cost_adjustments (
 id @ID@ PRIMARY KEY,revision_id @ID@ NOT NULL REFERENCES activity_revisions(id),instrument_id @ACCOUNT@ REFERENCES instruments(id),
 currency TEXT NOT NULL,basis_delta @DECIMAL@,basis_before @DECIMAL@,basis_after @DECIMAL@,allocation_method TEXT NOT NULL,evidence TEXT NOT NULL
);
CREATE INDEX idx_history_cost_revision ON activity_cost_adjustments(revision_id);
`
