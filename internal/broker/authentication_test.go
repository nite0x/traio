package broker

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAuthenticationTransientValuesAreNotSerializable(t *testing.T) {
	payload, err := json.Marshal(struct {
		Request  AuthenticationRequest  `json:"request"`
		Callback AuthenticationCallback `json:"callback"`
	}{
		Request:  AuthenticationRequest{State: "oauth-state-secret"},
		Callback: AuthenticationCallback{Code: "authorization-code-secret", CallbackURL: "https://example.test/?code=secret"},
	})
	if err != nil {
		t.Fatalf("marshal authentication values: %v", err)
	}
	for _, secret := range []string{"oauth-state-secret", "authorization-code-secret", "code=secret"} {
		if strings.Contains(string(payload), secret) {
			t.Fatalf("serialized transient credential %q in %s", secret, payload)
		}
	}
}

func TestAuthenticationCallbackResolvesCodeWithoutEchoingValues(t *testing.T) {
	callback := AuthenticationCallback{CallbackURL: "https://example.test/callback?code=code%40value&state=state"}
	code, err := callback.AuthorizationCode()
	if err != nil || code != "code@value" {
		t.Fatalf("authorization code = %q, err=%v", code, err)
	}
	secret := "sensitive-description"
	_, err = (AuthenticationCallback{CallbackURL: "https://example.test/callback?error=denied&error_description=" + secret}).AuthorizationCode()
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe callback error %v", err)
	}
	providerErr := errors.New("response echoed access_token=do-not-disclose")
	safeErr := AuthenticationOperationError("complete OAuth", providerErr)
	if !errors.Is(safeErr, ErrAuthenticationFailed) || strings.Contains(safeErr.Error(), "do-not-disclose") {
		t.Fatalf("unsafe operation error %v", safeErr)
	}
}

func TestConnectionHealthFromAuthentication(t *testing.T) {
	disconnected, err := ConnectionHealthFromAuthentication(LoginAction{}, nil)
	if err != nil || disconnected.State != ConnectionStateDisconnected || disconnected.CheckedAt.IsZero() {
		t.Fatalf("disconnected health = %#v, err=%v", disconnected, err)
	}
	connected, err := ConnectionHealthFromAuthentication(LoginAction{Authenticated: true}, nil)
	if err != nil || connected.State != ConnectionStateConnected {
		t.Fatalf("connected health = %#v, err=%v", connected, err)
	}
	authenticating, err := ConnectionHealthFromAuthentication(LoginAction{URL: "https://login.example.test"}, nil)
	if err != nil || authenticating.State != ConnectionStateAuthenticating {
		t.Fatalf("authenticating health = %#v, err=%v", authenticating, err)
	}
	statusErr := errors.New("upstream rejected api_secret=do-not-disclose")
	failed, err := ConnectionHealthFromAuthentication(LoginAction{}, statusErr)
	if !errors.Is(err, ErrAuthenticationFailed) || strings.Contains(err.Error(), "do-not-disclose") || failed.State != ConnectionStateError {
		t.Fatalf("failed health = %#v, err=%v", failed, err)
	}
	if strings.Contains(failed.Message, "do-not-disclose") || failed.Message != "authentication status check failed" {
		t.Fatalf("unsafe health message %q", failed.Message)
	}
}
