package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminGatewayPageIsEmbedded(t *testing.T) {
	router := NewRouter(Deps{}, ServerControl{})
	for _, path := range []string{"/admin", "/admin/", "/admin/gateways"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d", path, res.Code)
		}
		if !strings.Contains(res.Body.String(), "IBKR Gateway") {
			t.Fatalf("GET %s: embedded page marker missing", path)
		}
		if got := res.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("GET %s: unexpected cache policy %q", path, got)
		}
	}
}

func TestCORSMiddlewareAllowsAdminSameOrigin(t *testing.T) {
	router := NewRouter(Deps{}, ServerControl{})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Host = "127.0.0.1:38181"
	req.Header.Set("Origin", "http://127.0.0.1:38181")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected same-origin request to pass, got %d: %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:38181" {
		t.Fatalf("unexpected allow-origin header %q", got)
	}
}

func TestSameOriginRejectsOriginWithPathOrDifferentHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:38181/health", nil)
	for _, origin := range []string{
		"http://127.0.0.1:38181/admin",
		"http://localhost:38181",
		"file://127.0.0.1:38181",
	} {
		if sameOrigin(origin, req) {
			t.Fatalf("expected origin %q to be rejected", origin)
		}
	}
}
