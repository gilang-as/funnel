package store

import (
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"gopkg.gilang.dev/funnel/internal/daemon"
)

const pgStateSchema = `
CREATE TABLE IF NOT EXISTS state_torrents (
    id     VARCHAR(64)   NOT NULL PRIMARY KEY,
    magnet TEXT          NOT NULL,
    name   VARCHAR(512),
    paused BOOLEAN       NOT NULL DEFAULT FALSE
)`

type pgStateStore struct {
	db *sql.DB
}

func NewPostgresStateStore(db *sql.DB) (daemon.StateStore, error) {
	if _, err := db.Exec(pgStateSchema); err != nil {
		return nil, fmt.Errorf("migrate state_torrents: %w", err)
	}
	return &pgStateStore{db: db}, nil
}

func (s *pgStateStore) List() []daemon.SavedTorrent {
	rows, err := pgBuilder.Select("id", "magnet", "name", "paused").
		From("state_torrents").
		RunWith(s.db).
		Query()
	if err != nil {
		return nil
	}
	defer rows.Close()

	var torrents []daemon.SavedTorrent
	for rows.Next() {
		var t daemon.SavedTorrent
		if err := rows.Scan(&t.ID, &t.Magnet, &t.Name, &t.Paused); err != nil {
			continue
		}
		torrents = append(torrents, t)
	}
	return torrents
}

func (s *pgStateStore) Add(t daemon.SavedTorrent) error {
	_, err := pgBuilder.Insert("state_torrents").
		Columns("id", "magnet", "name", "paused").
		Values(t.ID, t.Magnet, t.Name, t.Paused).
		RunWith(s.db).
		Exec()
	return err
}

func (s *pgStateStore) Remove(id string) error {
	_, err := pgBuilder.Delete("state_torrents").
		Where(sq.Eq{"id": id}).
		RunWith(s.db).
		Exec()
	return err
}

func (s *pgStateStore) Update(id string, fn func(*daemon.SavedTorrent)) error {
	var t daemon.SavedTorrent
	err := pgBuilder.Select("id", "magnet", "name", "paused").
		From("state_torrents").
		Where(sq.Eq{"id": id}).
		RunWith(s.db).
		QueryRow().
		Scan(&t.ID, &t.Magnet, &t.Name, &t.Paused)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}

	fn(&t)

	_, err = pgBuilder.Update("state_torrents").
		SetMap(map[string]interface{}{
			"name":   t.Name,
			"paused": t.Paused,
		}).
		Where(sq.Eq{"id": id}).
		RunWith(s.db).
		Exec()

	return err
}
