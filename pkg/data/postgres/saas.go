package postgres

import (
	"database/sql"
	"embed"
)

//go:embed sql/migrations_saas/*.sql
var saasMigrationsFS embed.FS

// RunSaaSMigrations runs SaaS-specific migrations (tenant tables, RLS policies).
// Called by devpulse-cloud only, not the self-hosted CLI.
func RunSaaSMigrations(db *sql.DB) error {
	return applyMigrations(db, migrateConfig{
		fs:           saasMigrationsFS,
		dir:          "sql/migrations_saas",
		versionTable: "saas_schema_version",
		lockID:       2,
		label:        "saas",
	})
}
