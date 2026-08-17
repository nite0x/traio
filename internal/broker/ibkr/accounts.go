package ibkr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/nite/traio/internal/broker"
)

type accountRecord struct {
	ID             string `json:"id"`
	AccountID      string `json:"accountId"`
	AccountTitle   string `json:"accountTitle"`
	DisplayName    string `json:"displayName"`
	AccountAlias   string `json:"accountAlias"`
	Currency       string `json:"currency"`
	Type           string `json:"type"`
	ClearingStatus string `json:"clearingStatus"`
}

type dailyPnLEntry struct {
	DailyPnL        float64 `json:"dpl"`
	NetLiquidation  float64 `json:"nl"`
	UnrealizedPnL   float64 `json:"upl"`
	ExcessLiquidity float64 `json:"el"`
	MarketValue     float64 `json:"mv"`
}

// ListAccounts returns every portfolio account visible to the IBKR session.
func (c *Client) ListAccounts(ctx context.Context) ([]broker.Account, error) {
	var raw []accountRecord
	if err := c.getGatewayJSON(ctx, "/portfolio/accounts", "accounts", &raw); err != nil {
		return nil, err
	}

	out := make([]broker.Account, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, account := range raw {
		normalized := normalizeAccount(account)
		if normalized.ID == "" {
			continue
		}
		if _, ok := seen[normalized.ID]; ok {
			continue
		}
		seen[normalized.ID] = struct{}{}
		out = append(out, normalized)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ibkr: no accounts returned")
	}
	return out, nil
}

// GetAccount returns metadata for one IBKR account.
func (c *Client) GetAccount(ctx context.Context, accountID string) (broker.Account, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return broker.Account{}, fmt.Errorf("ibkr: account ID is required")
	}

	var payload json.RawMessage
	path := "/portfolio/" + url.PathEscape(accountID) + "/meta"
	if err := c.getGatewayJSON(ctx, path, "account metadata", &payload); err != nil {
		return broker.Account{}, err
	}
	record, err := decodeAccountRecord(payload)
	if err != nil {
		return broker.Account{}, fmt.Errorf("ibkr: decode account metadata: %w", err)
	}
	account := normalizeAccount(record)
	if account.ID == "" {
		account.ID = accountID
	}
	if account.ID != accountID {
		return broker.Account{}, fmt.Errorf("ibkr: account %s metadata returned ID %s", accountID, account.ID)
	}
	return account, nil
}

// GetCashBalances returns IBKR ledger cash values grouped by currency.
func (c *Client) GetCashBalances(ctx context.Context, accountID string) ([]broker.CashBalance, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("ibkr: account ID is required")
	}

	var raw map[string]struct {
		CashBalance  float64 `json:"cashbalance"`
		SettledCash  float64 `json:"settledcash"`
		ExchangeRate float64 `json:"exchangerate"`
		Currency     string  `json:"currency"`
		SecondKey    string  `json:"secondkey"`
		Timestamp    int64   `json:"timestamp"`
	}
	path := "/portfolio/" + url.PathEscape(accountID) + "/ledger"
	if err := c.getGatewayJSON(ctx, path, "account ledger", &raw); err != nil {
		return nil, err
	}

	out := make([]broker.CashBalance, 0, len(raw))
	for key, value := range raw {
		currency := strings.ToUpper(strings.TrimSpace(firstNonEmpty(value.Currency, value.SecondKey, key)))
		// The ledger's BASE entry is an aggregate, not another currency balance.
		// Persisting it alongside the real currencies would double count cash.
		if currency == "" || strings.EqualFold(key, "BASE") || currency == "BASE" {
			continue
		}
		out = append(out, broker.CashBalance{
			AccountID:      accountID,
			Currency:       currency,
			Total:          value.CashBalance,
			Settled:        value.SettledCash,
			ExchangeRate:   value.ExchangeRate,
			IsBaseCurrency: value.ExchangeRate == 1,
			AsOf:           formatIBKRTimestamp(value.Timestamp),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsBaseCurrency != out[j].IsBaseCurrency {
			return out[i].IsBaseCurrency
		}
		return out[i].Currency < out[j].Currency
	})
	return out, nil
}

// GetDailyPerformance returns the account's current IBKR daily P&L snapshot.
func (c *Client) GetDailyPerformance(ctx context.Context, accountID string) (broker.DailyPerformance, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return broker.DailyPerformance{}, fmt.Errorf("ibkr: account ID is required")
	}

	updatedPnL, err := c.dailyPnLSnapshot(ctx)
	if err != nil {
		return broker.DailyPerformance{}, err
	}

	key := accountID + ".Core"
	value, ok := updatedPnL[key]
	if !ok {
		for candidate, candidateValue := range updatedPnL {
			if strings.EqualFold(candidate, accountID) || strings.HasPrefix(strings.ToUpper(candidate), strings.ToUpper(accountID)+".") {
				value = candidateValue
				ok = true
				break
			}
		}
	}
	if !ok {
		return broker.DailyPerformance{}, fmt.Errorf("ibkr: daily performance for account %s not returned", accountID)
	}

	return broker.DailyPerformance{
		AccountID:       accountID,
		DailyPnL:        value.DailyPnL,
		NetLiquidation:  value.NetLiquidation,
		UnrealizedPnL:   value.UnrealizedPnL,
		ExcessLiquidity: value.ExcessLiquidity,
		MarketValue:     value.MarketValue,
		AsOf:            time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (c *Client) dailyPnLSnapshot(ctx context.Context) (map[string]dailyPnLEntry, error) {
	c.pnlMu.Lock()
	defer c.pnlMu.Unlock()
	if len(c.pnlSnapshot) > 0 && time.Since(c.pnlFetchedAt) < 5*time.Second {
		return c.pnlSnapshot, nil
	}

	var raw struct {
		UpdatedPnL map[string]dailyPnLEntry `json:"upnl"`
	}
	if err := c.getGatewayJSON(ctx, "/iserver/account/pnl/partitioned", "daily performance", &raw); err != nil {
		return nil, err
	}
	c.pnlSnapshot = raw.UpdatedPnL
	c.pnlFetchedAt = time.Now()
	return c.pnlSnapshot, nil
}

func (c *Client) getGatewayJSON(ctx context.Context, path, label string, dst any) error {
	u := strings.TrimRight(c.cfg.GatewayURL, "/") + "/v1/api" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("ibkr: create %s request: %w", label, err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ibkr: %s request: %w", label, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("ibkr: gateway not authenticated")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ibkr: %s status %d", label, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("ibkr: decode %s: %w", label, err)
	}
	return nil
}

func normalizeAccount(raw accountRecord) broker.Account {
	id := strings.TrimSpace(firstNonEmpty(raw.AccountID, raw.ID))
	return broker.Account{
		ID:           id,
		Broker:       "IBKR",
		DisplayName:  strings.TrimSpace(firstNonEmpty(raw.AccountAlias, raw.DisplayName, raw.AccountTitle, id)),
		AccountType:  strings.TrimSpace(raw.Type),
		Status:       normalizeClearingStatus(raw.ClearingStatus),
		BaseCurrency: strings.ToUpper(strings.TrimSpace(raw.Currency)),
	}
}

func decodeAccountRecord(payload []byte) (accountRecord, error) {
	var record accountRecord
	if err := json.Unmarshal(payload, &record); err == nil && firstNonEmpty(record.AccountID, record.ID) != "" {
		return record, nil
	}
	var records []accountRecord
	if err := json.Unmarshal(payload, &records); err != nil {
		return accountRecord{}, err
	}
	if len(records) == 0 {
		return accountRecord{}, fmt.Errorf("empty metadata response")
	}
	return records[0], nil
}

func normalizeClearingStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "O":
		return "open"
	case "P", "N":
		return "pending"
	case "A":
		return "abandoned"
	case "R":
		return "rejected"
	case "C":
		return "closed"
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

func formatIBKRTimestamp(timestamp int64) string {
	if timestamp <= 0 {
		return time.Now().UTC().Format(time.RFC3339)
	}
	if timestamp > 1_000_000_000_000 {
		timestamp /= 1000
	}
	return time.Unix(timestamp, 0).UTC().Format(time.RFC3339)
}
