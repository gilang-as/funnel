package store

import (
	"context"
	"database/sql"
	"time"

	sq "github.com/Masterminds/squirrel"
)

type pgTokenRepo struct {
	db *sql.DB
}

func (r *pgTokenRepo) Create(ctx context.Context, t *JoinToken) error {
	t.CreatedAt = time.Now()
	_, err := pgBuilder.Insert("join_tokens").
		Columns("id", "token_hash", "name", "created_at", "expires_at", "revoked").
		Values(t.ID, t.TokenHash, t.Name, t.CreatedAt, t.ExpiresAt, t.Revoked).
		RunWith(r.db).
		ExecContext(ctx)
	return err
}

func (r *pgTokenRepo) GetByHash(ctx context.Context, hash string) (*JoinToken, error) {
	var t JoinToken
	err := pgBuilder.Select("*").
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

func (r *pgTokenRepo) List(ctx context.Context) ([]JoinToken, error) {
	rows, err := pgBuilder.Select("*").From("join_tokens").RunWith(r.db).QueryContext(ctx)
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

func (r *pgTokenRepo) Revoke(ctx context.Context, id string) error {
	_, err := pgBuilder.Update("join_tokens").
		Set("revoked", true).
		Where(sq.Eq{"id": id}).
		RunWith(r.db).
		ExecContext(ctx)
	return err
}
