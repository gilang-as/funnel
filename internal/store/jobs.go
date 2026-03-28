package store

import (
	"context"
	"time"
)

type JobStatus string

const (
	JobQueued      JobStatus = "queued"
	JobAssigned    JobStatus = "assigned"
	JobDownloading JobStatus = "downloading"
	JobSeeding     JobStatus = "seeding"
	JobPaused      JobStatus = "paused"
	JobFailed      JobStatus = "failed"
	JobDone        JobStatus = "done"
)

type Job struct {
	ID          string    `db:"id"`
	Magnet      string    `db:"magnet"`
	InfoHash    string    `db:"info_hash"`
	Status      JobStatus `db:"status"`
	WorkerID    string    `db:"worker_id"` // empty if unassigned
	Name        string    `db:"name"`
	Size        int64     `db:"size"`
	Progress    float64   `db:"progress"`
	ErrorMsg    string    `db:"error_msg"`
	Paused      bool      `db:"paused"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
	StartedAt   *time.Time `db:"started_at"`
	CompletedAt *time.Time `db:"completed_at"`
}

type JobFilter struct {
	Status   JobStatus // empty = all
	WorkerID string    // empty = all
}

type JobRepository interface {
	Create(ctx context.Context, job *Job) error
	Get(ctx context.Context, id string) (*Job, error)
	GetByInfoHash(ctx context.Context, infoHash string) (*Job, error)
	Update(ctx context.Context, id string, fn func(*Job)) error
	List(ctx context.Context, filter JobFilter) ([]Job, error)
	Delete(ctx context.Context, id string) error
	// NextPending returns the oldest queued unassigned job, or nil.
	NextPending(ctx context.Context) (*Job, error)
	// Claim atomically assigns the oldest queued job to workerID, or returns nil if none.
	Claim(ctx context.Context, workerID string) (*Job, error)
	// ReleaseFromWorker requeues downloading/assigned jobs and marks seeding jobs as done
	// for the given worker. Used when a worker shuts down or goes offline.
	ReleaseFromWorker(ctx context.Context, workerID string) error
}
