package store

import (
	"database/sql"
	"fmt"
	"log"

	sq "github.com/Masterminds/squirrel"
	"gopkg.gilang.dev/funnel/internal/daemon"
)

const mysqlStateSchema = `
CREATE TABLE IF NOT EXISTS state_torrents (
    id     VARCHAR(64)  NOT NULL PRIMARY KEY,
    magnet TEXT         NOT NULL,
    name   VARCHAR(512),
    paused TINYINT(1)   NOT NULL DEFAULT 0
)`

type mysqlStateStore struct {
	db *sql.DB
}

func NewMySQLStateStore(db *sql.DB) (daemon.StateStore, error) {
	if _, err := db.Exec(mysqlStateSchema); err != nil {
		return nil, fmt.Errorf("migrate state_torrents: %w", err)
	}
	return &mysqlStateStore{db: db}, nil
}

func (s *mysqlStateStore) List() []daemon.SavedTorrent {
	rows, err := mysqlBuilder.Select("id", "magnet", "name", "paused").
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
	if err := rows.Err(); err != nil {
		log.Printf("[state] list error: %v", err)
		return nil
	}
	return torrents
}

func (s *mysqlStateStore) Close() error { return s.db.Close() }

func (s *mysqlStateStore) Add(t daemon.SavedTorrent) error {
	_, err := mysqlBuilder.Insert("state_torrents").
		Columns("id", "magnet", "name", "paused").
		Values(t.ID, t.Magnet, t.Name, t.Paused).
		RunWith(s.db).
		Exec()
	return err
}

func (s *mysqlStateStore) Remove(id string) error {
	_, err := mysqlBuilder.Delete("state_torrents").
		Where(sq.Eq{"id": id}).
		RunWith(s.db).
		Exec()
	return err
}

func (s *mysqlStateStore) Update(id string, fn func(*daemon.SavedTorrent)) error {
	var t daemon.SavedTorrent
	err := mysqlBuilder.Select("id", "magnet", "name", "paused").
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

	_, err = mysqlBuilder.Update("state_torrents").
		SetMap(map[string]interface{}{
			"name":   t.Name,
			"paused": t.Paused,
		}).
		Where(sq.Eq{"id": id}).
		RunWith(s.db).
		Exec()

	return err
}
