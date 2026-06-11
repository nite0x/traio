package store

import (
	"context"
	"database/sql"
	"time"
)

type OAuthToken struct {
	Provider     string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

func (s *Store) GetOAuthToken(ctx context.Context, provider string) (OAuthToken, error) {
	var token OAuthToken
	var expiresAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT provider, access_token, COALESCE(refresh_token, ''), expires_at
		FROM oauth_tokens
		WHERE provider = ?`, provider).Scan(
		&token.Provider,
		&token.AccessToken,
		&token.RefreshToken,
		&expiresAt,
	)
	if err != nil {
		return OAuthToken{}, err
	}
	if expiresAt.Valid && expiresAt.String != "" {
		token.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresAt.String)
	}
	return token, nil
}

func (s *Store) SaveOAuthToken(ctx context.Context, token OAuthToken) error {
	var expiresAt any
	if !token.ExpiresAt.IsZero() {
		expiresAt = token.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO oauth_tokens (provider, access_token, refresh_token, expires_at, updated_at)
		VALUES (?, ?, ?, ?, datetime('now'))
		ON CONFLICT(provider) DO UPDATE SET
			access_token = excluded.access_token,
			refresh_token = excluded.refresh_token,
			expires_at = excluded.expires_at,
			updated_at = datetime('now')`,
		token.Provider,
		token.AccessToken,
		token.RefreshToken,
		expiresAt,
	)
	return err
}
