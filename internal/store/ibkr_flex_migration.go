package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Detach legacy report credentials from Gateway connections atomically. Stable
// keys make restart/retry idempotent; Gateway account relationships stay intact.
func (s *Store) migrateIBKRFlexConnections() error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := s.txQueryContext(ctx, tx, `SELECT id,connection_key,name,config_json,secrets_json,enabled FROM broker_connections WHERE provider_code='IBKR'`)
	if err != nil {
		return err
	}
	type legacy struct {
		id                         int64
		key, name, config, secrets string
		enabled                    bool
	}
	all := []legacy{}
	for rows.Next() {
		var x legacy
		if err = rows.Scan(&x.id, &x.key, &x.name, &x.config, &x.secrets, &x.enabled); err != nil {
			rows.Close()
			return err
		}
		all = append(all, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, old := range all {
		cfg := map[string]any{}
		secrets := map[string]string{}
		if err = json.Unmarshal([]byte(old.config), &cfg); err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(old.secrets), &secrets); err != nil {
			return err
		}
		if cfg["connection_type"] == "flex" || strings.TrimSpace(secrets["flex_token"]) == "" {
			continue
		}
		query, _ := cfg["flex_query_id"].(string)
		activityQuery, _ := cfg["flex_activity_query_id"].(string)
		if strings.TrimSpace(query) == "" && strings.TrimSpace(activityQuery) == "" {
			continue
		}
		flexConfig := map[string]any{"connection_type": "flex"}
		for _, key := range []string{"flex_query_id", "flex_activity_query_id", "flex_base_url", "activity_history_enabled", "activity_history_from"} {
			if value, ok := cfg[key]; ok {
				flexConfig[key] = value
			}
			if strings.HasPrefix(key, "flex_") {
				delete(cfg, key)
			}
		}
		// Preserve the Gateway's independent recent-trade scheduling preference.
		key := fmt.Sprintf("flex:legacy:%d", old.id)
		var exists int
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM broker_connections WHERE provider_code='IBKR' AND connection_key=?`), key).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			return fmt.Errorf("legacy Flex connection key already exists")
		}
		c := BrokerConnection{ProviderCode: "IBKR", ConnectionKey: key, Name: old.name + " · Flex Query", AuthType: "api_key", Config: flexConfig, Secrets: map[string]string{"flex_token": secrets["flex_token"]}, Enabled: old.enabled}
		exec := func(ctx context.Context, q string, args ...any) (sql.Result, error) {
			return s.txExecContext(ctx, tx, q, args...)
		}
		if err = upsertBrokerConnection(ctx, exec, c); err != nil {
			return err
		}
		var newID int64
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT id FROM broker_connections WHERE provider_code='IBKR' AND connection_key=?`), key).Scan(&newID); err != nil {
			return err
		}
		if _, err = s.txExecContext(ctx, tx, `INSERT INTO broker_account_connections(account_id,connection_id,is_primary,status,first_seen_at,last_seen_at)
    SELECT account_id,?,0,status,first_seen_at,last_seen_at FROM broker_account_connections WHERE connection_id=?`, newID, old.id); err != nil {
			return err
		}
		pending, err := s.txQueryContext(ctx, tx, `SELECT id,request FROM broker_history_jobs WHERE connection_id=? AND source='flex' AND status IN ('queued','running','retry_wait','needs_action')`, old.id)
		if err != nil {
			return err
		}
		type pendingJob struct{ id, request string }
		jobs := []pendingJob{}
		for pending.Next() {
			var job pendingJob
			if err = pending.Scan(&job.id, &job.request); err != nil {
				pending.Close()
				return err
			}
			jobs = append(jobs, job)
		}
		err = pending.Err()
		pending.Close()
		if err != nil {
			return err
		}
		for _, job := range jobs {
			var request HistoryRequest
			if err = json.Unmarshal([]byte(job.request), &request); err != nil {
				return err
			}
			request.ConnectionID = newID
			if _, err = s.txExecContext(ctx, tx, `UPDATE broker_history_jobs SET connection_id=?,request=? WHERE id=?`, newID, historyJSON(request), job.id); err != nil {
				return err
			}
		}
		delete(secrets, "flex_token")
		if _, err = s.txExecContext(ctx, tx, `UPDATE broker_connections SET config_json=?,secrets_json=? WHERE id=?`, historyJSON(cfg), historyJSON(secrets), old.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
