package schwab

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nite/traio/internal/config"
)

func TestAuthURL(t *testing.T) {
	client := New(config.SchwabConfig{
		ClientID:    "client id",
		RedirectURI: "https://example.com/callback",
	})

	got, err := url.Parse(client.AuthURL("csrf-state"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Scheme+"://"+got.Host+got.Path != defaultAuthorizeURL {
		t.Fatalf("unexpected authorize URL: %s", got)
	}
	if got.Query().Get("client_id") != "client id" ||
		got.Query().Get("redirect_uri") != "https://example.com/callback" ||
		got.Query().Get("state") != "csrf-state" {
		t.Fatalf("unexpected authorize query: %v", got.Query())
	}
}

func TestParseCallbackURL(t *testing.T) {
	got, err := ParseCallbackURL("https://example.com/callback?code=abc%40&session=session-1&state=csrf-state")
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != "abc@" || got.Session != "session-1" || got.State != "csrf-state" {
		t.Fatalf("unexpected callback response: %+v", got)
	}
}

func TestExchangeCode(t *testing.T) {
	var received url.Values
	client := testClient(func(r *http.Request) *http.Response {
		if got := r.Header.Get("Authorization"); got != "Basic "+base64.StdEncoding.EncodeToString([]byte("client:secret")) {
			t.Errorf("unexpected authorization header: %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		received = r.Form
		return jsonResponse(http.StatusOK, `{"expires_in":1800,"token_type":"Bearer","refresh_token":"refresh-1","access_token":"access-1"}`)
	})
	if err := client.ExchangeCode(context.Background(), "code%40"); err != nil {
		t.Fatal(err)
	}
	if received.Get("grant_type") != "authorization_code" || received.Get("code") != "code@" ||
		received.Get("redirect_uri") != "https://example.com/callback" {
		t.Fatalf("unexpected form: %v", received)
	}
	token, ok := client.Token()
	if !ok || token.AccessToken != "access-1" || token.RefreshToken != "refresh-1" {
		t.Fatalf("unexpected token: %+v", token)
	}
	if token.ExpiresAt.IsZero() {
		t.Fatal("expected ExpiresAt to be calculated")
	}
}

func TestRefreshAndAuthenticatedRequest(t *testing.T) {
	var refreshCalls int
	client := testClient(func(r *http.Request) *http.Response {
		if r.URL.Path == "/token" {
			refreshCalls++
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-1" {
				t.Fatalf("unexpected refresh form: %v", r.Form)
			}
			return jsonResponse(http.StatusOK, `{"expires_in":1800,"token_type":"Bearer","access_token":"access-2"}`)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-2" {
			t.Fatalf("unexpected bearer header: %q", got)
		}
		return jsonResponse(http.StatusOK, `{"ok":true}`)
	})
	client.SetToken(Token{
		AccessToken:  "expired",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(-time.Minute),
	})
	resp, err := client.Do(context.Background(), http.MethodGet, "https://api.test/api", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if refreshCalls != 1 {
		t.Fatalf("refresh calls: got %d, want 1", refreshCalls)
	}
	token, _ := client.Token()
	if token.RefreshToken != "refresh-1" {
		t.Fatalf("refresh token was not preserved: %+v", token)
	}
}

func TestDoReturnsAPIError(t *testing.T) {
	client := testClient(func(_ *http.Request) *http.Response {
		return jsonResponse(http.StatusUnauthorized, "invalid token\n")
	})
	client.SetToken(Token{AccessToken: "access", ExpiresAt: time.Now().Add(time.Hour)})
	_, err := client.Do(context.Background(), http.MethodGet, "https://api.test/api", strings.NewReader(""))
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != http.StatusUnauthorized || apiErr.Body != "invalid token" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

type roundTripFunc func(*http.Request) *http.Response

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req), nil
}

func testClient(handler roundTripFunc) *Client {
	return New(
		config.SchwabConfig{
			ClientID:     "client",
			ClientSecret: "secret",
			RedirectURI:  "https://example.com/callback",
		},
		WithHTTPClient(&http.Client{Transport: handler}),
		WithOAuthURLs("https://api.test/authorize", "https://api.test/token"),
	)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
