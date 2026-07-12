package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type TokenRepository interface {
	FindByUserAndProvider(ctx context.Context, userID, provider string) (*OAuthToken, error)
	Upsert(ctx context.Context, token OAuthToken) error
	UpdateAccessToken(ctx context.Context, userID, provider, encryptedToken string, expiresAt time.Time) error
}

type OAuthToken struct {
	ID               string
	UserID           string
	Provider         string
	ProviderUserID   string
	ProviderUsername string
	AccessToken      string
	RefreshToken     string
	TokenExpiresAt   *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type PostgresTokenRepository struct {
	db *DB
}

func NewPostgresTokenRepository(db *DB) *PostgresTokenRepository {
	return &PostgresTokenRepository{db: db}
}

func (r *PostgresTokenRepository) FindByUserAndProvider(ctx context.Context, userID, provider string) (*OAuthToken, error) {
	var t OAuthToken
	var refreshToken sql.NullString
	var tokenExpiresAt sql.NullTime

	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, provider, provider_user_id, provider_username,
		        access_token, refresh_token, token_expires_at, created_at, updated_at
		 FROM oauth_tokens WHERE user_id = $1 AND provider = $2`,
		userID, provider,
	).Scan(&t.ID, &t.UserID, &t.Provider, &t.ProviderUserID, &t.ProviderUsername,
		&t.AccessToken, &refreshToken, &tokenExpiresAt, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if refreshToken.Valid {
		t.RefreshToken = refreshToken.String
	}
	if tokenExpiresAt.Valid {
		t.TokenExpiresAt = &tokenExpiresAt.Time
	}
	return &t, nil
}

func (r *PostgresTokenRepository) Upsert(ctx context.Context, token OAuthToken) error {
	var refreshToken sql.NullString
	if token.RefreshToken != "" {
		refreshToken = sql.NullString{String: token.RefreshToken, Valid: true}
	}
	var tokenExpiresAt sql.NullTime
	if token.TokenExpiresAt != nil {
		tokenExpiresAt = sql.NullTime{Time: *token.TokenExpiresAt, Valid: true}
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO oauth_tokens (user_id, provider, provider_user_id, provider_username, access_token, refresh_token, token_expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (user_id, provider) DO UPDATE SET
		     access_token = EXCLUDED.access_token,
		     refresh_token = EXCLUDED.refresh_token,
		     token_expires_at = EXCLUDED.token_expires_at,
		     updated_at = NOW()`,
		token.UserID, token.Provider, token.ProviderUserID, token.ProviderUsername,
		token.AccessToken, refreshToken, tokenExpiresAt,
	)
	return err
}

func (r *PostgresTokenRepository) UpdateAccessToken(ctx context.Context, userID, provider, encryptedToken string, expiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE oauth_tokens SET access_token = $1, token_expires_at = $2, updated_at = NOW()
		 WHERE user_id = $3 AND provider = $4`,
		encryptedToken, expiresAt, userID, provider,
	)
	return err
}
