package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/thingzio/devpulse/pkg/config"
	"github.com/thingzio/devpulse/pkg/data"
)

// Compile-time check that Store implements data.Store.
var _ data.Store = (*Store)(nil)

//go:embed sql/migrations/*.sql
var migrationsFS embed.FS

// DBTX is the common interface between *sql.DB and *sql.Conn.
// Both satisfy this interface natively — no adapter needed.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// Store implements data.Store for PostgreSQL.
type Store struct {
	db   DBTX
	pool *sql.DB // original pool, used for Close() and DB()
}

// PoolConfig controls database connection pool sizing.
type PoolConfig struct {
	AppName         string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

const (
	defaultConnMaxLifetime = 5 * time.Minute
	defaultConnMaxIdleTime = 1 * time.Minute
)

// DefaultPoolConfig returns pool settings suitable for the site service.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		AppName:         "devpulse-site",
		MaxOpenConns:    17,
		MaxIdleConns:    5,
		ConnMaxLifetime: defaultConnMaxLifetime,
		ConnMaxIdleTime: defaultConnMaxIdleTime,
	}
}

// ImportPoolConfig returns smaller pool settings suitable for batch import workers.
func ImportPoolConfig() PoolConfig {
	return PoolConfig{
		AppName:         "devpulse-import",
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: defaultConnMaxLifetime,
		ConnMaxIdleTime: defaultConnMaxIdleTime,
	}
}

// applyEnvOverrides lets DB_MAX_OPEN_CONNS and DB_MAX_IDLE_CONNS override code defaults.
func (c *PoolConfig) applyEnvOverrides() {
	c.MaxOpenConns = config.DBMaxOpenConns(c.MaxOpenConns)
	c.MaxIdleConns = config.DBMaxIdleConns(c.MaxIdleConns)
}

// applyAppName appends application_name to a DSN if AppName is set and not
// already present. Supports both URI and keyword/value connection strings.
func applyAppName(dsn, appName string) string {
	if appName == "" || strings.Contains(dsn, "application_name") {
		return dsn
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if strings.Contains(dsn, "?") {
			return dsn + "&application_name=" + appName
		}
		return dsn + "?application_name=" + appName
	}
	return dsn + " application_name=" + appName
}

// applyTimeZoneUTC appends timezone=UTC to a DSN if not already present.
// Supports both URI and keyword/value connection strings. Casting a
// TIMESTAMPTZ to date or text depends on the session TimeZone, so pinning it
// makes those conversions deterministic instead of inheriting whatever the
// server happens to default to.
func applyTimeZoneUTC(dsn string) string {
	if strings.Contains(dsn, "timezone") {
		return dsn
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if strings.Contains(dsn, "?") {
			return dsn + "&timezone=UTC"
		}
		return dsn + "?timezone=UTC"
	}
	return dsn + " timezone=UTC"
}

// New creates a new PostgreSQL Store, running migrations automatically.
// Uses DefaultPoolConfig if no config is provided.
// DB_MAX_OPEN_CONNS and DB_MAX_IDLE_CONNS env vars override code defaults.
func New(dsn string, cfgs ...PoolConfig) (*Store, error) {
	if dsn == "" {
		return nil, fmt.Errorf("dsn not specified")
	}

	cfg := DefaultPoolConfig()
	if len(cfgs) > 0 {
		cfg = cfgs[0]
	}
	cfg.applyEnvOverrides()

	db, err := sql.Open("postgres", applyTimeZoneUTC(applyAppName(dsn, cfg.AppName)))
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pingCancel()
	if err := db.PingContext(pingCtx); err != nil {
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
	return &Store{db: conn}
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

// NewFromEnv creates a Store from DATABASE_URL env var, runs base + SaaS migrations.
// Uses DefaultPoolConfig if no config is provided.
func NewFromEnv(cfgs ...PoolConfig) (*Store, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	store, err := New(dsn, cfgs...)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := RunSaaSMigrations(store.DB()); err != nil {
		store.Close()
		return nil, fmt.Errorf("running saas migrations: %w", err)
	}
	return store, nil
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
