package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

// migrateConfig holds the parameters for a migration run.
type migrateConfig struct {
	fs           embed.FS
	dir          string
	versionTable string
	lockID       int
	label        string
}

// applyMigrations runs numbered SQL migration files from an embedded FS.
// Uses a dedicated connection so the advisory lock is held on the same session
// as the version check and migration execution.
func applyMigrations(db *sql.DB, cfg migrateConfig) error {
	ctx := context.Background()

	conn, connErr := db.Conn(ctx)
	if connErr != nil {
		return fmt.Errorf("acquiring %s migration connection: %w", cfg.label, connErr)
	}
	defer conn.Close()

	createSQL := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		version INTEGER PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL DEFAULT NOW()
	)`, cfg.versionTable)
	if _, err := conn.ExecContext(ctx, createSQL); err != nil {
		return fmt.Errorf("creating %s table: %w", cfg.versionTable, err)
	}

	if _, err := conn.ExecContext(ctx, fmt.Sprintf("SELECT pg_advisory_lock(%d)", cfg.lockID)); err != nil {
		return fmt.Errorf("acquiring %s migration lock: %w", cfg.label, err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, fmt.Sprintf("SELECT pg_advisory_unlock(%d)", cfg.lockID)) }()

	var currentVersion int
	if err := conn.QueryRowContext(ctx, fmt.Sprintf("SELECT COALESCE(MAX(version), 0) FROM %s", cfg.versionTable)).Scan(&currentVersion); err != nil {
		return fmt.Errorf("reading %s version: %w", cfg.label, err)
	}

	entries, err := cfg.fs.ReadDir(cfg.dir)
	if err != nil {
		return fmt.Errorf("reading %s migrations dir: %w", cfg.label, err)
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

		content, err := cfg.fs.ReadFile(cfg.dir + "/" + name)
		if err != nil {
			return fmt.Errorf("reading %s migration %s: %w", cfg.label, name, err)
		}

		slog.Debug("applying migration", "label", cfg.label, "version", ver, "file", name)

		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("beginning %s migration tx %d: %w", cfg.label, ver, err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("executing %s migration %s: %w", cfg.label, name, err)
		}

		insertSQL := "INSERT INTO " + cfg.versionTable + " (version) VALUES ($1) ON CONFLICT DO NOTHING" //nolint:gosec // table name from trusted config, not user input
		if _, err := tx.Exec(insertSQL, ver); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("recording %s migration %d: %w", cfg.label, ver, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing %s migration %d: %w", cfg.label, ver, err)
		}

		slog.Info("applied migration", "label", cfg.label, "version", ver, "file", name)
	}

	return nil
}
