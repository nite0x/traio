package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/nite/traio/internal/store"
)

func TestRolePermissions(t *testing.T) {
	tests := []struct {
		role       string
		permission Permission
		want       bool
	}{
		{"owner", PermissionOwner, true},
		{"admin", PermissionBrokerManage, true},
		{"admin", PermissionOwner, false},
		{"member", PermissionBrokerSync, true},
		{"member", PermissionBrokerManage, false},
		{"viewer", PermissionView, true},
		{"viewer", PermissionWatchlistWrite, false},
		{"unknown", PermissionView, false},
	}
	for _, test := range tests {
		if got := Allows(test.role, test.permission); got != test.want {
			t.Errorf("Allows(%q, %q)=%v, want %v", test.role, test.permission, got, test.want)
		}
	}
}

func TestSafeReturnToRejectsExternalURLs(t *testing.T) {
	for _, value := range []string{"https://evil.example", "//evil.example/path", "login", ""} {
		if got := safeReturnTo(value); got != "/" {
			t.Errorf("safeReturnTo(%q)=%q", value, got)
		}
	}
	if got := safeReturnTo("/brokers?oauth=1"); got != "/brokers?oauth=1" {
		t.Fatalf("safe local return path changed: %q", got)
	}
}

func TestOIDCPKCELoginCreatesAuthenticatedSession(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	var expectedNonce string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
				"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys",
				"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test", Algorithm: string(jose.RS256), Use: "sig"}}})
		case "/token":
			if err := r.ParseForm(); err != nil || r.Form.Get("code_verifier") == "" {
				http.Error(w, "missing PKCE verifier", http.StatusBadRequest)
				return
			}
			now := time.Now()
			claims, _ := json.Marshal(map[string]any{
				"iss": issuer, "aud": "traio", "sub": "owner", "exp": now.Add(time.Hour).Unix(), "iat": now.Unix(),
				"nonce": expectedNonce, "email": "owner@example.com", "email_verified": true, "name": "Owner",
			})
			signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test"))
			signed, _ := signer.Sign(claims)
			compact, _ := signed.CompactSerialize()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "token_type": "Bearer", "expires_in": 3600, "id_token": compact})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	issuer = provider.URL

	repository, err := store.Open(filepath.Join(t.TempDir(), "oidc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	service, err := NewService(t.Context(), repository, Config{
		Mode: ModeOIDC, IssuerURL: issuer, ClientID: "traio", RedirectURL: "https://traio.example/auth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	loginURL, err := service.BeginLogin(t.Context(), "/brokers?from=login")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(loginURL)
	if err != nil {
		t.Fatal(err)
	}
	expectedNonce = parsed.Query().Get("nonce")
	state := parsed.Query().Get("state")
	if state == "" || expectedNonce == "" || parsed.Query().Get("code_challenge") == "" || parsed.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("login URL is missing state, nonce, or PKCE: %s", loginURL)
	}
	result, err := service.CompleteLogin(t.Context(), state, "authorization-code")
	if err != nil {
		t.Fatal(err)
	}
	if result.Identity.Role != "owner" || result.ReturnTo != "/brokers?from=login" || result.SessionToken == "" || result.CSRFToken == "" {
		t.Fatalf("unexpected login result: %#v", result)
	}
	principal, _, err := service.Authenticate(t.Context(), result.SessionToken)
	if err != nil || principal.Email != "owner@example.com" || principal.Role != "owner" {
		t.Fatalf("authenticated principal: %#v err=%v", principal, err)
	}
	if _, err := service.CompleteLogin(t.Context(), state, "authorization-code"); err == nil || !strings.Contains(err.Error(), "consume OIDC flow") {
		t.Fatalf("replayed state should fail, got %v", err)
	}
}

func TestPasswordLoginCreatesAuthenticatedSession(t *testing.T) {
	repository, err := store.Open(filepath.Join(t.TempDir(), "password.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	service, err := NewService(t.Context(), repository, Config{
		Mode: ModePassword, BootstrapUsername: "owner", BootstrapPassword: "correct horse battery staple", BootstrapName: "Owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.LoginWithPassword(t.Context(), "owner", "wrong password", "/"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", err)
	}
	result, err := service.LoginWithPassword(t.Context(), " OWNER ", "correct horse battery staple", "/settings")
	if err != nil {
		t.Fatal(err)
	}
	principal, _, err := service.Authenticate(t.Context(), result.SessionToken)
	if err != nil || principal.Source != ModePassword || principal.Role != "owner" || principal.Name != "Owner" {
		t.Fatalf("authenticated principal: %#v err=%v", principal, err)
	}
	if result.ReturnTo != "/settings" {
		t.Fatalf("return path = %q", result.ReturnTo)
	}
	if _, err := NewService(t.Context(), repository, Config{Mode: ModePassword}); err != nil {
		t.Fatalf("restart without bootstrap secret should use existing account: %v", err)
	}
}

func TestPasswordHashVerification(t *testing.T) {
	encoded, err := hashPassword("a long passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(encoded, "a long passphrase") || verifyPassword(encoded, "not the passphrase") {
		t.Fatal("Argon2id password verification returned an unexpected result")
	}
}
