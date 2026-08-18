package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebAppServesAssetsAndSPAFallback(t *testing.T) {
	webDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(webDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<main>traio web</main>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "assets", "app.js"), []byte("console.log('traio')"), 0o644); err != nil {
		t.Fatal(err)
	}

	router := NewRouter(Deps{WebDir: webDir}, ServerControl{})
	for _, path := range []string{"/", "/holdings", "/chart/AAPL"} {
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "traio web") {
			t.Fatalf("GET %s: got %d %q", path, res.Code, res.Body.String())
		}
		if got := res.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("GET %s: unexpected cache policy %q", path, got)
		}
	}

	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "console.log") {
		t.Fatalf("asset: got %d %q", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset: unexpected cache policy %q", got)
	}
}

func TestWebAppDoesNotMaskReservedOrMutationRoutes(t *testing.T) {
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("frontend"), 0o644); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Deps{WebDir: webDir}, ServerControl{})

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/not-a-route"},
		{http.MethodGet, "/admin/not-a-route"},
		{http.MethodPost, "/holdings"},
	} {
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequest(tc.method, tc.path, nil))
		if res.Code != http.StatusNotFound || strings.Contains(res.Body.String(), "frontend") {
			t.Fatalf("%s %s: got %d %q", tc.method, tc.path, res.Code, res.Body.String())
		}
	}
}
