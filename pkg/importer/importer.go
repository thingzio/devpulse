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
	"github.com/thingzio/devpulse/pkg/data/ghutil"
	"github.com/thingzio/devpulse/pkg/data/postgres"
	"github.com/thingzio/devpulse/pkg/plan"
	"github.com/thingzio/devpulse/pkg/tenant"
)

const (
	ModeAll        = "all"
	ModeImport     = "import"
	ModeReputation = "reputation"
)

// Run reads IMPORT_MODE env var and dispatches to the appropriate workflow.
// Supported modes: "all" (default), "import" (skip deep reputation), "reputation" (deep reputation only).
func Run(ctx context.Context) error {
	mode := os.Getenv("IMPORT_MODE")
	if mode == "" {
		mode = ModeAll
	}

	slog.Info("import mode selected", "mode", mode)

	switch mode {
	case ModeReputation:
		return RunDeepReputation(ctx)
	case ModeImport, ModeAll:
		return runImport(ctx, mode)
	default:
		return fmt.Errorf("unknown IMPORT_MODE: %q", mode)
	}
}

// runImport iterates active repos via a SKIP LOCKED claim queue and imports each one.
// Multiple concurrent executions safely share work without overlap.
func runImport(ctx context.Context, mode string) error {
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

	if prepErr := tenant.PrepareImportQueue(ctx, db); prepErr != nil {
		return fmt.Errorf("preparing import queue: %w", prepErr)
	}

	slog.Info("import worker starting", "execution", executionID, "mode", mode)

	pool, err := collectTokenPool(ctx, db, ghAppConfig)
	if err != nil {
		slog.Warn("token pool unavailable, falling back to unauthenticated", "error", err)
	} else {
		slog.Info("token pool ready", "tokens", pool.Size())
	}

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

		if importErr := importClaim(ctx, db, store, claim, pool, llmCfg, mode); importErr != nil {
			totalErrors++
			slog.Error("repo import failed",
				"org", claim.Org,
				"repo", claim.Repo,
				"error", importErr)
			if incErr := tenant.IncrementImportErrors(ctx, db, claim.ID, importErr.Error()); incErr != nil {
				slog.Warn("incrementing import errors", "error", incErr)
			}
			continue
		}

		if err := tenant.ResetImportErrors(ctx, db, claim.ID); err != nil {
			slog.Warn("resetting import errors", "error", err)
		}

		if err := tenant.MarkRepoDone(ctx, db, claim.ID); err != nil {
			slog.Warn("marking repo done",
				"org", claim.Org,
				"repo", claim.Repo,
				"error", err)
		}
	}

	postImport(ctx, store, pool)

	slog.Info("import worker complete",
		"repos", totalRepos,
		"errors", totalErrors,
		"duration", time.Since(start).String())

	if totalErrors > 0 && totalErrors == totalRepos {
		return fmt.Errorf("all %d repo imports failed", totalErrors)
	}

	return nil
}

// postImport runs developer enrichment and entity normalization after all repo imports.
func postImport(ctx context.Context, store data.Store, pool *ghutil.TokenPool) {
	if pool != nil {
		if anyToken := pool.Token(); anyToken != "" {
			slog.Info("phase: developer enrichment")
			if enrichErr := store.EnrichDeveloperEntities(ctx, anyToken); enrichErr != nil {
				slog.Warn("enriching developer entities", "error", enrichErr)
			}
		}
	}

	slog.Info("phase: entity normalization")
	if cleanErr := store.CleanEntities(ctx); cleanErr != nil {
		slog.Warn("cleaning entity names", "error", cleanErr)
	}
}

func importClaim(ctx context.Context, db *sql.DB, store data.Store,
	claim *tenant.ClaimedRepo, pool *ghutil.TokenPool,
	llmCfg *data.LLMConfig, mode string) error {
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

	var token string
	if pool != nil {
		token = pool.Token()
	}

	return importRepo(ctx, store, token, claim.Org, claim.Repo, llmCfg, tn.Plan, mode)
}

func importRepo(ctx context.Context, store data.Store, token, org, repo string, llmCfg *data.LLMConfig, planName, mode string) error {
	start := time.Now()
	slog.Info("importing repo", "org", org, "repo", repo)

	var errs int

	retryRL := newRetryRL(ctx)

	slog.Info("phase: metadata", "org", org, "repo", repo)
	if err := retryRL(func() error { return store.ImportRepoMeta(ctx, token, org, repo) }); err != nil {
		slog.Error("importing repo meta", "org", org, "repo", repo, "error", err)
		errs++
	}

	slog.Info("phase: events", "org", org, "repo", repo)
	if err := retryRL(func() error { _, _, err := store.ImportEvents(ctx, token, org, repo, 180); return err }); err != nil {
		slog.Error("importing events", "org", org, "repo", repo, "error", err)
		errs++
	}

	slog.Info("phase: releases", "org", org, "repo", repo)
	if err := retryRL(func() error { return store.ImportReleases(ctx, token, org, repo) }); err != nil {
		slog.Error("importing releases", "org", org, "repo", repo, "error", err)
		errs++
	}

	slog.Info("phase: metrics", "org", org, "repo", repo)
	if err := retryRL(func() error { return store.ImportRepoMetricHistory(ctx, token, org, repo) }); err != nil {
		slog.Error("importing metric history", "org", org, "repo", repo, "error", err)
		errs++
	}

	slog.Info("phase: containers", "org", org, "repo", repo)
	if err := retryRL(func() error { return store.ImportContainerVersions(ctx, token, org, repo) }); err != nil {
		slog.Error("importing container versions", "org", org, "repo", repo, "error", err)
		errs++
	}

	slog.Info("phase: reputation", "org", org, "repo", repo)
	if err := retryRL(func() error { _, err := store.ImportReputation(ctx, &org, &repo); return err }); err != nil {
		slog.Error("importing reputation", "org", org, "repo", repo, "error", err)
		errs++
	}

	limits, _ := plan.Get(planName)

	if token != "" && limits.DeepReputation && mode != ModeImport {
		slog.Info("phase: deep reputation", "org", org, "repo", repo)
		tokenFn := func() string { return token }
		var res *data.DeepReputationResult
		if err := retryRL(func() error {
			var drErr error
			res, drErr = store.ImportDeepReputation(ctx, tokenFn, nil, deepReputationDefaultLimit, 0, &org, &repo)
			return drErr
		}); err != nil {
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

// newRetryRL returns a function that calls fn and, if it fails with a GitHub
// rate limit error, waits for the reset and retries once.
func newRetryRL(ctx context.Context) func(func() error) error {
	return func(fn func() error) error {
		err := fn()
		if err == nil {
			return nil
		}
		if ghutil.WaitForRateReset(ctx, err) {
			if retryErr := fn(); retryErr != nil {
				return fmt.Errorf("retryRL (after wait): %w", retryErr)
			}
			return nil
		}
		return fmt.Errorf("retryRL: %w", err)
	}
}

const (
	insightsPeriodWeeks        = 9
	insightsMinAgeDays         = 7
	insightsEventDeltaPct      = 0.10
	deepReputationDefaultLimit = 1000
)

// checkInsightStaleness determines whether insights should be regenerated
// based on age and event count delta. Returns (shouldRegenerate, reason).
func checkInsightStaleness(generatedAt string, savedEventCount, currentEventCount int) (bool, string) {
	if generatedAt == "" {
		return true, "first generation"
	}

	ts, err := time.Parse("2006-01-02T15:04:05Z", generatedAt)
	if err != nil {
		return true, fmt.Sprintf("invalid generated_at timestamp: %v", err)
	}

	ageDays := int(time.Since(ts).Hours() / 24)
	if ageDays < insightsMinAgeDays {
		return false, fmt.Sprintf("insights are only %d days old (min %d)", ageDays, insightsMinAgeDays)
	}

	if savedEventCount == 0 {
		return true, "no prior event count"
	}

	delta := float64(currentEventCount-savedEventCount) / float64(savedEventCount)
	if delta < 0 {
		delta = -delta
	}
	pct := delta * 100

	if delta < insightsEventDeltaPct {
		return false, fmt.Sprintf("event delta %.1f%% below threshold (%.0f%%)", pct, insightsEventDeltaPct*100)
	}

	return true, fmt.Sprintf("event delta %.1f%% exceeds threshold (%.0f%%)", pct, insightsEventDeltaPct*100)
}

func generateRepoInsights(ctx context.Context, store data.Store, cfg *data.LLMConfig, org, repo string) error {
	insightDays := insightsPeriodWeeks * 7 // 63 days covers the 9-week analysis window

	// Check staleness: age gate + event delta gate
	generatedAt, err := store.GetRepoInsightsGeneratedAt(ctx, org, repo)
	if err != nil {
		return fmt.Errorf("checking insights age: %w", err)
	}

	savedCount, err := store.GetRepoInsightsEventCount(ctx, org, repo)
	if err != nil {
		return fmt.Errorf("checking insights event count: %w", err)
	}

	// Get current event count from summary (cheap DB query)
	summary, err := store.GetInsightsSummary(ctx, &org, &repo, nil, insightDays)
	if err != nil {
		return fmt.Errorf("getting insights summary for staleness check: %w", err)
	}

	shouldRegen, reason := checkInsightStaleness(generatedAt, savedCount, summary.Events)
	if !shouldRegen {
		slog.Debug("skipping insights generation", "org", org, "repo", repo, "reason", reason)
		return nil
	}
	slog.Info("regenerating insights", "org", org, "repo", repo, "reason", reason)

	metrics := data.GatherInsightsMetrics(ctx, store, org, repo, insightDays)

	insights, model, err := data.GenerateInsights(ctx, cfg, metrics, insightsPeriodWeeks)
	if err != nil {
		return fmt.Errorf("generating insights: %w", err)
	}

	ri := &data.RepoInsights{
		Org:         org,
		Repo:        repo,
		Insights:    insights,
		PeriodWeeks: insightsPeriodWeeks,
		Model:       model,
		GeneratedAt: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		EventCount:  summary.Events,
	}

	if err := store.SaveRepoInsights(ctx, org, repo, ri); err != nil {
		return fmt.Errorf("saving insights: %w", err)
	}

	slog.Info("insights generated", "org", org, "repo", repo, "model", model,
		"event_count", summary.Events, "prev_count", savedCount)
	return nil
}
