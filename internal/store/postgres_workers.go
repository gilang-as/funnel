package store

import (
	"context"
	"database/sql"
	"time"

	sq "github.com/Masterminds/squirrel"
)

type pgWorkerRepo struct {
	db *sql.DB
}

func (r *pgWorkerRepo) Upsert(ctx context.Context, w *WorkerInfo) error {
	now := time.Now()
	w.LastSeen = now
	if w.JoinedAt.IsZero() {
		w.JoinedAt = now
	}

	_, err := pgBuilder.Insert("workers").
		Columns("id", "address", "capacity", "active_jobs", "status", "version", "last_seen", "joined_at").
		Values(w.ID, w.Address, w.Capacity, w.ActiveJobs, w.Status, w.Version, w.LastSeen, w.JoinedAt).
		Suffix("ON CONFLICT (id) DO UPDATE SET address=EXCLUDED.address, capacity=EXCLUDED.capacity, active_jobs=EXCLUDED.active_jobs, status=EXCLUDED.status, version=EXCLUDED.version, last_seen=EXCLUDED.last_seen").
		RunWith(r.db).
		ExecContext(ctx)

	return err
}

func (r *pgWorkerRepo) Get(ctx context.Context, id string) (*WorkerInfo, error) {
	var w WorkerInfo
	err := pgBuilder.Select("*").
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

func (r *pgWorkerRepo) List(ctx context.Context) ([]WorkerInfo, error) {
	rows, err := pgBuilder.Select("*").From("workers").RunWith(r.db).QueryContext(ctx)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return workers, nil
}

func (r *pgWorkerRepo) Remove(ctx context.Context, id string) error {
	_, err := pgBuilder.Delete("workers").
		Where(sq.Eq{"id": id}).
		RunWith(r.db).
		ExecContext(ctx)
	return err
}

func (r *pgWorkerRepo) MarkStale(ctx context.Context, threshold time.Duration) error {
	deadline := time.Now().Add(-threshold)
	_, err := pgBuilder.Update("workers").
		Set("status", "offline").
		Where(sq.Lt{"last_seen": deadline}).
		Where(sq.NotEq{"status": "offline"}).
		RunWith(r.db).
		ExecContext(ctx)
	return err
}

func (r *pgWorkerRepo) StaleIDs(ctx context.Context, threshold time.Duration) ([]string, error) {
	deadline := time.Now().Add(-threshold)
	rows, err := pgBuilder.Select("id").
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}
