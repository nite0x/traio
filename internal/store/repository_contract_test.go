package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteRepositoryContract(t *testing.T) {
	st, err := OpenRepository(DriverSQLite, filepath.Join(t.TempDir(), "contract.db"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	runRepositoryContract(t, st)
}

func runRepositoryContract(t *testing.T, repository Repository) {
	t.Helper()
	ctx := context.Background()

	if _, err := repository.GetSettings(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing settings: got %v, want ErrNotFound", err)
	}
	settings := []byte(`{"broker_sync":{"enabled":true}}`)
	if err := repository.SaveSettings(ctx, settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	loadedSettings, err := repository.GetSettings(ctx)
	if err != nil || string(loadedSettings) != string(settings) {
		t.Fatalf("get settings: data=%s err=%v", loadedSettings, err)
	}

	expiresAt := time.Now().UTC().Truncate(time.Second)
	token := OAuthToken{Provider: "contract", AccessToken: "access", RefreshToken: "refresh", ExpiresAt: expiresAt}
	if err := repository.SaveOAuthToken(ctx, token); err != nil {
		t.Fatalf("save OAuth token: %v", err)
	}
	loadedToken, err := repository.GetOAuthToken(ctx, token.Provider)
	if err != nil || loadedToken.AccessToken != token.AccessToken || !loadedToken.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("get OAuth token: token=%#v err=%v", loadedToken, err)
	}

	item, err := repository.UpsertWatchlistItem(ctx, WatchlistItem{GroupID: 1, Symbol: "aapl", Notes: "contract"})
	if err != nil || item.Symbol != "AAPL" {
		t.Fatalf("upsert watchlist item: item=%#v err=%v", item, err)
	}
	items, err := repository.ListWatchlistItems(ctx, 1)
	if err != nil || len(items) != 1 || items[0].Symbol != "AAPL" {
		t.Fatalf("list watchlist items: items=%#v err=%v", items, err)
	}
	if err := repository.DeleteWatchlistItem(ctx, 1, "AAPL"); err != nil {
		t.Fatalf("delete watchlist item: %v", err)
	}
	if err := repository.DeleteWatchlistItem(ctx, 1, "AAPL"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing watchlist item: got %v, want ErrNotFound", err)
	}
}
