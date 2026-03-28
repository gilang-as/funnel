package store

import (
	"context"
	"time"
)

type JoinToken struct {
	ID        string     `db:"id"`
	TokenHash string     `db:"token_hash"` // bcrypt or SHA256 of raw token
	Name      string     `db:"name"`
	CreatedAt time.Time  `db:"created_at"`
	ExpiresAt *time.Time `db:"expires_at"`
	Revoked   bool       `db:"revoked"`
}

type TokenRepository interface {
	Create(ctx context.Context, t *JoinToken) error
	GetByHash(ctx context.Context, hash string) (*JoinToken, error)
	List(ctx context.Context) ([]JoinToken, error)
	Revoke(ctx context.Context, id string) error
}
