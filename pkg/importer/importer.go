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
		return fmt.Errorf("listing active tenants: %w", err)
	}

	slog.Info("import worker starting", "tenants", len(tenants))

	var totalErrors int
	for _, t := range tenants {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("import canceled: %w", err)
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

	if totalErrors > 0 && totalErrors == len(tenants) {
		return fmt.Errorf("all %d tenant imports failed", totalErrors)
	}

	return nil
}

func importTenant(ctx context.Context, db *sql.DB, store data.Store, t tenant.ActiveTenant) error {
	slog.Info("importing tenant", "tenant_id", t.ID, "username", t.Username)

	repos, err := tenant.ListTenantRepos(ctx, db, t.ID)
	if err != nil {
		return fmt.Errorf("listing repos for tenant %s: %w", t.ID, err)
	}

	if len(repos) == 0 {
		slog.Info("no active repos", "tenant_id", t.ID)
		return nil
	}

	// Check weekly event limit
	tn, err := tenant.GetTenantByID(ctx, db, t.ID)
	if err != nil {
		return fmt.Errorf("getting tenant: %w", err)
	}

	weeklyEvents, err := tenant.GetWeeklyEventCount(ctx, db, t.ID)
	if err != nil {
		return fmt.Errorf("getting weekly events: %w", err)
	}

	slog.Info("tenant usage",
		"tenant_id", t.ID,
		"username", t.Username,
		"weekly_events", weeklyEvents,
		"max_events_per_week", tn.MaxEventsPerWeek,
		"weekly_pct", float64(weeklyEvents)/float64(tn.MaxEventsPerWeek)*100,
	)

	if weeklyEvents >= tn.MaxEventsPerWeek {
		slog.Warn("weekly event limit reached, skipping import",
			"tenant_id", t.ID,
			"weekly_events", weeklyEvents,
			"max_events_per_week", tn.MaxEventsPerWeek,
		)
		return nil
	}

	token, err := resolveToken(ctx, db, t.ID)
	if err != nil {
		return fmt.Errorf("resolving token for tenant %s: %w", t.ID, err)
	}

	var repoErrors int
	for _, r := range repos {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("repo import canceled: %w", err)
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

	if _, err := store.ImportReputation(ctx, &org, &repo); err != nil {
		slog.Error("importing reputation", "org", org, "repo", repo, "error", err)
		errs++
	}

	// Generate LLM insights (skipped if ANTHROPIC_API_KEY not set)
	if llmCfg := data.NewLLMConfigFromEnv(); llmCfg != nil {
		if err := generateRepoInsights(ctx, store, llmCfg, org, repo); err != nil {
			slog.Error("generating insights", "org", org, "repo", repo, "error", err)
			errs++
		}
	}

	slog.Info("repo import complete", "org", org, "repo", repo, "errors", errs, "duration", time.Since(start).String())

	if errs > 0 {
		return fmt.Errorf("import completed with %d errors for %s/%s", errs, org, repo)
	}
	return nil
}

const insightsPeriodMonths = 3

func generateRepoInsights(ctx context.Context, store data.Store, cfg *data.LLMConfig, org, repo string) error {
	metrics, err := data.GatherInsightsMetrics(ctx, store, org, repo, insightsPeriodMonths)
	if err != nil {
		return fmt.Errorf("gathering metrics: %w", err)
	}

	insights, model, err := data.GenerateInsights(ctx, cfg, metrics, insightsPeriodMonths)
	if err != nil {
		return fmt.Errorf("generating insights: %w", err)
	}

	ri := &data.RepoInsights{
		Org:          org,
		Repo:         repo,
		Insights:     insights,
		PeriodMonths: insightsPeriodMonths,
		Model:        model,
		GeneratedAt:  time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}

	if err := store.SaveRepoInsights(ctx, org, repo, ri); err != nil {
		return fmt.Errorf("saving insights: %w", err)
	}

	slog.Info("insights generated", "org", org, "repo", repo, "model", model)
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
