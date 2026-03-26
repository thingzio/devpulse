package postgres

import (
	"database/sql"
	"embed"
	"fmt"
	"time"

	_ "github.com/lib/pq"

	"github.com/mchmarny/devpulse/pkg/data"
)

// Compile-time check that Store implements data.Store.
var _ data.Store = (*Store)(nil)

//go:embed sql/migrations/*.sql
var migrationsFS embed.FS

// Store implements data.Store for PostgreSQL.
type Store struct {
	db *sql.DB
}

const (
	maxOpenConns    = 25
	maxIdleConns    = 10
	connMaxLifetime = 5 * time.Minute
	connMaxIdleTime = 1 * time.Minute
)

// New creates a new PostgreSQL Store, running migrations automatically.
func New(dsn string) (*Store, error) {
	if dsn == "" {
		return nil, fmt.Errorf("dsn not specified")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	if err := runMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database connection pool.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB returns the underlying *sql.DB for cases that need direct access.
func (s *Store) DB() *sql.DB {
	return s.db
}

func runMigrations(db *sql.DB) error {
	return applyMigrations(db, migrateConfig{
		fs:           migrationsFS,
		dir:          "sql/migrations",
		versionTable: "schema_version",
		lockID:       1,
		label:        "base",
	})
}
