package store

import "context"

// Store is the top-level interface combining all repositories.
type Store interface {
	Jobs() JobRepository
	Workers() WorkerRepository
	Tokens() TokenRepository
	Close() error
	// RunMigrations applies embedded SQL migrations.
	RunMigrations(ctx context.Context) error
}
