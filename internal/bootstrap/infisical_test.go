package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testProvider(t *testing.T, handler http.HandlerFunc) (*infisicalProvider, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	p := &infisicalProvider{config: ProviderConfig{SiteURL: server.URL, ProjectID: "project", Environment: "prod", SecretPath: "/traio", ClientID: "machine", ClientSecret: "client-secret"}, transport: server.Client().Transport}
	t.Cleanup(server.Close)
	return p, server
}
func loginResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"accessToken":"session-secret","expiresIn":3600,"tokenType":"Bearer"}`)
}
func TestInfisicalSDKSnapshotAndFiltering(t *testing.T) {
	var calls atomic.Int32
	p, _ := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/universal-auth/login":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["clientId"] != "machine" || body["clientSecret"] != "client-secret" {
				t.Error("incorrect SDK authentication body")
			}
			loginResponse(w)
		case "/api/v3/secrets/raw":
			if r.Header.Get("Authorization") != "Bearer session-secret" {
				t.Error("missing bearer authentication")
			}
			q := r.URL.Query()
			if q.Get("workspaceId") != "project" || q.Get("environment") != "prod" || q.Get("secretPath") != "/traio" || q.Get("recursive") != "false" || q.Get("include_imports") != "false" {
				t.Error("incorrect SDK scope")
			}
			fmt.Fprint(w, `{"secrets":[{"secretKey":"TRAIO_DATABASE_DRIVER","secretValue":"postgres"},{"secretKey":"TRAIO_DATABASE_DSN","secretValue":"postgres://localhost/traio"},{"secretKey":"UNRELATED_SECRET","secretValue":"ignore"},{"secretKey":"TRAIO_OIDC_CLIENT_SECRET_FILE","secretValue":"/etc/secret"}]}`)
		default:
			t.Error("unexpected SDK endpoint")
			w.WriteHeader(404)
		}
	})
	first, err := p.Fetch(context.Background(), []string{"TRAIO_DATABASE_DRIVER"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Fetch(context.Background(), []string{"TRAIO_DATABASE_DSN", "UNRELATED_SECRET", "MISSING", "TRAIO_OIDC_CLIENT_SECRET_FILE"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Values["TRAIO_DATABASE_DRIVER"] != "postgres" || len(second.Values) != 1 || calls.Load() != 2 {
		t.Fatal("snapshot, filtering or absent-key handling failed")
	}
	if p.config.ClientSecret != "" {
		t.Fatal("bootstrap credential retained")
	}
}
func TestInfisicalErrorsAndRetries(t *testing.T) {
	for _, tc := range []struct {
		status   int
		attempts int
	}{{401, 1}, {403, 1}, {404, 1}, {429, 3}, {500, 3}, {502, 3}, {503, 3}, {504, 3}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var count atomic.Int32
			p, _ := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "login") {
					loginResponse(w)
					return
				}
				count.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"message":"server-echoed-sensitive-value","reqId":"sensitive-request-id"}`)
			})
			_, err := p.Fetch(context.Background(), []string{"TRAIO_DATABASE_DSN"})
			if err == nil || strings.Contains(err.Error(), "sensitive") || count.Load() != int32(tc.attempts) {
				t.Fatalf("status/retries: count=%d error=%v", count.Load(), err)
			}
			if tc.status == 404 && !strings.Contains(err.Error(), "404") {
				t.Fatal("wrong project/path mistaken for absent key")
			}
		})
	}
}
func TestInfisicalAuthenticationDoesNotRetryForbidden(t *testing.T) {
	var count atomic.Int32
	p, _ := testProvider(t, func(w http.ResponseWriter, r *http.Request) { count.Add(1); w.WriteHeader(403) })
	if _, err := p.Fetch(context.Background(), []string{"TRAIO_DATABASE_DSN"}); err == nil {
		t.Fatal("forbidden login accepted")
	}
	if count.Load() != 1 {
		t.Fatal("forbidden login retried")
	}
}
func TestInfisicalDeadlineCancelsHTTPRequest(t *testing.T) {
	canceled := make(chan struct{})
	p, _ := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
		close(canceled)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := p.Fetch(ctx, []string{"TRAIO_DATABASE_DSN"}); err == nil {
		t.Fatal("deadline ignored")
	}
	if time.Since(start) > time.Second {
		t.Fatal("deadline exceeded budget")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("HTTP request not canceled")
	}
}
func TestInfisicalRejectsRedirectAndMalformedSuccess(t *testing.T) {
	for _, body := range []string{`{}`, `<html>login</html>`, `{"secrets":[{"secretKey":"TRAIO_DATABASE_DRIVER","secretValue":"sqlite"},{"secretKey":"TRAIO_DATABASE_DRIVER","secretValue":"postgres"}]}`} {
		t.Run(body, func(t *testing.T) {
			p, _ := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "login") {
					loginResponse(w)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, body)
			})
			if _, err := p.Fetch(context.Background(), []string{"TRAIO_DATABASE_DRIVER"}); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	p, _ := testProvider(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) })
	if _, err := p.Fetch(context.Background(), []string{"TRAIO_DATABASE_DRIVER"}); err == nil {
		t.Fatal("redirect accepted")
	}
	if targetCalls.Load() != 0 {
		t.Fatal("credentials sent to redirect target")
	}
}
func TestInfisicalEmptyScopeIsValid(t *testing.T) {
	p, _ := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "login") {
			loginResponse(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"secrets":[]}`)
	})
	result, err := p.Fetch(context.Background(), []string{"TRAIO_DATABASE_DRIVER"})
	if err != nil || len(result.Values) != 0 {
		t.Fatalf("empty scope: %v", err)
	}
}

// Ensure a fully configured caller never constructs the SDK or contacts its endpoint.
func TestInfisicalLoadUsesRealSDK(t *testing.T) {
	p, server := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "login") {
			loginResponse(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"secrets":[{"secretKey":"TRAIO_DATABASE_DRIVER","secretValue":"postgres"},{"secretKey":"TRAIO_DATABASE_DSN","secretValue":"postgres://localhost/traio"},{"secretKey":"TRAIO_AUTH_MODE","secretValue":"local"}]}`)
	})
	local := enabled()
	local["TRAIO_INFISICAL_SITE_URL"] = server.URL
	result, err := LoadFrom(context.Background(), t.TempDir(), getter(local), func(c ProviderConfig) (Provider, error) { p.config = c; return p, nil })
	if err != nil || result.Database.Driver != "postgres" {
		t.Fatalf("SDK integration: %v", err)
	}
	buf := bytes.Buffer{}
	fmt.Fprintf(&buf, "%+v", result)
	if strings.Contains(buf.String(), "postgres://") {
		t.Fatal("result formatting leaked DSN")
	}
}

func TestInfisicalStartupPreservesSafeFailureDetails(t *testing.T) {
	for _, tc := range []struct {
		name, operation string
		status          int
		explicitDriver  bool
	}{
		{"login unauthorized", "authentication", 401, false},
		{"scope forbidden", "secret retrieval", 403, false},
		{"scope missing", "secret retrieval", 404, false},
		{"DSN forbidden", "secret retrieval", 403, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, server := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "login") && tc.operation != "authentication" {
					loginResponse(w)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"message":"server-echoed-sensitive-value","reqId":"sensitive-request-id"}`)
			})
			local := enabled()
			local["TRAIO_INFISICAL_SITE_URL"] = server.URL
			key := "configuration selector TRAIO_DATABASE_DRIVER"
			if tc.explicitDriver {
				local["TRAIO_DATABASE_DRIVER"] = "postgres"
				local["TRAIO_AUTH_MODE"] = "local"
				key = "TRAIO_DATABASE_DSN"
			}
			_, err := LoadFrom(context.Background(), t.TempDir(), getter(local), func(c ProviderConfig) (Provider, error) {
				p.config = c
				return p, nil
			})
			want := fmt.Sprintf("cannot retrieve %s: Infisical %s failed (HTTP %d)", key, tc.operation, tc.status)
			if err == nil || err.Error() != want {
				t.Fatalf("startup diagnostic: got %v, want %s", err, want)
			}
		})
	}
}
