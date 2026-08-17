package portfolio

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/store"
)

// Snapshot is a database-only view of the latest successful broker projections.
// Reading it never invokes a broker adapter.
type Snapshot struct {
	Summary      SnapshotSummary       `json:"summary"`
	Allocations  []SnapshotAllocation  `json:"allocations"`
	Positions    []AggregatedPosition  `json:"positions"`
	CashBalances []SnapshotCashBalance `json:"cash_balances"`
	Warnings     []string              `json:"warnings"`
}

type SnapshotSummary struct {
	NetAssetValue        float64  `json:"net_asset_value"`
	AvailableCash        float64  `json:"available_cash"`
	HoldingsValue        float64  `json:"holdings_value"`
	UnrealizedPnL        float64  `json:"unrealized_pnl"`
	UnrealizedPnLPercent *float64 `json:"unrealized_pnl_percent,omitempty"`
	DailyPnL             float64  `json:"daily_pnl"`
	DailyPnLPercent      *float64 `json:"daily_pnl_percent,omitempty"`
	BaseCurrency         string   `json:"base_currency"`
	UpdatedAt            string   `json:"updated_at,omitempty"`
	SyncedAccounts       int      `json:"synced_accounts"`
}

type SnapshotAllocation struct {
	Broker     string  `json:"broker"`
	Amount     float64 `json:"amount"`
	Percentage float64 `json:"percentage"`
}

type SnapshotCashBalance struct {
	Broker         string  `json:"broker"`
	Account        string  `json:"account"`
	Currency       string  `json:"currency"`
	Total          float64 `json:"total"`
	Settled        float64 `json:"settled"`
	ExchangeRate   float64 `json:"exchange_rate"`
	IsBaseCurrency bool    `json:"is_base_currency"`
	SyncedAt       string  `json:"synced_at"`
}

// Snapshot reads and aggregates the latest database projections for the
// overview, holdings, and cash pages.
func (s *SyncService) Snapshot(ctx context.Context) (Snapshot, error) {
	if s.store == nil {
		return Snapshot{}, fmt.Errorf("broker store is not available")
	}

	accounts, err := s.store.ListBrokerAccounts(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list broker accounts: %w", err)
	}
	balances, err := s.store.ListBrokerAccountBalances(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list broker cash balances: %w", err)
	}
	performances, err := s.store.ListBrokerAccountPerformance(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list broker performance: %w", err)
	}
	positions, err := s.store.ListBrokerPositions(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list broker positions: %w", err)
	}
	statuses, err := s.store.ListBrokerSyncStatuses(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list broker sync statuses: %w", err)
	}

	result := Snapshot{
		Summary:      SnapshotSummary{BaseCurrency: snapshotBaseCurrency(accounts)},
		Allocations:  []SnapshotAllocation{},
		CashBalances: make([]SnapshotCashBalance, 0, len(balances)),
		Warnings:     []string{},
	}
	warnings := map[string]struct{}{}
	if hasMixedBaseCurrencies(accounts) {
		warnings["accounts use different base currencies; aggregate values may not be directly comparable"] = struct{}{}
	}

	accountHasBalance := map[int64]bool{}
	cashByAccount := map[int64]float64{}
	for _, balance := range balances {
		result.CashBalances = append(result.CashBalances, SnapshotCashBalance{
			Broker: balance.Broker, Account: balance.Account, Currency: balance.Currency,
			Total: balance.TotalCashValue, Settled: balance.SettledCash,
			ExchangeRate: balance.ExchangeRate, IsBaseCurrency: balance.IsBaseCurrency,
			SyncedAt: balance.SyncedAt,
		})
		accountHasBalance[balance.AccountID] = true
		converted, ok := cashInBaseCurrency(balance, result.Summary.BaseCurrency)
		if ok {
			cashByAccount[balance.AccountID] += converted
		} else {
			warnings[fmt.Sprintf("%s %s cash has no usable exchange rate", balance.Broker, balance.Currency)] = struct{}{}
		}
	}

	positionValueByAccount := map[int64]float64{}
	positionPnLByAccount := map[int64]float64{}
	accountHasPositions := map[int64]bool{}
	for _, position := range positions {
		positionValueByAccount[position.BrokerAccountID] += position.MarketValue
		positionPnLByAccount[position.BrokerAccountID] += position.Unrealized
		accountHasPositions[position.BrokerAccountID] = true
	}

	performanceByAccount := make(map[int64]store.BrokerAccountPerformance, len(performances))
	for _, performance := range performances {
		performanceByAccount[performance.AccountID] = performance
	}

	allocationAmounts := map[string]float64{}
	syncedAccounts := map[int64]bool{}
	oldestProjection := ""
	for _, account := range accounts {
		performance, ok := performanceByAccount[account.ID]
		if ok {
			result.Summary.NetAssetValue += performance.NetLiquidation
			result.Summary.HoldingsValue += performance.MarketValue
			result.Summary.UnrealizedPnL += performance.UnrealizedPnL
			result.Summary.DailyPnL += performance.DailyPnL
			allocationAmounts[account.Broker] += performance.NetLiquidation
			oldestProjection = olderTimestamp(oldestProjection, performance.SyncedAt)
			syncedAccounts[account.ID] = true
		} else {
			holdings := positionValueByAccount[account.ID]
			cash := cashByAccount[account.ID]
			result.Summary.HoldingsValue += holdings
			result.Summary.UnrealizedPnL += positionPnLByAccount[account.ID]
			result.Summary.NetAssetValue += holdings + cash
			allocationAmounts[account.Broker] += holdings + cash
			warnings[fmt.Sprintf("%s %s has no daily performance projection", account.Broker, account.ProviderAccountID)] = struct{}{}
		}
		if accountHasBalance[account.ID] {
			oldestProjection = olderTimestamp(oldestProjection, latestBalanceTimestamp(balances, account.ID))
			syncedAccounts[account.ID] = true
		} else {
			warnings[fmt.Sprintf("%s %s has no cash balance projection", account.Broker, account.ProviderAccountID)] = struct{}{}
		}
		if accountHasPositions[account.ID] {
			oldestProjection = olderTimestamp(oldestProjection, latestPositionTimestamp(positions, account.ID))
			syncedAccounts[account.ID] = true
		}
	}
	result.Summary.AvailableCash = sumCash(cashByAccount)
	result.Summary.UpdatedAt = oldestProjection
	result.Summary.SyncedAccounts = len(syncedAccounts)
	result.Summary.UnrealizedPnLPercent = percentOfCost(result.Summary.UnrealizedPnL, result.Summary.HoldingsValue)
	result.Summary.DailyPnLPercent = percentOfPriorValue(result.Summary.DailyPnL, result.Summary.NetAssetValue)
	result.Positions, err = aggregatePositions(positions, result.Summary.NetAssetValue)
	if err != nil {
		return Snapshot{}, fmt.Errorf("aggregate positions: %w", err)
	}

	for brokerName, amount := range allocationAmounts {
		percentage := 0.0
		if result.Summary.NetAssetValue != 0 {
			percentage = amount / result.Summary.NetAssetValue * 100
		}
		result.Allocations = append(result.Allocations, SnapshotAllocation{
			Broker: brokerName, Amount: amount, Percentage: percentage,
		})
	}
	sort.Slice(result.Allocations, func(i, j int) bool {
		if result.Allocations[i].Amount == result.Allocations[j].Amount {
			return result.Allocations[i].Broker < result.Allocations[j].Broker
		}
		return result.Allocations[i].Amount > result.Allocations[j].Amount
	})

	for _, status := range statuses {
		if strings.TrimSpace(status.LastError) == "" {
			continue
		}
		account := strings.TrimSpace(status.Account)
		if account != "" {
			account = " " + account
		}
		warnings[fmt.Sprintf("%s%s %s sync failed: %s", status.Broker, account, status.DataType, status.LastError)] = struct{}{}
	}
	for warning := range warnings {
		result.Warnings = append(result.Warnings, warning)
	}
	sort.Strings(result.Warnings)
	return result, nil
}

func snapshotBaseCurrency(accounts []store.BrokerAccount) string {
	currencies := make([]string, 0, len(accounts))
	seen := map[string]struct{}{}
	for _, account := range accounts {
		currency := strings.ToUpper(strings.TrimSpace(account.BaseCurrency))
		if currency == "" {
			continue
		}
		if _, ok := seen[currency]; ok {
			continue
		}
		seen[currency] = struct{}{}
		currencies = append(currencies, currency)
	}
	if len(currencies) == 0 {
		return "USD"
	}
	sort.Strings(currencies)
	return currencies[0]
}

func hasMixedBaseCurrencies(accounts []store.BrokerAccount) bool {
	seen := map[string]struct{}{}
	for _, account := range accounts {
		currency := strings.ToUpper(strings.TrimSpace(account.BaseCurrency))
		if currency != "" {
			seen[currency] = struct{}{}
		}
	}
	return len(seen) > 1
}

func cashInBaseCurrency(balance store.BrokerAccountBalance, baseCurrency string) (float64, bool) {
	if balance.IsBaseCurrency || strings.EqualFold(balance.Currency, baseCurrency) {
		return balance.TotalCashValue, true
	}
	if balance.ExchangeRate <= 0 {
		return 0, false
	}
	return balance.TotalCashValue / balance.ExchangeRate, true
}

func sumCash(values map[int64]float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total
}

func percentOfCost(pnl, marketValue float64) *float64 {
	cost := marketValue - pnl
	if cost == 0 {
		return nil
	}
	value := pnl / cost * 100
	return &value
}

func percentOfPriorValue(pnl, currentValue float64) *float64 {
	prior := currentValue - pnl
	if prior == 0 {
		return nil
	}
	value := pnl / prior * 100
	return &value
}

func olderTimestamp(current, candidate string) string {
	if candidate == "" {
		return current
	}
	if current == "" || candidate < current {
		return candidate
	}
	return current
}

func latestBalanceTimestamp(balances []store.BrokerAccountBalance, accountID int64) string {
	latest := ""
	for _, balance := range balances {
		if balance.AccountID == accountID && balance.SyncedAt > latest {
			latest = balance.SyncedAt
		}
	}
	return latest
}

func latestPositionTimestamp(positions []broker.Position, accountID int64) string {
	latest := ""
	for _, position := range positions {
		if position.BrokerAccountID == accountID && position.SyncedAt > latest {
			latest = position.SyncedAt
		}
	}
	return latest
}
