package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"sort"
	"strings"

	sq "github.com/Masterminds/squirrel"
	_ "github.com/lib/pq"
)

//go:embed migrations/postgres/*.sql
var postgresMigrations embed.FS

type postgresStore struct {
	db   *sql.DB
	jobs *pgJobRepo
	wrk  *pgWorkerRepo
	tok  *pgTokenRepo
}

var pgBuilder = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

func NewPostgresStore(dsn string) (Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	s := &postgresStore{
		db: db,
	}
	s.jobs = &pgJobRepo{db: db}
	s.wrk = &pgWorkerRepo{db: db}
	s.tok = &pgTokenRepo{db: db}

	return s, nil
}

func (s *postgresStore) Jobs() JobRepository       { return s.jobs }
func (s *postgresStore) Workers() WorkerRepository { return s.wrk }
func (s *postgresStore) Tokens() TokenRepository   { return s.tok }
func (s *postgresStore) Close() error              { return s.db.Close() }

func (s *postgresStore) RunMigrations(ctx context.Context) error {
	entries, err := fs.ReadDir(postgresMigrations, "migrations/postgres")
	if err != nil {
		return err
	}

	var filenames []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			filenames = append(filenames, e.Name())
		}
	}
	sort.Strings(filenames)

	for _, name := range filenames {
		log.Printf("[store] applying postgres migration: %s", name)
		content, err := fs.ReadFile(postgresMigrations, "migrations/postgres/"+name)
		if err != nil {
			return err
		}

		queries := strings.Split(string(content), ";")
		for _, q := range queries {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			if _, err := s.db.ExecContext(ctx, q); err != nil {
				return fmt.Errorf("exec migration %s: %w", name, err)
			}
		}
	}

	return nil
}
