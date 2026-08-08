package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nite/traio/internal/broker"
)

// ReplaceBrokerAccounts refreshes broker account discovery independently from
// account-specific data. Accounts no longer returned are removed with their
// projections and sync statuses.
func (s *Store) ReplaceBrokerAccounts(ctx context.Context, brokerName string, accounts []broker.Account) error {
	brokerName = strings.ToUpper(strings.TrimSpace(brokerName))
	if brokerName == "" {
		return fmt.Errorf("broker is required")
	}
	if len(accounts) == 0 {
		return fmt.Errorf("at least one account is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	existing := map[string]struct{}{}
	rows, err := s.txQueryContext(ctx, tx, `SELECT account FROM broker_accounts WHERE broker = ?`, brokerName)
	if err != nil {
		return err
	}
	for rows.Next() {
		var accountID string
		if err := rows.Scan(&accountID); err != nil {
			_ = rows.Close()
			return err
		}
		existing[accountID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	syncedAt := time.Now().UTC().Format(time.RFC3339)
	seen := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		accountID := strings.TrimSpace(account.ID)
		if accountID == "" {
			return fmt.Errorf("account is required")
		}
		if _, duplicate := seen[accountID]; duplicate {
			return fmt.Errorf("duplicate account %s", accountID)
		}
		seen[accountID] = struct{}{}
		if _, err := s.txExecContext(ctx, tx, `
			INSERT INTO broker_accounts (
				broker, account, display_name, account_type, status, currency, synced_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(broker, account) DO UPDATE SET
				display_name = CASE WHEN excluded.display_name = '' THEN broker_accounts.display_name ELSE excluded.display_name END,
				account_type = CASE WHEN excluded.account_type = '' THEN broker_accounts.account_type ELSE excluded.account_type END,
				status = CASE WHEN excluded.status = '' THEN broker_accounts.status ELSE excluded.status END,
				currency = CASE WHEN excluded.currency = '' THEN broker_accounts.currency ELSE excluded.currency END,
				synced_at = excluded.synced_at`,
			brokerName, accountID, strings.TrimSpace(account.DisplayName),
			strings.TrimSpace(account.AccountType), strings.TrimSpace(account.Status),
			strings.ToUpper(strings.TrimSpace(account.BaseCurrency)), syncedAt,
		); err != nil {
			return err
		}
	}

	for accountID := range existing {
		if _, stillVisible := seen[accountID]; stillVisible {
			continue
		}
		if _, err := s.txExecContext(ctx, tx, `DELETE FROM broker_accounts WHERE broker = ? AND account = ?`, brokerName, accountID); err != nil {
			return err
		}
		if _, err := s.txExecContext(ctx, tx, `DELETE FROM broker_sync_status WHERE broker = ? AND account = ?`, brokerName, accountID); err != nil {
			return err
		}
	}

	if err := s.recordBrokerSyncSuccessTx(ctx, tx, brokerName, "", SyncDataAccounts, len(accounts), syncedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReplaceBrokerAccountDetails(ctx context.Context, brokerName string, account broker.Account) error {
	brokerName = strings.ToUpper(strings.TrimSpace(brokerName))
	accountID := strings.TrimSpace(account.ID)
	if brokerName == "" || accountID == "" {
		return fmt.Errorf("broker and account are required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	syncedAt := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.txExecContext(ctx, tx, `
		INSERT INTO broker_accounts (
			broker, account, display_name, account_type, status, currency, synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(broker, account) DO UPDATE SET
			display_name = excluded.display_name,
			account_type = excluded.account_type,
			status = excluded.status,
			currency = excluded.currency,
			synced_at = excluded.synced_at`,
		brokerName, accountID, strings.TrimSpace(account.DisplayName),
		strings.TrimSpace(account.AccountType), strings.TrimSpace(account.Status),
		strings.ToUpper(strings.TrimSpace(account.BaseCurrency)), syncedAt,
	); err != nil {
		return err
	}
	if err := s.recordBrokerSyncSuccessTx(ctx, tx, brokerName, accountID, SyncDataAccountDetails, 1, syncedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReplaceBrokerCashBalances(ctx context.Context, brokerName, accountID string, balances []broker.CashBalance) error {
	brokerName = strings.ToUpper(strings.TrimSpace(brokerName))
	accountID = strings.TrimSpace(accountID)
	if brokerName == "" || accountID == "" {
		return fmt.Errorf("broker and account are required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := s.txExecContext(ctx, tx, `DELETE FROM broker_account_balances WHERE broker = ? AND account = ?`, brokerName, accountID); err != nil {
		return err
	}
	syncedAt := time.Now().UTC().Format(time.RFC3339)
	inserted := 0
	for _, balance := range balances {
		currency := strings.ToUpper(strings.TrimSpace(balance.Currency))
		if currency == "" {
			return fmt.Errorf("currency is required for account %s", accountID)
		}
		asOf := strings.TrimSpace(balance.AsOf)
		if asOf == "" {
			asOf = syncedAt
		}
		if _, err := s.txExecContext(ctx, tx, `
			INSERT INTO broker_account_balances (
				broker, account, currency, total_cash_value, settled_cash,
				exchange_rate, is_base_currency, synced_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			brokerName, accountID, currency, balance.Total, balance.Settled,
			balance.ExchangeRate, balance.IsBaseCurrency, asOf,
		); err != nil {
			return err
		}
		inserted++
	}
	if err := s.recordBrokerSyncSuccessTx(ctx, tx, brokerName, accountID, SyncDataCashBalances, inserted, syncedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReplaceBrokerAccountPositions(ctx context.Context, brokerName, accountID string, positions []broker.Position) error {
	brokerName = strings.ToUpper(strings.TrimSpace(brokerName))
	accountID = strings.TrimSpace(accountID)
	if brokerName == "" || accountID == "" {
		return fmt.Errorf("broker and account are required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := s.txExecContext(ctx, tx, `DELETE FROM broker_asset_positions WHERE broker = ? AND account = ?`, brokerName, accountID); err != nil {
		return err
	}
	syncedAt := time.Now().UTC().Format(time.RFC3339)
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
				broker, account, asset_type, asset_key, symbol, conid, quantity,
				avg_cost, market_price, market_value, unrealized_pnl, realized_pnl,
				currency, synced_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			brokerName, accountID, assetType, assetKey, symbol, nullableConID(position.ConID),
			position.Quantity, nullableFloat(position.AvgCost), nullableFloat(marketPrice),
			position.MarketValue, nullableFloat(position.Unrealized), nullableFloat(position.Realized),
			strings.ToUpper(strings.TrimSpace(position.Currency)), syncedAt,
		); err != nil {
			return err
		}
		inserted++
	}
	if err := s.recordBrokerSyncSuccessTx(ctx, tx, brokerName, accountID, SyncDataPositions, inserted, syncedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReplaceBrokerAccountPerformance(ctx context.Context, brokerName string, performance broker.DailyPerformance) error {
	brokerName = strings.ToUpper(strings.TrimSpace(brokerName))
	accountID := strings.TrimSpace(performance.AccountID)
	if brokerName == "" || accountID == "" {
		return fmt.Errorf("broker and account are required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	attemptedAt := time.Now().UTC().Format(time.RFC3339)
	projectionAt := strings.TrimSpace(performance.AsOf)
	if projectionAt == "" {
		projectionAt = attemptedAt
	}
	if _, err := s.txExecContext(ctx, tx, `
		INSERT INTO broker_account_performance (
			broker, account, daily_pnl, net_liquidation, unrealized_pnl,
			excess_liquidity, market_value, synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(broker, account) DO UPDATE SET
			daily_pnl = excluded.daily_pnl,
			net_liquidation = excluded.net_liquidation,
			unrealized_pnl = excluded.unrealized_pnl,
			excess_liquidity = excluded.excess_liquidity,
			market_value = excluded.market_value,
			synced_at = excluded.synced_at`,
		brokerName, accountID, performance.DailyPnL, performance.NetLiquidation,
		performance.UnrealizedPnL, performance.ExcessLiquidity,
		performance.MarketValue, projectionAt,
	); err != nil {
		return err
	}
	if err := s.recordBrokerSyncSuccessTx(ctx, tx, brokerName, accountID, SyncDataDailyPerformance, 1, attemptedAt); err != nil {
		return err
	}
	return tx.Commit()
}

type BrokerAccount struct {
	Broker       string `json:"broker"`
	Account      string `json:"account"`
	DisplayName  string `json:"display_name"`
	AccountType  string `json:"account_type"`
	Status       string `json:"status"`
	BaseCurrency string `json:"base_currency"`
	SyncedAt     string `json:"synced_at"`
}

func (s *Store) ListBrokerAccounts(ctx context.Context) ([]BrokerAccount, error) {
	rows, err := s.queryContext(ctx, `
		SELECT broker, account, display_name, account_type, status, currency, synced_at
		FROM broker_accounts
		ORDER BY broker, account`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BrokerAccount{}
	for rows.Next() {
		var account BrokerAccount
		if err := rows.Scan(
			&account.Broker, &account.Account, &account.DisplayName,
			&account.AccountType, &account.Status, &account.BaseCurrency,
			&account.SyncedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, account)
	}
	return out, rows.Err()
}

type BrokerAccountPerformance struct {
	Broker          string  `json:"broker"`
	Account         string  `json:"account"`
	DailyPnL        float64 `json:"daily_pnl"`
	NetLiquidation  float64 `json:"net_liquidation"`
	UnrealizedPnL   float64 `json:"unrealized_pnl"`
	ExcessLiquidity float64 `json:"excess_liquidity"`
	MarketValue     float64 `json:"market_value"`
	SyncedAt        string  `json:"synced_at"`
}

func (s *Store) ListBrokerAccountPerformance(ctx context.Context) ([]BrokerAccountPerformance, error) {
	rows, err := s.queryContext(ctx, `
		SELECT broker, account, daily_pnl, net_liquidation, unrealized_pnl,
			excess_liquidity, market_value, synced_at
		FROM broker_account_performance
		ORDER BY broker, account`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BrokerAccountPerformance{}
	for rows.Next() {
		var performance BrokerAccountPerformance
		if err := rows.Scan(
			&performance.Broker, &performance.Account, &performance.DailyPnL,
			&performance.NetLiquidation, &performance.UnrealizedPnL,
			&performance.ExcessLiquidity, &performance.MarketValue,
			&performance.SyncedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, performance)
	}
	return out, rows.Err()
}
