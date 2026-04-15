package importer

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thingzio/devpulse/pkg/config"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/ghutil"
	"github.com/thingzio/devpulse/pkg/data/postgres"
	"github.com/thingzio/devpulse/pkg/plan"
	"github.com/thingzio/devpulse/pkg/tenant"
)

// Run executes the import pipeline: fetch events, score reputation, generate insights.
func Run(ctx context.Context) error {
	return runImport(ctx)
}

func runImport(ctx context.Context) error {
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

	// Per-task timeout.
	timeoutMin := config.ImportTaskTimeout()
	taskCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMin)*time.Minute)
	defer cancel()

	taskIndex := config.CloudRunTaskIndex()
	taskCount := config.CloudRunTaskCount()
	numWorkers := config.ImportWorkers()
	executionID := config.CloudRunExecution()

	slog.Info("import worker starting",
		"execution", executionID,
		"task_index", taskIndex,
		"task_count", taskCount,
		"workers", numWorkers)

	ghAppConfig, ghAppErr := tenant.LoadGitHubAppConfig()
	if ghAppErr != nil {
		slog.Warn("github app config not available, installation tokens disabled", "error", ghAppErr)
	}

	pool, err := collectTokenPool(taskCtx, db, ghAppConfig)
	if err != nil {
		slog.Warn("token pool unavailable, falling back to unauthenticated", "error", err)
	} else {
		slog.Info("token pool ready", "tokens", pool.Size())
	}

	llmCfg := data.NewLLMConfigFromEnv()

	// Fetch work list — single-repo mode or scheduled.
	rows, singleRepo, err := fetchWorkList(taskCtx, db)
	if err != nil {
		return fmt.Errorf("listing import work: %w", err)
	}
	if singleRepo {
		taskCount = 1
		taskIndex = 0
	}

	workList := BuildWorkList(rows)
	myRepos := ShardRepos(workList, taskCount, taskIndex)

	shardWeight := 0
	for _, rw := range myRepos {
		shardWeight += rw.Weight
	}
	slog.Info("shard assigned",
		"total_repos", len(workList),
		"shard_size", len(myRepos),
		"shard_weight", shardWeight,
		"task_index", taskIndex)

	// Fan out to workers.
	start := time.Now()

	if len(myRepos) == 0 {
		slog.Info("import worker complete",
			"repos", 0,
			"errors", 0,
			"skipped", 0,
			"duration", time.Since(start).String(),
			"duration_sec", time.Since(start).Seconds())
		return nil
	}
	work := make(chan RepoWork)
	var wg sync.WaitGroup
	shardSize := len(myRepos)
	var totalRepos, totalErrors, totalSkipped atomic.Int32

	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rw := range work {
				if taskCtx.Err() != nil {
					return
				}
				importErr, skipped := importRepoWork(taskCtx, db, store, pool, rw, llmCfg)
				switch {
				case skipped:
					totalSkipped.Add(1)
					markImportSuccess(taskCtx, db, rw)
				case importErr != nil:
					totalErrors.Add(1)
					slog.Error("repo import failed",
						"org", rw.Org,
						"repo", rw.Repo,
						"error", importErr)
					if incErr := tenant.IncrementImportErrorsByRepo(taskCtx, db, rw.Org, rw.Repo, importErr.Error()); incErr != nil {
						slog.Warn("incrementing import errors", "error", incErr)
					}
				default:
					markImportSuccess(taskCtx, db, rw)
				}
				done := int(totalRepos.Add(1))
				slog.Info("shard progress",
					"completed", done,
					"total", shardSize,
					"errors", int(totalErrors.Load()),
					"skipped", int(totalSkipped.Load()))
			}
		}()
	}

	for _, rw := range myRepos {
		if taskCtx.Err() != nil {
			break
		}
		work <- rw
	}
	close(work)
	wg.Wait()

	postImport(taskCtx, store, pool)

	repos := int(totalRepos.Load())
	errs := int(totalErrors.Load())
	skipped := int(totalSkipped.Load())
	elapsed := time.Since(start)
	slog.Info("import worker complete",
		"repos", repos,
		"errors", errs,
		"skipped", skipped,
		"duration", elapsed.String(),
		"duration_sec", elapsed.Seconds())

	if errs > 0 && errs == repos {
		return fmt.Errorf("all %d repo imports failed", errs)
	}
	return nil
}

// markImportSuccess marks a repo import as done and resets error counters.
func markImportSuccess(ctx context.Context, db *sql.DB, rw RepoWork) {
	if err := tenant.MarkImportDoneByRepo(ctx, db, rw.Org, rw.Repo); err != nil {
		slog.Warn("marking import done", "org", rw.Org, "repo", rw.Repo, "error", err)
	}
	for _, tr := range rw.Tenants {
		if err := tenant.ResetImportErrors(ctx, db, tr.TenantRepoID); err != nil {
			slog.Warn("resetting import errors", "error", err)
		}
	}
}

// fetchWorkList returns import work rows. In single-repo mode (IMPORT_ORG + IMPORT_REPO set),
// returns only that repo's rows and singleRepo=true. Otherwise returns the full scheduled work list.
func fetchWorkList(ctx context.Context, db *sql.DB) ([]tenant.ImportWorkRow, bool, error) {
	org := config.ImportOrg()
	repo := config.ImportRepo()
	if org != "" && repo != "" {
		slog.Info("single-repo import mode", "org", org, "repo", repo)
		rows, err := tenant.ListImportWorkForRepo(ctx, db, org, repo)
		return rows, true, err
	}
	rows, err := tenant.ListImportWork(ctx, db, config.ImportAdoptTimeout())
	return rows, false, err
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

// importRepoWork imports shared repo data and handles per-tenant event limits.
func importRepoWork(ctx context.Context, db *sql.DB, store data.Store,
	pool *ghutil.TokenPool, rw RepoWork, llmCfg *data.LLMConfig) (error, bool) {
	bestPlan := bestPlanForRepo(rw.Tenants)

	if limited, err := allTenantsAtLimit(ctx, db, rw.Tenants); err != nil {
		slog.Warn("checking event limits", "org", rw.Org, "repo", rw.Repo, "error", err)
	} else if limited {
		slog.Warn("all tenants at weekly event limit, skipping",
			"org", rw.Org, "repo", rw.Repo)
		return nil, false
	}

	slog.Info("importing repo",
		"org", rw.Org,
		"repo", rw.Repo,
		"plan", bestPlan,
		"tenants", len(rw.Tenants))

	importErr, skipped := importRepo(ctx, store, pool, rw.Org, rw.Repo, llmCfg, bestPlan)
	if skipped {
		return nil, true
	}
	return importErr, false
}

// bestPlanForRepo returns the highest-tier plan among all tenants tracking a repo.
// Uses MaxRepos as proxy for tier (0=unlimited=enterprise, highest tier).
func bestPlanForRepo(tenants []TenantRef) string {
	best := ""
	bestLevel := -1
	for _, t := range tenants {
		limits, _ := plan.Get(t.Plan)
		if limits.MaxRepos == 0 {
			return t.Plan // unlimited = enterprise
		}
		if limits.MaxRepos > bestLevel {
			bestLevel = limits.MaxRepos
			best = t.Plan
		}
	}
	if best == "" && len(tenants) > 0 {
		best = tenants[0].Plan
	}
	return best
}

// allTenantsAtLimit returns true if every tenant tracking this repo has hit
// their weekly event limit. Returns false if at least one has room or is unlimited.
func allTenantsAtLimit(ctx context.Context, db *sql.DB, tenants []TenantRef) (bool, error) {
	for _, t := range tenants {
		tn, err := tenant.GetTenantByID(ctx, db, t.TenantID)
		if err != nil {
			return false, fmt.Errorf("getting tenant %s: %w", t.TenantID, err)
		}
		if tn.MaxEventsPerWeek == 0 {
			return false, nil // unlimited
		}
		weeklyEvents, err := tenant.GetWeeklyEventCount(ctx, db, t.TenantID)
		if err != nil {
			return false, fmt.Errorf("getting weekly events for %s: %w", t.TenantID, err)
		}
		if weeklyEvents < tn.MaxEventsPerWeek {
			return false, nil
		}
	}
	return true, nil
}

// importMetaAndCheckSkip runs the metadata phase and checks whether the repo
// can be skipped because pushed_at has not advanced past our last event.
// Returns (metaOK, skip): metaOK=false means metadata import failed, skip=true
// means the repo is unchanged and remaining phases should be skipped.
func importMetaAndCheckSkip(ctx context.Context, store data.Store, retryRL func(func() error) error, token, org, repo string) (bool, bool) {
	var pushedAt time.Time
	if err := retryRL(func() error {
		var metaErr error
		pushedAt, metaErr = store.ImportRepoMeta(ctx, token, org, repo)
		return metaErr
	}); err != nil {
		slog.Error("importing repo meta", "org", org, "repo", repo, "error", err)
		return false, false
	}

	if pushedAt.IsZero() {
		return true, false
	}

	hasState, err := store.HasState(ctx, org, repo)
	if err != nil {
		slog.Warn("checking state", "org", org, "repo", repo, "error", err)
		return true, false
	}
	if !hasState {
		return true, false // no state rows — fresh or hard-reset, always import
	}

	maxEventTime, err := store.GetMaxEventTime(ctx, org, repo)
	if err != nil {
		slog.Warn("checking max event time", "org", org, "repo", repo, "error", err)
		return true, false
	}

	if shouldSkipUnchangedRepo(pushedAt, maxEventTime) {
		slog.Info("repo unchanged, skipping",
			"org", org,
			"repo", repo,
			"pushed_at", pushedAt.Format(time.RFC3339),
			"last_event", maxEventTime.Format("2006-01-02"))
		return true, true
	}

	return true, false
}

func importRepo(ctx context.Context, store data.Store, pool *ghutil.TokenPool, org, repo string, llmCfg *data.LLMConfig, planName string) (error, bool) {
	start := time.Now()

	tokenForPhase := func() string {
		if pool == nil {
			return ""
		}
		return pool.Token()
	}

	var errs int

	retryRL := newRetryRL(ctx)

	token := tokenForPhase()

	// Phase 1: Metadata + skip check (unchanged)
	slog.Info("phase: metadata", "org", org, "repo", repo)
	metaOK, skip := importMetaAndCheckSkip(ctx, store, retryRL, token, org, repo)
	if !metaOK {
		errs++
	}
	if skip {
		return nil, true
	}

	// Phase 2: Fresh pass — recent events
	freshDays := config.ImportFreshDays()
	freshStart := time.Now().AddDate(0, 0, -freshDays).UTC()
	freshEnd := time.Now().UTC()

	token = tokenForPhase()
	slog.Info("phase: events (fresh)", "org", org, "repo", repo,
		"window_start", freshStart.Format("2006-01-02"),
		"window_end", freshEnd.Format("2006-01-02"))

	var eventTokenFn data.TokenFunc
	var eventExhaustFn data.ExhaustFunc
	if pool != nil {
		eventTokenFn = func() string { return pool.Token() }
		eventExhaustFn = func(t string) { pool.Exhaust(t) }
	} else {
		eventTokenFn = func() string { return token }
	}

	if err := retryRL(func() error {
		_, _, importErr := store.ImportEvents(ctx, eventTokenFn, eventExhaustFn, org, repo, freshStart, freshEnd)
		if importErr != nil {
			return fmt.Errorf("importing events (fresh): %w", importErr)
		}
		return nil
	}); err != nil {
		slog.Error("importing events (fresh)", "org", org, "repo", repo, "error", err)
		errs++
	}

	// Set backfill_until on first import (when NULL).
	backfillUntil, err := store.GetBackfillUntil(ctx, org, repo)
	if err != nil {
		slog.Warn("checking backfill state", "org", org, "repo", repo, "error", err)
	}
	if backfillUntil == nil {
		if err := store.SaveBackfillUntil(ctx, org, repo, freshStart); err != nil {
			slog.Warn("setting initial backfill_until", "org", org, "repo", repo, "error", err)
		}
		t := freshStart
		backfillUntil = &t
	}

	// Phases 3-8: Non-event phases run after fresh pass so the dashboard
	// has complete data for the recent window before backfill starts.

	token = tokenForPhase()
	slog.Info("phase: releases", "org", org, "repo", repo)
	if err := retryRL(func() error { return store.ImportReleases(ctx, token, org, repo) }); err != nil {
		slog.Error("importing releases", "org", org, "repo", repo, "error", err)
		errs++
	}

	token = tokenForPhase()
	slog.Info("phase: metrics", "org", org, "repo", repo)
	if err := retryRL(func() error { return store.ImportRepoMetricHistory(ctx, token, org, repo) }); err != nil {
		slog.Error("importing metric history", "org", org, "repo", repo, "error", err)
		errs++
	}

	token = tokenForPhase()
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

	token = tokenForPhase()
	limits, _ := plan.Get(planName)

	if token != "" && limits.DeepReputation {
		slog.Info("phase: deep reputation", "org", org, "repo", repo)
		tokenFn := func() string { return pool.Token() }
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

	if llmCfg != nil && limits.AILevel > 0 {
		slog.Info("phase: insights", "org", org, "repo", repo)
		if err := generateRepoInsights(ctx, store, llmCfg, org, repo); err != nil {
			slog.Error("generating insights", "org", org, "repo", repo, "error", err)
			errs++
		}
	} else if llmCfg != nil {
		slog.Debug("skipping insights, not included in plan", "org", org, "repo", repo, "plan", planName)
	}

	// Phase 9: Backfill pass — extend historical coverage
	targetDate := time.Now().AddDate(0, 0, -data.EventAgeDaysDefault).UTC()
	chunkDays := config.ImportBackfillChunkDays()

	if backfillUntil != nil && backfillUntil.After(targetDate) {
		chunkStart := backfillUntil.AddDate(0, 0, -chunkDays).UTC()
		if chunkStart.Before(targetDate) {
			chunkStart = targetDate
		}
		chunkEnd := *backfillUntil

		slog.Info("phase: events (backfill)", "org", org, "repo", repo,
			"chunk_start", chunkStart.Format("2006-01-02"),
			"chunk_end", chunkEnd.Format("2006-01-02"),
			"coverage_days", int(time.Since(chunkEnd).Hours()/24),
			"target_days", data.EventAgeDaysDefault)

		token = tokenForPhase()
		if pool != nil {
			eventTokenFn = func() string { return pool.Token() }
		} else {
			eventTokenFn = func() string { return token }
		}

		if err := retryRL(func() error {
			_, _, importErr := store.ImportEvents(ctx, eventTokenFn, eventExhaustFn, org, repo, chunkStart, chunkEnd)
			if importErr != nil {
				return fmt.Errorf("importing events (backfill): %w", importErr)
			}
			return nil
		}); err != nil {
			slog.Error("importing events (backfill)", "org", org, "repo", repo, "error", err)
			errs++
		} else {
			// Advance backfill_until on success
			if err := store.SaveBackfillUntil(ctx, org, repo, chunkStart); err != nil {
				slog.Warn("advancing backfill_until", "org", org, "repo", repo, "error", err)
			}
			coverageDays := int(time.Since(chunkStart).Hours() / 24)
			slog.Info("backfill pass complete", "org", org, "repo", repo,
				"chunk_start", chunkStart.Format("2006-01-02"),
				"chunk_end", chunkEnd.Format("2006-01-02"),
				"coverage_days", coverageDays,
				"target_days", data.EventAgeDaysDefault)
			if coverageDays >= data.EventAgeDaysDefault {
				slog.Info("backfill complete", "org", org, "repo", repo,
					"coverage_days", coverageDays)
			}
		}
	} else {
		slog.Debug("backfill complete, skipping", "org", org, "repo", repo)
	}

	slog.Info("repo import complete", "org", org, "repo", repo, "errors", errs, "duration", time.Since(start).String())

	if errs > 0 {
		return fmt.Errorf("import completed with %d errors for %s/%s", errs, org, repo), false
	}
	return nil, false
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
