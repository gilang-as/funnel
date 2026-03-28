package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
)

type mysqlJobRepo struct {
	db *sql.DB
}

func (r *mysqlJobRepo) Create(ctx context.Context, job *Job) error {
	now := time.Now()
	job.CreatedAt = now
	job.UpdatedAt = now

	_, err := mysqlBuilder.Insert("jobs").
		Columns("id", "magnet", "info_hash", "status", "worker_id", "name", "size", "progress", "error_msg", "paused", "created_at", "updated_at").
		Values(job.ID, job.Magnet, job.InfoHash, job.Status, job.WorkerID, job.Name, job.Size, job.Progress, job.ErrorMsg, job.Paused, job.CreatedAt, job.UpdatedAt).
		RunWith(r.db).
		ExecContext(ctx)

	return err
}

func (r *mysqlJobRepo) Get(ctx context.Context, id string) (*Job, error) {
	var job Job
	err := mysqlBuilder.Select("*").
		From("jobs").
		Where(sq.Eq{"id": id}).
		RunWith(r.db).
		QueryRowContext(ctx).
		Scan(&job.ID, &job.Magnet, &job.InfoHash, &job.Status, &job.WorkerID, &job.Name, &job.Size, &job.Progress, &job.ErrorMsg, &job.Paused, &job.CreatedAt, &job.UpdatedAt, &job.StartedAt, &job.CompletedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &job, err
}

func (r *mysqlJobRepo) GetByInfoHash(ctx context.Context, infoHash string) (*Job, error) {
	var job Job
	err := mysqlBuilder.Select("*").
		From("jobs").
		Where(sq.Eq{"info_hash": infoHash}).
		RunWith(r.db).
		QueryRowContext(ctx).
		Scan(&job.ID, &job.Magnet, &job.InfoHash, &job.Status, &job.WorkerID, &job.Name, &job.Size, &job.Progress, &job.ErrorMsg, &job.Paused, &job.CreatedAt, &job.UpdatedAt, &job.StartedAt, &job.CompletedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &job, err
}

func (r *mysqlJobRepo) Update(ctx context.Context, id string, fn func(*Job)) error {
	job, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if job == nil {
		return fmt.Errorf("job %s not found", id)
	}

	fn(job)
	job.UpdatedAt = time.Now()

	_, err = mysqlBuilder.Update("jobs").
		SetMap(map[string]interface{}{
			"status":       job.Status,
			"worker_id":    job.WorkerID,
			"name":         job.Name,
			"size":         job.Size,
			"progress":     job.Progress,
			"error_msg":    job.ErrorMsg,
			"paused":       job.Paused,
			"updated_at":   job.UpdatedAt,
			"started_at":   job.StartedAt,
			"completed_at": job.CompletedAt,
		}).
		Where(sq.Eq{"id": id}).
		RunWith(r.db).
		ExecContext(ctx)

	return err
}

func (r *mysqlJobRepo) List(ctx context.Context, filter JobFilter) ([]Job, error) {
	q := mysqlBuilder.Select("*").From("jobs")
	if filter.Status != "" {
		q = q.Where(sq.Eq{"status": filter.Status})
	}
	if filter.WorkerID != "" {
		q = q.Where(sq.Eq{"worker_id": filter.WorkerID})
	}

	rows, err := q.RunWith(r.db).QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(&job.ID, &job.Magnet, &job.InfoHash, &job.Status, &job.WorkerID, &job.Name, &job.Size, &job.Progress, &job.ErrorMsg, &job.Paused, &job.CreatedAt, &job.UpdatedAt, &job.StartedAt, &job.CompletedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (r *mysqlJobRepo) Delete(ctx context.Context, id string) error {
	_, err := mysqlBuilder.Delete("jobs").
		Where(sq.Eq{"id": id}).
		RunWith(r.db).
		ExecContext(ctx)
	return err
}

func (r *mysqlJobRepo) NextPending(ctx context.Context) (*Job, error) {
	var job Job
	err := mysqlBuilder.Select("*").
		From("jobs").
		Where(sq.Eq{"status": JobQueued, "worker_id": ""}).
		OrderBy("created_at ASC").
		Limit(1).
		RunWith(r.db).
		QueryRowContext(ctx).
		Scan(&job.ID, &job.Magnet, &job.InfoHash, &job.Status, &job.WorkerID, &job.Name, &job.Size, &job.Progress, &job.ErrorMsg, &job.Paused, &job.CreatedAt, &job.UpdatedAt, &job.StartedAt, &job.CompletedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &job, err
}

func (r *mysqlJobRepo) Claim(ctx context.Context, workerID string) (*Job, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var id, magnet, infoHash string
	err = tx.QueryRowContext(ctx,
		`SELECT id, magnet, info_hash FROM jobs WHERE status='queued' AND worker_id='' ORDER BY created_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED`,
	).Scan(&id, &magnet, &infoHash)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if _, err = tx.ExecContext(ctx,
		`UPDATE jobs SET status='assigned', worker_id=?, updated_at=?, started_at=? WHERE id=?`,
		workerID, now, now, id,
	); err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &Job{ID: id, Magnet: magnet, InfoHash: infoHash, Status: JobAssigned, WorkerID: workerID}, nil
}

func (r *mysqlJobRepo) ReleaseFromWorker(ctx context.Context, workerID string) error {
	now := time.Now()
	// downloading/assigned → queued (reassignable)
	if _, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status='queued', worker_id='', updated_at=? WHERE worker_id=? AND status IN ('assigned','downloading')`,
		now, workerID,
	); err != nil {
		return err
	}
	// seeding → done (user must re-add manually to resume seeding)
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status='done', worker_id='', updated_at=?, completed_at=? WHERE worker_id=? AND status='seeding'`,
		now, now, workerID,
	)
	return err
}
