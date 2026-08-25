package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSMiddlewareAllowsConfiguredDeploymentOrigin(t *testing.T) {
	router := NewRouter(Deps{AllowedOrigins: []string{"https://traio-web.vercel.app"}}, ServerControl{})
	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	req.Header.Set("Origin", "https://traio-web.vercel.app")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
	}
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "https://traio-web.vercel.app" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := res.Header().Get("Access-Control-Expose-Headers"); got != "X-CSRF-Token" {
		t.Fatalf("Access-Control-Expose-Headers = %q", got)
	}
}

func TestCORSMiddlewareRejectsUnconfiguredDeploymentOrigin(t *testing.T) {
	router := NewRouter(Deps{AllowedOrigins: []string{"https://traio-web.vercel.app"}}, ServerControl{})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://attacker.example")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusForbidden)
	}
}
