package store

import (
	"context"
	"errors"
	"testing"
)

func TestHistorySymbolFilterPagination(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			s := historyTestStore(t, driver)
			seedHistoryAccount(t, s)
			first := historyTrade("aapl-first", "-1", "2026-09-02T10:00:00Z")
			second := historyTrade("aapl-second", "-1", "2026-09-02T10:00:00Z")
			second.Activity.TradeDate = "2026-09-02"
			other := historyTrade("msft", "-1", "2026-09-02T10:00:00Z")
			other.Activity.Legs[0].Symbol = "MSFT"
			other.Activity.Legs[0].ExternalInstrumentID = "272093"
			runHistoryRecords(t, s, first, second, other)
			ctx := context.Background()
			q := HistoryQuery{Symbol: " aapl ", Types: "trade", Limit: 1}
			page, err := s.ListActivities(ctx, q)
			if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
				t.Fatalf("first page: %#v, %v", page, err)
			}
			if page.Items[0].Fill.ExecutionID != "aapl-second" {
				t.Fatalf("expected newest AAPL trade, got %#v", page.Items[0])
			}
			q.Symbol, q.Cursor = "AAPL", page.NextCursor
			next, err := s.ListActivities(ctx, q)
			if err != nil || len(next.Items) != 1 || next.NextCursor != "" || next.Items[0].Fill.ExecutionID != "aapl-first" {
				t.Fatalf("second page: %#v, %v", next, err)
			}
			q.Symbol = "MSFT"
			if _, err = s.ListActivities(ctx, q); !errors.Is(err, ErrHistoryCursorStale) {
				t.Fatalf("cursor must be bound to symbol: %v", err)
			}
			for symbol, want := range map[string]int{"MSFT": 1, "MS": 0, "NVDA": 0} {
				page, err := s.ListActivities(ctx, HistoryQuery{Symbol: symbol, Types: "trade"})
				if err != nil || len(page.Items) != want {
					t.Fatalf("symbol %s: %#v, %v", symbol, page, err)
				}
			}
		})
	}
}
