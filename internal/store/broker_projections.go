package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nite/traio/internal/broker"
)

// ReplaceBrokerConnectionAccounts refreshes the accounts visible through one
// connection. An account is stored once per provider; this method only adds or
// removes the connection's access relationship.
func (s *Store) ReplaceBrokerConnectionAccounts(ctx context.Context, connectionID int64, accounts []broker.Account) error {
	if connectionID == 0 {
		return fmt.Errorf("connection is required")
	}
	if len(accounts) == 0 {
		return fmt.Errorf("at least one account is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var providerCode string
	if err := tx.QueryRowContext(ctx, s.bind(`
		SELECT provider_code FROM broker_connections WHERE id = ?`), connectionID).Scan(&providerCode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	existing := map[string]int64{}
	rows, err := s.txQueryContext(ctx, tx, `
		SELECT a.id, a.provider_account_id
		FROM broker_accounts a
		JOIN broker_account_connections ac ON ac.account_id = a.id
		WHERE ac.connection_id = ?`, connectionID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var providerAccountID string
		if err := rows.Scan(&id, &providerAccountID); err != nil {
			_ = rows.Close()
			return err
		}
		existing[providerAccountID] = id
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	syncedAt := nowRFC3339()
	seen := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		providerAccountID := strings.TrimSpace(account.ID)
		if providerAccountID == "" {
			return fmt.Errorf("account is required")
		}
		if _, duplicate := seen[providerAccountID]; duplicate {
			return fmt.Errorf("duplicate account %s", providerAccountID)
		}
		seen[providerAccountID] = struct{}{}
		if _, err := s.txExecContext(ctx, tx, `
			INSERT INTO broker_accounts (
				provider_code, provider_account_id, first_discovered_connection_id,
				display_name, account_type, status, currency,
				first_discovered_at, last_seen_at, synced_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(provider_code, provider_account_id) DO UPDATE SET
				display_name = CASE WHEN excluded.display_name = '' THEN broker_accounts.display_name ELSE excluded.display_name END,
				account_type = CASE WHEN excluded.account_type = '' THEN broker_accounts.account_type ELSE excluded.account_type END,
				status = CASE WHEN excluded.status = '' THEN broker_accounts.status ELSE excluded.status END,
				currency = CASE WHEN excluded.currency = '' THEN broker_accounts.currency ELSE excluded.currency END,
				last_seen_at = excluded.last_seen_at,
				synced_at = excluded.synced_at,
				updated_at = CURRENT_TIMESTAMP`,
			providerCode, providerAccountID, connectionID, strings.TrimSpace(account.DisplayName),
			strings.TrimSpace(account.AccountType), strings.TrimSpace(account.Status),
			strings.ToUpper(strings.TrimSpace(account.BaseCurrency)), syncedAt, syncedAt, syncedAt,
		); err != nil {
			return err
		}
		var accountID int64
		if err := tx.QueryRowContext(ctx, s.bind(`
			SELECT id FROM broker_accounts
			WHERE provider_code = ? AND provider_account_id = ?`), providerCode, providerAccountID).Scan(&accountID); err != nil {
			return err
		}
		var relationshipCount int
		if err := tx.QueryRowContext(ctx, s.bind(`
			SELECT COUNT(*) FROM broker_account_connections WHERE account_id = ?`), accountID).Scan(&relationshipCount); err != nil {
			return err
		}
		isPrimary := 0
		if relationshipCount == 0 {
			isPrimary = 1
		}
		if _, err := s.txExecContext(ctx, tx, `
			INSERT INTO broker_account_connections (
				account_id, connection_id, is_primary, status, first_seen_at, last_seen_at
			) VALUES (?, ?, ?, 'active', ?, ?)
			ON CONFLICT(account_id, connection_id) DO UPDATE SET
				status = 'active', last_seen_at = excluded.last_seen_at`,
			accountID, connectionID, isPrimary, syncedAt, syncedAt,
		); err != nil {
			return err
		}
		if err := s.promoteOrDeleteBrokerAccountTx(ctx, tx, accountID); err != nil {
			return err
		}
	}
	for providerAccountID, accountID := range existing {
		if _, stillVisible := seen[providerAccountID]; stillVisible {
			continue
		}
		if _, err := s.txExecContext(ctx, tx, `
			DELETE FROM broker_account_connections
			WHERE account_id = ? AND connection_id = ?`, accountID, connectionID); err != nil {
			return err
		}
		if err := s.promoteOrDeleteBrokerAccountTx(ctx, tx, accountID); err != nil {
			return err
		}
	}
	if err := s.recordBrokerSyncSuccessTx(ctx, tx, connectionID, nil, "", SyncDataAccounts, len(accounts), syncedAt); err != nil {
		return err
	}
	if err := s.markBrokerConnectionAuthenticatedTx(ctx, tx, connectionID, syncedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) BrokerAccountConnectionIsPrimary(ctx context.Context, connectionID int64, providerAccountID string) (bool, error) {
	providerAccountID = strings.TrimSpace(providerAccountID)
	var primary bool
	err := s.queryRowContext(ctx, `
		SELECT ac.is_primary
		FROM broker_account_connections ac
		JOIN broker_accounts a ON a.id = ac.account_id
		JOIN broker_connections c ON c.id = ac.connection_id
		WHERE ac.connection_id = ? AND a.provider_code = c.provider_code
			AND a.provider_account_id = ?`, connectionID, providerAccountID).Scan(&primary)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return primary, err
}

func (s *Store) promoteOrDeleteBrokerAccountTx(ctx context.Context, tx *sql.Tx, accountID int64) error {
	var count int
	if err := tx.QueryRowContext(ctx, s.bind(`
		SELECT COUNT(*) FROM broker_account_connections WHERE account_id = ?`), accountID).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		_, err := s.txExecContext(ctx, tx, `DELETE FROM broker_accounts WHERE id = ?`, accountID)
		return err
	}
	var primaryCount int
	if err := tx.QueryRowContext(ctx, s.bind(`
		SELECT COUNT(*) FROM broker_account_connections
		WHERE account_id = ? AND is_primary = 1`), accountID).Scan(&primaryCount); err != nil {
		return err
	}
	if primaryCount > 0 {
		return nil
	}
	_, err := s.txExecContext(ctx, tx, `
		UPDATE broker_account_connections SET is_primary = 1
		WHERE account_id = ? AND connection_id = (
			SELECT ac.connection_id
			FROM broker_account_connections ac
			JOIN broker_connections c ON c.id = ac.connection_id
			WHERE ac.account_id = ?
			ORDER BY c.enabled DESC, ac.first_seen_at, ac.connection_id LIMIT 1
		)`, accountID, accountID)
	return err
}

func (s *Store) ReplaceBrokerConnectionAccountDetails(ctx context.Context, connectionID int64, account broker.Account) error {
	providerAccountID := strings.TrimSpace(account.ID)
	if connectionID == 0 || providerAccountID == "" {
		return fmt.Errorf("connection and account are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	syncedAt := nowRFC3339()
	accountID, err := s.brokerAccountIDTx(ctx, tx, connectionID, providerAccountID)
	if err != nil {
		return err
	}
	if _, err := s.txExecContext(ctx, tx, `
		UPDATE broker_accounts SET
			display_name = ?, account_type = ?, status = ?, currency = ?,
			last_seen_at = ?, synced_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		strings.TrimSpace(account.DisplayName), strings.TrimSpace(account.AccountType),
		strings.TrimSpace(account.Status), strings.ToUpper(strings.TrimSpace(account.BaseCurrency)),
		syncedAt, syncedAt, accountID,
	); err != nil {
		return err
	}
	if err := s.recordBrokerSyncSuccessTx(ctx, tx, connectionID, &accountID, providerAccountID, SyncDataAccountDetails, 1, syncedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReplaceBrokerConnectionCashBalances(ctx context.Context, connectionID int64, providerAccountID string, balances []broker.CashBalance) error {
	providerAccountID = strings.TrimSpace(providerAccountID)
	if connectionID == 0 || providerAccountID == "" {
		return fmt.Errorf("connection and account are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	accountID, err := s.brokerAccountIDTx(ctx, tx, connectionID, providerAccountID)
	if err != nil {
		return err
	}
	if _, err := s.txExecContext(ctx, tx, `DELETE FROM broker_account_balances WHERE account_id = ?`, accountID); err != nil {
		return err
	}
	syncedAt := nowRFC3339()
	inserted := 0
	for _, balance := range balances {
		currency := strings.ToUpper(strings.TrimSpace(balance.Currency))
		if currency == "" {
			return fmt.Errorf("currency is required for account %s", providerAccountID)
		}
		asOf := strings.TrimSpace(balance.AsOf)
		if asOf == "" {
			asOf = syncedAt
		}
		if _, err := s.txExecContext(ctx, tx, `
			INSERT INTO broker_account_balances (
				account_id, currency, total_cash_value, settled_cash,
				exchange_rate, is_base_currency, synced_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			accountID, currency, balance.Total, balance.Settled,
			balance.ExchangeRate, balance.IsBaseCurrency, asOf,
		); err != nil {
			return err
		}
		inserted++
	}
	if err := s.recordBrokerSyncSuccessTx(ctx, tx, connectionID, &accountID, providerAccountID, SyncDataCashBalances, inserted, syncedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReplaceBrokerConnectionAccountPositions(ctx context.Context, connectionID int64, providerAccountID string, positions []broker.Position) error {
	providerAccountID = strings.TrimSpace(providerAccountID)
	if connectionID == 0 || providerAccountID == "" {
		return fmt.Errorf("connection and account are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	accountID, err := s.brokerAccountIDTx(ctx, tx, connectionID, providerAccountID)
	if err != nil {
		return err
	}
	if _, err := s.txExecContext(ctx, tx, `DELETE FROM broker_asset_positions WHERE account_id = ?`, accountID); err != nil {
		return err
	}
	syncedAt := nowRFC3339()
	inserted := 0
	for _, position := range positions {
		symbol := strings.ToUpper(strings.TrimSpace(position.Symbol))
		if symbol == "" || position.Quantity == 0 {
			continue
		}
		marketPrice := position.MarketPrice
		if marketPrice == 0 {
			marketPrice = position.MarketValue / position.Quantity
		}
		assetType, assetKey := positionAssetIdentity(position)
		if _, err := s.txExecContext(ctx, tx, `
			INSERT INTO broker_asset_positions (
				account_id, asset_type, asset_key, symbol, name, conid, quantity,
				avg_cost, market_price, market_value, unrealized_pnl, realized_pnl,
				day_pnl, day_pnl_pct, currency, synced_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			accountID, assetType, assetKey, symbol, strings.TrimSpace(position.Name), nullableConID(position.ConID),
			position.Quantity, nullableFloat(position.AvgCost), nullableFloat(marketPrice),
			position.MarketValue, nullableFloat(position.Unrealized), nullableFloat(position.Realized),
			nullableFloatPtr(position.DailyPnL), nullableFloatPtr(position.DailyPnLPct),
			strings.ToUpper(strings.TrimSpace(position.Currency)), syncedAt,
		); err != nil {
			return err
		}
		inserted++
	}
	if err := s.recordBrokerSyncSuccessTx(ctx, tx, connectionID, &accountID, providerAccountID, SyncDataPositions, inserted, syncedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func nullableFloatPtr(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func (s *Store) ReplaceBrokerConnectionAccountPerformance(ctx context.Context, connectionID int64, performance broker.DailyPerformance) error {
	providerAccountID := strings.TrimSpace(performance.AccountID)
	if connectionID == 0 || providerAccountID == "" {
		return fmt.Errorf("connection and account are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	accountID, err := s.brokerAccountIDTx(ctx, tx, connectionID, providerAccountID)
	if err != nil {
		return err
	}
	attemptedAt := nowRFC3339()
	projectionAt := strings.TrimSpace(performance.AsOf)
	if projectionAt == "" {
		projectionAt = attemptedAt
	}
	if _, err := s.txExecContext(ctx, tx, `
		INSERT INTO broker_account_performance (
			account_id, daily_pnl, net_liquidation, unrealized_pnl,
			excess_liquidity, market_value, synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id) DO UPDATE SET
			daily_pnl = excluded.daily_pnl,
			net_liquidation = excluded.net_liquidation,
			unrealized_pnl = excluded.unrealized_pnl,
			excess_liquidity = excluded.excess_liquidity,
			market_value = excluded.market_value,
			synced_at = excluded.synced_at`,
		accountID, performance.DailyPnL, performance.NetLiquidation,
		performance.UnrealizedPnL, performance.ExcessLiquidity,
		performance.MarketValue, projectionAt,
	); err != nil {
		return err
	}
	if err := s.recordBrokerSyncSuccessTx(ctx, tx, connectionID, &accountID, providerAccountID, SyncDataDailyPerformance, 1, attemptedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) brokerAccountIDTx(ctx context.Context, tx *sql.Tx, connectionID int64, providerAccountID string) (int64, error) {
	var accountID int64
	err := tx.QueryRowContext(ctx, s.bind(`
		SELECT a.id
		FROM broker_accounts a
		JOIN broker_account_connections ac ON ac.account_id = a.id
		JOIN broker_connections c ON c.id = ac.connection_id
		WHERE ac.connection_id = ? AND a.provider_code = c.provider_code
			AND a.provider_account_id = ?`), connectionID, providerAccountID).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return accountID, err
}

type BrokerAccount struct {
	ID                          int64   `json:"id"`
	Broker                      string  `json:"broker"`
	ProviderAccountID           string  `json:"provider_account_id"`
	FirstDiscoveredConnectionID *int64  `json:"first_discovered_connection_id,omitempty"`
	PrimaryConnectionID         *int64  `json:"primary_connection_id,omitempty"`
	ConnectionIDs               []int64 `json:"connection_ids"`
	MaskedAccountNumber         string  `json:"masked_account_number,omitempty"`
	DisplayName                 string  `json:"display_name"`
	AccountType                 string  `json:"account_type"`
	Status                      string  `json:"status"`
	BaseCurrency                string  `json:"base_currency"`
	FirstDiscoveredAt           string  `json:"first_discovered_at"`
	LastSeenAt                  string  `json:"last_seen_at"`
	SyncedAt                    string  `json:"synced_at"`
}

func (s *Store) ListBrokerAccounts(ctx context.Context) ([]BrokerAccount, error) {
	return s.listBrokerAccounts(ctx, nil)
}

func (s *Store) ListBrokerAccountsByConnection(ctx context.Context, connectionID int64) ([]BrokerAccount, error) {
	if connectionID <= 0 {
		return nil, fmt.Errorf("connection is required")
	}
	return s.listBrokerAccounts(ctx, &connectionID)
}

func (s *Store) listBrokerAccounts(ctx context.Context, connectionID *int64) ([]BrokerAccount, error) {
	connectionIDsAggregate := "GROUP_CONCAT(ac.connection_id)"
	if s.dialect == dialectPostgres {
		connectionIDsAggregate = "STRING_AGG(ac.connection_id::text, ',' ORDER BY ac.connection_id)"
	}
	query := fmt.Sprintf(`
		SELECT a.id, a.provider_code, a.provider_account_id,
			a.first_discovered_connection_id,
			MAX(CASE WHEN ac.is_primary = 1 THEN ac.connection_id END),
			%s, a.masked_account_number,
			a.display_name, a.account_type, a.status, a.currency,
			a.first_discovered_at, a.last_seen_at, a.synced_at
		FROM broker_accounts a
		LEFT JOIN broker_account_connections ac ON ac.account_id = a.id
		WHERE (? IS NULL OR EXISTS (
			SELECT 1 FROM broker_account_connections requested
			WHERE requested.account_id = a.id AND requested.connection_id = ?
		))
		GROUP BY a.id, a.provider_code, a.provider_account_id,
			a.first_discovered_connection_id, a.masked_account_number,
			a.display_name, a.account_type, a.status, a.currency,
			a.first_discovered_at, a.last_seen_at, a.synced_at
		ORDER BY a.provider_code, a.provider_account_id`, connectionIDsAggregate)
	var filter any
	if connectionID != nil {
		filter = *connectionID
	}
	rows, err := s.queryContext(ctx, query, filter, filter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BrokerAccount{}
	for rows.Next() {
		var account BrokerAccount
		var firstDiscovered, primary sql.NullInt64
		var connectionIDs sql.NullString
		if err := rows.Scan(
			&account.ID, &account.Broker, &account.ProviderAccountID,
			&firstDiscovered, &primary, &connectionIDs, &account.MaskedAccountNumber,
			&account.DisplayName, &account.AccountType, &account.Status, &account.BaseCurrency,
			&account.FirstDiscoveredAt, &account.LastSeenAt, &account.SyncedAt,
		); err != nil {
			return nil, err
		}
		if firstDiscovered.Valid {
			id := firstDiscovered.Int64
			account.FirstDiscoveredConnectionID = &id
		}
		if primary.Valid {
			id := primary.Int64
			account.PrimaryConnectionID = &id
		}
		account.ConnectionIDs = parseInt64CSV(connectionIDs.String)
		out = append(out, account)
	}
	return out, rows.Err()
}

type BrokerAccountPerformance struct {
	AccountID         int64   `json:"account_id"`
	ConnectionID      int64   `json:"primary_connection_id"`
	Broker            string  `json:"broker"`
	ProviderAccountID string  `json:"provider_account_id"`
	DailyPnL          float64 `json:"daily_pnl"`
	NetLiquidation    float64 `json:"net_liquidation"`
	UnrealizedPnL     float64 `json:"unrealized_pnl"`
	ExcessLiquidity   float64 `json:"excess_liquidity"`
	MarketValue       float64 `json:"market_value"`
	SyncedAt          string  `json:"synced_at"`
}

func (s *Store) ListBrokerAccountPerformance(ctx context.Context) ([]BrokerAccountPerformance, error) {
	rows, err := s.queryContext(ctx, `
		SELECT x.account_id, COALESCE(ac.connection_id, 0), a.provider_code, a.provider_account_id,
			x.daily_pnl, x.net_liquidation, x.unrealized_pnl,
			x.excess_liquidity, x.market_value, x.synced_at
		FROM broker_account_performance x
		JOIN broker_accounts a ON a.id = x.account_id
		LEFT JOIN broker_account_connections ac ON ac.account_id = a.id AND ac.is_primary = 1
		ORDER BY a.provider_code, a.provider_account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BrokerAccountPerformance{}
	for rows.Next() {
		var performance BrokerAccountPerformance
		if err := rows.Scan(
			&performance.AccountID, &performance.ConnectionID, &performance.Broker,
			&performance.ProviderAccountID, &performance.DailyPnL, &performance.NetLiquidation,
			&performance.UnrealizedPnL, &performance.ExcessLiquidity,
			&performance.MarketValue, &performance.SyncedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, performance)
	}
	return out, rows.Err()
}

func parseInt64CSV(value string) []int64 {
	if strings.TrimSpace(value) == "" {
		return []int64{}
	}
	parts := strings.Split(value, ",")
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}
