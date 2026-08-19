package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nite/traio/internal/broker"
)

type apiInstrumentSession struct{ id int64 }

func (s *apiInstrumentSession) ConnectionID() int64 { return s.id }
func (*apiInstrumentSession) ProviderCode() string  { return "TEST" }
func (*apiInstrumentSession) Health(context.Context) (broker.ConnectionHealth, error) {
	return broker.ConnectionHealth{State: broker.ConnectionStateConnected}, nil
}
func (*apiInstrumentSession) Close(context.Context) error { return nil }
func (s *apiInstrumentSession) SearchInstruments(context.Context, string) ([]broker.Instrument, error) {
	return []broker.Instrument{{ConID: s.id, Symbol: "AAPL"}}, nil
}

func TestMarketDataServicePreservesLiveInstrumentAPIAvailability(t *testing.T) {
	service := broker.NewMarketDataService()
	router := NewRouter(Deps{Instruments: service}, ServerControl{})

	request := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/instruments/search?q=AAPL", nil)
		router.ServeHTTP(recorder, req)
		return recorder
	}

	if response := request(); response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "instrument search is not available") {
		t.Fatalf("empty service response: status=%d body=%s", response.Code, response.Body.String())
	}
	service.Replace(map[int64]broker.BrokerSession{7: &apiInstrumentSession{id: 7}})
	if response := request(); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"conid":7`) {
		t.Fatalf("enabled service response: status=%d body=%s", response.Code, response.Body.String())
	}
	service.Replace(nil)
	if response := request(); response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "instrument search is not available") {
		t.Fatalf("disabled service response: status=%d body=%s", response.Code, response.Body.String())
	}
}
