package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type SyncDataType string

const (
	SyncDataAccounts         SyncDataType = "accounts"
	SyncDataAccountDetails   SyncDataType = "account_details"
	SyncDataCashBalances     SyncDataType = "cash_balances"
	SyncDataPositions        SyncDataType = "positions"
	SyncDataDailyPerformance SyncDataType = "daily_performance"
)

// BrokerSyncStatus is the latest status for one broker/account/data type.
// Account is empty only for broker-wide resources such as account discovery.
type BrokerSyncStatus struct {
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
	brokerName, account string,
	dataType SyncDataType,
	itemCount int,
	syncedAt string,
) error {
	_, err := s.txExecContext(ctx, tx, `
		INSERT INTO broker_sync_status (
			broker, account, data_type, synced_at, last_attempt_at, last_error, item_count
		) VALUES (?, ?, ?, ?, ?, '', ?)
		ON CONFLICT(broker, account, data_type) DO UPDATE SET
			synced_at = excluded.synced_at,
			last_attempt_at = excluded.last_attempt_at,
			last_error = '',
			item_count = excluded.item_count`,
		brokerName, account, dataType, syncedAt, syncedAt, itemCount,
	)
	return err
}

func (s *Store) RecordBrokerSyncError(
	ctx context.Context,
	brokerName, account string,
	dataType SyncDataType,
	syncErr error,
) error {
	brokerName = strings.ToUpper(strings.TrimSpace(brokerName))
	account = strings.TrimSpace(account)
	if brokerName == "" {
		return fmt.Errorf("broker is required")
	}
	if dataType == "" {
		return fmt.Errorf("sync data type is required")
	}
	attemptedAt := time.Now().UTC().Format(time.RFC3339)
	message := ""
	if syncErr != nil {
		message = syncErr.Error()
	}
	_, err := s.execContext(ctx, `
		INSERT INTO broker_sync_status (
			broker, account, data_type, synced_at, last_attempt_at, last_error, item_count
		) VALUES (?, ?, ?, '', ?, ?, 0)
		ON CONFLICT(broker, account, data_type) DO UPDATE SET
			last_attempt_at = excluded.last_attempt_at,
			last_error = excluded.last_error`,
		brokerName, account, dataType, attemptedAt, message,
	)
	return err
}

func (s *Store) ListBrokerSyncStatuses(ctx context.Context) ([]BrokerSyncStatus, error) {
	rows, err := s.queryContext(ctx, `
		SELECT broker, account, data_type, synced_at, last_attempt_at, last_error, item_count
		FROM broker_sync_status
		ORDER BY broker, account, data_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BrokerSyncStatus{}
	for rows.Next() {
		var status BrokerSyncStatus
		if err := rows.Scan(
			&status.Broker, &status.Account, &status.DataType, &status.SyncedAt,
			&status.LastAttemptAt, &status.LastError, &status.ItemCount,
		); err != nil {
			return nil, err
		}
		out = append(out, status)
	}
	return out, rows.Err()
}
