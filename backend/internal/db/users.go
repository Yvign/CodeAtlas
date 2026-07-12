package db

import (
	"context"
	"database/sql"
	"errors"
)

type UserRepository interface {
	FindOrCreateByProvider(ctx context.Context, provider, providerUserID, providerUsername string) (userID string, err error)
}

type PostgresUserRepository struct {
	db *DB
}

func NewPostgresUserRepository(db *DB) *PostgresUserRepository {
	return &PostgresUserRepository{db: db}
}

func (r *PostgresUserRepository) FindOrCreateByProvider(ctx context.Context, provider, providerUserID, providerUsername string) (string, error) {
	var userID string
	err := r.db.QueryRowContext(ctx,
		`SELECT user_id FROM oauth_tokens WHERE provider = $1 AND provider_user_id = $2`,
		provider, providerUserID,
	).Scan(&userID)
	if err == nil {
		return userID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	err = r.db.QueryRowContext(ctx,
		`INSERT INTO users DEFAULT VALUES RETURNING id`,
	).Scan(&userID)
	if err != nil {
		return "", err
	}
	return userID, nil
}
