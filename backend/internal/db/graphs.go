package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type GraphRepository interface {
	FindByID(ctx context.Context, id string) (*GraphRecord, error)
	FindByUser(ctx context.Context, userID string) ([]*GraphRecord, error)
	FindOrCreate(ctx context.Context, entry GraphRecord) (*GraphRecord, error)
	UpdateStatus(ctx context.Context, id, status, errorMsg string) error
	Delete(ctx context.Context, id string) error
}

type GraphRecord struct {
	ID           string
	UserID       string
	Provider     string
	Owner        string
	RepoName     string
	Branch       string
	Status       string
	ErrorMessage string
	CreatedAt    time.Time
}

type PostgresGraphRepository struct {
	db *DB
}

func NewPostgresGraphRepository(db *DB) *PostgresGraphRepository {
	return &PostgresGraphRepository{db: db}
}

func (r *PostgresGraphRepository) FindByID(ctx context.Context, id string) (*GraphRecord, error) {
	var g GraphRecord
	var errMsg sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, provider, owner, repo_name, branch, status, error_message, created_at
		 FROM graph_registry WHERE id = $1`,
		id,
	).Scan(&g.ID, &g.UserID, &g.Provider, &g.Owner, &g.RepoName, &g.Branch,
		&g.Status, &errMsg, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if errMsg.Valid {
		g.ErrorMessage = errMsg.String
	}
	return &g, nil
}

func (r *PostgresGraphRepository) FindByUser(ctx context.Context, userID string) ([]*GraphRecord, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, provider, owner, repo_name, branch, status, error_message, created_at
		 FROM graph_registry WHERE user_id = $1 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []*GraphRecord
	for rows.Next() {
		var g GraphRecord
		var errMsg sql.NullString
		if err := rows.Scan(&g.ID, &g.UserID, &g.Provider, &g.Owner, &g.RepoName, &g.Branch,
			&g.Status, &errMsg, &g.CreatedAt); err != nil {
			return nil, err
		}
		if errMsg.Valid {
			g.ErrorMessage = errMsg.String
		}
		records = append(records, &g)
	}
	return records, rows.Err()
}

func (r *PostgresGraphRepository) FindOrCreate(ctx context.Context, entry GraphRecord) (*GraphRecord, error) {
	var g GraphRecord
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO graph_registry (user_id, provider, owner, repo_name, branch, status)
		 VALUES ($1, $2, $3, $4, $5, 'processing')
		 ON CONFLICT (user_id, provider, owner, repo_name, branch) DO UPDATE SET
		     status = 'processing',
		     error_message = NULL
		 RETURNING id, status, created_at`,
		entry.UserID, entry.Provider, entry.Owner, entry.RepoName, entry.Branch,
	).Scan(&g.ID, &g.Status, &g.CreatedAt)
	if err != nil {
		return nil, err
	}
	g.UserID = entry.UserID
	g.Provider = entry.Provider
	g.Owner = entry.Owner
	g.RepoName = entry.RepoName
	g.Branch = entry.Branch
	return &g, nil
}

func (r *PostgresGraphRepository) UpdateStatus(ctx context.Context, id, status, errorMsg string) error {
	var errMsg sql.NullString
	if errorMsg != "" {
		errMsg = sql.NullString{String: errorMsg, Valid: true}
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE graph_registry SET status = $1, error_message = $2 WHERE id = $3`,
		status, errMsg, id,
	)
	return err
}

func (r *PostgresGraphRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM graph_registry WHERE id = $1`, id)
	return err
}
