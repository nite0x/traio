package store

import (
	"path/filepath"
	"testing"
)

func TestOpenRepositoryDefaultsToSQLite(t *testing.T) {
	st, err := OpenRepository("", filepath.Join(t.TempDir(), "traio.db"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.ListWatchlistGroups(t.Context()); err != nil {
		t.Fatalf("list default watchlist groups: %v", err)
	}
}

func TestOpenRepositoryRejectsUnsupportedDriver(t *testing.T) {
	if _, err := OpenRepository("unknown", "unused"); err == nil {
		t.Fatal("expected unsupported driver error")
	}
}

func TestRebindPostgres(t *testing.T) {
	got := rebindPostgres("SELECT * FROM items WHERE group_id = ? AND symbol = ?")
	want := "SELECT * FROM items WHERE group_id = $1 AND symbol = $2"
	if got != want {
		t.Fatalf("rebound query: got %q, want %q", got, want)
	}
}
