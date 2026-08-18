package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/nite/traio/internal/store"
	"golang.org/x/oauth2"
)

type Mode string

const (
	ModeLocal       Mode = "local"
	ModeOIDC        Mode = "oidc"
	ModePassword    Mode = "password"
	ModeDisabledDev Mode = "disabled-dev"
)

type Permission string

const (
	PermissionView           Permission = "view"
	PermissionWatchlistWrite Permission = "watchlist.write"
	PermissionBrokerManage   Permission = "broker.manage"
	PermissionBrokerSync     Permission = "broker.sync"
	PermissionSettings       Permission = "settings.manage"
	PermissionMembers        Permission = "members.manage"
	PermissionOwner          Permission = "workspace.owner"
	PermissionSystem         Permission = "system.manage"
	PermissionTrade          Permission = "trade.execute"
)

type Principal struct {
	UserID      int64  `json:"user_id"`
	WorkspaceID int64  `json:"workspace_id"`
	Email       string `json:"email,omitempty"`
	Name        string `json:"name,omitempty"`
	Role        string `json:"role"`
	Source      Mode   `json:"source"`
}

type contextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(contextKey{}).(Principal)
	return principal, ok
}

func LocalPrincipal() Principal {
	return Principal{WorkspaceID: store.DefaultWorkspaceID, Role: "owner", Source: ModeLocal, Name: "Local Desktop"}
}

func Allows(role string, permission Permission) bool {
	if role == "owner" {
		return true
	}
	switch role {
	case "admin":
		return permission != PermissionOwner
	case "member":
		return permission == PermissionView || permission == PermissionWatchlistWrite || permission == PermissionBrokerSync
	case "viewer":
		return permission == PermissionView
	default:
		return false
	}
}

type Config struct {
	Mode              Mode
	IssuerURL         string
	ClientID          string
	ClientSecret      string
	RedirectURL       string
	Scopes            []string
	SessionTTL        time.Duration
	FlowTTL           time.Duration
	CookieName        string
	CSRFCookieName    string
	CookieSecure      bool
	BootstrapUsername string
	BootstrapPassword string
	BootstrapEmail    string
	BootstrapName     string
}

type Service struct {
	repository   store.AuthRepository
	config       Config
	provider     *oidc.Provider
	verifier     *oidc.IDTokenVerifier
	oauth2Config oauth2.Config
	now          func() time.Time
}

type LoginResult struct {
	Identity     store.AuthIdentity
	SessionToken string
	CSRFToken    string
	ExpiresAt    time.Time
	ReturnTo     string
}

func NewService(ctx context.Context, repository store.AuthRepository, config Config) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("auth repository is required")
	}
	if config.Mode == "" {
		config.Mode = ModeLocal
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = 12 * time.Hour
	}
	if config.FlowTTL <= 0 {
		config.FlowTTL = 10 * time.Minute
	}
	if config.CookieName == "" {
		config.CookieName = "traio_session"
	}
	if config.CSRFCookieName == "" {
		config.CSRFCookieName = "traio_csrf"
	}
	service := &Service{repository: repository, config: config, now: time.Now}
	if config.Mode == ModePassword {
		hasIdentity, err := repository.HasPasswordIdentity(ctx)
		if err != nil {
			return nil, fmt.Errorf("check built-in account: %w", err)
		}
		username := normalizeUsername(config.BootstrapUsername)
		password := config.BootstrapPassword
		if username == "" && password == "" {
			if !hasIdentity {
				return nil, fmt.Errorf("built-in login is not initialized; set TRAIO_BOOTSTRAP_ADMIN_USERNAME and TRAIO_BOOTSTRAP_ADMIN_PASSWORD")
			}
			return service, nil
		}
		if err := validateBootstrapCredential(username, password); err != nil {
			return nil, err
		}
		passwordHash, err := hashPassword(password)
		if err != nil {
			return nil, fmt.Errorf("hash bootstrap password: %w", err)
		}
		email := strings.ToLower(strings.TrimSpace(config.BootstrapEmail))
		if email == "" && strings.Contains(username, "@") {
			email = username
		}
		name := strings.TrimSpace(config.BootstrapName)
		if name == "" {
			name = username
		}
		if _, _, err := repository.BootstrapPasswordIdentity(ctx, store.PasswordCredential{
			Username: username, PasswordHash: passwordHash, Email: email, Name: name,
		}); err != nil {
			return nil, fmt.Errorf("bootstrap built-in account: %w", err)
		}
		return service, nil
	}
	if config.Mode != ModeOIDC {
		return service, nil
	}
	if strings.TrimSpace(config.IssuerURL) == "" || strings.TrimSpace(config.ClientID) == "" || strings.TrimSpace(config.RedirectURL) == "" {
		return nil, fmt.Errorf("OIDC issuer URL, client ID, and redirect URL are required")
	}
	provider, err := oidc.NewProvider(ctx, config.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	scopes := append([]string{oidc.ScopeOpenID, "profile", "email"}, config.Scopes...)
	service.provider = provider
	service.verifier = provider.Verifier(&oidc.Config{ClientID: config.ClientID})
	service.oauth2Config = oauth2.Config{
		ClientID: config.ClientID, ClientSecret: config.ClientSecret,
		Endpoint: provider.Endpoint(), RedirectURL: config.RedirectURL, Scopes: uniqueStrings(scopes),
	}
	return service, nil
}

func (s *Service) Mode() Mode                { return s.config.Mode }
func (s *Service) CookieName() string        { return s.config.CookieName }
func (s *Service) CSRFCookieName() string    { return s.config.CSRFCookieName }
func (s *Service) CookieSecure() bool        { return s.config.CookieSecure }
func (s *Service) SessionTTL() time.Duration { return s.config.SessionTTL }
func (s *Service) UsesSessions() bool {
	return s.config.Mode == ModeOIDC || s.config.Mode == ModePassword
}

func (s *Service) BeginLogin(ctx context.Context, returnTo string) (string, error) {
	if s.config.Mode != ModeOIDC || s.provider == nil {
		return "", fmt.Errorf("OIDC login is not enabled")
	}
	returnTo = safeReturnTo(returnTo)
	state, err := randomToken(32)
	if err != nil {
		return "", err
	}
	nonce, err := randomToken(24)
	if err != nil {
		return "", err
	}
	verifier := oauth2.GenerateVerifier()
	flow := store.AuthFlow{
		StateHash: hashToken(state), CodeVerifier: verifier, Nonce: nonce,
		ReturnTo: returnTo, ExpiresAt: s.now().UTC().Add(s.config.FlowTTL).Format(time.RFC3339Nano),
	}
	if err := s.repository.CreateAuthFlow(ctx, flow); err != nil {
		return "", fmt.Errorf("save OIDC flow: %w", err)
	}
	return s.oauth2Config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), nil
}

func (s *Service) CompleteLogin(ctx context.Context, state, code string) (LoginResult, error) {
	if s.config.Mode != ModeOIDC || s.verifier == nil {
		return LoginResult{}, fmt.Errorf("OIDC login is not enabled")
	}
	state = strings.TrimSpace(state)
	code = strings.TrimSpace(code)
	if state == "" || code == "" {
		return LoginResult{}, fmt.Errorf("OIDC state and code are required")
	}
	now := s.now().UTC()
	flow, err := s.repository.ConsumeAuthFlow(ctx, hashToken(state), now.Format(time.RFC3339Nano))
	if err != nil {
		return LoginResult{}, fmt.Errorf("consume OIDC flow: %w", err)
	}
	token, err := s.oauth2Config.Exchange(ctx, code, oauth2.VerifierOption(flow.CodeVerifier))
	if err != nil {
		return LoginResult{}, fmt.Errorf("exchange OIDC code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return LoginResult{}, fmt.Errorf("OIDC response did not include an ID token")
	}
	idToken, err := s.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return LoginResult{}, fmt.Errorf("verify OIDC ID token: %w", err)
	}
	var claims struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Nonce         string `json:"nonce"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return LoginResult{}, fmt.Errorf("decode OIDC claims: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(flow.Nonce)) != 1 {
		return LoginResult{}, fmt.Errorf("OIDC nonce mismatch")
	}
	if strings.TrimSpace(claims.Email) == "" || !claims.EmailVerified {
		return LoginResult{}, fmt.Errorf("OIDC identity must include a verified email")
	}
	identity, err := s.repository.UpsertOIDCIdentity(ctx, idToken.Issuer, claims.Subject, claims.Email, claims.Name)
	if err != nil {
		return LoginResult{}, fmt.Errorf("persist OIDC identity: %w", err)
	}
	return s.createLoginSession(ctx, identity, safeReturnTo(flow.ReturnTo))
}

func (s *Service) LoginWithPassword(ctx context.Context, username, password, returnTo string) (LoginResult, error) {
	if s.config.Mode != ModePassword {
		return LoginResult{}, fmt.Errorf("built-in login is not enabled")
	}
	identity, encodedHash, err := s.repository.GetPasswordIdentity(ctx, normalizeUsername(username))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_ = verifyPassword(dummyPasswordHash(), password)
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, fmt.Errorf("load built-in identity: %w", err)
	}
	if !verifyPassword(encodedHash, password) {
		return LoginResult{}, ErrInvalidCredentials
	}
	return s.createLoginSession(ctx, identity, safeReturnTo(returnTo))
}

func (s *Service) createLoginSession(ctx context.Context, identity store.AuthIdentity, returnTo string) (LoginResult, error) {
	now := s.now().UTC()
	sessionToken, err := randomToken(32)
	if err != nil {
		return LoginResult{}, err
	}
	csrfToken, err := randomToken(32)
	if err != nil {
		return LoginResult{}, err
	}
	expiresAt := now.Add(s.config.SessionTTL)
	if err := s.repository.CreateAuthSession(ctx, store.AuthSession{
		TokenHash: hashToken(sessionToken), CSRFTokenHash: hashToken(csrfToken),
		UserID: identity.User.ID, WorkspaceID: identity.Workspace.ID,
		ExpiresAt: expiresAt.Format(time.RFC3339Nano), LastSeenAt: now.Format(time.RFC3339Nano),
	}); err != nil {
		return LoginResult{}, fmt.Errorf("create auth session: %w", err)
	}
	return LoginResult{Identity: identity, SessionToken: sessionToken, CSRFToken: csrfToken, ExpiresAt: expiresAt, ReturnTo: safeReturnTo(returnTo)}, nil
}

func (s *Service) Authenticate(ctx context.Context, rawToken string) (Principal, store.AuthSession, error) {
	if strings.TrimSpace(rawToken) == "" {
		return Principal{}, store.AuthSession{}, store.ErrNotFound
	}
	identity, session, err := s.repository.GetAuthSession(ctx, hashToken(rawToken))
	if err != nil {
		return Principal{}, store.AuthSession{}, err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, session.ExpiresAt)
	if err != nil || !expiresAt.After(s.now()) {
		_ = s.repository.DeleteAuthSession(ctx, session.TokenHash)
		return Principal{}, store.AuthSession{}, store.ErrNotFound
	}
	_ = s.repository.TouchAuthSession(ctx, session.TokenHash, s.now().UTC().Format(time.RFC3339Nano))
	return Principal{
		UserID: identity.User.ID, WorkspaceID: identity.Workspace.ID,
		Email: identity.User.Email, Name: identity.User.Name, Role: identity.Role, Source: s.config.Mode,
	}, session, nil
}

func (s *Service) ValidateCSRF(session store.AuthSession, rawToken string) bool {
	left, err := hex.DecodeString(session.CSRFTokenHash)
	if err != nil {
		return false
	}
	right, err := hex.DecodeString(hashToken(rawToken))
	return err == nil && subtle.ConstantTimeCompare(left, right) == 1
}

func (s *Service) Logout(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	return s.repository.DeleteAuthSession(ctx, hashToken(rawToken))
}

func (s *Service) BeginBrokerOAuth(ctx context.Context, principal Principal, connectionID int64) (string, error) {
	if principal.UserID <= 0 || principal.WorkspaceID <= 0 || connectionID <= 0 {
		return "", fmt.Errorf("authenticated broker OAuth context is required")
	}
	state, err := randomToken(32)
	if err != nil {
		return "", err
	}
	err = s.repository.CreateBrokerOAuthFlow(ctx, store.BrokerOAuthFlow{
		StateHash: hashToken(state), ConnectionID: connectionID,
		WorkspaceID: principal.WorkspaceID, UserID: principal.UserID,
		ExpiresAt: s.now().UTC().Add(s.config.FlowTTL).Format(time.RFC3339Nano),
	})
	if err != nil {
		return "", err
	}
	return state, nil
}

func (s *Service) ConsumeBrokerOAuth(ctx context.Context, principal Principal, rawState string) (int64, error) {
	flow, err := s.repository.ConsumeBrokerOAuthFlow(ctx, hashToken(strings.TrimSpace(rawState)), s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	if flow.WorkspaceID != principal.WorkspaceID || flow.UserID != principal.UserID {
		return 0, fmt.Errorf("broker OAuth state does not belong to this session")
	}
	return flow.ConnectionID, nil
}

func (s *Service) ListMembers(ctx context.Context, workspaceID int64) ([]store.WorkspaceMember, error) {
	return s.repository.ListWorkspaceMembers(ctx, workspaceID)
}

func (s *Service) InviteMember(ctx context.Context, principal Principal, email, role string) error {
	return s.repository.InviteWorkspaceMember(ctx, store.WorkspaceInvite{
		WorkspaceID: principal.WorkspaceID, Email: email, Role: role, InvitedBy: principal.UserID,
	})
}

func (s *Service) UpdateMemberRole(ctx context.Context, workspaceID, userID int64, role string) error {
	return s.repository.UpdateWorkspaceMemberRole(ctx, workspaceID, userID, strings.ToLower(strings.TrimSpace(role)))
}

func (s *Service) DeleteMember(ctx context.Context, workspaceID, userID int64) error {
	return s.repository.DeleteWorkspaceMember(ctx, workspaceID, userID)
}

func (s *Service) Audit(ctx context.Context, event store.AuditEvent) {
	_ = s.repository.AppendAuditEvent(ctx, event)
}

func randomToken(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func safeReturnTo(value string) string {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") {
		return "/"
	}
	return parsed.RequestURI()
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func IsNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }
