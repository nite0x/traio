package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
)

type dialect uint8

const (
	dialectSQLite dialect = iota
	dialectPostgres
)

// OpenRepository constructs the configured persistence adapter. SQLite is the
// embedded adapter; additional server adapters can be registered here without
// changing API, settings, broker, or portfolio packages.
func OpenRepository(driver, dataSource string) (Repository, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", DriverSQLite:
		if strings.TrimSpace(dataSource) == "" {
			return nil, fmt.Errorf("sqlite data source is required")
		}
		return Open(dataSource)
	case DriverPostgres, "postgresql":
		if strings.TrimSpace(dataSource) == "" {
			return nil, fmt.Errorf("postgres data source is required")
		}
		return openPostgres(dataSource)
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func (s *Store) migrate() error {
	if s.dialect == dialectPostgres {
		return s.migratePostgres()
	}
	return s.migrateSQLite()
}

func (s *Store) bind(query string) string {
	if s.dialect != dialectPostgres {
		return query
	}
	return rebindPostgres(query)
}

func rebindPostgres(query string) string {
	var b strings.Builder
	b.Grow(len(query) + 8)
	parameter := 1
	for _, r := range query {
		if r == '?' {
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(parameter))
			parameter++
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (s *Store) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, s.bind(query), args...)
}

func (s *Store) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, s.bind(query), args...)
}

func (s *Store) queryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.bind(query), args...)
}

func (s *Store) txExecContext(ctx context.Context, tx *sql.Tx, query string, args ...any) (sql.Result, error) {
	return tx.ExecContext(ctx, s.bind(query), args...)
}

func (s *Store) txQueryContext(ctx context.Context, tx *sql.Tx, query string, args ...any) (*sql.Rows, error) {
	return tx.QueryContext(ctx, s.bind(query), args...)
}
