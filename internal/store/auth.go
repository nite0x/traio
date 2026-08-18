package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const DefaultWorkspaceID int64 = 1

type AuthUser struct {
	ID      int64  `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Issuer  string `json:"-"`
	Subject string `json:"-"`
}

type Workspace struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type WorkspaceMember struct {
	UserID      int64  `json:"user_id"`
	WorkspaceID int64  `json:"workspace_id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Role        string `json:"role"`
}

type WorkspaceInvite struct {
	WorkspaceID int64  `json:"workspace_id"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	InvitedBy   int64  `json:"invited_by"`
}

type AuthIdentity struct {
	User      AuthUser  `json:"user"`
	Workspace Workspace `json:"workspace"`
	Role      string    `json:"role"`
}

type PasswordCredential struct {
	Username     string
	PasswordHash string
	Email        string
	Name         string
}

type AuthSession struct {
	TokenHash     string
	CSRFTokenHash string
	UserID        int64
	WorkspaceID   int64
	ExpiresAt     string
	LastSeenAt    string
}

type AuthFlow struct {
	StateHash    string
	CodeVerifier string
	Nonce        string
	ReturnTo     string
	ExpiresAt    string
}

type BrokerOAuthFlow struct {
	StateHash    string
	ConnectionID int64
	WorkspaceID  int64
	UserID       int64
	ExpiresAt    string
}

type AuditEvent struct {
	WorkspaceID int64  `json:"workspace_id"`
	UserID      int64  `json:"user_id,omitempty"`
	Action      string `json:"action"`
	TargetType  string `json:"target_type,omitempty"`
	TargetID    string `json:"target_id,omitempty"`
	RemoteAddr  string `json:"remote_addr,omitempty"`
	Metadata    string `json:"metadata,omitempty"`
}

func (s *Store) migrateAuth() error {
	var statements []string
	if s.dialect == dialectPostgres {
		statements = []string{
			`CREATE TABLE IF NOT EXISTS users (
				id BIGSERIAL PRIMARY KEY,
				issuer TEXT NOT NULL,
				subject TEXT NOT NULL,
				email TEXT NOT NULL DEFAULT '',
				name TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				UNIQUE(issuer, subject)
			)`,
			`CREATE TABLE IF NOT EXISTS password_credentials (
				user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
				username TEXT NOT NULL UNIQUE,
				password_hash TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS workspaces (
				id BIGSERIAL PRIMARY KEY,
				name TEXT NOT NULL,
				slug TEXT NOT NULL UNIQUE,
				created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS workspace_members (
				workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				role TEXT NOT NULL CHECK (role IN ('owner','admin','member','viewer')),
				created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY(workspace_id, user_id)
			)`,
			`CREATE TABLE IF NOT EXISTS workspace_invites (
				workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				email TEXT NOT NULL,
				role TEXT NOT NULL CHECK (role IN ('admin','member','viewer')),
				invited_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY(workspace_id, email)
			)`,
			`CREATE TABLE IF NOT EXISTS auth_sessions (
				token_hash TEXT PRIMARY KEY,
				csrf_token_hash TEXT NOT NULL,
				user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				expires_at TEXT NOT NULL,
				last_seen_at TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS auth_flows (
				state_hash TEXT PRIMARY KEY,
				code_verifier TEXT NOT NULL,
				nonce TEXT NOT NULL,
				return_to TEXT NOT NULL DEFAULT '/',
				expires_at TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS broker_oauth_flows (
				state_hash TEXT PRIMARY KEY,
				connection_id BIGINT NOT NULL,
				workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				expires_at TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS audit_events (
				id BIGSERIAL PRIMARY KEY,
				workspace_id BIGINT NOT NULL DEFAULT 1,
				user_id BIGINT,
				action TEXT NOT NULL,
				target_type TEXT NOT NULL DEFAULT '',
				target_id TEXT NOT NULL DEFAULT '',
				remote_addr TEXT NOT NULL DEFAULT '',
				metadata TEXT NOT NULL DEFAULT '{}',
				created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
		}
	} else {
		statements = []string{
			`CREATE TABLE IF NOT EXISTS users (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				issuer TEXT NOT NULL,
				subject TEXT NOT NULL,
				email TEXT NOT NULL DEFAULT '',
				name TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
				UNIQUE(issuer, subject)
			)`,
			`CREATE TABLE IF NOT EXISTS password_credentials (
				user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
				username TEXT NOT NULL UNIQUE,
				password_hash TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS workspaces (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL,
				slug TEXT NOT NULL UNIQUE,
				created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS workspace_members (
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				role TEXT NOT NULL CHECK (role IN ('owner','admin','member','viewer')),
				created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY(workspace_id, user_id)
			)`,
			`CREATE TABLE IF NOT EXISTS workspace_invites (
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				email TEXT NOT NULL,
				role TEXT NOT NULL CHECK (role IN ('admin','member','viewer')),
				invited_by INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY(workspace_id, email)
			)`,
			`CREATE TABLE IF NOT EXISTS auth_sessions (
				token_hash TEXT PRIMARY KEY,
				csrf_token_hash TEXT NOT NULL,
				user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				expires_at TEXT NOT NULL,
				last_seen_at TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS auth_flows (
				state_hash TEXT PRIMARY KEY,
				code_verifier TEXT NOT NULL,
				nonce TEXT NOT NULL,
				return_to TEXT NOT NULL DEFAULT '/',
				expires_at TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS broker_oauth_flows (
				state_hash TEXT PRIMARY KEY,
				connection_id INTEGER NOT NULL,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				expires_at TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE TABLE IF NOT EXISTS audit_events (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				workspace_id INTEGER NOT NULL DEFAULT 1,
				user_id INTEGER,
				action TEXT NOT NULL,
				target_type TEXT NOT NULL DEFAULT '',
				target_id TEXT NOT NULL DEFAULT '',
				remote_addr TEXT NOT NULL DEFAULT '',
				metadata TEXT NOT NULL DEFAULT '{}',
				created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
		}
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("create auth schema: %w", err)
		}
	}
	_, err := s.db.Exec(`INSERT INTO workspaces (id, name, slug) VALUES (1, 'Traio', 'default') ON CONFLICT(id) DO NOTHING`)
	return err
}

func (s *Store) UpsertOIDCIdentity(ctx context.Context, issuer, subject, email, name string) (AuthIdentity, error) {
	issuer = strings.TrimSpace(issuer)
	subject = strings.TrimSpace(subject)
	if issuer == "" || subject == "" {
		return AuthIdentity{}, fmt.Errorf("issuer and subject are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AuthIdentity{}, err
	}
	defer tx.Rollback()
	// Serializing on the default workspace prevents two concurrent first OIDC
	// callbacks from both becoming the bootstrap owner.
	if s.dialect == dialectPostgres {
		var workspaceID int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE id = $1 FOR UPDATE`, DefaultWorkspaceID).Scan(&workspaceID); err != nil {
			return AuthIdentity{}, err
		}
	}
	if _, err := s.txExecContext(ctx, tx, `
		INSERT INTO users (issuer, subject, email, name) VALUES (?, ?, ?, ?)
		ON CONFLICT(issuer, subject) DO UPDATE SET email = excluded.email, name = excluded.name, updated_at = CURRENT_TIMESTAMP`,
		issuer, subject, strings.TrimSpace(email), strings.TrimSpace(name)); err != nil {
		return AuthIdentity{}, err
	}
	var identity AuthIdentity
	if err := tx.QueryRowContext(ctx, s.bind(`SELECT id, email, name, issuer, subject FROM users WHERE issuer = ? AND subject = ?`), issuer, subject).Scan(
		&identity.User.ID, &identity.User.Email, &identity.User.Name, &identity.User.Issuer, &identity.User.Subject); err != nil {
		return AuthIdentity{}, err
	}
	var memberCount int
	if err := tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM workspace_members WHERE workspace_id = ?`), DefaultWorkspaceID).Scan(&memberCount); err != nil {
		return AuthIdentity{}, err
	}
	role := ""
	err = tx.QueryRowContext(ctx, s.bind(`SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`), DefaultWorkspaceID, identity.User.ID).Scan(&role)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AuthIdentity{}, err
	}
	isNewMembership := errors.Is(err, sql.ErrNoRows)
	if isNewMembership && memberCount == 0 {
		role = "owner"
	} else if isNewMembership {
		if strings.TrimSpace(email) == "" {
			return AuthIdentity{}, fmt.Errorf("%w: identity has no invited email", ErrForbidden)
		}
		if err := tx.QueryRowContext(ctx, s.bind(`
			SELECT role FROM workspace_invites WHERE workspace_id = ? AND LOWER(email) = LOWER(?)`), DefaultWorkspaceID, email).Scan(&role); errors.Is(err, sql.ErrNoRows) {
			return AuthIdentity{}, fmt.Errorf("%w: user is not invited to this workspace", ErrForbidden)
		} else if err != nil {
			return AuthIdentity{}, err
		}
	}
	if _, err := s.txExecContext(ctx, tx, `
		INSERT INTO workspace_members (workspace_id, user_id, role) VALUES (?, ?, ?)
		ON CONFLICT(workspace_id, user_id) DO NOTHING`, DefaultWorkspaceID, identity.User.ID, role); err != nil {
		return AuthIdentity{}, err
	}
	if isNewMembership && role != "owner" {
		if _, err := s.txExecContext(ctx, tx, `DELETE FROM workspace_invites WHERE workspace_id = ? AND LOWER(email) = LOWER(?)`, DefaultWorkspaceID, email); err != nil {
			return AuthIdentity{}, err
		}
	}
	if err := tx.QueryRowContext(ctx, s.bind(`
		SELECT w.id, w.name, w.slug, m.role
		FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.workspace_id = ? AND m.user_id = ?`), DefaultWorkspaceID, identity.User.ID).Scan(
		&identity.Workspace.ID, &identity.Workspace.Name, &identity.Workspace.Slug, &identity.Role); err != nil {
		return AuthIdentity{}, err
	}
	if err := tx.Commit(); err != nil {
		return AuthIdentity{}, err
	}
	return identity, nil
}

func (s *Store) HasPasswordIdentity(ctx context.Context) (bool, error) {
	var count int
	if err := s.queryRowContext(ctx, `SELECT COUNT(*) FROM password_credentials`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// BootstrapPasswordIdentity creates the one built-in administrator. Once a
// built-in credential exists, startup is idempotent and never rotates its
// password from an environment variable.
func (s *Store) BootstrapPasswordIdentity(ctx context.Context, credential PasswordCredential) (AuthIdentity, bool, error) {
	credential.Username = strings.ToLower(strings.TrimSpace(credential.Username))
	credential.Email = strings.ToLower(strings.TrimSpace(credential.Email))
	credential.Name = strings.TrimSpace(credential.Name)
	if credential.Username == "" || credential.PasswordHash == "" {
		return AuthIdentity{}, false, fmt.Errorf("username and password hash are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AuthIdentity{}, false, err
	}
	defer tx.Rollback()
	if s.dialect == dialectPostgres {
		var workspaceID int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE id = $1 FOR UPDATE`, DefaultWorkspaceID).Scan(&workspaceID); err != nil {
			return AuthIdentity{}, false, err
		}
	}
	var credentialCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM password_credentials`).Scan(&credentialCount); err != nil {
		return AuthIdentity{}, false, err
	}
	if credentialCount > 0 {
		identity, _, err := passwordIdentityFromQuery(tx.QueryRowContext(ctx, s.bind(`
			SELECT u.id, u.email, u.name, u.issuer, u.subject,
			       w.id, w.name, w.slug, m.role, p.password_hash
			FROM password_credentials p
			JOIN users u ON u.id = p.user_id
			JOIN workspace_members m ON m.user_id = u.id AND m.workspace_id = ?
			JOIN workspaces w ON w.id = m.workspace_id
			WHERE p.username = ?`), DefaultWorkspaceID, credential.Username))
		if errors.Is(err, sql.ErrNoRows) {
			return AuthIdentity{}, false, fmt.Errorf("built-in account is already initialized with a different username")
		}
		if err != nil {
			return AuthIdentity{}, false, err
		}
		return identity, false, nil
	}
	if _, err := s.txExecContext(ctx, tx, `
		INSERT INTO users (issuer, subject, email, name) VALUES (?, ?, ?, ?)
		ON CONFLICT(issuer, subject) DO UPDATE SET email = excluded.email, name = excluded.name, updated_at = CURRENT_TIMESTAMP`,
		"traio:password", credential.Username, credential.Email, credential.Name); err != nil {
		return AuthIdentity{}, false, err
	}
	var userID int64
	if err := tx.QueryRowContext(ctx, s.bind(`SELECT id FROM users WHERE issuer = ? AND subject = ?`), "traio:password", credential.Username).Scan(&userID); err != nil {
		return AuthIdentity{}, false, err
	}
	if _, err := s.txExecContext(ctx, tx, `INSERT INTO password_credentials (user_id, username, password_hash) VALUES (?, ?, ?)`, userID, credential.Username, credential.PasswordHash); err != nil {
		return AuthIdentity{}, false, err
	}
	if _, err := s.txExecContext(ctx, tx, `
		INSERT INTO workspace_members (workspace_id, user_id, role) VALUES (?, ?, 'owner')
		ON CONFLICT(workspace_id, user_id) DO UPDATE SET role = 'owner', updated_at = CURRENT_TIMESTAMP`, DefaultWorkspaceID, userID); err != nil {
		return AuthIdentity{}, false, err
	}
	identity, _, err := passwordIdentityFromQuery(tx.QueryRowContext(ctx, s.bind(`
		SELECT u.id, u.email, u.name, u.issuer, u.subject,
		       w.id, w.name, w.slug, m.role, p.password_hash
		FROM password_credentials p
		JOIN users u ON u.id = p.user_id
		JOIN workspace_members m ON m.user_id = u.id AND m.workspace_id = ?
		JOIN workspaces w ON w.id = m.workspace_id
		WHERE p.username = ?`), DefaultWorkspaceID, credential.Username))
	if err != nil {
		return AuthIdentity{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return AuthIdentity{}, false, err
	}
	return identity, true, nil
}

func (s *Store) GetPasswordIdentity(ctx context.Context, username string) (AuthIdentity, string, error) {
	identity, passwordHash, err := passwordIdentityFromQuery(s.queryRowContext(ctx, `
		SELECT u.id, u.email, u.name, u.issuer, u.subject,
		       w.id, w.name, w.slug, m.role, p.password_hash
		FROM password_credentials p
		JOIN users u ON u.id = p.user_id
		JOIN workspace_members m ON m.user_id = u.id AND m.workspace_id = ?
		JOIN workspaces w ON w.id = m.workspace_id
		WHERE p.username = ?`, DefaultWorkspaceID, strings.ToLower(strings.TrimSpace(username))))
	if errors.Is(err, sql.ErrNoRows) {
		return AuthIdentity{}, "", ErrNotFound
	}
	return identity, passwordHash, err
}

type authRowScanner interface {
	Scan(...any) error
}

func passwordIdentityFromQuery(row authRowScanner) (AuthIdentity, string, error) {
	var identity AuthIdentity
	var passwordHash string
	err := row.Scan(
		&identity.User.ID, &identity.User.Email, &identity.User.Name, &identity.User.Issuer, &identity.User.Subject,
		&identity.Workspace.ID, &identity.Workspace.Name, &identity.Workspace.Slug, &identity.Role, &passwordHash,
	)
	return identity, passwordHash, err
}

func (s *Store) CreateAuthSession(ctx context.Context, session AuthSession) error {
	_, _ = s.execContext(ctx, `DELETE FROM auth_sessions WHERE expires_at <= ?`, time.Now().UTC().Format(time.RFC3339Nano))
	_, err := s.execContext(ctx, `
		INSERT INTO auth_sessions (token_hash, csrf_token_hash, user_id, workspace_id, expires_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?)`, session.TokenHash, session.CSRFTokenHash, session.UserID, session.WorkspaceID, session.ExpiresAt, session.LastSeenAt)
	return err
}

func (s *Store) GetAuthSession(ctx context.Context, tokenHash string) (AuthIdentity, AuthSession, error) {
	var identity AuthIdentity
	var session AuthSession
	err := s.queryRowContext(ctx, `
		SELECT u.id, u.email, u.name, u.issuer, u.subject,
		       w.id, w.name, w.slug, m.role,
		       s.token_hash, s.csrf_token_hash, s.user_id, s.workspace_id, s.expires_at, s.last_seen_at
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		JOIN workspaces w ON w.id = s.workspace_id
		JOIN workspace_members m ON m.workspace_id = s.workspace_id AND m.user_id = s.user_id
		WHERE s.token_hash = ?`, tokenHash).Scan(
		&identity.User.ID, &identity.User.Email, &identity.User.Name, &identity.User.Issuer, &identity.User.Subject,
		&identity.Workspace.ID, &identity.Workspace.Name, &identity.Workspace.Slug, &identity.Role,
		&session.TokenHash, &session.CSRFTokenHash, &session.UserID, &session.WorkspaceID, &session.ExpiresAt, &session.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AuthIdentity{}, AuthSession{}, ErrNotFound
	}
	return identity, session, err
}

func (s *Store) TouchAuthSession(ctx context.Context, tokenHash, seenAt string) error {
	_, err := s.execContext(ctx, `UPDATE auth_sessions SET last_seen_at = ? WHERE token_hash = ?`, seenAt, tokenHash)
	return err
}

func (s *Store) DeleteAuthSession(ctx context.Context, tokenHash string) error {
	_, err := s.execContext(ctx, `DELETE FROM auth_sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) CreateAuthFlow(ctx context.Context, flow AuthFlow) error {
	_, _ = s.execContext(ctx, `DELETE FROM auth_flows WHERE expires_at <= ?`, time.Now().UTC().Format(time.RFC3339Nano))
	_, err := s.execContext(ctx, `
		INSERT INTO auth_flows (state_hash, code_verifier, nonce, return_to, expires_at) VALUES (?, ?, ?, ?, ?)`,
		flow.StateHash, flow.CodeVerifier, flow.Nonce, flow.ReturnTo, flow.ExpiresAt)
	return err
}

func (s *Store) ConsumeAuthFlow(ctx context.Context, stateHash, now string) (AuthFlow, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AuthFlow{}, err
	}
	defer tx.Rollback()
	var flow AuthFlow
	err = tx.QueryRowContext(ctx, s.bind(`
		SELECT state_hash, code_verifier, nonce, return_to, expires_at FROM auth_flows WHERE state_hash = ?`), stateHash).Scan(
		&flow.StateHash, &flow.CodeVerifier, &flow.Nonce, &flow.ReturnTo, &flow.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AuthFlow{}, ErrNotFound
	}
	if err != nil {
		return AuthFlow{}, err
	}
	if _, err := s.txExecContext(ctx, tx, `DELETE FROM auth_flows WHERE state_hash = ?`, stateHash); err != nil {
		return AuthFlow{}, err
	}
	if flow.ExpiresAt <= now {
		return AuthFlow{}, fmt.Errorf("auth flow expired")
	}
	if err := tx.Commit(); err != nil {
		return AuthFlow{}, err
	}
	return flow, nil
}

func (s *Store) CreateBrokerOAuthFlow(ctx context.Context, flow BrokerOAuthFlow) error {
	_, _ = s.execContext(ctx, `DELETE FROM broker_oauth_flows WHERE expires_at <= ?`, time.Now().UTC().Format(time.RFC3339Nano))
	_, err := s.execContext(ctx, `
		INSERT INTO broker_oauth_flows (state_hash, connection_id, workspace_id, user_id, expires_at)
		VALUES (?, ?, ?, ?, ?)`, flow.StateHash, flow.ConnectionID, flow.WorkspaceID, flow.UserID, flow.ExpiresAt)
	return err
}

func (s *Store) ConsumeBrokerOAuthFlow(ctx context.Context, stateHash, now string) (BrokerOAuthFlow, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BrokerOAuthFlow{}, err
	}
	defer tx.Rollback()
	var flow BrokerOAuthFlow
	err = tx.QueryRowContext(ctx, s.bind(`
		SELECT state_hash, connection_id, workspace_id, user_id, expires_at
		FROM broker_oauth_flows WHERE state_hash = ?`), stateHash).Scan(
		&flow.StateHash, &flow.ConnectionID, &flow.WorkspaceID, &flow.UserID, &flow.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return BrokerOAuthFlow{}, ErrNotFound
	}
	if err != nil {
		return BrokerOAuthFlow{}, err
	}
	if _, err := s.txExecContext(ctx, tx, `DELETE FROM broker_oauth_flows WHERE state_hash = ?`, stateHash); err != nil {
		return BrokerOAuthFlow{}, err
	}
	if flow.ExpiresAt <= now {
		return BrokerOAuthFlow{}, fmt.Errorf("broker OAuth flow expired")
	}
	if err := tx.Commit(); err != nil {
		return BrokerOAuthFlow{}, err
	}
	return flow, nil
}

func (s *Store) ListWorkspaceMembers(ctx context.Context, workspaceID int64) ([]WorkspaceMember, error) {
	rows, err := s.queryContext(ctx, `
		SELECT m.user_id, m.workspace_id, u.email, u.name, m.role
		FROM workspace_members m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = ? ORDER BY u.email, u.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []WorkspaceMember{}
	for rows.Next() {
		var member WorkspaceMember
		if err := rows.Scan(&member.UserID, &member.WorkspaceID, &member.Email, &member.Name, &member.Role); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func (s *Store) InviteWorkspaceMember(ctx context.Context, invite WorkspaceInvite) error {
	invite.Email = strings.ToLower(strings.TrimSpace(invite.Email))
	invite.Role = strings.ToLower(strings.TrimSpace(invite.Role))
	if invite.WorkspaceID <= 0 || invite.InvitedBy <= 0 || invite.Email == "" || !strings.Contains(invite.Email, "@") {
		return fmt.Errorf("valid workspace, inviter, and email are required")
	}
	if invite.Role == "owner" || !validRole(invite.Role) {
		return fmt.Errorf("invite role must be admin, member, or viewer")
	}
	_, err := s.execContext(ctx, `
		INSERT INTO workspace_invites (workspace_id, email, role, invited_by) VALUES (?, ?, ?, ?)
		ON CONFLICT(workspace_id, email) DO UPDATE SET role = excluded.role, invited_by = excluded.invited_by, created_at = CURRENT_TIMESTAMP`,
		invite.WorkspaceID, invite.Email, invite.Role, invite.InvitedBy)
	return err
}

func (s *Store) UpdateWorkspaceMemberRole(ctx context.Context, workspaceID, userID int64, role string) error {
	if !validRole(role) {
		return fmt.Errorf("invalid role")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRowContext(ctx, s.bind(`SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`), workspaceID, userID).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if current == "owner" && role != "owner" {
		var owners int
		if err := tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM workspace_members WHERE workspace_id = ? AND role = 'owner'`), workspaceID).Scan(&owners); err != nil {
			return err
		}
		if owners <= 1 {
			return fmt.Errorf("workspace must retain an owner")
		}
	}
	if _, err := s.txExecContext(ctx, tx, `UPDATE workspace_members SET role = ?, updated_at = CURRENT_TIMESTAMP WHERE workspace_id = ? AND user_id = ?`, role, workspaceID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteWorkspaceMember(ctx context.Context, workspaceID, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var role string
	if err := tx.QueryRowContext(ctx, s.bind(`SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`), workspaceID, userID).Scan(&role); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if role == "owner" {
		var owners int
		if err := tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM workspace_members WHERE workspace_id = ? AND role = 'owner'`), workspaceID).Scan(&owners); err != nil {
			return err
		}
		if owners <= 1 {
			return fmt.Errorf("workspace must retain an owner")
		}
	}
	if _, err := s.txExecContext(ctx, tx, `DELETE FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, workspaceID, userID); err != nil {
		return err
	}
	if _, err := s.txExecContext(ctx, tx, `DELETE FROM auth_sessions WHERE workspace_id = ? AND user_id = ?`, workspaceID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AppendAuditEvent(ctx context.Context, event AuditEvent) error {
	if event.WorkspaceID == 0 {
		event.WorkspaceID = DefaultWorkspaceID
	}
	if event.Metadata == "" {
		event.Metadata = "{}"
	}
	_, err := s.execContext(ctx, `
		INSERT INTO audit_events (workspace_id, user_id, action, target_type, target_id, remote_addr, metadata)
		VALUES (?, NULLIF(?, 0), ?, ?, ?, ?, ?)`, event.WorkspaceID, event.UserID, event.Action, event.TargetType, event.TargetID, event.RemoteAddr, event.Metadata)
	return err
}

func validRole(role string) bool {
	switch role {
	case "owner", "admin", "member", "viewer":
		return true
	default:
		return false
	}
}
