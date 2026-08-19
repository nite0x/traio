package alpaca

import (
	"context"
	"strings"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

// Factory opens connection-scoped Alpaca API-key sessions.
type Factory struct{}

func NewFactory() *Factory { return &Factory{} }

func (*Factory) Definition() broker.ProviderDefinition {
	return broker.ProviderDefinition{
		Code: "ALPACA", Name: "Alpaca Markets", DisplayName: "Alpaca",
		AuthModes: []broker.AuthMode{broker.AuthModeAPIKey},
		Capabilities: broker.NewCapabilitySet(
			broker.CapabilityAccounts, broker.CapabilityCashBalances,
			broker.CapabilityPositions, broker.CapabilityDailyPerformance,
			broker.CapabilityTrading, broker.CapabilityAccountEquity,
		),
		ConfigSchema: broker.ConfigSchema{
			ProviderFields: []broker.ConfigField{
				{Key: "paper_base_url", Label: "Paper API 地址", Type: "url"},
				{Key: "live_base_url", Label: "Live API 地址", Type: "url"},
			},
			ConnectionFields: []broker.ConfigField{
				{Key: "username", Label: "登录用户名", Type: "string"},
				{Key: "api_key", Label: "API Key", Type: "string", Secret: true, Required: true},
				{Key: "api_secret", Label: "API Secret", Type: "string", Secret: true, Required: true},
				{Key: "base_url", Label: "API 地址覆盖", Type: "url"},
			},
		},
	}
}

func (*Factory) Open(_ context.Context, connection broker.ConnectionConfig) (broker.BrokerSession, error) {
	baseURL := configString(connection.Config, "base_url")
	if baseURL == "" {
		if strings.EqualFold(connection.Environment, "live") {
			baseURL = configString(connection.ProviderConfig, "live_base_url")
		} else {
			baseURL = configString(connection.ProviderConfig, "paper_base_url")
		}
	}
	cfg := config.AlpacaConfig{
		APIKey: connection.Secrets["api_key"], APISecret: connection.Secrets["api_secret"], BaseURL: baseURL,
	}
	cfg.Normalize()
	client := New(cfg)
	return &Session{id: connection.ID, Client: client}, nil
}

type Session struct {
	id int64
	*Client
}

var _ broker.BrokerSession = (*Session)(nil)
var _ broker.AuthenticationProvider = (*Session)(nil)
var _ broker.TradingProvider = (*Session)(nil)
var _ broker.AccountEquityProvider = (*Session)(nil)

func (s *Session) ConnectionID() int64       { return s.id }
func (*Session) ProviderCode() string        { return "ALPACA" }
func (*Session) Close(context.Context) error { return nil }

func (s *Session) BeginAuthentication(ctx context.Context, _ broker.AuthenticationRequest) (broker.LoginAction, error) {
	action, err := s.BeginLogin(ctx)
	return action, broker.AuthenticationOperationError("verify API credentials", err)
}

func (s *Session) AuthenticationStatus(ctx context.Context) (broker.LoginAction, error) {
	action, err := s.LoginStatus(ctx)
	return action, broker.AuthenticationOperationError("verify API credentials", err)
}

func (s *Session) Health(ctx context.Context) (broker.ConnectionHealth, error) {
	action, err := s.AuthenticationStatus(ctx)
	return broker.ConnectionHealthFromAuthentication(action, err)
}

func configString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}
