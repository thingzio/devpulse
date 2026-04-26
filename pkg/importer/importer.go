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
	"github.com/thingzio/devpulse/pkg/digest"
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

	// Sample token quotas at the start of each import run.
	sampleTokenQuotas(taskCtx, db, ghAppConfig)

	llmCfg := data.NewLLMConfigFromEnv()

	// Fetch work list — single-repo mode or scheduled.
	rows, singleRepo, err := fetchWorkList(taskCtx, db)
	if err != nil {
		return fmt.Errorf("listing import work: %w", err)
	}
	if singleRepo {
		// Only task 0 processes a single-repo override; sibling Cloud Run
		// tasks would race on every DB write for the same repo.
		if taskIndex != 0 {
			slog.Info("single-repo mode: non-zero task index exiting",
				"task_index", taskIndex, "task_count", taskCount)
			return nil
		}
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

	return dispatchAndFinalize(taskCtx, db, store, pool, llmCfg, myRepos, numWorkers, taskIndex)
}

// dispatchAndFinalize fans out repo imports to workers, runs post-import
// phases, and reports the final status.
func dispatchAndFinalize(
	taskCtx context.Context, db *sql.DB, store data.Store,
	pool *ghutil.TokenPool, llmCfg *data.LLMConfig,
	myRepos []RepoWork, numWorkers, taskIndex int,
) error {
	start := time.Now()
	if len(myRepos) == 0 {
		logImportComplete(0, 0, 0, start)
		return nil
	}

	repos, errs, skippedN := runWorkers(taskCtx, db, store, pool, llmCfg, myRepos, numWorkers)

	if taskCtx.Err() != nil {
		slog.Warn("import interrupted by timeout, skipping post-import phases",
			"completed", repos,
			"total", len(myRepos))
	} else {
		postImport(taskCtx, store, pool)
	}

	if taskIndex == 0 && taskCtx.Err() == nil {
		sendDigests(taskCtx, db)
	}

	logImportComplete(repos, errs, skippedN, start)

	if errs > 0 && errs == repos {
		return fmt.Errorf("all %d repo imports failed", errs)
	}
	if taskCtx.Err() != nil {
		return fmt.Errorf("import interrupted: %w", taskCtx.Err())
	}
	return nil
}

// runWorkers dispatches repo import work to a pool of goroutines and waits
// for all to complete. Returns (total, errors, skipped).
func runWorkers(
	taskCtx context.Context, db *sql.DB, store data.Store,
	pool *ghutil.TokenPool, llmCfg *data.LLMConfig,
	myRepos []RepoWork, numWorkers int,
) (int, int, int) {
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
				skipped, importErr := importRepoWork(taskCtx, db, store, pool, rw, llmCfg)
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

sendLoop:
	for _, rw := range myRepos {
		select {
		case work <- rw:
		case <-taskCtx.Done():
			break sendLoop
		}
	}
	close(work)
	wg.Wait()

	return int(totalRepos.Load()), int(totalErrors.Load()), int(totalSkipped.Load())
}

func logImportComplete(repos, errs, skipped int, start time.Time) {
	slog.Info("import worker complete",
		"repos", repos,
		"errors", errs,
		"skipped", skipped,
		"duration", time.Since(start).String(),
		"duration_sec", time.Since(start).Seconds())
}

// markImportSuccess resets error counters after a successful import.
func markImportSuccess(ctx context.Context, db *sql.DB, rw RepoWork) {
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
	rows, err := tenant.ListImportWork(ctx, db)
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

// sendDigests runs the weekly digest email sender.
func sendDigests(ctx context.Context, db *sql.DB) {
	cfg := digest.NewConfigFromEnv()
	if cfg == nil {
		slog.Debug("digest: SEND_API_KEY or BASE_URL not set, skipping")
		return
	}

	slog.Info("phase: weekly digest")
	if err := digest.Run(ctx, db, cfg); err != nil {
		slog.Error("digest send failed", "error", err)
	}
}

// importRepoWork imports shared repo data and handles per-tenant event limits.
func importRepoWork(ctx context.Context, db *sql.DB, store data.Store,
	pool *ghutil.TokenPool, rw RepoWork, llmCfg *data.LLMConfig) (bool, error) {
	bestPlan := bestPlanForRepo(rw.Tenants)

	if limited, err := allTenantsAtLimit(ctx, db, rw.Tenants); err != nil {
		slog.Warn("checking event limits", "org", rw.Org, "repo", rw.Repo, "error", err)
	} else if limited {
		slog.Warn("all tenants at weekly event limit, skipping",
			"org", rw.Org, "repo", rw.Repo)
		return false, nil
	}

	slog.Info("importing repo",
		"org", rw.Org,
		"repo", rw.Repo,
		"plan", bestPlan,
		"tenants", len(rw.Tenants))

	// recheckLimits narrows the TOCTOU window between the initial gate and
	// the expensive event-import phases. Concurrent imports of shared repos
	// can fill other tenants' weekly buckets while this worker holds the
	// repo, so we re-evaluate just before each event-fetching phase. The
	// ultimate per-tenant cap remains best-effort since events are stored
	// in a shared table — but this caps wasted GitHub API quota.
	recheckLimits := func() bool {
		limited, err := allTenantsAtLimit(ctx, db, rw.Tenants)
		if err != nil {
			slog.Warn("rechecking event limits", "org", rw.Org, "repo", rw.Repo, "error", err)
			return false
		}
		if limited {
			slog.Warn("all tenants reached weekly limit mid-import, aborting remaining phases",
				"org", rw.Org, "repo", rw.Repo)
		}
		return limited
	}

	skipped, importErr := importRepo(ctx, store, pool, rw.Org, rw.Repo, llmCfg, bestPlan, recheckLimits)
	if skipped {
		return true, nil
	}
	return false, importErr
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
		// Don't skip if backfill hasn't reached target depth.
		targetDate := time.Now().AddDate(0, 0, -data.EventAgeDaysDefault).UTC()
		backfillUntil, bErr := store.GetBackfillUntil(ctx, org, repo)
		if bErr == nil && backfillUntil != nil && backfillUntil.After(targetDate) {
			slog.Info("repo unchanged but backfill pending, continuing",
				"org", org, "repo", repo,
				"backfill_days", int(time.Since(*backfillUntil).Hours()/24),
				"target_days", data.EventAgeDaysDefault)
			return true, false
		}

		slog.Info("repo unchanged, skipping",
			"org", org,
			"repo", repo,
			"pushed_at", pushedAt.Format(time.RFC3339),
			"last_event", maxEventTime.Format("2006-01-02"))
		return true, true
	}

	return true, false
}

func importRepo(ctx context.Context, store data.Store, pool *ghutil.TokenPool, org, repo string, llmCfg *data.LLMConfig, planName string, atLimit func() bool) (bool, error) {
	start := time.Now()

	tokenForPhase := func() string {
		if pool == nil {
			return ""
		}
		return pool.Token()
	}

	// phaseGate aborts a phase early when the parent context has been
	// canceled (worker shutdown). Returning ctx.Err lets the caller decide
	// whether to count it as a failure or a graceful stop.
	phaseGate := func() error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("import canceled before next phase: %w", err)
		}
		return nil
	}

	var errs int

	retryRL := newRetryRL(ctx)

	token := tokenForPhase()

	// Phase 1: Metadata + skip check (unchanged)
	if err := phaseGate(); err != nil {
		return false, err
	}
	slog.Info("phase: metadata", "org", org, "repo", repo)
	metaOK, skip := importMetaAndCheckSkip(ctx, store, retryRL, token, org, repo)
	if !metaOK {
		errs++
	}
	if skip {
		return true, nil
	}

	// Phase 2: Fresh pass — recent events
	freshDays := config.ImportFreshDays()
	freshStart := time.Now().AddDate(0, 0, -freshDays).UTC()
	freshEnd := time.Now().UTC()

	token = tokenForPhase()
	slog.Info("phase: events (fresh)", "org", org, "repo", repo,
		"window_start", freshStart.Format("2006-01-02"),
		"window_end", freshEnd.Format("2006-01-02"))

	// Recheck the per-tenant limit immediately before the expensive event
	// fetch — concurrent imports may have filled tenant buckets since the
	// initial gate.
	if atLimit != nil && atLimit() {
		return false, nil
	}
	if err := phaseGate(); err != nil {
		return false, err
	}

	eventTokenFn, eventExhaustFn, backfillUntil, freshErrs := runFreshPass(
		ctx, store, retryRL, pool, token, org, repo, freshStart, freshEnd)
	errs += freshErrs

	// Phases 3-8: Non-event phases run after fresh pass so the dashboard
	// has complete data for the recent window before backfill starts.
	if err := phaseGate(); err != nil {
		return false, err
	}
	errs += runEnrichmentPhases(ctx, store, retryRL, tokenForPhase, org, repo, planName, llmCfg)

	// Phase 9: Backfill pass — extend historical coverage. Recheck limits
	// before this potentially long-running phase since other workers may
	// have consumed the shared event budget.
	if atLimit != nil && atLimit() {
		slog.Info("repo import complete (backfill skipped, all tenants at limit)",
			"org", org, "repo", repo, "errors", errs, "duration", time.Since(start).String())
		if errs > 0 {
			return false, fmt.Errorf("import completed with %d errors for %s/%s", errs, org, repo)
		}
		return false, nil
	}
	if err := phaseGate(); err != nil {
		return false, err
	}
	if backfillErr := runBackfillPass(ctx, store, retryRL, eventTokenFn, eventExhaustFn, pool, tokenForPhase, org, repo, backfillUntil); backfillErr != nil {
		slog.Error("importing events (backfill)", "org", org, "repo", repo, "error", backfillErr)
		errs++
	}

	slog.Info("repo import complete", "org", org, "repo", repo, "errors", errs, "duration", time.Since(start).String())

	if errs > 0 {
		return false, fmt.Errorf("import completed with %d errors for %s/%s", errs, org, repo)
	}
	return false, nil
}

// runFreshPass executes the recent-events import (Phase 2) and ensures
// backfill_until is initialized for first imports. The token closures it
// builds are shared with the backfill pass so token rotation/exhaustion
// state is preserved across both event phases.
func runFreshPass(
	ctx context.Context, store data.Store,
	retryRL func(func() error) error,
	pool *ghutil.TokenPool,
	fallbackToken, org, repo string,
	freshStart, freshEnd time.Time,
) (data.TokenFunc, data.ExhaustFunc, *time.Time, int) {
	var eventTokenFn data.TokenFunc
	var eventExhaustFn data.ExhaustFunc
	if pool != nil {
		eventTokenFn = func() string { return pool.Token() }
		eventExhaustFn = func(t string) { pool.Exhaust(t) }
	} else {
		eventTokenFn = func() string { return fallbackToken }
	}

	var errs int
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
		if saveErr := store.SaveBackfillUntil(ctx, org, repo, freshStart); saveErr != nil {
			slog.Warn("setting initial backfill_until", "org", org, "repo", repo, "error", saveErr)
		}
		t := freshStart
		backfillUntil = &t
	}

	return eventTokenFn, eventExhaustFn, backfillUntil, errs
}

// runEnrichmentPhases runs the non-event import phases: releases, metrics,
// containers, reputation, and insights. Returns the number of phases that
// failed. Deep reputation runs separately after the backfill pass.
func runEnrichmentPhases(
	ctx context.Context, store data.Store,
	retryRL func(func() error) error,
	tokenForPhase func() string,
	org, repo, planName string, llmCfg *data.LLMConfig,
) int {
	var errs int

	// canceled returns true once the parent ctx has been canceled. Each
	// enrichment phase is independent, so we exit cleanly between phases
	// rather than letting a long retry loop run after shutdown was signaled.
	canceled := func() bool { return ctx.Err() != nil }

	if canceled() {
		return errs
	}
	token := tokenForPhase()
	slog.Info("phase: releases", "org", org, "repo", repo)
	if err := retryRL(func() error { return store.ImportReleases(ctx, token, org, repo) }); err != nil {
		slog.Error("importing releases", "org", org, "repo", repo, "error", err)
		errs++
	}

	if canceled() {
		return errs
	}
	token = tokenForPhase()
	slog.Info("phase: metrics", "org", org, "repo", repo)
	if err := retryRL(func() error { return store.ImportRepoMetricHistory(ctx, token, org, repo) }); err != nil {
		slog.Error("importing metric history", "org", org, "repo", repo, "error", err)
		errs++
	}

	if canceled() {
		return errs
	}
	token = tokenForPhase()
	slog.Info("phase: containers", "org", org, "repo", repo)
	if err := retryRL(func() error { return store.ImportContainerVersions(ctx, token, org, repo) }); err != nil {
		slog.Error("importing container versions", "org", org, "repo", repo, "error", err)
		errs++
	}

	if canceled() {
		return errs
	}
	slog.Info("phase: reputation", "org", org, "repo", repo)
	if err := retryRL(func() error { _, err := store.ImportReputation(ctx, &org, &repo); return err }); err != nil {
		slog.Error("importing reputation", "org", org, "repo", repo, "error", err)
		errs++
	}

	limits, _ := plan.Get(planName)

	if canceled() {
		return errs
	}
	if llmCfg != nil && limits.AILevel > 0 {
		slog.Info("phase: insights", "org", org, "repo", repo)
		if err := generateRepoInsights(ctx, store, llmCfg, org, repo); err != nil {
			slog.Error("generating insights", "org", org, "repo", repo, "error", err)
			errs++
		}
	} else if llmCfg != nil {
		slog.Info("skipping insights, not included in plan", "org", org, "repo", repo, "plan", planName)
	}

	return errs
}

// runBackfillPass extends historical event coverage by one chunk. Returns nil
// if backfill is already at target depth or if the chunk completes successfully.
func runBackfillPass(
	ctx context.Context, store data.Store,
	retryRL func(func() error) error,
	eventTokenFn data.TokenFunc, eventExhaustFn data.ExhaustFunc,
	pool *ghutil.TokenPool, tokenForPhase func() string,
	org, repo string, backfillUntil *time.Time,
) error {
	targetDate := time.Now().AddDate(0, 0, -data.EventAgeDaysDefault).UTC()
	chunkDays := config.ImportBackfillChunkDays()

	if backfillUntil == nil || !backfillUntil.After(targetDate) {
		slog.Debug("backfill complete, skipping", "org", org, "repo", repo)
		return nil
	}

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

	token := tokenForPhase()
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
		return err
	}

	// Advance backfill_until on success.
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
	return nil
}

// retry tunables. transientRetryAttempts caps total transient retries per
// call so a persistent upstream failure does not block import progress.
const (
	transientRetryAttempts = 4
	transientRetryBase     = 500 * time.Millisecond
	transientRetryMax      = 30 * time.Second
)

// newRetryRL returns a function that wraps fn with rate-limit and transient
// failure handling:
//
//   - On GitHub primary/secondary rate limit: wait for reset (one shot).
//   - On transient error (5xx, EOF, network timeout): exponential backoff
//     with jitter, up to transientRetryAttempts retries.
//   - On context cancellation: stop immediately and propagate the error.
//   - On non-retriable error: return wrapped immediately.
//
// The two strategies are layered: if a call fails with a rate-limit error
// after a transient retry (rare), the rate-limit branch will run on the next
// iteration. This keeps each branch's logic local and easy to reason about.
func newRetryRL(ctx context.Context) func(func() error) error {
	return func(fn func() error) error {
		var lastErr error
		for attempt := 0; attempt <= transientRetryAttempts; attempt++ {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("retryRL: %w", err)
			}

			err := fn()
			if err == nil {
				return nil
			}
			lastErr = err

			// Rate limit: wait for reset and retry without consuming an
			// attempt slot — this is a deterministic wait, not a backoff.
			if ghutil.WaitForRateReset(ctx, err) {
				continue
			}

			if !ghutil.IsTransient(err) {
				return fmt.Errorf("retryRL: %w", err)
			}

			if attempt == transientRetryAttempts {
				break
			}

			delay := ghutil.BackoffDuration(attempt, transientRetryBase, transientRetryMax)
			slog.Warn("transient error, backing off",
				"attempt", attempt+1,
				"max_attempts", transientRetryAttempts,
				"delay_ms", delay.Milliseconds(),
				"error", err)
			if sleepErr := ghutil.SleepWithContext(ctx, delay); sleepErr != nil {
				return fmt.Errorf("retryRL: %w", sleepErr)
			}
		}
		return fmt.Errorf("retryRL (exhausted): %w", lastErr)
	}
}

const (
	insightsPeriodWeeks   = 9
	insightsMinAgeDays    = 7
	insightsEventDeltaPct = 0.10
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
		slog.Info("skipping insights generation", "org", org, "repo", repo, "reason", reason)
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
