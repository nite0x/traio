package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	traioauth "github.com/nite/traio/internal/auth"
	"github.com/nite/traio/internal/store"
)

func TestSessionMiddlewareAuthenticatesAndRequiresCSRF(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	identity, err := st.UpsertOIDCIdentity(t.Context(), "issuer", "subject", "owner@example.com", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	service, err := traioauth.NewService(t.Context(), st, traioauth.Config{Mode: traioauth.ModeLocal})
	if err != nil {
		t.Fatal(err)
	}
	rawSession, rawCSRF := "session-secret", "csrf-secret"
	now := time.Now().UTC()
	if err := st.CreateAuthSession(t.Context(), store.AuthSession{
		TokenHash: testTokenHash(rawSession), CSRFTokenHash: testTokenHash(rawCSRF),
		UserID: identity.User.ID, WorkspaceID: identity.Workspace.ID,
		ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano), LastSeenAt: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(sessionMiddleware(service, nil))
	router.GET("/read", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.POST("/write", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := func(method, path string, csrf bool) int {
		req := httptest.NewRequest(method, path, nil)
		req.Host = "127.0.0.1:8080"
		req.AddCookie(&http.Cookie{Name: service.CookieName(), Value: rawSession})
		if csrf {
			req.AddCookie(&http.Cookie{Name: service.CSRFCookieName(), Value: rawCSRF})
			req.Header.Set("X-CSRF-Token", rawCSRF)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res.Code
	}
	if got := request(http.MethodGet, "/read", false); got != http.StatusNoContent {
		t.Fatalf("authenticated read: got %d", got)
	}
	if got := request(http.MethodPost, "/write", false); got != http.StatusForbidden {
		t.Fatalf("write without CSRF: got %d", got)
	}
	if got := request(http.MethodPost, "/write", true); got != http.StatusNoContent {
		t.Fatalf("write with CSRF: got %d", got)
	}
}

func testTokenHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func TestPasswordLoginRouteSetsSessionCookies(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "password-route.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	service, err := traioauth.NewService(t.Context(), st, traioauth.Config{
		Mode: traioauth.ModePassword, BootstrapUsername: "owner", BootstrapPassword: "correct horse battery staple",
	})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	registerAuthRoutes(router, Deps{Auth: service})

	login := httptest.NewRequest(http.MethodPost, "/auth/password/login", bytes.NewBufferString(`{"username":"owner","password":"correct horse battery staple","return_to":"/settings"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Host = "127.0.0.1:8080"
	login.RemoteAddr = "127.0.0.1:50000"
	loginResponse := httptest.NewRecorder()
	router.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusNoContent {
		t.Fatalf("login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	cookies := loginResponse.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("login cookies=%d, want 2", len(cookies))
	}
	var csrfToken string
	for _, cookie := range cookies {
		if cookie.Name == service.CookieName() && !cookie.HttpOnly {
			t.Fatal("session cookie must be HttpOnly")
		}
		if cookie.Name == service.CSRFCookieName() {
			csrfToken = cookie.Value
			if cookie.HttpOnly {
				t.Fatal("CSRF cookie must be readable by the frontend")
			}
		}
	}

	me := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	me.Host = "127.0.0.1:8080"
	for _, cookie := range cookies {
		me.AddCookie(cookie)
	}
	meResponse := httptest.NewRecorder()
	router.ServeHTTP(meResponse, me)
	if meResponse.Code != http.StatusOK || !bytes.Contains(meResponse.Body.Bytes(), []byte(`"source":"password"`)) {
		t.Fatalf("me status=%d body=%s", meResponse.Code, meResponse.Body.String())
	}

	logout := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	logout.Host = "127.0.0.1:8080"
	logout.Header.Set("X-CSRF-Token", csrfToken)
	for _, cookie := range cookies {
		logout.AddCookie(cookie)
	}
	logoutResponse := httptest.NewRecorder()
	router.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", logoutResponse.Code, logoutResponse.Body.String())
	}
	meAfterLogout := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	meAfterLogout.Host = "127.0.0.1:8080"
	for _, cookie := range cookies {
		meAfterLogout.AddCookie(cookie)
	}
	meAfterLogoutResponse := httptest.NewRecorder()
	router.ServeHTTP(meAfterLogoutResponse, meAfterLogout)
	if meAfterLogoutResponse.Code != http.StatusUnauthorized {
		t.Fatalf("old session remained valid after logout: %d", meAfterLogoutResponse.Code)
	}
}

func TestPasswordLoginLimiter(t *testing.T) {
	now := time.Now()
	limiter := newPasswordLoginLimiter()
	limiter.now = func() time.Time { return now }
	for range 5 {
		if !limiter.allowed("client") {
			t.Fatal("client was blocked before five failed attempts")
		}
		limiter.failed("client")
	}
	if limiter.allowed("client") {
		t.Fatal("client should be blocked after five failed attempts")
	}
	now = now.Add(5*time.Minute + time.Second)
	if !limiter.allowed("client") {
		t.Fatal("client should be unblocked after the rate-limit window")
	}
}
