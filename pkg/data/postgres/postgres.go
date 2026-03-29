package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	_ "github.com/lib/pq"

	"github.com/thingzio/devpulse/pkg/data"
)

// Compile-time check that Store implements data.Store.
var _ data.Store = (*Store)(nil)

//go:embed sql/migrations/*.sql
var migrationsFS embed.FS

// DBTX is the common interface between *sql.DB and *sql.Conn.
// It includes both context and non-context variants so that *sql.DB
// satisfies it directly. For *sql.Conn (which lacks the non-context
// methods) use connAdapter via NewFromConn.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)

	// Non-context variants — present on *sql.DB, bridged for *sql.Conn.
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Prepare(query string) (*sql.Stmt, error)
	Begin() (*sql.Tx, error)
}

// connAdapter wraps *sql.Conn to satisfy the full DBTX interface by
// delegating non-context methods to their context counterparts.
type connAdapter struct {
	*sql.Conn
}

func (c *connAdapter) Exec(query string, args ...any) (sql.Result, error) {
	return c.Conn.ExecContext(context.Background(), query, args...)
}

func (c *connAdapter) Query(query string, args ...any) (*sql.Rows, error) {
	return c.Conn.QueryContext(context.Background(), query, args...)
}

func (c *connAdapter) QueryRow(query string, args ...any) *sql.Row {
	return c.Conn.QueryRowContext(context.Background(), query, args...)
}

func (c *connAdapter) Prepare(query string) (*sql.Stmt, error) {
	return c.Conn.PrepareContext(context.Background(), query)
}

func (c *connAdapter) Begin() (*sql.Tx, error) {
	return c.Conn.BeginTx(context.Background(), nil)
}

// Store implements data.Store for PostgreSQL.
type Store struct {
	db   DBTX
	pool *sql.DB // original pool, used for Close() and DB()
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

	return &Store{db: db, pool: db}, nil
}

// NewFromConn creates a Store backed by a dedicated connection.
// Used for tenant-scoped requests where RLS requires set_config on a specific connection.
func NewFromConn(conn *sql.Conn) *Store {
	return &Store{db: &connAdapter{Conn: conn}}
}

// Close closes the underlying database connection pool.
func (s *Store) Close() error {
	if s.pool == nil {
		return nil
	}
	return s.pool.Close()
}

// DB returns the underlying *sql.DB for cases that need direct access.
func (s *Store) DB() *sql.DB {
	return s.pool
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
