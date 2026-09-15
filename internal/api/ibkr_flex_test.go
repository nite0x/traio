package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/nite/traio/internal/store"
)

func TestCreateFlexConnectionWithoutManager(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "flex.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	router := NewRouter(Deps{Brokers: st}, ServerControl{})
	body := `{"connection_key":"flex:test","name":"Flex","config":{"connection_type":"flex","flex_activity_query_id":"123"},"secrets":{"flex_token":"test-flex-secret"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/brokers/IBKR/connections", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("test-flex-secret")) {
		t.Fatal("secret exposed")
	}
	var created store.BrokerConnection
	if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.AuthType != "api_key" || created.Config["gateway_id"] != nil {
		t.Fatalf("created=%+v", created)
	}
	req = httptest.NewRequest(http.MethodPut, "/api/v1/broker-connections/"+strconv.FormatInt(created.ID, 10), bytes.NewBufferString(`{"name":"Updated","config":{"connection_type":"flex","flex_activity_query_id":"456"}}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	saved, err := st.GetBrokerConnectionRuntimeConfig(t.Context(), created.ID)
	if err != nil || saved.Secrets["flex_token"] != "test-flex-secret" {
		t.Fatal("secret lost on edit")
	}
}

func TestCreateFlexRejectsTokenInQueryFieldBeforeSaving(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "flex.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	router := NewRouter(Deps{Brokers: st}, ServerControl{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/brokers/IBKR/connections", bytes.NewBufferString(`{"connection_key":"invalid","config":{"connection_type":"flex","flex_activity_query_id":"935800000000000000000000"},"secrets":{"flex_token":"secret"}}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != 400 || !bytes.Contains(response.Body.Bytes(), []byte("invalid_flex_activity_query_id")) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte("935800")) {
		t.Fatal("input echoed in error")
	}
	connections, err := st.ListBrokerConnections(t.Context())
	if err != nil || len(connections) != 0 {
		t.Fatal("invalid configuration persisted")
	}
}
