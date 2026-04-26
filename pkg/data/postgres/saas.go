package postgres

import (
	"context"
	"database/sql"
	"embed"
	"log/slog"
	"time"
)

//go:embed sql/migrations_saas/*.sql
var saasMigrationsFS embed.FS

// RunSaaSMigrations runs SaaS-specific migrations (tenant tables, RLS policies).
// Called by devpulse-cloud only, not the self-hosted CLI.
func RunSaaSMigrations(db *sql.DB) error {
	if err := applyMigrations(db, migrateConfig{
		fs:           saasMigrationsFS,
		dir:          "sql/migrations_saas",
		versionTable: "saas_schema_version",
		lockID:       2,
		label:        "saas",
	}); err != nil {
		return err
	}

	return ensureSampleColumn(db)
}

// ensureSampleColumn adds the sample column if missing.
// Belt-and-suspenders for the 002 migration in case the versioned
// migration was skipped on an existing database.
func ensureSampleColumn(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.columns
		 WHERE table_name = 'devpulse_tenant_repo' AND column_name = 'sample')`).Scan(&exists)
	if err != nil {
		slog.Warn("checking sample column existence", "error", err)
		return nil
	}
	if exists {
		return nil
	}

	slog.Warn("sample column missing, applying directly")
	_, err = db.ExecContext(ctx,
		`ALTER TABLE devpulse_tenant_repo ADD COLUMN IF NOT EXISTS sample BOOLEAN NOT NULL DEFAULT FALSE`)
	if err != nil {
		return err
	}
	slog.Info("sample column added successfully")
	return nil
}
