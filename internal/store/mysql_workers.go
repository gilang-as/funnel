package store

import (
	"context"
	"database/sql"
	"time"

	sq "github.com/Masterminds/squirrel"
)

type mysqlWorkerRepo struct {
	db *sql.DB
}

func (r *mysqlWorkerRepo) Upsert(ctx context.Context, w *WorkerInfo) error {
	now := time.Now()
	w.LastSeen = now
	if w.JoinedAt.IsZero() {
		w.JoinedAt = now
	}

	_, err := mysqlBuilder.Insert("workers").
		Columns("id", "address", "capacity", "active_jobs", "status", "version", "last_seen", "joined_at").
		Values(w.ID, w.Address, w.Capacity, w.ActiveJobs, w.Status, w.Version, w.LastSeen, w.JoinedAt).
		Suffix("ON DUPLICATE KEY UPDATE address=VALUES(address), capacity=VALUES(capacity), active_jobs=VALUES(active_jobs), status=VALUES(status), version=VALUES(version), last_seen=VALUES(last_seen)").
		RunWith(r.db).
		ExecContext(ctx)

	return err
}

func (r *mysqlWorkerRepo) Get(ctx context.Context, id string) (*WorkerInfo, error) {
	var w WorkerInfo
	err := mysqlBuilder.Select("*").
		From("workers").
		Where(sq.Eq{"id": id}).
		RunWith(r.db).
		QueryRowContext(ctx).
		Scan(&w.ID, &w.Address, &w.Capacity, &w.ActiveJobs, &w.Status, &w.Version, &w.LastSeen, &w.JoinedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &w, err
}

func (r *mysqlWorkerRepo) List(ctx context.Context) ([]WorkerInfo, error) {
	rows, err := mysqlBuilder.Select("*").From("workers").RunWith(r.db).QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []WorkerInfo
	for rows.Next() {
		var w WorkerInfo
		if err := rows.Scan(&w.ID, &w.Address, &w.Capacity, &w.ActiveJobs, &w.Status, &w.Version, &w.LastSeen, &w.JoinedAt); err != nil {
			return nil, err
		}
		workers = append(workers, w)
	}
	return workers, nil
}

func (r *mysqlWorkerRepo) Remove(ctx context.Context, id string) error {
	_, err := mysqlBuilder.Delete("workers").
		Where(sq.Eq{"id": id}).
		RunWith(r.db).
		ExecContext(ctx)
	return err
}

func (r *mysqlWorkerRepo) MarkStale(ctx context.Context, threshold time.Duration) error {
	deadline := time.Now().Add(-threshold)
	_, err := mysqlBuilder.Update("workers").
		Set("status", "offline").
		Where(sq.Lt{"last_seen": deadline}).
		Where(sq.NotEq{"status": "offline"}).
		RunWith(r.db).
		ExecContext(ctx)
	return err
}

func (r *mysqlWorkerRepo) StaleIDs(ctx context.Context, threshold time.Duration) ([]string, error) {
	deadline := time.Now().Add(-threshold)
	rows, err := mysqlBuilder.Select("id").
		From("workers").
		Where(sq.Lt{"last_seen": deadline}).
		Where(sq.NotEq{"status": "offline"}).
		RunWith(r.db).
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
