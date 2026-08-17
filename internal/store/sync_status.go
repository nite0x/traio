package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type SyncDataType string

const (
	SyncDataAccounts         SyncDataType = "accounts"
	SyncDataAccountDetails   SyncDataType = "account_details"
	SyncDataCashBalances     SyncDataType = "cash_balances"
	SyncDataPositions        SyncDataType = "positions"
	SyncDataDailyPerformance SyncDataType = "daily_performance"
)

// BrokerSyncStatus is the latest status for one connection/account/data type.
// Account is empty only for connection-wide resources such as account discovery.
type BrokerSyncStatus struct {
	ConnectionID  int64        `json:"connection_id"`
	AccountID     *int64       `json:"account_id,omitempty"`
	Broker        string       `json:"broker"`
	Account       string       `json:"account,omitempty"`
	DataType      SyncDataType `json:"data_type"`
	SyncedAt      string       `json:"synced_at"`
	LastAttemptAt string       `json:"last_attempt_at"`
	LastError     string       `json:"last_error,omitempty"`
	ItemCount     int          `json:"item_count"`
}

func (s *Store) recordBrokerSyncSuccessTx(
	ctx context.Context,
	tx *sql.Tx,
	connectionID int64,
	accountID *int64,
	accountScope string,
	dataType SyncDataType,
	itemCount int,
	syncedAt string,
) error {
	var storedAccountID any
	if accountID != nil {
		storedAccountID = *accountID
	}
	_, err := s.txExecContext(ctx, tx, `
		INSERT INTO broker_sync_status (
			connection_id, account_id, account_scope, data_type,
			synced_at, last_attempt_at, last_error, item_count
		) VALUES (?, ?, ?, ?, ?, ?, '', ?)
		ON CONFLICT(connection_id, account_scope, data_type) DO UPDATE SET
			account_id = excluded.account_id,
			synced_at = excluded.synced_at,
			last_attempt_at = excluded.last_attempt_at,
			last_error = '',
			item_count = excluded.item_count`,
		connectionID, storedAccountID, accountScope, dataType, syncedAt, syncedAt, itemCount,
	)
	return err
}

func (s *Store) RecordBrokerConnectionSyncError(
	ctx context.Context,
	connectionID int64,
	accountScope string,
	dataType SyncDataType,
	syncErr error,
) error {
	accountScope = strings.TrimSpace(accountScope)
	if connectionID == 0 {
		return fmt.Errorf("connection is required")
	}
	if dataType == "" {
		return fmt.Errorf("sync data type is required")
	}
	var accountID any
	if accountScope != "" {
		var id int64
		err := s.queryRowContext(ctx, `
			SELECT a.id
			FROM broker_accounts a
			JOIN broker_account_connections ac ON ac.account_id = a.id
			JOIN broker_connections c ON c.id = ac.connection_id
			WHERE ac.connection_id = ? AND a.provider_code = c.provider_code
				AND a.provider_account_id = ?`, connectionID, accountScope).Scan(&id)
		if err == nil {
			accountID = id
		} else if err != sql.ErrNoRows {
			return err
		}
	}
	attemptedAt := nowRFC3339()
	message := ""
	if syncErr != nil {
		message = syncErr.Error()
	}
	_, err := s.execContext(ctx, `
		INSERT INTO broker_sync_status (
			connection_id, account_id, account_scope, data_type,
			synced_at, last_attempt_at, last_error, item_count
		) VALUES (?, ?, ?, ?, '', ?, ?, 0)
		ON CONFLICT(connection_id, account_scope, data_type) DO UPDATE SET
			account_id = excluded.account_id,
			last_attempt_at = excluded.last_attempt_at,
			last_error = excluded.last_error`,
		connectionID, accountID, accountScope, dataType, attemptedAt, message,
	)
	return err
}

func (s *Store) ListBrokerSyncStatuses(ctx context.Context) ([]BrokerSyncStatus, error) {
	rows, err := s.queryContext(ctx, `
		SELECT x.connection_id, x.account_id, c.provider_code,
			COALESCE(a.provider_account_id, x.account_scope), x.data_type,
			x.synced_at, x.last_attempt_at, x.last_error, x.item_count
		FROM broker_sync_status x
		JOIN broker_connections c ON c.id = x.connection_id
		LEFT JOIN broker_accounts a ON a.id = x.account_id
		ORDER BY c.provider_code, x.connection_id, x.account_scope, x.data_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BrokerSyncStatus{}
	for rows.Next() {
		var status BrokerSyncStatus
		var accountID sql.NullInt64
		if err := rows.Scan(
			&status.ConnectionID, &accountID, &status.Broker, &status.Account,
			&status.DataType, &status.SyncedAt, &status.LastAttemptAt,
			&status.LastError, &status.ItemCount,
		); err != nil {
			return nil, err
		}
		if accountID.Valid {
			id := accountID.Int64
			status.AccountID = &id
		}
		out = append(out, status)
	}
	return out, rows.Err()
}
