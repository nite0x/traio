package schwab

import (
	"context"
	"strings"
	"time"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

type FactoryOption func(*Factory)

// WithFactoryTokenHandler persists refreshed OAuth tokens outside the provider
// package without coupling the factory to a store implementation.
func WithFactoryTokenHandler(handler func(connectionID int64, token Token)) FactoryOption {
	return func(factory *Factory) { factory.onToken = handler }
}

// Factory opens connection-scoped Schwab OAuth sessions.
type Factory struct {
	onToken func(connectionID int64, token Token)
}

func NewFactory(options ...FactoryOption) *Factory {
	factory := &Factory{}
	for _, option := range options {
		option(factory)
	}
	return factory
}

func (*Factory) Definition() broker.ProviderDefinition {
	return broker.ProviderDefinition{
		Code: "SCHWAB", Name: "Charles Schwab", DisplayName: "Charles Schwab",
		AuthModes: []broker.AuthMode{broker.AuthModeOAuth},
		Capabilities: broker.NewCapabilitySet(
			broker.CapabilityAccounts, broker.CapabilityCashBalances,
			broker.CapabilityPositions, broker.CapabilityDailyPerformance,
			broker.CapabilityAccountSnapshots, broker.CapabilityMarketData,
			broker.CapabilityTrading, broker.CapabilityAccountEquity,
		),
		ConfigSchema: broker.ConfigSchema{
			ProviderFields: []broker.ConfigField{
				{Key: "client_id", Label: "Client ID", Type: "string", Secret: true, Required: true},
				{Key: "client_secret", Label: "Client Secret", Type: "string", Secret: true, Required: true},
				{Key: "redirect_uri", Label: "Redirect URI", Type: "url", Required: true},
			},
			ConnectionFields: []broker.ConfigField{
				{Key: "username", Label: "登录用户名", Type: "string"},
				{Key: "access_token", Label: "Access Token", Type: "string", Secret: true},
				{Key: "refresh_token", Label: "Refresh Token", Type: "string", Secret: true},
				{Key: "expires_at", Label: "Token 过期时间", Type: "datetime"},
			},
		},
	}
}

func (f *Factory) Open(_ context.Context, connection broker.ConnectionConfig) (broker.BrokerSession, error) {
	cfg := config.SchwabConfig{
		ClientID: connection.ProviderSecrets["client_id"], ClientSecret: connection.ProviderSecrets["client_secret"],
		RedirectURI: configString(connection.ProviderConfig, "redirect_uri"),
	}
	options := []Option{}
	if f.onToken != nil {
		options = append(options, WithTokenHandler(func(token Token) { f.onToken(connection.ID, token) }))
	}
	client := New(cfg, options...)
	if accessToken := connection.Secrets["access_token"]; accessToken != "" {
		expiresAt, _ := time.Parse(time.RFC3339Nano, configString(connection.Config, "expires_at"))
		client.SetToken(Token{AccessToken: accessToken, RefreshToken: connection.Secrets["refresh_token"], ExpiresAt: expiresAt})
	}
	return &Session{id: connection.ID, Client: client}, nil
}

type Session struct {
	id int64
	*Client
}

var _ broker.BrokerSession = (*Session)(nil)
var _ broker.AuthenticationProvider = (*Session)(nil)
var _ broker.AuthenticationCallbackHandler = (*Session)(nil)
var _ broker.AuthenticationRefresher = (*Session)(nil)
var _ broker.PortfolioProvider = (*Session)(nil)
var _ broker.MarketDataProvider = (*Session)(nil)
var _ broker.TradingProvider = (*Session)(nil)
var _ broker.AccountEquityProvider = (*Session)(nil)

func (s *Session) ConnectionID() int64         { return s.id }
func (*Session) ProviderCode() string          { return "SCHWAB" }
func (s *Session) Close(context.Context) error { return s.Client.Close() }

func (s *Session) BeginAuthentication(_ context.Context, request broker.AuthenticationRequest) (broker.LoginAction, error) {
	_, authenticated := s.Token()
	return broker.LoginAction{URL: s.AuthURL(request.State), Authenticated: authenticated}, nil
}

func (s *Session) AuthenticationStatus(context.Context) (broker.LoginAction, error) {
	_, authenticated := s.Token()
	return broker.LoginAction{Authenticated: authenticated}, nil
}

func (s *Session) CompleteAuthentication(ctx context.Context, callback broker.AuthenticationCallback) error {
	code, err := callback.AuthorizationCode()
	if err != nil {
		return broker.AuthenticationOperationError("complete OAuth", err)
	}
	return broker.AuthenticationOperationError("complete OAuth", s.ExchangeCode(ctx, code))
}

func (s *Session) RefreshAuthentication(ctx context.Context) error {
	_, err := s.RefreshAccessToken(ctx)
	return broker.AuthenticationOperationError("refresh OAuth", err)
}

func (s *Session) Health(ctx context.Context) (broker.ConnectionHealth, error) {
	action, err := s.AuthenticationStatus(ctx)
	return broker.ConnectionHealthFromAuthentication(action, err)
}

func configString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}
