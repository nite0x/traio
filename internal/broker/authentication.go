package broker

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrAuthenticationUnavailable reports that an opened broker session does
	// not expose the provider-neutral authentication capability.
	ErrAuthenticationUnavailable = errors.New("broker authentication unavailable")
	// ErrAuthenticationCallbackUnavailable reports that a provider does not
	// support completing authentication from an authorization callback.
	ErrAuthenticationCallbackUnavailable = errors.New("broker authentication callback unavailable")
	// ErrAuthenticationRefreshUnavailable reports that a provider does not
	// support explicitly refreshing its authentication material.
	ErrAuthenticationRefreshUnavailable = errors.New("broker authentication refresh unavailable")
	// ErrAuthenticationRevokeUnavailable reports that a provider does not
	// support revoking its authentication material through this application.
	ErrAuthenticationRevokeUnavailable = errors.New("broker authentication revoke unavailable")
	// ErrAuthenticationFailed is a safe public error for provider failures.
	// The underlying error is deliberately not wrapped because OAuth codes,
	// API keys, and upstream response bodies can contain credentials.
	ErrAuthenticationFailed = errors.New("broker authentication failed")
)

// AuthenticationRequest contains non-persistent input needed to begin a
// provider login. Values are deliberately excluded from JSON so transient
// OAuth state cannot accidentally be returned or logged as connection data.
type AuthenticationRequest struct {
	State string `json:"-"`
}

// AuthenticationCallback contains transient values returned by an
// authorization provider. Callback data may contain short-lived credentials,
// so it must never be serialized as part of API or diagnostic output.
type AuthenticationCallback struct {
	Code        string `json:"-"`
	CallbackURL string `json:"-"`
}

// AuthorizationCode resolves a direct code or an OAuth callback URL without
// retaining or echoing either value in an error.
func (c AuthenticationCallback) AuthorizationCode() (string, error) {
	if code := strings.TrimSpace(c.Code); code != "" {
		return code, nil
	}
	callbackURL := strings.TrimSpace(c.CallbackURL)
	if callbackURL == "" {
		return "", errors.New("authorization code or callback URL is required")
	}
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		return "", errors.New("authorization callback URL is invalid")
	}
	if parsed.Query().Get("error") != "" {
		return "", errors.New("authorization provider returned an error")
	}
	code := strings.TrimSpace(parsed.Query().Get("code"))
	if code == "" {
		return "", errors.New("authorization callback URL does not include a code")
	}
	return code, nil
}

// AuthenticationProvider is the common authentication surface implemented by
// every session that can authenticate. Begin may return a browser URL; Status
// only observes the current session and must not start an interactive flow.
type AuthenticationProvider interface {
	BeginAuthentication(ctx context.Context, request AuthenticationRequest) (LoginAction, error)
	AuthenticationStatus(ctx context.Context) (LoginAction, error)
}

// AuthenticationCallbackHandler is an optional capability for OAuth-like
// providers that complete authentication from a callback or code.
type AuthenticationCallbackHandler interface {
	CompleteAuthentication(ctx context.Context, callback AuthenticationCallback) error
}

// AuthenticationRefresher is an optional capability for renewable credentials.
type AuthenticationRefresher interface {
	RefreshAuthentication(ctx context.Context) error
}

// AuthenticationRevoker is an optional capability. Providers should only
// implement it when revocation is supported and persisted atomically.
type AuthenticationRevoker interface {
	RevokeAuthentication(ctx context.Context) error
}

// AuthenticationOperationError converts a provider error into a stable public
// error without retaining credential-bearing request or response details.
func AuthenticationOperationError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	operation = strings.TrimSpace(operation)
	if operation == "" {
		return ErrAuthenticationFailed
	}
	return fmt.Errorf("%w: %s", ErrAuthenticationFailed, operation)
}

// ConnectionHealthFromAuthentication applies one state mapping across broker
// implementations. Both Message and the returned public error are sanitized so
// health JSON and routine error logging cannot leak provider responses, request
// URLs, tokens, keys, or secrets.
func ConnectionHealthFromAuthentication(status LoginAction, err error) (ConnectionHealth, error) {
	health := ConnectionHealth{
		State:     ConnectionStateDisconnected,
		CheckedAt: time.Now().UTC(),
	}
	if err != nil {
		health.State = ConnectionStateError
		health.Message = "authentication status check failed"
		return health, AuthenticationOperationError("check status", err)
	}
	if status.Authenticated {
		health.State = ConnectionStateConnected
	} else if status.URL != "" {
		health.State = ConnectionStateAuthenticating
	}
	return health, nil
}
