package importer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/postgres"
	"github.com/thingzio/devpulse/pkg/plan"
	"github.com/thingzio/devpulse/pkg/tenant"
)

// Run iterates active repos via a SKIP LOCKED claim queue and imports each one.
// Multiple concurrent executions safely share work without overlap.
func Run(ctx context.Context) error {
	store, err := postgres.NewFromEnv(postgres.ImportPoolConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			slog.Error("closing store", "error", closeErr)
		}
	}()

	db := store.DB()
	start := time.Now()

	executionID := os.Getenv("CLOUD_RUN_EXECUTION")
	if executionID == "" {
		executionID = fmt.Sprintf("local-%d", time.Now().Unix())
	}

	ghAppConfig, ghAppErr := tenant.LoadGitHubAppConfig()
	if ghAppErr != nil {
		slog.Warn("github app config not available, installation tokens disabled", "error", ghAppErr)
	}

	llmCfg := data.NewLLMConfigFromEnv()

	if err := tenant.PrepareImportQueue(ctx, db); err != nil {
		return fmt.Errorf("preparing import queue: %w", err)
	}

	slog.Info("import worker starting", "execution", executionID)

	// Cache tokens per tenant to avoid re-minting for each repo of the same tenant.
	// Installation tokens are valid for 1 hour — safe to reuse within a single run.
	tokenCache := make(map[string]string)

	var totalRepos, totalErrors int
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("import canceled: %w", err)
		}

		claim, err := tenant.ClaimNextRepo(ctx, db, executionID)
		if errors.Is(err, tenant.ErrNoWork) {
			break
		}
		if err != nil {
			return fmt.Errorf("claiming next repo: %w", err)
		}

		totalRepos++
		slog.Info("claimed repo",
			"org", claim.Org,
			"repo", claim.Repo,
			"tenant_id", claim.TenantID,
			"execution", executionID)

		if importErr := importClaim(ctx, db, store, claim, tokenCache, ghAppConfig, llmCfg); importErr != nil {
			totalErrors++
			slog.Error("repo import failed",
				"org", claim.Org,
				"repo", claim.Repo,
				"error", importErr)
			// Do not call MarkRepoDone on failure — leaving done_at NULL means
			// this repo re-enters the queue on the next execution.
			continue
		}

		if err := tenant.MarkRepoDone(ctx, db, claim.ID); err != nil {
			slog.Warn("marking repo done",
				"org", claim.Org,
				"repo", claim.Repo,
				"error", err)
		}
	}

	// Enrich developer profiles from GitHub once per execution, after all repo
	// imports. Uses any available token from the cache.
	for _, anyToken := range tokenCache {
		slog.Info("phase: developer enrichment")
		if enrichErr := store.EnrichDeveloperEntities(ctx, anyToken); enrichErr != nil {
			slog.Warn("enriching developer entities", "error", enrichErr)
		}
		break
	}

	slog.Info("import worker complete",
		"repos", totalRepos,
		"errors", totalErrors,
		"duration", time.Since(start).String())

	if totalErrors > 0 && totalErrors == totalRepos {
		return fmt.Errorf("all %d repo imports failed", totalErrors)
	}

	return nil
}

func importClaim(ctx context.Context, db *sql.DB, store data.Store,
	claim *tenant.ClaimedRepo, tokenCache map[string]string,
	ghAppConfig *tenant.GitHubAppConfig, llmCfg *data.LLMConfig) error {
	tn, err := tenant.GetTenantByID(ctx, db, claim.TenantID)
	if err != nil {
		return fmt.Errorf("getting tenant: %w", err)
	}

	weeklyEvents, err := tenant.GetWeeklyEventCount(ctx, db, claim.TenantID)
	if err != nil {
		return fmt.Errorf("getting weekly events: %w", err)
	}

	weeklyPct := float64(0)
	if tn.MaxEventsPerWeek > 0 {
		weeklyPct = float64(weeklyEvents) / float64(tn.MaxEventsPerWeek) * 100
	}

	slog.Info("tenant usage",
		"tenant_id", claim.TenantID,
		"weekly_events", weeklyEvents,
		"max_events_per_week", tn.MaxEventsPerWeek,
		"weekly_pct", weeklyPct)

	if tn.MaxEventsPerWeek > 0 && weeklyEvents >= tn.MaxEventsPerWeek {
		slog.Warn("weekly event limit reached, skipping import",
			"tenant_id", claim.TenantID,
			"org", claim.Org,
			"repo", claim.Repo,
			"weekly_events", weeklyEvents,
			"max_events_per_week", tn.MaxEventsPerWeek)
		return nil
	}

	token, ok := tokenCache[claim.TenantID]
	if !ok {
		token = resolveToken(ctx, db, claim.TenantID, ghAppConfig)
		if token != "" {
			tokenCache[claim.TenantID] = token
		}
	}

	return importRepo(ctx, store, token, claim.Org, claim.Repo, llmCfg, tn.Plan)
}

func importRepo(ctx context.Context, store data.Store, token, org, repo string, llmCfg *data.LLMConfig, planName string) error {
	start := time.Now()
	slog.Info("importing repo", "org", org, "repo", repo)

	var errs int

	slog.Info("phase: metadata", "org", org, "repo", repo)
	if err := store.ImportRepoMeta(ctx, token, org, repo); err != nil {
		slog.Error("importing repo meta", "org", org, "repo", repo, "error", err)
		errs++
	}

	slog.Info("phase: events", "org", org, "repo", repo)
	if _, _, err := store.ImportEvents(ctx, token, org, repo, 6); err != nil {
		slog.Error("importing events", "org", org, "repo", repo, "error", err)
		errs++
	}

	slog.Info("phase: releases", "org", org, "repo", repo)
	if err := store.ImportReleases(ctx, token, org, repo); err != nil {
		slog.Error("importing releases", "org", org, "repo", repo, "error", err)
		errs++
	}

	slog.Info("phase: metrics", "org", org, "repo", repo)
	if err := store.ImportRepoMetricHistory(ctx, token, org, repo); err != nil {
		slog.Error("importing metric history", "org", org, "repo", repo, "error", err)
		errs++
	}

	slog.Info("phase: containers", "org", org, "repo", repo)
	if err := store.ImportContainerVersions(ctx, token, org, repo); err != nil {
		slog.Error("importing container versions", "org", org, "repo", repo, "error", err)
		errs++
	}

	slog.Info("phase: reputation", "org", org, "repo", repo)
	if _, err := store.ImportReputation(ctx, &org, &repo); err != nil {
		slog.Error("importing reputation", "org", org, "repo", repo, "error", err)
		errs++
	}

	limits, _ := plan.Get(planName)

	if token != "" && limits.DeepReputation {
		slog.Info("phase: deep reputation", "org", org, "repo", repo)
		tokenFn := func() string { return token }
		if res, err := store.ImportDeepReputation(ctx, tokenFn, deepReputationDefaultLimit, 0, &org, &repo); err != nil {
			slog.Error("importing deep reputation", "org", org, "repo", repo, "error", err)
			errs++
		} else {
			slog.Info("deep reputation complete", "org", org, "repo", repo, "scored", res.Scored, "errors", res.Errors)
		}
	} else if token != "" {
		slog.Debug("skipping deep reputation, not included in plan", "org", org, "repo", repo, "plan", planName)
	}

	// Generate LLM insights (skipped if ANTHROPIC_API_KEY not set)
	if llmCfg != nil && limits.AILevel > 0 {
		slog.Info("phase: insights", "org", org, "repo", repo)
		if err := generateRepoInsights(ctx, store, llmCfg, org, repo); err != nil {
			slog.Error("generating insights", "org", org, "repo", repo, "error", err)
			errs++
		}
	} else if llmCfg != nil {
		slog.Debug("skipping insights, not included in plan", "org", org, "repo", repo, "plan", planName)
	}

	slog.Info("repo import complete", "org", org, "repo", repo, "errors", errs, "duration", time.Since(start).String())

	if errs > 0 {
		return fmt.Errorf("import completed with %d errors for %s/%s", errs, org, repo)
	}
	return nil
}

const (
	insightsPeriodMonths       = 3
	deepReputationDefaultLimit = 100
)

func generateRepoInsights(ctx context.Context, store data.Store, cfg *data.LLMConfig, org, repo string) error {
	metrics := data.GatherInsightsMetrics(ctx, store, org, repo, insightsPeriodMonths)

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
// Priority: 1) GITHUB_TOKEN env var, 2) GitHub App installation token, 3) empty (unauthenticated, public repos only).
func resolveToken(ctx context.Context, db *sql.DB, tenantID string, ghAppConfig *tenant.GitHubAppConfig) string {
	// 1. Explicit token (dev/testing)
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		slog.Debug("using GITHUB_TOKEN env var")
		return token
	}

	// 2. GitHub App installation token
	if ghAppConfig != nil {
		if token, err := mintInstallationToken(ctx, db, tenantID, ghAppConfig); err == nil {
			return token
		} else {
			slog.Debug("github app token minting failed, falling back to unauthenticated",
				"tenant_id", tenantID, "error", err)
		}
	}

	// 3. Unauthenticated (60 req/hr, public repos only)
	slog.Warn("no GitHub token available, using unauthenticated API (rate limited to 60 req/hr)",
		"tenant_id", tenantID)
	return ""
}

// mintInstallationToken mints an installation token for the tenant's first active installation.
func mintInstallationToken(ctx context.Context, db *sql.DB, tenantID string, cfg *tenant.GitHubAppConfig) (string, error) {
	installs, err := tenant.GetActiveInstallations(ctx, db, tenantID)
	if err != nil {
		return "", fmt.Errorf("getting installations: %w", err)
	}

	if len(installs) == 0 {
		return "", fmt.Errorf("no active installations for tenant %s", tenantID)
	}

	// Use the first active installation
	install := installs[0]
	token, err := tenant.MintInstallationToken(ctx, cfg, install.ID)
	if err != nil {
		return "", fmt.Errorf("minting token for installation %d: %w", install.ID, err)
	}

	slog.Info("minted installation token",
		"tenant_id", tenantID,
		"installation_id", install.ID,
		"login", install.Login,
		"expires_at", token.ExpiresAt,
	)

	return token.Token, nil
}
