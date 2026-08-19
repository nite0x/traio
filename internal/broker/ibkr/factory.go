package ibkr

import (
	"context"
	"errors"
	"net/url"
	"strings"

	brokerapi "github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

// Factory opens connection-scoped IBKR Client Portal sessions.
type Factory struct{}

func NewFactory() *Factory { return &Factory{} }

func (*Factory) Definition() brokerapi.ProviderDefinition {
	return brokerapi.ProviderDefinition{
		Code: "IBKR", Name: "Interactive Brokers", DisplayName: "Interactive Brokers",
		AuthModes: []brokerapi.AuthMode{brokerapi.AuthModeGateway},
		Capabilities: brokerapi.NewCapabilitySet(
			brokerapi.CapabilityAccounts, brokerapi.CapabilityCashBalances,
			brokerapi.CapabilityPositions, brokerapi.CapabilityDailyPerformance,
			brokerapi.CapabilityInstruments, brokerapi.CapabilityMarketData,
			brokerapi.CapabilityCandles, brokerapi.CapabilityTrading,
			brokerapi.CapabilityAccountEquity,
		),
		ConfigSchema: brokerapi.ConfigSchema{
			ProviderFields: []brokerapi.ConfigField{
				{Key: "bundled_gateway_dir", Label: "内置 Gateway 目录", Type: "path"},
				{Key: "download_proxy", Label: "下载代理", Type: "url"},
				{Key: "gateway_proxy_host", Label: "Gateway 上游地址", Type: "url"},
				{Key: "gateway_allow_ips", Label: "允许访问的 IP", Type: "string_list"},
			},
			ConnectionFields: []brokerapi.ConfigField{
				{Key: "username", Label: "登录用户名", Type: "string"},
				{Key: "gateway_url", Label: "Gateway 地址", Type: "url", Required: true},
				{Key: "flex_token", Label: "Flex Token", Type: "string", Secret: true},
				{Key: "flex_query_id", Label: "Flex Query ID", Type: "string"},
				{Key: "flex_base_url", Label: "Flex API 地址", Type: "url"},
			},
		},
	}
}

func (*Factory) Open(_ context.Context, connection brokerapi.ConnectionConfig) (brokerapi.BrokerSession, error) {
	cfg, err := connectionConfig(connection)
	if err != nil {
		return nil, err
	}
	return &Session{id: connection.ID, Broker: NewBroker(cfg)}, nil
}

func connectionConfig(connection brokerapi.ConnectionConfig) (config.IBKRConfig, error) {
	gatewayURL := strings.TrimSuffix(strings.TrimRight(configString(connection.Config, "gateway_url"), "/"), "/v1/api")
	if gatewayURL == "" {
		return config.IBKRConfig{}, errors.New("gateway_url is required")
	}
	parsed, err := url.Parse(gatewayURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return config.IBKRConfig{}, errors.New("gateway_url must be an HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return config.IBKRConfig{}, errors.New("gateway_url must be an origin without credentials, path, query, or fragment")
	}
	flexBaseURL := configString(connection.Config, "flex_base_url")
	if flexBaseURL == "" {
		flexBaseURL = "https://ndcdyn.interactivebrokers.com/AccountManagement/FlexWebService"
	}
	return config.IBKRConfig{
		FlexToken: connection.Secrets["flex_token"], FlexQueryID: configString(connection.Config, "flex_query_id"),
		FlexBaseURL: flexBaseURL, GatewayURL: gatewayURL,
	}, nil
}

// Session is an opened IBKR connection. Embedding Broker preserves all current
// capabilities while BrokerSession supplies provider-neutral lifecycle hooks.
type Session struct {
	id int64
	*Broker
}

var _ brokerapi.BrokerSession = (*Session)(nil)
var _ brokerapi.AuthenticationProvider = (*Session)(nil)
var _ brokerapi.TradingProvider = (*Session)(nil)
var _ brokerapi.InstrumentProvider = (*Session)(nil)
var _ brokerapi.BatchMarketDataProvider = (*Session)(nil)
var _ brokerapi.CandleProvider = (*Session)(nil)
var _ brokerapi.AccountEquityProvider = (*Session)(nil)

func (s *Session) ConnectionID() int64       { return s.id }
func (*Session) ProviderCode() string        { return "IBKR" }
func (*Session) Close(context.Context) error { return nil }

// GatewayTarget exposes the private Client Portal origin to the runtime's
// IBKR login proxy without leaking the concrete client type.
func (s *Session) GatewayTarget() (*url.URL, error) {
	return url.Parse(s.BaseURL())
}

func (s *Session) BeginAuthentication(ctx context.Context, _ brokerapi.AuthenticationRequest) (brokerapi.LoginAction, error) {
	action, err := s.BeginLogin(ctx)
	return action, brokerapi.AuthenticationOperationError("begin login", err)
}

func (s *Session) AuthenticationStatus(ctx context.Context) (brokerapi.LoginAction, error) {
	action, err := s.LoginStatus(ctx)
	return action, brokerapi.AuthenticationOperationError("check status", err)
}

func (s *Session) SearchInstruments(ctx context.Context, query string) ([]brokerapi.Instrument, error) {
	return s.Broker.Client().SearchInstruments(ctx, query)
}

func (s *Session) GetQuotesByConID(ctx context.Context, conIDs []int64) ([]brokerapi.Quote, error) {
	return s.Broker.Client().GetQuotesByConID(ctx, conIDs)
}

func (s *Session) GetCandles(ctx context.Context, conID int64, period, bar string) ([]brokerapi.Candle, error) {
	return s.Broker.Client().GetCandles(ctx, conID, period, bar)
}

func (s *Session) AccountSummary(ctx context.Context) (brokerapi.AccountSummary, error) {
	return s.Broker.Client().AccountSummary(ctx)
}

func (s *Session) HistoricalEquity(ctx context.Context) ([]brokerapi.AccountEquityPoint, error) {
	return s.Broker.Client().HistoricalEquity(ctx)
}

func (s *Session) Health(ctx context.Context) (brokerapi.ConnectionHealth, error) {
	action, err := s.AuthenticationStatus(ctx)
	return brokerapi.ConnectionHealthFromAuthentication(action, err)
}

func configString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}
