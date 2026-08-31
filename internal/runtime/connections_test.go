package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/schwab"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

type trackingProviderFactory struct {
	opens    atomic.Int32
	sessions []*trackingBrokerSession
	opened   chan struct{}
}

func (*trackingProviderFactory) Definition() broker.ProviderDefinition {
	return broker.ProviderDefinition{Code: "IBKR", Name: "Test IBKR"}
}

func (f *trackingProviderFactory) Open(_ context.Context, cfg broker.ConnectionConfig) (broker.BrokerSession, error) {
	f.opens.Add(1)
	session := &trackingBrokerSession{id: cfg.ID, code: cfg.ProviderCode}
	f.sessions = append(f.sessions, session)
	if f.opened != nil {
		close(f.opened)
		f.opened = nil
	}
	return session, nil
}

type trackingBrokerSession struct {
	id     int64
	code   string
	closed atomic.Int32
}

type lifecycleCapabilitySession struct {
	id             int64
	label          string
	summaryStarted chan struct{}
	summaryRelease chan struct{}
}

func (s *lifecycleCapabilitySession) ConnectionID() int64       { return s.id }
func (*lifecycleCapabilitySession) ProviderCode() string        { return "IBKR" }
func (*lifecycleCapabilitySession) Close(context.Context) error { return nil }
func (*lifecycleCapabilitySession) Health(context.Context) (broker.ConnectionHealth, error) {
	return broker.ConnectionHealth{State: broker.ConnectionStateConnected}, nil
}
func (s *lifecycleCapabilitySession) AccountSummary(context.Context) (broker.AccountSummary, error) {
	if s.summaryStarted != nil {
		close(s.summaryStarted)
		<-s.summaryRelease
	}
	return broker.AccountSummary{Broker: s.label}, nil
}
func (s *lifecycleCapabilitySession) HistoricalEquity(context.Context) ([]broker.AccountEquityPoint, error) {
	return []broker.AccountEquityPoint{{Source: s.label}}, nil
}
func (s *lifecycleCapabilitySession) ListAccountSnapshots(context.Context) ([]broker.AccountSnapshot, error) {
	return []broker.AccountSnapshot{{Account: broker.Account{ID: s.label}}}, nil
}

type authenticationProviderFactory struct {
	session *authenticationBrokerSession
}

func (*authenticationProviderFactory) Definition() broker.ProviderDefinition {
	return broker.ProviderDefinition{Code: "IBKR", Name: "Authentication Test"}
}

func (f *authenticationProviderFactory) Open(_ context.Context, cfg broker.ConnectionConfig) (broker.BrokerSession, error) {
	f.session = &authenticationBrokerSession{id: cfg.ID, code: cfg.ProviderCode}
	return f.session, nil
}

type authenticationBrokerSession struct {
	id            int64
	code          string
	beginState    string
	beginStarted  chan struct{}
	releaseBegin  chan struct{}
	callbackCode  string
	refreshCalled bool
	status        broker.LoginAction
	closed        atomic.Int32
}

func (s *authenticationBrokerSession) ConnectionID() int64  { return s.id }
func (s *authenticationBrokerSession) ProviderCode() string { return s.code }
func (s *authenticationBrokerSession) Health(context.Context) (broker.ConnectionHealth, error) {
	return broker.ConnectionHealthFromAuthentication(s.status, nil)
}
func (s *authenticationBrokerSession) Close(context.Context) error {
	s.closed.Add(1)
	return nil
}
func (s *authenticationBrokerSession) BeginAuthentication(_ context.Context, request broker.AuthenticationRequest) (broker.LoginAction, error) {
	s.beginState = request.State
	if s.beginStarted != nil {
		close(s.beginStarted)
		<-s.releaseBegin
	}
	return broker.LoginAction{URL: "https://login.example.test/authorize"}, nil
}
func (s *authenticationBrokerSession) AuthenticationStatus(context.Context) (broker.LoginAction, error) {
	return s.status, nil
}
func (s *authenticationBrokerSession) CompleteAuthentication(_ context.Context, callback broker.AuthenticationCallback) error {
	s.callbackCode = callback.Code
	s.status = broker.LoginAction{Authenticated: true, AccountID: "account-1"}
	return nil
}
func (s *authenticationBrokerSession) RefreshAuthentication(context.Context) error {
	s.refreshCalled = true
	return nil
}

func (s *trackingBrokerSession) ConnectionID() int64  { return s.id }
func (s *trackingBrokerSession) ProviderCode() string { return s.code }
func (*trackingBrokerSession) Health(context.Context) (broker.ConnectionHealth, error) {
	return broker.ConnectionHealth{State: broker.ConnectionStateConnected, CheckedAt: time.Now()}, nil
}
func (s *trackingBrokerSession) Close(context.Context) error {
	s.closed.Add(1)
	return nil
}

func TestBuildConnectionManagerLoadsEnabledConnectionsWithoutInventingDefaults(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := config.Default(baseDir)
	registry, err := BuildConnectionManager(cfg, st, baseDir)
	if err != nil {
		t.Fatalf("build brokers: %v", err)
	}
	if len(registry.SyncSources()) != 0 {
		t.Fatalf("empty database invented broker sources: %#v", registry.SyncSources())
	}
	connections, err := st.ListBrokerConnections(t.Context())
	if err != nil || len(connections) != 0 {
		t.Fatalf("build must not invent connections: connections=%#v err=%v", connections, err)
	}

	first, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "first", Name: "First", Enabled: true,
		Config: map[string]any{
			"gateway_id":  "one",
			"gateway_url": "https://gateway-one.example.test",
		},
	})
	if err != nil {
		t.Fatalf("create first connection: %v", err)
	}
	second, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "second", Name: "Second", Enabled: true,
		Config: map[string]any{
			"gateway_id":  "two",
			"gateway_url": "https://gateway-two.example.test",
		},
	})
	if err != nil {
		t.Fatalf("create second connection: %v", err)
	}
	if err := registry.Reload(t.Context()); err != nil {
		t.Fatalf("reload brokers: %v", err)
	}
	sources := registry.SyncSources()
	if len(sources) != 2 || sources[0].ConnectionID != first.ID || sources[1].ConnectionID != second.ID {
		t.Fatalf("unexpected connection-scoped sources: %#v", sources)
	}
	if err := st.SetBrokerConnectionEnabled(t.Context(), first.ID, false); err != nil {
		t.Fatalf("disable first connection: %v", err)
	}
	if err := registry.Reload(t.Context()); err != nil {
		t.Fatalf("reload disabled brokers: %v", err)
	}
	sources = registry.SyncSources()
	if len(sources) != 1 || sources[0].ConnectionID != second.ID {
		t.Fatalf("disabled connection remained active: %#v", sources)
	}
}

func TestReloadReusesUnchangedSessionAndClosesReplacedOrDisabledSession(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "tracked", Name: "Tracked", Enabled: true,
		Config: map[string]any{"gateway_url": "https://one.example.test"},
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	factory := &trackingProviderFactory{}
	providers := broker.NewProviderRegistry()
	if err := providers.Register(factory); err != nil {
		t.Fatalf("register factory: %v", err)
	}
	runtime, err := buildConnectionManagerWithProviderRegistry(config.Default(baseDir), st, baseDir, providers)
	if err != nil {
		t.Fatalf("build brokers: %v", err)
	}
	if sources := runtime.SyncSources(); len(sources) != 0 {
		t.Fatalf("session without portfolio capability produced sync sources: %#v", sources)
	}
	if factory.opens.Load() != 1 {
		t.Fatalf("initial opens = %d, want 1", factory.opens.Load())
	}
	first := factory.sessions[0]
	if err := runtime.Reload(t.Context()); err != nil {
		t.Fatalf("reload unchanged: %v", err)
	}
	if factory.opens.Load() != 1 || first.closed.Load() != 0 {
		t.Fatalf("unchanged session was replaced: opens=%d closes=%d", factory.opens.Load(), first.closed.Load())
	}

	connection.Config["gateway_url"] = "https://two.example.test"
	if _, err := st.UpsertBrokerConnection(t.Context(), connection); err != nil {
		t.Fatalf("update connection: %v", err)
	}
	if err := runtime.Reload(t.Context()); err != nil {
		t.Fatalf("reload changed: %v", err)
	}
	if factory.opens.Load() != 2 || first.closed.Load() != 1 {
		t.Fatalf("changed session lifecycle: opens=%d first closes=%d", factory.opens.Load(), first.closed.Load())
	}
	second := factory.sessions[1]
	if err := st.SetBrokerConnectionEnabled(t.Context(), connection.ID, false); err != nil {
		t.Fatalf("disable connection: %v", err)
	}
	if err := runtime.Reload(t.Context()); err != nil {
		t.Fatalf("reload disabled: %v", err)
	}
	if second.closed.Load() != 1 || len(runtime.sessions) != 0 {
		t.Fatalf("disabled session remained: closes=%d sessions=%d", second.closed.Load(), len(runtime.sessions))
	}
}

func TestDefaultSessionLeaseBlocksReloadUntilReleased(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "leased", Name: "Leased", Enabled: true,
		Config: map[string]any{"gateway_url": "https://one.example.test"},
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	factory := &trackingProviderFactory{}
	providers := broker.NewProviderRegistry()
	if err := providers.Register(factory); err != nil {
		t.Fatalf("register factory: %v", err)
	}
	manager, err := buildConnectionManagerWithProviderRegistry(config.Default(baseDir), st, baseDir, providers)
	if err != nil {
		t.Fatalf("build connection manager: %v", err)
	}
	first := factory.sessions[0]
	session, release := manager.AcquireDefaultSession("ibkr")
	if session != first {
		release()
		t.Fatalf("leased session = %p, want %p", session, first)
	}

	connection.Config["gateway_url"] = "https://two.example.test"
	if _, err := st.UpsertBrokerConnection(t.Context(), connection); err != nil {
		release()
		t.Fatalf("update connection: %v", err)
	}
	opened := make(chan struct{})
	factory.opened = opened
	reloadDone := make(chan error, 1)
	go func() { reloadDone <- manager.Reload(t.Context()) }()
	<-opened
	select {
	case err := <-reloadDone:
		release()
		t.Fatalf("reload completed while session was leased: %v", err)
	default:
	}
	if got := first.closed.Load(); got != 0 {
		release()
		t.Fatalf("leased session closed before release: %d", got)
	}

	release()
	if err := <-reloadDone; err != nil {
		t.Fatalf("reload after release: %v", err)
	}
	if got := first.closed.Load(); got != 1 {
		t.Fatalf("released session close count = %d, want 1", got)
	}
}

func TestAccountSourceLeasesCurrentSessionDuringCall(t *testing.T) {
	old := &lifecycleCapabilitySession{
		id: 1, label: "old", summaryStarted: make(chan struct{}), summaryRelease: make(chan struct{}),
	}
	replacement := &lifecycleCapabilitySession{id: 1, label: "new"}
	manager := &ConnectionManager{sessions: map[int64]broker.BrokerSession{1: old}}
	sources := manager.AccountSources()
	if len(sources) != 1 {
		t.Fatalf("account sources = %#v", sources)
	}
	callDone := make(chan broker.AccountSummary, 1)
	go func() {
		summary, _ := sources[0].Provider.AccountSummary(t.Context())
		callDone <- summary
	}()
	<-old.summaryStarted
	replaceDone := make(chan struct{})
	go func() {
		manager.mu.Lock()
		manager.sessions[1] = replacement
		manager.mu.Unlock()
		close(replaceDone)
	}()
	select {
	case <-replaceDone:
		t.Fatal("session replacement completed during an account call")
	case <-time.After(20 * time.Millisecond):
	}
	close(old.summaryRelease)
	if summary := <-callDone; summary.Broker != "old" {
		t.Fatalf("in-flight account summary = %#v", summary)
	}
	select {
	case <-replaceDone:
	case <-time.After(time.Second):
		t.Fatal("session replacement did not complete after account call")
	}
	summary, err := sources[0].Provider.AccountSummary(t.Context())
	if err != nil || summary.Broker != "new" {
		t.Fatalf("dynamic account source did not resolve replacement: summary=%#v err=%v", summary, err)
	}
}

func TestPortfolioSourceLeaseBlocksSessionReplacement(t *testing.T) {
	old := &lifecycleCapabilitySession{id: 1, label: "old"}
	replacement := &lifecycleCapabilitySession{id: 1, label: "new"}
	manager := &ConnectionManager{sessions: map[int64]broker.BrokerSession{1: old}}
	sources := manager.SyncSources()
	if len(sources) != 1 {
		t.Fatalf("portfolio sources = %#v", sources)
	}
	provider, release := sources[0].Acquire()
	if provider == nil {
		release()
		t.Fatal("portfolio source did not acquire provider")
	}
	replaceDone := make(chan struct{})
	go func() {
		manager.mu.Lock()
		manager.sessions[1] = replacement
		manager.mu.Unlock()
		close(replaceDone)
	}()
	select {
	case <-replaceDone:
		release()
		t.Fatal("session replacement completed while portfolio provider was leased")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case <-replaceDone:
	case <-time.After(time.Second):
		t.Fatal("session replacement did not complete after portfolio release")
	}
	provider, release = sources[0].Acquire()
	defer release()
	snapshots, err := provider.ListAccountSnapshots(t.Context())
	if err != nil || len(snapshots) != 1 || snapshots[0].Account.ID != "new" {
		t.Fatalf("dynamic portfolio source did not resolve replacement: snapshots=%#v err=%v", snapshots, err)
	}
}

func TestConnectionAuthenticationRoutesThroughSessionCapabilities(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "auth", Name: "Authentication", Enabled: true,
		Config: map[string]any{"gateway_url": "https://unused.example.test"},
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	factory := &authenticationProviderFactory{}
	providers := broker.NewProviderRegistry()
	if err := providers.Register(factory); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	runtime, err := buildConnectionManagerWithProviderRegistry(config.Default(baseDir), st, baseDir, providers)
	if err != nil {
		t.Fatalf("build brokers: %v", err)
	}
	action, err := runtime.BeginConnectionLogin(t.Context(), connection.ID, "oauth-state")
	if err != nil || action.URL != "https://login.example.test/authorize" || factory.session.beginState != "oauth-state" {
		t.Fatalf("begin action=%#v state=%q err=%v", action, factory.session.beginState, err)
	}
	status, err := runtime.ConnectionLoginStatus(t.Context(), connection.ID)
	if err != nil || status.Authenticated {
		t.Fatalf("initial status=%#v err=%v", status, err)
	}
	if err := runtime.ExchangeConnectionOAuthCode(t.Context(), connection.ID, "callback-code"); err != nil {
		t.Fatalf("complete authentication: %v", err)
	}
	if factory.session.callbackCode != "callback-code" {
		t.Fatalf("callback code = %q", factory.session.callbackCode)
	}
	status, err = runtime.ConnectionLoginStatus(t.Context(), connection.ID)
	if err != nil || !status.Authenticated || status.AccountID != "account-1" {
		t.Fatalf("authenticated status=%#v err=%v", status, err)
	}
	if err := runtime.RefreshConnectionAuthentication(t.Context(), connection.ID); err != nil || !factory.session.refreshCalled {
		t.Fatalf("refresh called=%v err=%v", factory.session.refreshCalled, err)
	}
	if err := runtime.RevokeConnectionAuthentication(t.Context(), connection.ID); !errors.Is(err, broker.ErrAuthenticationRevokeUnavailable) {
		t.Fatalf("revoke error=%v, want ErrAuthenticationRevokeUnavailable", err)
	}
}

func TestConnectionAuthenticationReportsMissingCapabilities(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "no-auth", Name: "No Authentication", Enabled: true,
		Config: map[string]any{"gateway_url": "https://unused.example.test"},
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	providers := broker.NewProviderRegistry()
	if err := providers.Register(&trackingProviderFactory{}); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	runtime, err := buildConnectionManagerWithProviderRegistry(config.Default(baseDir), st, baseDir, providers)
	if err != nil {
		t.Fatalf("build brokers: %v", err)
	}
	if _, err := runtime.BeginConnectionLogin(t.Context(), connection.ID, ""); !errors.Is(err, broker.ErrAuthenticationUnavailable) {
		t.Fatalf("begin error=%v, want ErrAuthenticationUnavailable", err)
	}
	if _, err := runtime.ConnectionLoginStatus(t.Context(), connection.ID); !errors.Is(err, broker.ErrAuthenticationUnavailable) {
		t.Fatalf("status error=%v, want ErrAuthenticationUnavailable", err)
	}
	if err := runtime.ExchangeConnectionOAuthCode(t.Context(), connection.ID, "code"); !errors.Is(err, broker.ErrAuthenticationCallbackUnavailable) {
		t.Fatalf("callback error=%v, want ErrAuthenticationCallbackUnavailable", err)
	}
	if err := runtime.RefreshConnectionAuthentication(t.Context(), connection.ID); !errors.Is(err, broker.ErrAuthenticationRefreshUnavailable) {
		t.Fatalf("refresh error=%v, want ErrAuthenticationRefreshUnavailable", err)
	}
}

func TestSchwabAuthenticationTokenPersistenceKeepsPublicConnectionSecretFree(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "SCHWAB", ConnectionKey: "oauth", Name: "OAuth", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	persistSchwabToken(st, connection.ID, schwab.Token{
		AccessToken: "access-token-secret", RefreshToken: "refresh-token-secret", ExpiresAt: expiresAt,
	})
	runtimeConfig, err := st.GetBrokerConnectionRuntimeConfig(t.Context(), connection.ID)
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	if runtimeConfig.Status != store.BrokerConnectionStatusConnected ||
		runtimeConfig.Secrets["access_token"] != "access-token-secret" ||
		runtimeConfig.Secrets["refresh_token"] != "refresh-token-secret" ||
		runtimeConfig.Config["expires_at"] != expiresAt.Format(time.RFC3339Nano) {
		t.Fatal("persisted OAuth state did not retain status, tokens, and expiration")
	}
	publicConnection, err := st.GetBrokerConnection(t.Context(), connection.ID)
	if err != nil {
		t.Fatalf("load public connection: %v", err)
	}
	payload, err := json.Marshal(publicConnection)
	if err != nil {
		t.Fatalf("marshal public connection: %v", err)
	}
	if strings.Contains(string(payload), "access-token-secret") || strings.Contains(string(payload), "refresh-token-secret") {
		t.Fatalf("public connection exposed OAuth token: %s", payload)
	}
}

func TestReloadWaitsForInFlightAuthenticationBeforeClosingSession(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "auth-race", Name: "Authentication Race", Enabled: true,
		Config: map[string]any{"gateway_url": "https://one.example.test"},
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	factory := &authenticationProviderFactory{}
	providers := broker.NewProviderRegistry()
	if err := providers.Register(factory); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	runtime, err := buildConnectionManagerWithProviderRegistry(config.Default(baseDir), st, baseDir, providers)
	if err != nil {
		t.Fatalf("build brokers: %v", err)
	}
	first := factory.session
	first.beginStarted = make(chan struct{})
	first.releaseBegin = make(chan struct{})
	loginDone := make(chan error, 1)
	go func() {
		_, err := runtime.BeginConnectionLogin(context.Background(), connection.ID, "state")
		loginDone <- err
	}()
	<-first.beginStarted
	connection.Config["gateway_url"] = "https://two.example.test"
	if _, err := st.UpsertBrokerConnection(t.Context(), connection); err != nil {
		t.Fatalf("update connection: %v", err)
	}
	reloadDone := make(chan error, 1)
	go func() { reloadDone <- runtime.Reload(context.Background()) }()
	select {
	case err := <-reloadDone:
		t.Fatalf("Reload completed during authentication: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if first.closed.Load() != 0 {
		t.Fatal("session closed during in-flight authentication")
	}
	close(first.releaseBegin)
	if err := <-loginDone; err != nil {
		t.Fatalf("begin login: %v", err)
	}
	if err := <-reloadDone; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if first.closed.Load() != 1 {
		t.Fatalf("replaced session closes = %d, want 1", first.closed.Load())
	}
}

func TestBuildConnectionManagerFailsForUnregisteredProvider(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "missing", Name: "Missing", Enabled: true,
		Config: map[string]any{"gateway_id": "missing", "gateway_url": "https://gateway.example.test"},
	}); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	_, err = buildConnectionManagerWithProviderRegistry(config.Default(baseDir), st, baseDir, broker.NewProviderRegistry())
	if !errors.Is(err, broker.ErrProviderNotRegistered) {
		t.Fatalf("build error = %v, want ErrProviderNotRegistered", err)
	}
}

func TestBuildConnectionManagerUsesResolvedManagerGatewayConnection(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, err = st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "primary", Name: "My IBKR", Enabled: true,
		Config: map[string]any{
			"gateway_id":  "primary",
			"gateway_url": "https://primary.ibkr.example.test",
		},
	})
	if err != nil {
		t.Fatalf("create legacy connection: %v", err)
	}

	_, err = BuildConnectionManager(config.Default(baseDir), st, baseDir)
	if err != nil {
		t.Fatalf("build brokers: %v", err)
	}
}

func TestSyncSourcesIncludeEverySupportedConnection(t *testing.T) {
	baseDir := t.TempDir()
	st, err := store.Open(filepath.Join(baseDir, "traio.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	connections := []store.BrokerConnection{
		{ProviderCode: "IBKR", ConnectionKey: "ibkr", Name: "IBKR", Enabled: true, Config: map[string]any{"gateway_id": "primary", "gateway_url": "https://ibkr.example.test"}},
		{ProviderCode: "SCHWAB", ConnectionKey: "schwab", Name: "Schwab", Enabled: true},
		{ProviderCode: "ALPACA", ConnectionKey: "alpaca", Name: "Alpaca", Enabled: true},
	}
	wantIDs := make([]int64, 0, len(connections))
	for _, connection := range connections {
		created, err := st.UpsertBrokerConnection(t.Context(), connection)
		if err != nil {
			t.Fatalf("create %s connection: %v", connection.ProviderCode, err)
		}
		wantIDs = append(wantIDs, created.ID)
	}

	registry, err := BuildConnectionManager(config.Default(baseDir), st, baseDir)
	if err != nil {
		t.Fatalf("build brokers: %v", err)
	}
	sources := registry.SyncSources()
	if len(sources) != 3 {
		t.Fatalf("got %d sync sources, want 3: %#v", len(sources), sources)
	}
	gotNames := make([]string, 0, len(sources))
	gotIDs := make([]int64, 0, len(sources))
	for _, source := range sources {
		gotNames = append(gotNames, source.Name)
		gotIDs = append(gotIDs, source.ConnectionID)
	}
	if !slices.Equal(gotNames, []string{"IBKR", "SCHWAB", "ALPACA"}) {
		t.Fatalf("unexpected sync source names: %v", gotNames)
	}
	slices.Sort(gotIDs)
	slices.Sort(wantIDs)
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("unexpected sync source IDs: got %v want %v", gotIDs, wantIDs)
	}
}
