package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	BrokerConnectionStatusDisconnected = "disconnected"
	BrokerConnectionStatusConnected    = "connected"
)

// BrokerProvider combines static integration metadata with provider-scoped
// configuration. Secrets are write-only; callers only receive their keys.
type BrokerProvider struct {
	Code                 string                  `json:"code"`
	Name                 string                  `json:"name"`
	DisplayName          string                  `json:"display_name"`
	DisplayInfo          map[string]any          `json:"display_info"`
	Capabilities         []string                `json:"capabilities"`
	ProviderFields       []BrokerFieldDefinition `json:"provider_fields"`
	ConnectionFields     []BrokerFieldDefinition `json:"connection_fields"`
	Config               map[string]any          `json:"config"`
	ConfiguredSecretKeys []string                `json:"configured_secret_keys"`
	Secrets              map[string]string       `json:"-"`
}

type BrokerFieldDefinition struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Secret      bool   `json:"secret,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// BrokerConnection is one configured login/environment for a provider.
type BrokerConnection struct {
	ID                   int64             `json:"id"`
	ProviderCode         string            `json:"provider_code"`
	ConnectionKey        string            `json:"connection_key"`
	Name                 string            `json:"name"`
	ProviderUserID       string            `json:"provider_user_id,omitempty"`
	Username             string            `json:"username,omitempty"`
	Environment          string            `json:"environment"`
	AuthType             string            `json:"auth_type"`
	Config               map[string]any    `json:"config"`
	ConfiguredSecretKeys []string          `json:"configured_secret_keys"`
	Secrets              map[string]string `json:"-"`
	Enabled              bool              `json:"enabled"`
	Status               string            `json:"status"`
	LastAuthenticatedAt  string            `json:"last_authenticated_at,omitempty"`
}

type providerSeed struct {
	code, name, displayName, displayInfo string
	capabilities                         []string
	providerFields, connectionFields     []BrokerFieldDefinition
}

var defaultBrokerProviders = []providerSeed{
	{
		code: "IBKR", name: "Interactive Brokers", displayName: "Interactive Brokers",
		displayInfo:  `{"short_name":"IBKR"}`,
		capabilities: []string{"accounts", "cash_balances", "positions", "daily_performance"},
		providerFields: []BrokerFieldDefinition{
			{Key: "manager_url", Label: "Gateway Manager 地址", Type: "url", Required: true, Description: "IBKR Gateway Manager 的 HTTP(S) origin"},
			{Key: "manager_api_token", Label: "Manager API Token", Type: "string", Secret: true, Description: "Gateway Manager 的 api_token"},
		},
		connectionFields: []BrokerFieldDefinition{
			{Key: "username", Label: "登录用户名", Type: "string"},
			{Key: "gateway_id", Label: "Gateway 实例", Type: "string", Required: true, Description: "从 Gateway Manager 返回的实例中选择"},
			{Key: "gateway_token", Label: "Gateway Proxy Token", Type: "string", Secret: true},
			{Key: "flex_token", Label: "Flex Token", Type: "string", Secret: true},
			{Key: "flex_query_id", Label: "Flex Query ID", Type: "string"},
			{Key: "flex_base_url", Label: "Flex API 地址", Type: "url"},
		},
	},
	{
		code: "SCHWAB", name: "Charles Schwab", displayName: "Charles Schwab",
		displayInfo:  `{"short_name":"Schwab"}`,
		capabilities: []string{"accounts", "cash_balances", "positions", "daily_performance", "oauth"},
		providerFields: []BrokerFieldDefinition{
			{Key: "client_id", Label: "Client ID", Type: "string", Secret: true, Required: true},
			{Key: "client_secret", Label: "Client Secret", Type: "string", Secret: true, Required: true},
			{Key: "redirect_uri", Label: "Redirect URI", Type: "url", Required: true},
		},
		connectionFields: []BrokerFieldDefinition{
			{Key: "username", Label: "登录用户名", Type: "string"},
			{Key: "access_token", Label: "Access Token", Type: "string", Secret: true},
			{Key: "refresh_token", Label: "Refresh Token", Type: "string", Secret: true},
			{Key: "expires_at", Label: "Token 过期时间", Type: "datetime"},
		},
	},
	{
		code: "ALPACA", name: "Alpaca Markets", displayName: "Alpaca",
		displayInfo:  `{"short_name":"Alpaca"}`,
		capabilities: []string{"accounts", "cash_balances", "positions", "daily_performance", "trading"},
		providerFields: []BrokerFieldDefinition{
			{Key: "paper_base_url", Label: "Paper API 地址", Type: "url"},
			{Key: "live_base_url", Label: "Live API 地址", Type: "url"},
		},
		connectionFields: []BrokerFieldDefinition{
			{Key: "api_key", Label: "API Key", Type: "string", Secret: true, Required: true},
			{Key: "api_secret", Label: "API Secret", Type: "string", Secret: true, Required: true},
			{Key: "base_url", Label: "API 地址覆盖", Type: "url"},
		},
	},
}

func (s *Store) initializeBrokerModelSQLite() error {
	return s.initializeBrokerModel(brokerModelSQLiteSchema)
}

func (s *Store) initializeBrokerModelPostgres() error {
	return s.initializeBrokerModel(brokerModelPostgresSchema)
}

func (s *Store) initializeBrokerModel(schema string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, statement := range strings.Split(schema, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("create broker model: %w", err)
		}
	}
	if err := s.seedBrokerProvidersTx(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) seedBrokerProvidersTx(tx *sql.Tx) error {
	for _, provider := range defaultBrokerProviders {
		capabilities, err := json.Marshal(provider.capabilities)
		if err != nil {
			return err
		}
		providerFields, err := json.Marshal(provider.providerFields)
		if err != nil {
			return err
		}
		connectionFields, err := json.Marshal(provider.connectionFields)
		if err != nil {
			return err
		}
		if _, err := s.txExecContext(context.Background(), tx, `
			INSERT INTO broker_providers (
				code, name, display_name, display_info, capabilities,
				provider_fields, connection_fields
			)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(code) DO UPDATE SET
				name = excluded.name,
				display_name = excluded.display_name,
				display_info = excluded.display_info,
				capabilities = excluded.capabilities,
				provider_fields = excluded.provider_fields,
				connection_fields = excluded.connection_fields,
				updated_at = CURRENT_TIMESTAMP`,
			provider.code, provider.name, provider.displayName, provider.displayInfo,
			string(capabilities), string(providerFields), string(connectionFields)); err != nil {
			return fmt.Errorf("seed broker provider %s: %w", provider.code, err)
		}
	}
	return nil
}

func (s *Store) ListBrokerProviders(ctx context.Context) ([]BrokerProvider, error) {
	rows, err := s.queryContext(ctx, `
		SELECT code, name, display_name, display_info, capabilities,
			provider_fields, connection_fields,
			config_json, secrets_json
		FROM broker_providers ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	providers := []BrokerProvider{}
	for rows.Next() {
		var provider BrokerProvider
		var displayInfo, capabilities, providerFields, connectionFields, configJSON, secretsJSON string
		if err := rows.Scan(
			&provider.Code, &provider.Name, &provider.DisplayName,
			&displayInfo, &capabilities, &providerFields, &connectionFields,
			&configJSON, &secretsJSON,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(displayInfo), &provider.DisplayInfo); err != nil {
			return nil, fmt.Errorf("decode %s display info: %w", provider.Code, err)
		}
		if err := json.Unmarshal([]byte(capabilities), &provider.Capabilities); err != nil {
			return nil, fmt.Errorf("decode %s capabilities: %w", provider.Code, err)
		}
		if err := json.Unmarshal([]byte(providerFields), &provider.ProviderFields); err != nil {
			return nil, fmt.Errorf("decode %s provider fields: %w", provider.Code, err)
		}
		if err := json.Unmarshal([]byte(connectionFields), &provider.ConnectionFields); err != nil {
			return nil, fmt.Errorf("decode %s connection fields: %w", provider.Code, err)
		}
		if err := decodeJSONObject(configJSON, &provider.Config); err != nil {
			return nil, fmt.Errorf("decode %s config: %w", provider.Code, err)
		}
		var secrets map[string]string
		if err := decodeJSONObject(secretsJSON, &secrets); err != nil {
			return nil, fmt.Errorf("decode %s secrets: %w", provider.Code, err)
		}
		provider.ConfiguredSecretKeys = configuredSecretKeys(secrets)
		providers = append(providers, provider)
	}
	return providers, rows.Err()
}

func (s *Store) UpdateBrokerProviderConfig(
	ctx context.Context,
	providerCode string,
	config map[string]any,
	secrets map[string]string,
) (BrokerProvider, error) {
	providerCode = strings.ToUpper(strings.TrimSpace(providerCode))
	if providerCode == "" {
		return BrokerProvider{}, fmt.Errorf("provider code is required")
	}
	configJSON, err := json.Marshal(nonNilMap(config))
	if err != nil {
		return BrokerProvider{}, fmt.Errorf("encode provider config: %w", err)
	}
	secretJSON := ""
	if secrets != nil {
		encoded, err := json.Marshal(secrets)
		if err != nil {
			return BrokerProvider{}, fmt.Errorf("encode provider secrets: %w", err)
		}
		secretJSON = string(encoded)
	}
	result, err := s.execContext(ctx, `
		UPDATE broker_providers SET
			config_json = ?,
			secrets_json = CASE WHEN ? = '' THEN secrets_json ELSE ? END,
			updated_at = CURRENT_TIMESTAMP
		WHERE code = ?`, string(configJSON), secretJSON, secretJSON, providerCode)
	if err != nil {
		return BrokerProvider{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return BrokerProvider{}, err
	}
	if affected == 0 {
		return BrokerProvider{}, ErrNotFound
	}
	providers, err := s.ListBrokerProviders(ctx)
	if err != nil {
		return BrokerProvider{}, err
	}
	for _, provider := range providers {
		if provider.Code == providerCode {
			return provider, nil
		}
	}
	return BrokerProvider{}, ErrNotFound
}

type BrokerProviderRuntimeConfig struct {
	ProviderCode string
	Config       map[string]any
	Secrets      map[string]string
}

func (s *Store) GetBrokerProviderRuntimeConfig(ctx context.Context, providerCode string) (BrokerProviderRuntimeConfig, error) {
	providerCode = strings.ToUpper(strings.TrimSpace(providerCode))
	var runtimeConfig BrokerProviderRuntimeConfig
	var configJSON, secretsJSON string
	err := s.queryRowContext(ctx, `
		SELECT code, config_json, secrets_json
		FROM broker_providers WHERE code = ?`, providerCode).Scan(
		&runtimeConfig.ProviderCode, &configJSON, &secretsJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return BrokerProviderRuntimeConfig{}, ErrNotFound
	}
	if err != nil {
		return BrokerProviderRuntimeConfig{}, err
	}
	if err := decodeJSONObject(configJSON, &runtimeConfig.Config); err != nil {
		return BrokerProviderRuntimeConfig{}, err
	}
	if err := decodeJSONObject(secretsJSON, &runtimeConfig.Secrets); err != nil {
		return BrokerProviderRuntimeConfig{}, err
	}
	return runtimeConfig, nil
}

func decodeJSONObject[T any](raw string, target *T) error {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	return json.Unmarshal([]byte(raw), target)
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func configuredSecretKeys(secrets map[string]string) []string {
	keys := make([]string, 0, len(secrets))
	for key, value := range secrets {
		if strings.TrimSpace(value) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func (s *Store) UpsertBrokerConnection(ctx context.Context, connection BrokerConnection) (BrokerConnection, error) {
	connection.ProviderCode = strings.ToUpper(strings.TrimSpace(connection.ProviderCode))
	connection.ConnectionKey = strings.TrimSpace(connection.ConnectionKey)
	connection.Name = strings.TrimSpace(connection.Name)
	connection.ProviderUserID = strings.TrimSpace(connection.ProviderUserID)
	connection.Username = strings.TrimSpace(connection.Username)
	connection.Environment = strings.TrimSpace(connection.Environment)
	connection.AuthType = strings.ToLower(strings.TrimSpace(connection.AuthType))
	connection.Status = strings.ToLower(strings.TrimSpace(connection.Status))
	if connection.ProviderCode == "" || connection.ConnectionKey == "" {
		return BrokerConnection{}, fmt.Errorf("provider code and connection key are required")
	}
	if connection.Environment == "" {
		connection.Environment = "default"
	}
	if connection.AuthType == "" {
		connection.AuthType = "interactive"
	}
	configJSON := ""
	if connection.Config != nil {
		encoded, err := json.Marshal(connection.Config)
		if err != nil {
			return BrokerConnection{}, fmt.Errorf("encode connection config: %w", err)
		}
		configJSON = string(encoded)
	}
	secretsJSON := ""
	if connection.Secrets != nil {
		encoded, err := json.Marshal(connection.Secrets)
		if err != nil {
			return BrokerConnection{}, fmt.Errorf("encode connection secrets: %w", err)
		}
		secretsJSON = string(encoded)
	}
	if _, err := s.execContext(ctx, `
		INSERT INTO broker_connections (
			provider_code, connection_key, name, provider_user_id, username,
			environment, auth_type, config_json, secrets_json, enabled, status
		)
		SELECT code, ?, ?, ?, ?, ?, ?,
			COALESCE(NULLIF(?, ''), '{}'), COALESCE(NULLIF(?, ''), '{}'),
			?, COALESCE(NULLIF(?, ''), 'disconnected')
		FROM broker_providers WHERE code = ?
		ON CONFLICT(provider_code, connection_key) DO UPDATE SET
			name = excluded.name,
			provider_user_id = excluded.provider_user_id,
			username = excluded.username,
			environment = excluded.environment,
			auth_type = excluded.auth_type,
			config_json = CASE WHEN ? = '' THEN broker_connections.config_json ELSE excluded.config_json END,
			secrets_json = CASE WHEN ? = '' THEN broker_connections.secrets_json ELSE excluded.secrets_json END,
			enabled = excluded.enabled,
			status = CASE WHEN ? = '' THEN broker_connections.status ELSE excluded.status END,
			updated_at = CURRENT_TIMESTAMP`,
		connection.ConnectionKey, connection.Name, connection.ProviderUserID, connection.Username,
		connection.Environment, connection.AuthType, configJSON, secretsJSON, connection.Enabled,
		connection.Status, connection.ProviderCode, configJSON, secretsJSON, connection.Status,
	); err != nil {
		return BrokerConnection{}, err
	}
	result, err := s.getBrokerConnection(ctx, connection.ProviderCode, connection.ConnectionKey)
	if errors.Is(err, ErrNotFound) {
		return BrokerConnection{}, fmt.Errorf("broker provider %s is not initialized", connection.ProviderCode)
	}
	return result, err
}

func (s *Store) getBrokerConnection(ctx context.Context, providerCode, connectionKey string) (BrokerConnection, error) {
	connection, err := scanBrokerConnection(s.queryRowContext(ctx, `
		SELECT c.id, c.provider_code, c.connection_key, c.name,
			c.provider_user_id, c.username, c.environment, c.auth_type,
			c.config_json, c.secrets_json, c.enabled, c.status, c.last_authenticated_at
		FROM broker_connections c
		WHERE c.provider_code = ? AND c.connection_key = ?`, providerCode, connectionKey))
	if errors.Is(err, sql.ErrNoRows) {
		return BrokerConnection{}, ErrNotFound
	}
	return connection, err
}

func (s *Store) ListBrokerConnections(ctx context.Context) ([]BrokerConnection, error) {
	rows, err := s.queryContext(ctx, `
		SELECT c.id, c.provider_code, c.connection_key, c.name,
			c.provider_user_id, c.username, c.environment, c.auth_type,
			c.config_json, c.secrets_json, c.enabled, c.status, c.last_authenticated_at
		FROM broker_connections c
		ORDER BY c.provider_code, c.connection_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	connections := []BrokerConnection{}
	for rows.Next() {
		connection, err := scanBrokerConnection(rows)
		if err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	return connections, rows.Err()
}

func (s *Store) GetBrokerConnection(ctx context.Context, connectionID int64) (BrokerConnection, error) {
	connection, err := scanBrokerConnection(s.queryRowContext(ctx, `
		SELECT c.id, c.provider_code, c.connection_key, c.name,
			c.provider_user_id, c.username, c.environment, c.auth_type,
			c.config_json, c.secrets_json, c.enabled, c.status, c.last_authenticated_at
		FROM broker_connections c
		WHERE c.id = ?`, connectionID))
	if errors.Is(err, sql.ErrNoRows) {
		return BrokerConnection{}, ErrNotFound
	}
	return connection, err
}

func (s *Store) GetBrokerConnectionRuntimeConfig(ctx context.Context, connectionID int64) (BrokerConnection, error) {
	connection, err := s.GetBrokerConnection(ctx, connectionID)
	if err != nil {
		return BrokerConnection{}, err
	}
	var secretsJSON string
	if err := s.queryRowContext(ctx, `
		SELECT secrets_json FROM broker_connections WHERE id = ?`, connectionID).Scan(&secretsJSON); err != nil {
		return BrokerConnection{}, err
	}
	if err := decodeJSONObject(secretsJSON, &connection.Secrets); err != nil {
		return BrokerConnection{}, err
	}
	return connection, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanBrokerConnection(scanner rowScanner) (BrokerConnection, error) {
	var connection BrokerConnection
	var configJSON, secretsJSON string
	var lastAuthenticated sql.NullString
	if err := scanner.Scan(
		&connection.ID, &connection.ProviderCode,
		&connection.ConnectionKey, &connection.Name, &connection.ProviderUserID,
		&connection.Username, &connection.Environment, &connection.AuthType,
		&configJSON, &secretsJSON, &connection.Enabled, &connection.Status,
		&lastAuthenticated,
	); err != nil {
		return BrokerConnection{}, err
	}
	if err := decodeJSONObject(configJSON, &connection.Config); err != nil {
		return BrokerConnection{}, fmt.Errorf("decode connection config: %w", err)
	}
	var secrets map[string]string
	if err := decodeJSONObject(secretsJSON, &secrets); err != nil {
		return BrokerConnection{}, fmt.Errorf("decode connection secrets: %w", err)
	}
	connection.ConfiguredSecretKeys = configuredSecretKeys(secrets)
	if lastAuthenticated.Valid {
		connection.LastAuthenticatedAt = lastAuthenticated.String
	}
	return connection, nil
}

// SetBrokerConnectionEnabled controls synchronization without deleting the
// connection, its discovered accounts, or their latest successful projections.
func (s *Store) SetBrokerConnectionEnabled(ctx context.Context, connectionID int64, enabled bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := s.txQueryContext(ctx, tx, `
		SELECT account_id FROM broker_account_connections WHERE connection_id = ?`, connectionID)
	if err != nil {
		return err
	}
	var accountIDs []int64
	for rows.Next() {
		var accountID int64
		if err := rows.Scan(&accountID); err != nil {
			_ = rows.Close()
			return err
		}
		accountIDs = append(accountIDs, accountID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	result, err := s.txExecContext(ctx, tx, `
		UPDATE broker_connections SET enabled = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, enabled, connectionID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	if !enabled {
		if _, err := s.txExecContext(ctx, tx, `
			UPDATE broker_account_connections SET is_primary = 0
			WHERE connection_id = ? AND is_primary = 1`, connectionID); err != nil {
			return err
		}
	}
	for _, accountID := range accountIDs {
		if err := s.promoteOrDeleteBrokerAccountTx(ctx, tx, accountID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type BrokerConnectionAccountImpact struct {
	AccountID         int64  `json:"account_id"`
	ProviderAccountID string `json:"provider_account_id"`
	RemainingLinks    int    `json:"remaining_connections"`
}

type BrokerConnectionDeleteImpact struct {
	ConnectionID int64                           `json:"connection_id"`
	Shared       []BrokerConnectionAccountImpact `json:"shared_accounts"`
	Orphaned     []BrokerConnectionAccountImpact `json:"orphan_accounts"`
}

func (s *Store) GetBrokerConnectionDeleteImpact(ctx context.Context, connectionID int64) (BrokerConnectionDeleteImpact, error) {
	if _, err := s.GetBrokerConnection(ctx, connectionID); err != nil {
		return BrokerConnectionDeleteImpact{}, err
	}
	rows, err := s.queryContext(ctx, `
		SELECT a.id, a.provider_account_id,
			(SELECT COUNT(*) FROM broker_account_connections other
			 WHERE other.account_id = a.id AND other.connection_id <> ?)
		FROM broker_account_connections ac
		JOIN broker_accounts a ON a.id = ac.account_id
		WHERE ac.connection_id = ?
		ORDER BY a.provider_account_id`, connectionID, connectionID)
	if err != nil {
		return BrokerConnectionDeleteImpact{}, err
	}
	defer rows.Close()
	impact := BrokerConnectionDeleteImpact{
		ConnectionID: connectionID,
		Shared:       []BrokerConnectionAccountImpact{},
		Orphaned:     []BrokerConnectionAccountImpact{},
	}
	for rows.Next() {
		var account BrokerConnectionAccountImpact
		if err := rows.Scan(&account.AccountID, &account.ProviderAccountID, &account.RemainingLinks); err != nil {
			return BrokerConnectionDeleteImpact{}, err
		}
		if account.RemainingLinks == 0 {
			impact.Orphaned = append(impact.Orphaned, account)
		} else {
			impact.Shared = append(impact.Shared, account)
		}
	}
	return impact, rows.Err()
}

// DeleteBrokerConnection removes its account relationships. Shared accounts
// remain and receive a new primary relationship; accounts with no remaining
// connection are deleted together with their projections.
func (s *Store) DeleteBrokerConnection(ctx context.Context, connectionID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := s.txQueryContext(ctx, tx, `
		SELECT account_id FROM broker_account_connections WHERE connection_id = ?`, connectionID)
	if err != nil {
		return err
	}
	var accountIDs []int64
	for rows.Next() {
		var accountID int64
		if err := rows.Scan(&accountID); err != nil {
			_ = rows.Close()
			return err
		}
		accountIDs = append(accountIDs, accountID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	result, err := s.txExecContext(ctx, tx, `DELETE FROM broker_connections WHERE id = ?`, connectionID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	for _, accountID := range accountIDs {
		if err := s.promoteOrDeleteBrokerAccountTx(ctx, tx, accountID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) markBrokerConnectionAuthenticatedTx(ctx context.Context, tx *sql.Tx, connectionID int64, at string) error {
	result, err := s.txExecContext(ctx, tx, `
		UPDATE broker_connections
		SET status = 'connected', last_authenticated_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, at, connectionID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// The broker model separates three different concerns:
//
//   - broker_providers defines an integration; its readable code is the primary key.
//   - broker_connections stores authentication/configuration routes into a provider.
//   - broker_accounts stores one canonical account per (provider_code, provider_account_id).
//
// A canonical account can be visible through more than one connection, so
// broker_account_connections records access and elects one connection as the
// projection writer. Balances, positions, and performance belong to the
// canonical account and therefore reference account_id only.
const brokerModelSQLiteSchema = `
CREATE TABLE IF NOT EXISTS broker_providers (
	code TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	display_name TEXT NOT NULL DEFAULT '',
	display_info TEXT NOT NULL DEFAULT '{}',
	capabilities TEXT NOT NULL DEFAULT '[]',
	provider_fields TEXT NOT NULL DEFAULT '[]',
	connection_fields TEXT NOT NULL DEFAULT '[]',
	config_json TEXT NOT NULL DEFAULT '{}',
	secrets_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS instruments (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	asset_type TEXT NOT NULL,
	market TEXT NOT NULL,
	symbol TEXT NOT NULL,
	normalized_symbol TEXT NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	currency TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active',
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(asset_type, market, normalized_symbol)
);
CREATE TABLE IF NOT EXISTS broker_instruments (
	provider_code TEXT NOT NULL REFERENCES broker_providers(code) ON DELETE CASCADE,
	external_id TEXT NOT NULL,
	instrument_id INTEGER NOT NULL REFERENCES instruments(id) ON DELETE RESTRICT,
	broker_symbol TEXT NOT NULL DEFAULT '',
	broker_exchange TEXT NOT NULL DEFAULT '',
	last_seen_at TEXT NOT NULL,
	PRIMARY KEY(provider_code, external_id)
);
CREATE INDEX IF NOT EXISTS idx_broker_instruments_instrument ON broker_instruments(instrument_id);
CREATE TABLE IF NOT EXISTS broker_connections (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	provider_code TEXT NOT NULL REFERENCES broker_providers(code) ON DELETE RESTRICT,
	connection_key TEXT NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	provider_user_id TEXT NOT NULL DEFAULT '',
	username TEXT NOT NULL DEFAULT '',
	environment TEXT NOT NULL DEFAULT 'default',
	auth_type TEXT NOT NULL DEFAULT 'interactive',
	config_json TEXT NOT NULL DEFAULT '{}',
	secrets_json TEXT NOT NULL DEFAULT '{}',
	enabled INTEGER NOT NULL DEFAULT 1,
	status TEXT NOT NULL DEFAULT 'disconnected',
	last_authenticated_at TEXT,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(provider_code, connection_key)
);
CREATE TABLE IF NOT EXISTS broker_accounts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	provider_code TEXT NOT NULL REFERENCES broker_providers(code) ON DELETE RESTRICT,
	provider_account_id TEXT NOT NULL,
	first_discovered_connection_id INTEGER REFERENCES broker_connections(id) ON DELETE SET NULL,
	masked_account_number TEXT NOT NULL DEFAULT '',
	display_name TEXT NOT NULL DEFAULT '',
	account_type TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	currency TEXT NOT NULL DEFAULT '',
	first_discovered_at TEXT NOT NULL,
	last_seen_at TEXT NOT NULL,
	synced_at TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(provider_code, provider_account_id)
);
CREATE TABLE IF NOT EXISTS broker_account_connections (
	account_id INTEGER NOT NULL REFERENCES broker_accounts(id) ON DELETE CASCADE,
	connection_id INTEGER NOT NULL REFERENCES broker_connections(id) ON DELETE CASCADE,
	is_primary INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'active',
	first_seen_at TEXT NOT NULL,
	last_seen_at TEXT NOT NULL,
	PRIMARY KEY(account_id, connection_id)
);
CREATE INDEX IF NOT EXISTS idx_broker_account_connections_connection
	ON broker_account_connections(connection_id, account_id);
CREATE TABLE IF NOT EXISTS broker_account_balances (
	account_id INTEGER NOT NULL REFERENCES broker_accounts(id) ON DELETE CASCADE,
	currency TEXT NOT NULL DEFAULT '',
	net_liquidation REAL NOT NULL DEFAULT 0,
	total_cash_value REAL NOT NULL DEFAULT 0,
	gross_position_value REAL NOT NULL DEFAULT 0,
	buying_power REAL NOT NULL DEFAULT 0,
	unrealized_pnl REAL NOT NULL DEFAULT 0,
	realized_pnl REAL NOT NULL DEFAULT 0,
	settled_cash REAL NOT NULL DEFAULT 0,
	exchange_rate REAL NOT NULL DEFAULT 0,
	is_base_currency INTEGER NOT NULL DEFAULT 0,
	synced_at TEXT NOT NULL,
	PRIMARY KEY(account_id, currency)
);
CREATE TABLE IF NOT EXISTS broker_asset_positions (
	account_id INTEGER NOT NULL REFERENCES broker_accounts(id) ON DELETE CASCADE,
	instrument_id INTEGER NOT NULL REFERENCES instruments(id) ON DELETE RESTRICT,
	external_id TEXT NOT NULL DEFAULT '',
	asset_type TEXT NOT NULL,
	asset_key TEXT NOT NULL,
	symbol TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL DEFAULT '',
	conid INTEGER,
	currency TEXT NOT NULL DEFAULT '',
	quantity REAL NOT NULL,
	avg_cost REAL,
	market_price REAL,
	market_value REAL NOT NULL DEFAULT 0,
	unrealized_pnl REAL,
	realized_pnl REAL,
	cost_basis REAL,
	day_pnl REAL,
	day_pnl_pct REAL,
	raw_payload TEXT,
	synced_at TEXT NOT NULL,
	PRIMARY KEY(account_id, asset_key)
);
CREATE INDEX IF NOT EXISTS idx_broker_asset_positions_symbol ON broker_asset_positions(symbol);
CREATE INDEX IF NOT EXISTS idx_broker_asset_positions_asset_key ON broker_asset_positions(asset_key);
CREATE INDEX IF NOT EXISTS idx_broker_asset_positions_instrument ON broker_asset_positions(instrument_id);
CREATE TABLE IF NOT EXISTS broker_account_performance (
	account_id INTEGER PRIMARY KEY REFERENCES broker_accounts(id) ON DELETE CASCADE,
	daily_pnl REAL NOT NULL DEFAULT 0,
	net_liquidation REAL NOT NULL DEFAULT 0,
	unrealized_pnl REAL NOT NULL DEFAULT 0,
	excess_liquidity REAL NOT NULL DEFAULT 0,
	market_value REAL NOT NULL DEFAULT 0,
	synced_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS broker_sync_status (
	connection_id INTEGER NOT NULL REFERENCES broker_connections(id) ON DELETE CASCADE,
	account_id INTEGER REFERENCES broker_accounts(id) ON DELETE CASCADE,
	account_scope TEXT NOT NULL DEFAULT '',
	data_type TEXT NOT NULL,
	synced_at TEXT NOT NULL DEFAULT '',
	last_attempt_at TEXT NOT NULL,
	last_error TEXT NOT NULL DEFAULT '',
	item_count INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY(connection_id, account_scope, data_type)
);`

const brokerModelPostgresSchema = `
CREATE TABLE IF NOT EXISTS broker_providers (
	code TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	display_name TEXT NOT NULL DEFAULT '',
	display_info TEXT NOT NULL DEFAULT '{}',
	capabilities TEXT NOT NULL DEFAULT '[]',
	provider_fields TEXT NOT NULL DEFAULT '[]',
	connection_fields TEXT NOT NULL DEFAULT '[]',
	config_json TEXT NOT NULL DEFAULT '{}',
	secrets_json TEXT NOT NULL DEFAULT '{}',
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS instruments (
	id BIGSERIAL PRIMARY KEY,
	asset_type TEXT NOT NULL,
	market TEXT NOT NULL,
	symbol TEXT NOT NULL,
	normalized_symbol TEXT NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	currency TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active',
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(asset_type, market, normalized_symbol)
);
CREATE TABLE IF NOT EXISTS broker_instruments (
	provider_code TEXT NOT NULL REFERENCES broker_providers(code) ON DELETE CASCADE,
	external_id TEXT NOT NULL,
	instrument_id BIGINT NOT NULL REFERENCES instruments(id) ON DELETE RESTRICT,
	broker_symbol TEXT NOT NULL DEFAULT '',
	broker_exchange TEXT NOT NULL DEFAULT '',
	last_seen_at TEXT NOT NULL,
	PRIMARY KEY(provider_code, external_id)
);
CREATE INDEX IF NOT EXISTS idx_broker_instruments_instrument ON broker_instruments(instrument_id);
CREATE TABLE IF NOT EXISTS broker_connections (
	id BIGSERIAL PRIMARY KEY,
	provider_code TEXT NOT NULL REFERENCES broker_providers(code) ON DELETE RESTRICT,
	connection_key TEXT NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	provider_user_id TEXT NOT NULL DEFAULT '',
	username TEXT NOT NULL DEFAULT '',
	environment TEXT NOT NULL DEFAULT 'default',
	auth_type TEXT NOT NULL DEFAULT 'interactive',
	config_json TEXT NOT NULL DEFAULT '{}',
	secrets_json TEXT NOT NULL DEFAULT '{}',
	enabled BOOLEAN NOT NULL DEFAULT TRUE,
	status TEXT NOT NULL DEFAULT 'disconnected',
	last_authenticated_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(provider_code, connection_key)
);
CREATE TABLE IF NOT EXISTS broker_accounts (
	id BIGSERIAL PRIMARY KEY,
	provider_code TEXT NOT NULL REFERENCES broker_providers(code) ON DELETE RESTRICT,
	provider_account_id TEXT NOT NULL,
	first_discovered_connection_id BIGINT REFERENCES broker_connections(id) ON DELETE SET NULL,
	masked_account_number TEXT NOT NULL DEFAULT '',
	display_name TEXT NOT NULL DEFAULT '',
	account_type TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	currency TEXT NOT NULL DEFAULT '',
	first_discovered_at TEXT NOT NULL,
	last_seen_at TEXT NOT NULL,
	synced_at TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(provider_code, provider_account_id)
);
CREATE TABLE IF NOT EXISTS broker_account_connections (
	account_id BIGINT NOT NULL REFERENCES broker_accounts(id) ON DELETE CASCADE,
	connection_id BIGINT NOT NULL REFERENCES broker_connections(id) ON DELETE CASCADE,
	is_primary SMALLINT NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'active',
	first_seen_at TEXT NOT NULL,
	last_seen_at TEXT NOT NULL,
	PRIMARY KEY(account_id, connection_id)
);
CREATE INDEX IF NOT EXISTS idx_broker_account_connections_connection
	ON broker_account_connections(connection_id, account_id);
CREATE TABLE IF NOT EXISTS broker_account_balances (
	account_id BIGINT NOT NULL REFERENCES broker_accounts(id) ON DELETE CASCADE,
	currency TEXT NOT NULL DEFAULT '',
	net_liquidation DOUBLE PRECISION NOT NULL DEFAULT 0,
	total_cash_value DOUBLE PRECISION NOT NULL DEFAULT 0,
	gross_position_value DOUBLE PRECISION NOT NULL DEFAULT 0,
	buying_power DOUBLE PRECISION NOT NULL DEFAULT 0,
	unrealized_pnl DOUBLE PRECISION NOT NULL DEFAULT 0,
	realized_pnl DOUBLE PRECISION NOT NULL DEFAULT 0,
	settled_cash DOUBLE PRECISION NOT NULL DEFAULT 0,
	exchange_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
	is_base_currency BOOLEAN NOT NULL DEFAULT FALSE,
	synced_at TEXT NOT NULL,
	PRIMARY KEY(account_id, currency)
);
CREATE TABLE IF NOT EXISTS broker_asset_positions (
	account_id BIGINT NOT NULL REFERENCES broker_accounts(id) ON DELETE CASCADE,
	instrument_id BIGINT NOT NULL REFERENCES instruments(id) ON DELETE RESTRICT,
	external_id TEXT NOT NULL DEFAULT '',
	asset_type TEXT NOT NULL,
	asset_key TEXT NOT NULL,
	symbol TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL DEFAULT '',
	conid BIGINT,
	currency TEXT NOT NULL DEFAULT '',
	quantity DOUBLE PRECISION NOT NULL,
	avg_cost DOUBLE PRECISION,
	market_price DOUBLE PRECISION,
	market_value DOUBLE PRECISION NOT NULL DEFAULT 0,
	unrealized_pnl DOUBLE PRECISION,
	realized_pnl DOUBLE PRECISION,
	cost_basis DOUBLE PRECISION,
	day_pnl DOUBLE PRECISION,
	day_pnl_pct DOUBLE PRECISION,
	raw_payload TEXT,
	synced_at TEXT NOT NULL,
	PRIMARY KEY(account_id, asset_key)
);
CREATE INDEX IF NOT EXISTS idx_broker_asset_positions_symbol ON broker_asset_positions(symbol);
CREATE INDEX IF NOT EXISTS idx_broker_asset_positions_asset_key ON broker_asset_positions(asset_key);
CREATE INDEX IF NOT EXISTS idx_broker_asset_positions_instrument ON broker_asset_positions(instrument_id);
CREATE TABLE IF NOT EXISTS broker_account_performance (
	account_id BIGINT PRIMARY KEY REFERENCES broker_accounts(id) ON DELETE CASCADE,
	daily_pnl DOUBLE PRECISION NOT NULL DEFAULT 0,
	net_liquidation DOUBLE PRECISION NOT NULL DEFAULT 0,
	unrealized_pnl DOUBLE PRECISION NOT NULL DEFAULT 0,
	excess_liquidity DOUBLE PRECISION NOT NULL DEFAULT 0,
	market_value DOUBLE PRECISION NOT NULL DEFAULT 0,
	synced_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS broker_sync_status (
	connection_id BIGINT NOT NULL REFERENCES broker_connections(id) ON DELETE CASCADE,
	account_id BIGINT REFERENCES broker_accounts(id) ON DELETE CASCADE,
	account_scope TEXT NOT NULL DEFAULT '',
	data_type TEXT NOT NULL,
	synced_at TEXT NOT NULL DEFAULT '',
	last_attempt_at TEXT NOT NULL,
	last_error TEXT NOT NULL DEFAULT '',
	item_count INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY(connection_id, account_scope, data_type)
);`
