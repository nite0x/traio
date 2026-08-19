package schwab

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/nite/traio/internal/broker"
)

func TestFactoryRestoresConnectionTokenAndProviderCredentials(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	sessionValue, err := NewFactory().Open(t.Context(), broker.ConnectionConfig{
		ID: 7, ProviderCode: "SCHWAB",
		ProviderConfig:  map[string]any{"redirect_uri": "https://app.example.test/callback"},
		ProviderSecrets: map[string]string{"client_id": "client", "client_secret": "secret"},
		Config:          map[string]any{"expires_at": expiresAt.Format(time.RFC3339Nano)},
		Secrets:         map[string]string{"access_token": "access", "refresh_token": "refresh"},
	})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	session := sessionValue.(*Session)
	if session.cfg.ClientID != "client" || session.cfg.ClientSecret != "secret" || session.cfg.RedirectURI != "https://app.example.test/callback" {
		t.Fatalf("unexpected provider config: %#v", session.cfg)
	}
	token, ok := session.Token()
	if !ok || token.AccessToken != "access" || token.RefreshToken != "refresh" || !token.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("unexpected restored token: token=%#v ok=%v", token, ok)
	}
}

func TestOAuthSessionExposesAuthenticationAndOptionalCapabilities(t *testing.T) {
	client := testClient(func(r *http.Request) *http.Response {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token form: %v", err)
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "callback-code" {
				t.Fatalf("callback code = %q", r.Form.Get("code"))
			}
			return jsonResponse(http.StatusOK, `{"expires_in":1800,"refresh_token":"refresh-1","access_token":"access-1"}`)
		case "refresh_token":
			return jsonResponse(http.StatusOK, `{"expires_in":1800,"access_token":"access-2"}`)
		default:
			t.Fatalf("unexpected grant type %q", r.Form.Get("grant_type"))
			return jsonResponse(http.StatusBadRequest, `{}`)
		}
	})
	session := &Session{id: 9, Client: client}
	authentication := any(session).(broker.AuthenticationProvider)
	begin, err := authentication.BeginAuthentication(t.Context(), broker.AuthenticationRequest{State: "oauth-state"})
	if err != nil {
		t.Fatalf("begin authentication: %v", err)
	}
	loginURL, err := url.Parse(begin.URL)
	if err != nil || loginURL.Query().Get("state") != "oauth-state" || begin.Authenticated {
		t.Fatalf("begin action = %#v, parsed=%v, err=%v", begin, loginURL, err)
	}
	callback := any(session).(broker.AuthenticationCallbackHandler)
	if err := callback.CompleteAuthentication(t.Context(), broker.AuthenticationCallback{
		CallbackURL: "https://example.com/callback?code=callback-code&state=oauth-state",
	}); err != nil {
		t.Fatalf("complete authentication: %v", err)
	}
	status, err := authentication.AuthenticationStatus(t.Context())
	if err != nil || !status.Authenticated {
		t.Fatalf("status = %#v, err=%v", status, err)
	}
	refresher := any(session).(broker.AuthenticationRefresher)
	if err := refresher.RefreshAuthentication(t.Context()); err != nil {
		t.Fatalf("refresh authentication: %v", err)
	}
	token, _ := session.Token()
	if token.AccessToken != "access-2" || token.RefreshToken != "refresh-1" {
		t.Fatalf("refreshed token = %#v", token)
	}
	if _, ok := any(session).(broker.AuthenticationRevoker); ok {
		t.Fatal("Schwab session advertises unsupported remote revocation")
	}
}

func TestFactoryRoutesTokenUpdatesByConnectionID(t *testing.T) {
	var gotID int64
	var gotToken Token
	factory := NewFactory(WithFactoryTokenHandler(func(connectionID int64, token Token) {
		gotID, gotToken = connectionID, token
	}))
	sessionValue, err := factory.Open(t.Context(), broker.ConnectionConfig{ID: 9})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	session := sessionValue.(*Session)
	session.onToken(Token{AccessToken: "new-access"})
	if gotID != 9 || gotToken.AccessToken != "new-access" {
		t.Fatalf("token callback: id=%d token=%#v", gotID, gotToken)
	}
}

func TestSessionCloseReleasesQuoteSubscribers(t *testing.T) {
	sessionValue, err := NewFactory().Open(t.Context(), broker.ConnectionConfig{ID: 10})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	session := sessionValue.(*Session)
	quotes, cancel := session.SubscribeQuotes([]string{"AAPL"})
	defer cancel()
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("close session: %v", err)
	}
	if _, open := <-quotes; open {
		t.Fatal("quote subscriber remained open")
	}
	select {
	case <-session.stream.exited:
	case <-time.After(time.Second):
		t.Fatal("quote streamer goroutine did not exit")
	}
}
