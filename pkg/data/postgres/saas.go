package postgres

import (
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

//go:embed sql/migrations_saas/*.sql
var saasMigrationsFS embed.FS

// RunSaaSMigrations runs SaaS-specific migrations (tenant tables, RLS policies).
// Called by devpulse-cloud only, not the self-hosted CLI.
func RunSaaSMigrations(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS saas_schema_version (
		version INTEGER PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("creating saas_schema_version table: %w", err)
	}

	if _, err := db.Exec("SELECT pg_advisory_lock(2)"); err != nil {
		return fmt.Errorf("acquiring saas migration lock: %w", err)
	}
	defer func() { _, _ = db.Exec("SELECT pg_advisory_unlock(2)") }()

	var currentVersion int
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM saas_schema_version").Scan(&currentVersion); err != nil {
		return fmt.Errorf("reading saas schema version: %w", err)
	}

	entries, err := saasMigrationsFS.ReadDir("sql/migrations_saas")
	if err != nil {
		return fmt.Errorf("reading saas migrations dir: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		parts := strings.SplitN(name, "_", 2)
		if len(parts) < 2 {
			continue
		}

		var ver int
		if _, err := fmt.Sscanf(parts[0], "%d", &ver); err != nil {
			continue
		}

		if ver <= currentVersion {
			continue
		}

		content, err := saasMigrationsFS.ReadFile("sql/migrations_saas/" + name)
		if err != nil {
			return fmt.Errorf("reading saas migration %s: %w", name, err)
		}

		slog.Debug("applying saas migration", "version", ver, "file", name)

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("beginning saas migration tx %d: %w", ver, err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("executing saas migration %s: %w", name, err)
		}

		if _, err := tx.Exec("INSERT INTO saas_schema_version (version) VALUES ($1)", ver); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("recording saas migration %d: %w", ver, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing saas migration %d: %w", ver, err)
		}

		slog.Info("applied saas migration", "version", ver, "file", name)
	}

	return nil
}
