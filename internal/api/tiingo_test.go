package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/market"
	"github.com/nite/traio/internal/store"
)

func TestTiingoConfigEncryptsTokenAndNeverReturnsIt(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token raw-tiingo-token-must-stay-secret" {
			t.Fatalf("authorization header=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"ticker":"AAPL","tngoLast":123,"timestamp":"2026-09-20T00:00:00Z"}]`))
	}))
	defer upstream.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "tiingo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cipher, err := market.NewSecretCipher(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	reloads := 0
	router := gin.New()
	router.GET("/tiingo", tiingoConfiguration(st))
	router.PUT("/tiingo", saveTiingoConfiguration(st, cipher, func(context.Context) error { reloads++; return nil }))
	router.POST("/tiingo/test", testTiingoConfiguration(st, cipher))
	router.DELETE("/tiingo", deleteTiingoConfiguration(st, func(context.Context) error { reloads++; return nil }))
	router.PUT("/brokers/:code", updateBrokerProvider(st, nil))
	const token = "raw-tiingo-token-must-stay-secret"
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/tiingo", bytes.NewBufferString(`{"api_token":"`+token+`","base_url":"`+upstream.URL+`","enabled":true,"license_mode":"transient"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), token) || !strings.Contains(response.Body.String(), `"token_configured":true`) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
	stored, err := st.GetBrokerProviderRuntimeConfig(t.Context(), "TIINGO")
	if err != nil {
		t.Fatal(err)
	}
	encrypted := stored.Secrets["api_token"]
	if encrypted == token || strings.Contains(encrypted, token) {
		t.Fatal("plaintext token was persisted")
	}
	decrypted, err := cipher.Decrypt(encrypted, tiingoTokenPurpose)
	if err != nil || decrypted != token || reloads != 1 {
		t.Fatalf("decrypted=%q reloads=%d err=%v", decrypted, reloads, err)
	}

	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/tiingo", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), token) || strings.Contains(response.Body.String(), encrypted) || !strings.Contains(response.Body.String(), `"scope":"deployment"`) {
		t.Fatalf("GET leaked credential: %d %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/tiingo/test", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), token) || !strings.Contains(response.Body.String(), `"connected":true`) || !strings.Contains(response.Body.String(), `"status":"connected"`) {
		t.Fatalf("test response=%d %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPut, "/brokers/TIINGO", bytes.NewBufferString(`{"secrets":{"api_token":"plaintext-bypass"}}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("generic provider endpoint allowed Tiingo bypass: %d %s", response.Code, response.Body.String())
	}
	stored, err = st.GetBrokerProviderRuntimeConfig(t.Context(), "TIINGO")
	if err != nil || stored.Secrets["api_token"] != encrypted {
		t.Fatalf("generic endpoint changed encrypted token: err=%v", err)
	}

	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/tiingo", nil))
	if response.Code != http.StatusNoContent || reloads != 2 {
		t.Fatalf("DELETE response=%d reloads=%d body=%s", response.Code, reloads, response.Body.String())
	}
	stored, err = st.GetBrokerProviderRuntimeConfig(t.Context(), "TIINGO")
	if err != nil || stored.Secrets["api_token"] != "" || boolConfigValue(stored.Config, "enabled", true) {
		t.Fatalf("deleted config=%#v err=%v", stored, err)
	}
}
