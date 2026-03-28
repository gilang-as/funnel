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
	_ "github.com/go-sql-driver/mysql"
)

//go:embed migrations/mysql/*.sql
var mysqlMigrations embed.FS

type mysqlStore struct {
	db   *sql.DB
	jobs *mysqlJobRepo
	wrk  *mysqlWorkerRepo
	tok  *mysqlTokenRepo
}

var mysqlBuilder = sq.StatementBuilder.PlaceholderFormat(sq.Question)

func NewMySQLStore(dsn string) (Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	s := &mysqlStore{
		db: db,
	}
	s.jobs = &mysqlJobRepo{db: db}
	s.wrk = &mysqlWorkerRepo{db: db}
	s.tok = &mysqlTokenRepo{db: db}

	return s, nil
}

func (s *mysqlStore) Jobs() JobRepository       { return s.jobs }
func (s *mysqlStore) Workers() WorkerRepository { return s.wrk }
func (s *mysqlStore) Tokens() TokenRepository   { return s.tok }
func (s *mysqlStore) Close() error              { return s.db.Close() }

func (s *mysqlStore) RunMigrations(ctx context.Context) error {
	entries, err := fs.ReadDir(mysqlMigrations, "migrations/mysql")
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
		log.Printf("[store] applying mysql migration: %s", name)
		content, err := fs.ReadFile(mysqlMigrations, "migrations/mysql/"+name)
		if err != nil {
			return err
		}

		// Split by semicolon for multiple statements
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
