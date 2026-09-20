package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nite/traio/internal/performance"
)

type fakePerformanceAPI struct {
	query        performance.Query
	recalculated bool
}

func (f *fakePerformanceAPI) Calculate(_ context.Context, query performance.Query) (performance.Series, error) {
	f.query = query
	return performance.Series{AccountID: query.AccountID, BaseCurrency: "USD", Points: []performance.Point{}, CalculationVersion: performance.CalculationVersion}, nil
}
func (f *fakePerformanceAPI) Recalculate(_ context.Context, query performance.Query) (performance.Series, error) {
	f.query = query
	f.recalculated = true
	return performance.Series{AccountID: query.AccountID, BaseCurrency: "USD", Points: []performance.Point{}, CalculationVersion: performance.CalculationVersion}, nil
}

func TestPerformanceQueryAndRecalculationRoutes(t *testing.T) {
	fake := &fakePerformanceAPI{}
	router := NewRouter(Deps{Performance: fake}, ServerControl{})

	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/performance/accounts/7/daily?from=2026-01-01&to=2026-01-31&base_currency=eur", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", get.Code, get.Body.String())
	}
	if fake.query.AccountID != 7 || fake.query.From != "2026-01-01" || fake.query.BaseCurrency != "eur" {
		t.Fatalf("query=%#v", fake.query)
	}
	if get.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control=%q", get.Header().Get("Cache-Control"))
	}

	post := httptest.NewRecorder()
	router.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/api/v1/performance/accounts/7/recalculate?to=2026-01-31", nil))
	if post.Code != http.StatusOK || !fake.recalculated {
		t.Fatalf("POST status=%d recalculated=%v body=%s", post.Code, fake.recalculated, post.Body.String())
	}

	invalid := httptest.NewRecorder()
	router.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/api/v1/performance/accounts/nope/daily", nil))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d", invalid.Code)
	}
}
