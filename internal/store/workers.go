package store

import (
	"context"
	"time"
)

type WorkerInfo struct {
	ID         string    `db:"id"`
	Address    string    `db:"address"`
	Capacity   int       `db:"capacity"`
	ActiveJobs int       `db:"active_jobs"`
	Status     string    `db:"status"` // "active" | "draining" | "offline"
	Version    string    `db:"version"`
	LastSeen   time.Time `db:"last_seen"`
	JoinedAt   time.Time `db:"joined_at"`
}

type WorkerRepository interface {
	Upsert(ctx context.Context, w *WorkerInfo) error
	Get(ctx context.Context, id string) (*WorkerInfo, error)
	List(ctx context.Context) ([]WorkerInfo, error)
	Remove(ctx context.Context, id string) error
	// MarkStale marks workers not seen in > threshold as offline.
	MarkStale(ctx context.Context, threshold time.Duration) error
	// StaleIDs returns IDs of workers that have not sent a heartbeat within
	// threshold and are not yet marked offline. Used before reassigning their jobs.
	StaleIDs(ctx context.Context, threshold time.Duration) ([]string, error)
}
