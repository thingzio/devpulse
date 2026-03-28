package importer

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/tenant"
)

// Run iterates active tenants and imports data for each tracked repo.
func Run(ctx context.Context, db *sql.DB, store data.Store) error {
	start := time.Now()

	tenants, err := tenant.GetActiveTenants(ctx, db)
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

func importTenant(ctx context.Context, db *sql.DB, store data.Store, t tenant.ActiveTenant) error {
	slog.Info("importing tenant", "tenant_id", t.ID, "username", t.Username)

	repos, err := tenant.ListTenantRepos(ctx, db, t.ID)
	if err != nil {
		return err
	}

	if len(repos) == 0 {
		slog.Info("no active repos", "tenant_id", t.ID)
		return nil
	}

	token, err := resolveToken(ctx, db, t.ID)
	if err != nil {
		return err
	}

	var repoErrors int
	for _, r := range repos {
		if err := ctx.Err(); err != nil {
			return err
		}

		if importErr := importRepo(ctx, store, token, r.Org, r.Repo); importErr != nil {
			repoErrors++
			slog.Error("repo import failed",
				"org", r.Org,
				"repo", r.Repo,
				"error", importErr,
			)
			continue
		}
	}

	slog.Info("tenant import complete",
		"tenant_id", t.ID,
		"repos", len(repos),
		"errors", repoErrors,
	)

	return nil
}

func importRepo(ctx context.Context, store data.Store, token, org, repo string) error {
	start := time.Now()
	slog.Info("importing repo", "org", org, "repo", repo)

	var errs int

	if err := store.ImportRepoMeta(ctx, token, org, repo); err != nil {
		slog.Error("importing repo meta", "org", org, "repo", repo, "error", err)
		errs++
	}

	if _, _, err := store.ImportEvents(ctx, token, org, repo, 6); err != nil {
		slog.Error("importing events", "org", org, "repo", repo, "error", err)
		errs++
	}

	if err := store.ImportReleases(ctx, token, org, repo); err != nil {
		slog.Error("importing releases", "org", org, "repo", repo, "error", err)
		errs++
	}

	if err := store.ImportRepoMetricHistory(ctx, token, org, repo); err != nil {
		slog.Error("importing metric history", "org", org, "repo", repo, "error", err)
		errs++
	}

	if err := store.ImportContainerVersions(ctx, token, org, repo); err != nil {
		slog.Error("importing container versions", "org", org, "repo", repo, "error", err)
		errs++
	}

	if _, err := store.ImportReputation(&org, &repo); err != nil {
		slog.Error("importing reputation", "org", org, "repo", repo, "error", err)
		errs++
	}

	slog.Info("repo import complete", "org", org, "repo", repo, "errors", errs, "duration", time.Since(start).String())

	if errs > 0 {
		return fmt.Errorf("import completed with %d errors for %s/%s", errs, org, repo)
	}
	return nil
}

// resolveToken returns a GitHub token for API access.
// Prefers GITHUB_TOKEN env var; falls back to minting a GitHub App installation token.
func resolveToken(ctx context.Context, db *sql.DB, tenantID string) (string, error) { //nolint:unparam // ctx+db used when GitHub App token minting is implemented
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		slog.Debug("using GITHUB_TOKEN env var")
		return token, nil
	}

	// TODO: mint GitHub App installation token using ctx, db, tenantID
	// 1. tenant.GetActiveInstallations(ctx, db, tenantID)
	// 2. Load GitHub App private key from GITHUB_APP_KEY_PATH
	// 3. tenant.CreateAppJWT(cfg)
	// 4. tenant.MintInstallationToken(ctx, cfg, installationID)

	return "", fmt.Errorf("%w (tenant %s)", errNoToken, tenantID)
}
