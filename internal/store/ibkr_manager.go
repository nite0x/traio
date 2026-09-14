package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// SaveIBKRManagerConfig commits the provider and every discovered connection
// together. A failed import never leaves a partially replaced credential set.
func (s *Store) SaveIBKRManagerConfig(ctx context.Context, config map[string]any, secrets map[string]string, discovered []BrokerConnection) (BrokerProvider, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BrokerProvider{}, err
	}
	defer tx.Rollback()
	query := "SELECT config_json FROM broker_providers WHERE code = 'IBKR'"
	if s.dialect == dialectPostgres {
		query += " FOR UPDATE"
	}
	var oldJSON string
	if err := tx.QueryRowContext(ctx, query).Scan(&oldJSON); err != nil {
		return BrokerProvider{}, err
	}
	var oldConfig map[string]any
	if err := json.Unmarshal([]byte(oldJSON), &oldConfig); err != nil {
		return BrokerProvider{}, err
	}
	origin, _ := config["manager_url"].(string)
	oldOrigin, _ := oldConfig["manager_url"].(string)
	oldOrigin = strings.TrimRight(strings.TrimSpace(oldOrigin), "/")
	configJSON, err := json.Marshal(config)
	if err != nil {
		return BrokerProvider{}, err
	}
	secretJSON := ""
	if secrets != nil {
		encoded, err := json.Marshal(secrets)
		if err != nil {
			return BrokerProvider{}, err
		}
		secretJSON = string(encoded)
	}
	if _, err := s.txExecContext(ctx, tx, `UPDATE broker_providers SET config_json = ?,
		secrets_json = CASE WHEN ? = '' THEN secrets_json ELSE ? END,
		updated_at = CURRENT_TIMESTAMP WHERE code = 'IBKR'`, string(configJSON), secretJSON, secretJSON); err != nil {
		return BrokerProvider{}, err
	}
	rows, err := s.txQueryContext(ctx, tx, `SELECT id, provider_code, connection_key, name,
		provider_user_id, username, environment, auth_type, config_json, secrets_json,
		enabled, status, last_authenticated_at FROM broker_connections WHERE provider_code = 'IBKR'`)
	if err != nil {
		return BrokerProvider{}, err
	}
	var existing []BrokerConnection
	for rows.Next() {
		connection, err := scanBrokerConnectionRuntime(rows)
		if err != nil {
			rows.Close()
			return BrokerProvider{}, err
		}
		existing = append(existing, connection)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return BrokerProvider{}, err
	}
	exec := func(ctx context.Context, query string, args ...any) (sql.Result, error) {
		return s.txExecContext(ctx, tx, query, args...)
	}
	for _, incoming := range discovered {
		gatewayID, _ := incoming.Config["gateway_id"].(string)
		matched := false
		for _, current := range existing {
			managerURL, _ := current.Config["manager_url"].(string)
			if current.Config["gateway_id"] != gatewayID || (managerURL != origin && !(managerURL == "" && oldOrigin == origin)) {
				continue
			}
			matched = true
			// Keep identity, preferences, account associations and unrelated secrets.
			if current.Config["gateway_url"] != incoming.Config["gateway_url"] || current.Secrets["gateway_token"] != incoming.Secrets["gateway_token"] {
				current.Status = BrokerConnectionStatusDisconnected
			}
			if current.Config == nil {
				current.Config = map[string]any{}
			}
			if current.Secrets == nil {
				current.Secrets = map[string]string{}
			}
			for k, v := range incoming.Config {
				current.Config[k] = v
			}
			for k, v := range incoming.Secrets {
				current.Secrets[k] = v
			}
			if err := upsertBrokerConnection(ctx, exec, current); err != nil {
				return BrokerProvider{}, err
			}
		}
		if !matched {
			// Include the Manager origin so identical IDs on different Managers
			// cannot overwrite each other's connections and account associations.
			identity := sha256.Sum256([]byte(origin + "\x00" + gatewayID))
			incoming.ConnectionKey = fmt.Sprintf("ibkr-%x", identity[:16])
			if err := upsertBrokerConnection(ctx, exec, incoming); err != nil {
				return BrokerProvider{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return BrokerProvider{}, err
	}
	providers, err := s.ListBrokerProviders(ctx)
	if err != nil {
		return BrokerProvider{}, err
	}
	for _, provider := range providers {
		if provider.Code == "IBKR" {
			return provider, nil
		}
	}
	return BrokerProvider{}, ErrNotFound
}
