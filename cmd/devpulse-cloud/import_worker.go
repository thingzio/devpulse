package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/mchmarny/devpulse/pkg/data"
	"github.com/mchmarny/devpulse/pkg/tenant"
	urfave "github.com/urfave/cli/v3"
)

var importWorkerCmd = &urfave.Command{
	Name:  "import",
	Usage: "Run tenant data import worker",
	Flags: []urfave.Flag{
		&urfave.StringFlag{
			Name:     "github-app-id",
			Sources:  urfave.EnvVars("GITHUB_APP_ID"),
			Required: true,
		},
		&urfave.StringFlag{
			Name:     "github-app-key-path",
			Usage:    "Path to GitHub App private key PEM file",
			Sources:  urfave.EnvVars("GITHUB_APP_KEY_PATH"),
			Required: true,
		},
	},
	Action: cmdImportWorker,
}

func cmdImportWorker(ctx context.Context, cmd *urfave.Command) error {
	start := time.Now()

	dsn := cmd.Root().String("db")
	store, err := openSaaSStore(dsn)
	if err != nil {
		return err
	}
	defer store.Close()

	db := store.DB()

	// TODO: parse GitHub App config from flags
	// appCfg := &tenant.GitHubAppConfig{...}

	tenants, err := tenant.GetActiveTenants(db)
	if err != nil {
		return err
	}

	slog.Info("import worker starting", "tenants", len(tenants))

	var totalErrors int
	for _, t := range tenants {
		if err := ctx.Err(); err != nil {
			return err
		}

		if importErr := importTenant(ctx, db, store, t); importErr != nil {
			totalErrors++
			slog.Error("tenant import failed",
				"tenant_id", t.ID,
				"username", t.Username,
				"error", importErr,
			)
			continue
		}
	}

	slog.Info("import worker complete",
		"tenants", len(tenants),
		"errors", totalErrors,
		"duration", time.Since(start).String(),
	)

	return nil
}

func importTenant(ctx context.Context, db interface{}, store data.Store, t tenant.ActiveTenant) error {
	slog.Info("importing tenant", "tenant_id", t.ID, "username", t.Username)

	// TODO: full implementation
	// 1. Get installations for this tenant
	// 2. Skip suspended installations
	// 3. Mint installation token per installation
	// 4. Get repos for each installation
	// 5. Check staleness per repo
	// 6. Call existing Store.ImportEvents, ImportRepoMeta, etc.
	// 7. Log sync_summary with tenant context

	return nil
}
