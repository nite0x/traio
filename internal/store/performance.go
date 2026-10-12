package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nite/traio/internal/activity"
	"github.com/nite/traio/internal/performance"
)

const performanceSchema = `
CREATE TABLE account_performance_rebuild (
 account_id @ACCOUNT@ PRIMARY KEY REFERENCES broker_accounts(id) ON DELETE CASCADE,
 dirty_from TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE daily_account_performance (
 account_id @ACCOUNT@ NOT NULL REFERENCES broker_accounts(id) ON DELETE CASCADE,
 performance_date TEXT NOT NULL, base_currency TEXT NOT NULL,
 total_value @DECIMAL@, cash_value @DECIMAL@, holdings_value @DECIMAL@,
 external_cash_flow @DECIMAL@, daily_pnl @DECIMAL@, daily_return @DECIMAL@, cumulative_twr @DECIMAL@,
 quality TEXT NOT NULL, finality TEXT NOT NULL,
 missing_prices TEXT NOT NULL, missing_fx TEXT NOT NULL, missing_cost_basis TEXT NOT NULL,
 source_mix TEXT NOT NULL, coverage TEXT NOT NULL,
 calculation_version TEXT NOT NULL, activity_generation BIGINT NOT NULL, calculated_at TEXT NOT NULL,
 PRIMARY KEY(account_id, performance_date, base_currency)
);
CREATE INDEX idx_daily_account_performance_date ON daily_account_performance(account_id,base_currency,performance_date);
`

func (s *Store) migratePerformance() error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.dialect == dialectPostgres {
		if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(78134122)`); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS performance_schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	schema := strings.NewReplacer("@ACCOUNT@", "INTEGER", "@DECIMAL@", "TEXT").Replace(performanceSchema)
	if s.dialect == dialectPostgres {
		schema = strings.NewReplacer("@ACCOUNT@", "BIGINT", "@DECIMAL@", "NUMERIC(38,18)").Replace(performanceSchema)
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(schema)))
	var existing string
	err = tx.QueryRow(`SELECT checksum FROM performance_schema_migrations WHERE version=1`).Scan(&existing)
	if err == nil {
		if existing != checksum {
			return fmt.Errorf("performance migration checksum mismatch")
		}
		return tx.Commit()
	}
	if err != sql.ErrNoRows {
		return err
	}
	for _, statement := range strings.Split(schema, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err = tx.Exec(statement); err != nil {
			return fmt.Errorf("performance schema: %w", err)
		}
	}
	if _, err = s.txExecContext(ctx, tx, `INSERT INTO performance_schema_migrations(version,checksum,applied_at) VALUES(1,?,?)`, checksum, nowRFC3339()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) LoadPerformanceInput(ctx context.Context, accountID int64, to string) (performance.Input, error) {
	input := performance.Input{AccountID: accountID, Instruments: map[int64]performance.Instrument{}, Activities: []activity.Activity{}, Coverage: []performance.CoverageWindow{}}
	if err := s.queryRowContext(ctx, `SELECT COALESCE((SELECT connection_id FROM broker_account_connections WHERE account_id=broker_accounts.id AND status='active' ORDER BY is_primary DESC,connection_id LIMIT 1),first_discovered_connection_id,0),provider_code,currency,activity_generation FROM broker_accounts WHERE id=?`, accountID).Scan(&input.ConnectionID, &input.Provider, &input.BaseCurrency, &input.ActivityGeneration); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return input, performance.ErrAccountNotFound
		}
		return input, err
	}
	rows, err := s.queryContext(ctx, `SELECT v.payload FROM account_activities a JOIN activity_revisions v ON v.activity_id=a.id AND v.revision_no=a.current_revision WHERE a.account_id=? AND v.sort_key<=? ORDER BY v.sort_key,a.id`, accountID, to+"~")
	if err != nil {
		return input, err
	}
	for rows.Next() {
		var payload string
		if err = rows.Scan(&payload); err != nil {
			rows.Close()
			return input, err
		}
		var item activity.Activity
		if err = json.Unmarshal([]byte(payload), &item); err != nil {
			rows.Close()
			return input, err
		}
		input.Activities = append(input.Activities, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return input, err
	}

	rows, err = s.queryContext(ctx, `SELECT i.id,i.symbol,i.currency,COALESCE((SELECT bi.external_id FROM broker_instruments bi WHERE bi.instrument_id=i.id ORDER BY CASE WHEN bi.provider_code=? THEN 0 ELSE 1 END,bi.provider_code,bi.external_id LIMIT 1),'') FROM instruments i WHERE EXISTS (SELECT 1 FROM activity_legs l JOIN activity_revisions v ON v.id=l.revision_id JOIN account_activities a ON a.id=v.activity_id AND a.current_revision=v.revision_no WHERE a.account_id=? AND l.instrument_id=i.id) ORDER BY i.id`, input.Provider, accountID)
	if err != nil {
		return input, err
	}
	for rows.Next() {
		var instrument performance.Instrument
		if err = rows.Scan(&instrument.ID, &instrument.Symbol, &instrument.Currency, &instrument.ExternalID); err != nil {
			rows.Close()
			return input, err
		}
		instrument.ConnectionID = input.ConnectionID
		input.Instruments[instrument.ID] = instrument
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return input, err
	}

	rows, err = s.queryContext(ctx, `SELECT source,from_date,to_date,fetch_status,normalize_status,reconcile_status FROM broker_history_coverage WHERE account_id=? AND data_type='activities' AND scope_key='account' ORDER BY from_date,source`, accountID)
	if err != nil {
		return input, err
	}
	defer rows.Close()
	for rows.Next() {
		var item performance.CoverageWindow
		if err = rows.Scan(&item.Source, &item.From, &item.To, &item.FetchStatus, &item.NormalizeStatus, &item.ReconcileStatus); err != nil {
			return input, err
		}
		input.Coverage = append(input.Coverage, item)
	}
	return input, rows.Err()
}

func (s *Store) LoadPerformanceSeries(ctx context.Context, query performance.Query, version string) (performance.Series, bool, error) {
	series := performance.Series{
		AccountID: query.AccountID, Points: []performance.Point{},
		CalculationVersion: version,
	}
	base := strings.ToUpper(strings.TrimSpace(query.BaseCurrency))
	if err := s.queryRowContext(ctx, `SELECT currency FROM broker_accounts WHERE id=? AND archived_at=''`, query.AccountID).Scan(&series.BaseCurrency); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return series, false, performance.ErrAccountNotFound
		}
		return series, false, err
	}
	series.BaseCurrency = strings.ToUpper(strings.TrimSpace(series.BaseCurrency))
	if base != "" {
		series.BaseCurrency = base
	}

	statement := `SELECT performance_date,total_value,cash_value,holdings_value,external_cash_flow,daily_pnl,daily_return,cumulative_twr,quality,finality,missing_prices,missing_fx,missing_cost_basis,source_mix,coverage,activity_generation,calculated_at FROM daily_account_performance WHERE account_id=? AND base_currency=? AND calculation_version=?`
	args := []any{query.AccountID, series.BaseCurrency, version}
	if query.From != "" {
		statement += ` AND performance_date>=?`
		args = append(args, query.From)
	}
	if query.To != "" {
		statement += ` AND performance_date<=?`
		args = append(args, query.To)
	}
	statement += ` ORDER BY performance_date`
	rows, err := s.queryContext(ctx, statement, args...)
	if err != nil {
		return series, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var point performance.Point
		var total, cash, holdings, flow, pnl, dailyReturn, twr sql.NullString
		var missingPrices, missingFX, missingCost, sources, coverageJSON string
		var calculatedAt string
		if err = rows.Scan(
			&point.Date, &total, &cash, &holdings, &flow, &pnl, &dailyReturn, &twr,
			&point.Quality, &point.Finality, &missingPrices, &missingFX, &missingCost,
			&sources, &coverageJSON, &series.ActivityGeneration, &calculatedAt,
		); err != nil {
			return series, false, err
		}
		point.TotalValue = nullStringPointer(total)
		point.CashValue = nullStringPointer(cash)
		point.HoldingsValue = nullStringPointer(holdings)
		point.ExternalCashFlow = nullStringPointer(flow)
		point.DailyPnL = nullStringPointer(pnl)
		point.DailyReturn = nullStringPointer(dailyReturn)
		point.CumulativeTWR = nullStringPointer(twr)
		point.CalculationVersion = version
		if err = unmarshalStringList(missingPrices, &point.MissingPrices); err != nil {
			return series, false, err
		}
		if err = unmarshalStringList(missingFX, &point.MissingFX); err != nil {
			return series, false, err
		}
		if err = unmarshalStringList(missingCost, &point.MissingCostBasis); err != nil {
			return series, false, err
		}
		if err = unmarshalStringList(sources, &point.SourceMix); err != nil {
			return series, false, err
		}
		if err = json.Unmarshal([]byte(coverageJSON), &series.Coverage); err != nil {
			return series, false, err
		}
		if calculatedAt > series.AsOf {
			series.AsOf = calculatedAt
		}
		series.Points = append(series.Points, point)
	}
	if err = rows.Err(); err != nil {
		return series, false, err
	}
	if len(series.Points) == 0 {
		return series, false, nil
	}
	if query.From != "" {
		series.Coverage.RequestedFrom = query.From
	} else {
		series.Coverage.RequestedFrom = series.Points[0].Date
	}
	if query.To != "" {
		series.Coverage.RequestedTo = query.To
	}
	return series, true, nil
}

func (s *Store) ListPerformanceAccountIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.queryContext(ctx, `SELECT id FROM broker_accounts WHERE archived_at='' AND EXISTS (SELECT 1 FROM account_activities WHERE account_id=broker_accounts.id) ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accountIDs []int64
	for rows.Next() {
		var accountID int64
		if err = rows.Scan(&accountID); err != nil {
			return nil, err
		}
		accountIDs = append(accountIDs, accountID)
	}
	return accountIDs, rows.Err()
}

func (s *Store) PerformanceDirtyFrom(ctx context.Context, accountID int64) (string, error) {
	var value string
	err := s.queryRowContext(ctx, `SELECT dirty_from FROM account_performance_rebuild WHERE account_id=?`, accountID).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (s *Store) LoadPerformanceAnchor(ctx context.Context, accountID int64, before, version string) (performance.Anchor, error) {
	var anchor performance.Anchor
	var total, twr sql.NullString
	err := s.queryRowContext(ctx, `SELECT performance_date,total_value,cumulative_twr FROM daily_account_performance WHERE account_id=? AND performance_date<? AND calculation_version=? ORDER BY performance_date DESC LIMIT 1`, accountID, before, version).Scan(&anchor.Date, &total, &twr)
	if errors.Is(err, sql.ErrNoRows) {
		return anchor, nil
	}
	if err != nil {
		return anchor, err
	}
	if total.Valid {
		anchor.TotalValue = &total.String
	}
	if twr.Valid {
		anchor.CumulativeTWR = &twr.String
	}
	return anchor, nil
}

func (s *Store) ReplacePerformanceSeries(ctx context.Context, series performance.Series, from string, clearDirty bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = s.txExecContext(ctx, tx, `DELETE FROM daily_account_performance WHERE account_id=? AND base_currency=? AND performance_date>=?`, series.AccountID, series.BaseCurrency, from); err != nil {
		return err
	}
	coverage, _ := json.Marshal(series.Coverage)
	for _, point := range series.Points {
		missingPrices, _ := json.Marshal(point.MissingPrices)
		missingFX, _ := json.Marshal(point.MissingFX)
		missingCost, _ := json.Marshal(point.MissingCostBasis)
		sources, _ := json.Marshal(point.SourceMix)
		if _, err = s.txExecContext(ctx, tx, `INSERT INTO daily_account_performance(account_id,performance_date,base_currency,total_value,cash_value,holdings_value,external_cash_flow,daily_pnl,daily_return,cumulative_twr,quality,finality,missing_prices,missing_fx,missing_cost_basis,source_mix,coverage,calculation_version,activity_generation,calculated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, series.AccountID, point.Date, series.BaseCurrency, nullableString(point.TotalValue), nullableString(point.CashValue), nullableString(point.HoldingsValue), nullableString(point.ExternalCashFlow), nullableString(point.DailyPnL), nullableString(point.DailyReturn), nullableString(point.CumulativeTWR), point.Quality, point.Finality, string(missingPrices), string(missingFX), string(missingCost), string(sources), string(coverage), point.CalculationVersion, series.ActivityGeneration, series.AsOf); err != nil {
			return err
		}
	}
	if clearDirty {
		if _, err = s.txExecContext(ctx, tx, `DELETE FROM account_performance_rebuild WHERE account_id=?`, series.AccountID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// InvalidatePerformanceSource removes persisted derived results that used a
// revoked provider and marks the dependent suffix dirty. No market-data rows
// are deleted because public Tiingo/Yahoo OHLC is never persisted.
func (s *Store) InvalidatePerformanceSource(ctx context.Context, source string) error {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		return performance.ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := s.txQueryContext(ctx, tx, `SELECT account_id,MIN(performance_date) FROM daily_account_performance WHERE source_mix LIKE ? GROUP BY account_id`, "%\""+source+"\"%")
	if err != nil {
		return err
	}
	type boundary struct {
		accountID int64
		from      string
	}
	var boundaries []boundary
	for rows.Next() {
		var item boundary
		if err = rows.Scan(&item.accountID, &item.from); err != nil {
			rows.Close()
			return err
		}
		boundaries = append(boundaries, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, item := range boundaries {
		if _, err = s.txExecContext(ctx, tx, `DELETE FROM daily_account_performance WHERE account_id=? AND performance_date>=?`, item.accountID, item.from); err != nil {
			return err
		}
		if err = s.markPerformanceDirtyTx(ctx, tx, item.accountID, item.from); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func unmarshalStringList(raw string, target *[]string) error {
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return err
	}
	if *target == nil {
		*target = []string{}
	}
	return nil
}

func (s *Store) markPerformanceDirtyTx(ctx context.Context, tx *sql.Tx, accountID int64, from string) error {
	if from == "" {
		return nil
	}
	_, err := s.txExecContext(ctx, tx, `INSERT INTO account_performance_rebuild(account_id,dirty_from,updated_at) VALUES(?,?,?) ON CONFLICT(account_id) DO UPDATE SET dirty_from=CASE WHEN excluded.dirty_from<account_performance_rebuild.dirty_from THEN excluded.dirty_from ELSE account_performance_rebuild.dirty_from END,updated_at=excluded.updated_at`, accountID, from, nowRFC3339())
	return err
}

func earliestPerformanceDate(items ...activity.Activity) string {
	earliest := ""
	for _, item := range items {
		for _, leg := range item.Legs {
			date := leg.EffectiveDate
			if date == "" {
				date = leg.SettlementDate
			}
			if date != "" && (earliest == "" || date < earliest) {
				earliest = date
			}
		}
		for _, date := range []string{item.SettlementDate, item.TradeDate} {
			if date != "" && (earliest == "" || date < earliest) {
				earliest = date
			}
		}
		if len(item.OccurredAt) >= 10 {
			date := item.OccurredAt[:10]
			if earliest == "" || date < earliest {
				earliest = date
			}
		}
	}
	return earliest
}

var _ performance.Repository = (*Store)(nil)
