package store

import (
	"context"
	"database/sql"
	"time"

	sq "github.com/Masterminds/squirrel"
)

type mysqlTokenRepo struct {
	db *sql.DB
}

func (r *mysqlTokenRepo) Create(ctx context.Context, t *JoinToken) error {
	t.CreatedAt = time.Now()
	_, err := mysqlBuilder.Insert("join_tokens").
		Columns("id", "token_hash", "name", "created_at", "expires_at", "revoked").
		Values(t.ID, t.TokenHash, t.Name, t.CreatedAt, t.ExpiresAt, t.Revoked).
		RunWith(r.db).
		ExecContext(ctx)
	return err
}

func (r *mysqlTokenRepo) GetByHash(ctx context.Context, hash string) (*JoinToken, error) {
	var t JoinToken
	err := mysqlBuilder.Select("*").
		From("join_tokens").
		Where(sq.Eq{"token_hash": hash}).
		RunWith(r.db).
		QueryRowContext(ctx).
		Scan(&t.ID, &t.TokenHash, &t.Name, &t.CreatedAt, &t.ExpiresAt, &t.Revoked)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &t, err
}

func (r *mysqlTokenRepo) List(ctx context.Context) ([]JoinToken, error) {
	rows, err := mysqlBuilder.Select("*").From("join_tokens").RunWith(r.db).QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []JoinToken
	for rows.Next() {
		var t JoinToken
		if err := rows.Scan(&t.ID, &t.TokenHash, &t.Name, &t.CreatedAt, &t.ExpiresAt, &t.Revoked); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, nil
}

func (r *mysqlTokenRepo) Revoke(ctx context.Context, id string) error {
	_, err := mysqlBuilder.Update("join_tokens").
		Set("revoked", true).
		Where(sq.Eq{"id": id}).
		RunWith(r.db).
		ExecContext(ctx)
	return err
}
