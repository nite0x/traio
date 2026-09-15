package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/nite/traio/internal/store"
)

type symbolHistoryAPI struct {
	fakeHistoryAPI
	query store.HistoryQuery
}

func (f *symbolHistoryAPI) List(_ context.Context, q store.HistoryQuery) (store.HistoryPage, error) {
	f.query = q
	return store.HistoryPage{}, nil
}

func TestHistoryRoutePassesSymbolFilter(t *testing.T) {
	fake := &symbolHistoryAPI{}
	router := NewRouter(Deps{APIToken: "history-test-token", AllowedAPIHosts: []string{"example.com"}, History: fake}, ServerControl{})
	response := historyRequest(t, router, http.MethodGet, "/api/v1/transactions?symbol=%20aapl%20&types=trade&limit=1", "history-test-token", "", nil)
	if response.Code != http.StatusOK || fake.query.Symbol != "AAPL" || fake.query.Types != "trade" || fake.query.Limit != 1 {
		t.Fatalf("status %d, query %#v", response.Code, fake.query)
	}
}
