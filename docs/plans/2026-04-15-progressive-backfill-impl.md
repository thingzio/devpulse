# Progressive Backfill Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the single-pass event import with a two-pass system (fresh + backfill) and sub-batched DB writes to handle large repos reliably within the 55-minute Cloud Run timeout.

**Architecture:** Fresh pass fetches 3 weeks of recent events, then all non-event phases run (releases, metrics, reputation, insights). Backfill pass runs last, marching backward in 1-week chunks until 90-day coverage. DB writes split from 500-event transactions into configurable sub-batches (default 100).

**Tech Stack:** Go, PostgreSQL, `github.com/google/go-github/v83`, `github.com/stretchr/testify`

**Design doc:** `docs/plans/2026-04-15-progressive-backfill-design.md`

---

### Task 1: Add configuration functions for new env vars

**Files:**
- Modify: `pkg/config/env.go:90-100`
- Test: `pkg/config/env_test.go` (if exists, otherwise verify via `make test`)

**Step 1: Add three new config functions**

Add after `BackfillMaxDays()` (line 60) in `pkg/config/env.go`:

```go
// ImportDBBatchSize returns the number of events per DB transaction during flush.
// Override: IMPORT_DB_BATCH_SIZE (default 100).
func ImportDBBatchSize() int { return GetEnvAsInt("IMPORT_DB_BATCH_SIZE", 100) }

// ImportFreshDays returns the fresh pass lookback window in days.
// Override: IMPORT_FRESH_DAYS (default 21).
func ImportFreshDays() int { return GetEnvAsInt("IMPORT_FRESH_DAYS", 21) }

// ImportBackfillChunkDays returns the backfill chunk size in days.
// Override: IMPORT_BACKFILL_CHUNK_DAYS (default 7).
func ImportBackfillChunkDays() int { return GetEnvAsInt("IMPORT_BACKFILL_CHUNK_DAYS", 7) }
```

**Step 2: Run tests**

Run: `make test`
Expected: PASS (no behavior change yet, just new functions)

**Step 3: Commit**

```bash
git add pkg/config/env.go
git commit -S -m "feat: add config for progressive backfill tuning knobs"
```

---

### Task 2: Add `backfill_until` column to state table and migration

**Files:**
- Modify: `pkg/data/postgres/sql/migrations/001_initial.sql:111-118`
- Modify: `pkg/data/types.go:31-34`
- Modify: `pkg/data/postgres/state.go` (SQL constants, GetState, SaveState)
- Modify: `pkg/data/store.go:18-24` (StateStore interface)

**Step 1: Add migration**

Create `pkg/data/postgres/sql/migrations/013_backfill_until.sql`:

```sql
-- Progressive backfill: track how far back each event type has been backfilled.
ALTER TABLE devpulse_state ADD COLUMN IF NOT EXISTS backfill_until INTEGER;
```

The column is nullable INTEGER (Unix timestamp), matching the `since` column convention. NULL means no backfill started.

**Step 2: Add `BackfillUntil` to the State struct**

In `pkg/data/types.go`, modify the State struct (lines 31-34):

```go
type State struct {
	Since        time.Time  `json:"since" yaml:"since"`
	Page         int        `json:"page" yaml:"page"`
	BackfillUntil *time.Time `json:"backfill_until,omitempty" yaml:"backfill_until,omitempty"`
}
```

Use `*time.Time` — nil means "not started yet" (maps to SQL NULL).

**Step 3: Update SQL constants in `pkg/data/postgres/state.go`**

Replace the existing constants (lines 19-25):

```go
const (
	insertStateSQL = `INSERT INTO devpulse_state (query, org, repo, page, since, backfill_until)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT(query, org, repo) DO UPDATE SET page = $7, since = $8, backfill_until = $9
	`

	selectStateSQL = `SELECT since, page, backfill_until FROM devpulse_state WHERE query = $1 AND org = $2 AND repo = $3`
)
```

**Step 4: Update `GetState` to scan `backfill_until`**

In `pkg/data/postgres/state.go`, modify `GetState` (lines 27-56). The scan changes from:

```go
err = row.Scan(&since, &st.Page)
```

to:

```go
var backfillUntil sql.NullInt64
err = row.Scan(&since, &st.Page, &backfillUntil)
```

And after setting `st.Since`, add:

```go
if backfillUntil.Valid {
	t := time.Unix(backfillUntil.Int64, 0).UTC()
	st.BackfillUntil = &t
}
```

**Step 5: Update `SaveState` to persist `backfill_until`**

In `pkg/data/postgres/state.go`, modify `SaveState` (lines 58-83). The exec call changes from 7 params to 9:

```go
var backfillUnix sql.NullInt64
if state.BackfillUntil != nil {
	backfillUnix = sql.NullInt64{Int64: state.BackfillUntil.Unix(), Valid: true}
}

if _, err = stateStmt.ExecContext(ctx, query, org, repo, state.Page, since, backfillUnix,
	state.Page, since, backfillUnix); err != nil {
	return fmt.Errorf("failed to insert state: %w", err)
}
```

**Step 6: Update `StateStore` interface**

In `pkg/data/store.go`, add a new method to `StateStore` (lines 18-24):

```go
type StateStore interface {
	GetState(ctx context.Context, query, org, repo string, min time.Time) (*State, error)
	HasState(ctx context.Context, org, repo string) (bool, error)
	SaveState(ctx context.Context, query, org, repo string, state *State) error
	ClearState(ctx context.Context, org, repo string) error
	GetDataState(ctx context.Context) (map[string]int64, error)
	GetBackfillUntil(ctx context.Context, org, repo string) (*time.Time, error)
	SaveBackfillUntil(ctx context.Context, org, repo string, until time.Time) error
}
```

**Step 7: Implement `GetBackfillUntil` and `SaveBackfillUntil`**

Add to `pkg/data/postgres/state.go`:

```go
// GetBackfillUntil returns the oldest backfill boundary across all event types
// for a repo. Returns nil if no backfill has started.
func (s *Store) GetBackfillUntil(ctx context.Context, org, repo string) (*time.Time, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	var backfillUnix sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MIN(backfill_until) FROM devpulse_state
		 WHERE org = $1 AND repo = $2 AND backfill_until IS NOT NULL`,
		org, repo).Scan(&backfillUnix)
	if err != nil {
		return nil, fmt.Errorf("querying backfill_until for %s/%s: %w", org, repo, err)
	}

	if !backfillUnix.Valid {
		return nil, nil
	}

	t := time.Unix(backfillUnix.Int64, 0).UTC()
	return &t, nil
}

// SaveBackfillUntil updates backfill_until for all event types of a repo.
func (s *Store) SaveBackfillUntil(ctx context.Context, org, repo string, until time.Time) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	unixTs := until.Unix()
	_, err := s.db.ExecContext(ctx,
		`UPDATE devpulse_state SET backfill_until = $1 WHERE org = $2 AND repo = $3`,
		unixTs, org, repo)
	if err != nil {
		return fmt.Errorf("updating backfill_until for %s/%s: %w", org, repo, err)
	}

	return nil
}
```

**Step 8: Run tests**

Run: `make test`
Expected: PASS. Existing state tests use `setupTestDB(t)` which runs migrations, so the new column is present.

**Step 9: Update existing state tests and add new ones**

In `pkg/data/postgres/state_test.go`, update `TestSaveAndGetState` to verify `BackfillUntil` is nil by default:

```go
assert.Nil(t, got.BackfillUntil)
```

Add new tests:

```go
func TestSaveAndGetState_WithBackfillUntil(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	min := time.Now().AddDate(0, -6, 0).UTC()
	since := time.Now().AddDate(0, -3, 0).UTC()
	backfill := time.Now().AddDate(0, 0, -21).UTC()

	s := &data.State{Page: 1, Since: since, BackfillUntil: &backfill}
	require.NoError(t, store.SaveState(ctx, "pr", "testorg", "testrepo", s))

	got, err := store.GetState(ctx, "pr", "testorg", "testrepo", min)
	require.NoError(t, err)
	require.NotNil(t, got.BackfillUntil)
	assert.WithinDuration(t, backfill, *got.BackfillUntil, time.Second)
}

func TestGetBackfillUntil_NoState(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	got, err := store.GetBackfillUntil(ctx, "testorg", "testrepo")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetBackfillUntil_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetBackfillUntil(ctx, "org", "repo")
	assert.Error(t, err)
}

func TestSaveBackfillUntil(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	since := time.Now().AddDate(0, -3, 0).UTC()

	// Save state for two event types first
	s := &data.State{Page: 1, Since: since}
	require.NoError(t, store.SaveState(ctx, "pr", "testorg", "testrepo", s))
	require.NoError(t, store.SaveState(ctx, "issue", "testorg", "testrepo", s))

	// Update backfill_until for both
	until := time.Now().AddDate(0, 0, -28).UTC()
	require.NoError(t, store.SaveBackfillUntil(ctx, "testorg", "testrepo", until))

	got, err := store.GetBackfillUntil(ctx, "testorg", "testrepo")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.WithinDuration(t, until, *got, time.Second)
}

func TestSaveBackfillUntil_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	err := s.SaveBackfillUntil(ctx, "org", "repo", time.Now())
	assert.Error(t, err)
}
```

**Step 10: Run tests**

Run: `make test`
Expected: PASS

**Step 11: Commit**

```bash
git add pkg/data/types.go pkg/data/store.go pkg/data/postgres/state.go \
       pkg/data/postgres/state_test.go \
       pkg/data/postgres/sql/migrations/013_backfill_until.sql
git commit -S -m "feat: add backfill_until column to state table for progressive backfill"
```

---

### Task 3: Implement DB write sub-batching in `flush()`

**Files:**
- Modify: `pkg/data/postgres/event.go:40-42` (constants)
- Modify: `pkg/data/postgres/event.go:410-537` (flush method)

**Step 1: Write a unit test for sub-batch splitting**

Add to `pkg/data/postgres/event_test.go`:

```go
func TestSplitBatch(t *testing.T) {
	tests := []struct {
		name     string
		total    int
		batchSz  int
		expected int // number of sub-batches
	}{
		{"exact", 100, 100, 1},
		{"remainder", 150, 100, 2},
		{"smaller", 50, 100, 1},
		{"zero", 0, 100, 0},
		{"large", 500, 100, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := make([]*data.Event, tt.total)
			batches := splitIntoBatches(events, tt.batchSz)
			assert.Equal(t, tt.expected, len(batches))
			// Verify all events accounted for
			total := 0
			for _, b := range batches {
				total += len(b)
			}
			assert.Equal(t, tt.total, total)
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `make test`
Expected: FAIL — `splitIntoBatches` not defined

**Step 3: Add the `splitIntoBatches` helper and `dbBatchSize` variable**

In `pkg/data/postgres/event.go`, replace the constants block (lines 40-42):

```go
const (
	pageSizeDefault = 100
	importBatchSize = 500
	nilNumber       = 0
)

// dbBatchSize is the number of events per DB transaction during flush.
// Initialized from IMPORT_DB_BATCH_SIZE env var (default 100).
var dbBatchSize = config.ImportDBBatchSize()
```

Add the helper function (near `flush`, e.g. after line 537):

```go
func splitIntoBatches[T any](items []T, size int) [][]T {
	if len(items) == 0 || size < 1 {
		return nil
	}

	batches := make([][]T, 0, (len(items)+size-1)/size)
	for i := 0; i < len(items); i += size {
		end := i + size
		if end > len(items) {
			end = len(items)
		}
		batches = append(batches, items[i:end])
	}

	return batches
}
```

**Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS

**Step 5: Refactor `flush()` to use sub-batches**

Replace the flush method body (lines 410-537). The key change: instead of one transaction for all events+developers, iterate over sub-batches of events with their corresponding developers, each in its own transaction. State is saved once after all sub-batches.

The new `flush()`:

```go
func (e *eventImporter) flush(ctx context.Context) error {
	if len(e.list) == 0 {
		return nil
	}

	start := time.Now()

	var events []*data.Event
	var users map[string]*github.User
	var state map[string]*data.State

	e.mu.Lock()
	events = e.list
	e.list = make([]*data.Event, 0)

	users = make(map[string]*github.User, len(e.users))
	for k, v := range e.users {
		users[k] = v
	}

	state = make(map[string]*data.State, len(e.state))
	for k, v := range e.state {
		cp := *v
		state[k] = &cp
	}
	e.mu.Unlock()

	slices.SortFunc(events, compareEventsByPK)

	slog.Debug("flushing events and developers to db",
		"events", len(events), "developers", len(users), "db_batch_size", dbBatchSize)

	db := e.store.db

	eventStmt, err := db.PrepareContext(ctx, insertEventSQL)
	if err != nil {
		return fmt.Errorf("failed to prepare event insert statement: %w", err)
	}
	defer eventStmt.Close()

	devStmt, err := db.PrepareContext(ctx, insertDeveloperSQL)
	if err != nil {
		return fmt.Errorf("failed to prepare developer insert statement: %w", err)
	}
	defer devStmt.Close()

	// Split events into sub-batches for smaller transactions.
	batches := splitIntoBatches(events, dbBatchSize)

	for _, batch := range batches {
		// Collect developers referenced by this sub-batch.
		seen := make(map[string]struct{}, len(batch))
		for _, ev := range batch {
			seen[ev.Username] = struct{}{}
		}
		batchDevs := make([]*data.Developer, 0, len(seen))
		for username := range seen {
			if u, ok := users[username]; ok {
				batchDevs = append(batchDevs, ghutil.MapUserToDeveloper(u))
			}
		}
		slices.SortFunc(batchDevs, func(a, b *data.Developer) int {
			return strings.Compare(a.Username, b.Username)
		})

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer rollbackTransaction(tx)

		txDevStmt := tx.Stmt(devStmt)
		defer txDevStmt.Close()
		for i, u := range batchDevs {
			if _, err = txDevStmt.ExecContext(ctx, u.Username,
				u.FullName, u.Email, u.AvatarURL, u.ProfileURL, u.Entity,
				u.FullName, u.Email, u.AvatarURL, u.ProfileURL, u.Entity, u.Entity); err != nil {
				return fmt.Errorf("error inserting developer[%d]: %s: %w", i, u.Username, err)
			}
		}

		txEventStmt := tx.Stmt(eventStmt)
		defer txEventStmt.Close()
		for i, ev := range batch {
			_, err = txEventStmt.ExecContext(ctx,
				ev.Org, ev.Repo, ev.Username, ev.Type, ev.Date,
				ev.URL, ev.Mentions, ev.Labels,
				ev.State, ev.Number, ev.CreatedAt, ev.ClosedAt, ev.MergedAt, ev.Additions, ev.Deletions,
				ev.ChangedFiles, ev.Commits, ev.Title,
				ev.URL, ev.Mentions, ev.Labels,
				ev.State, ev.Number, ev.CreatedAt, ev.ClosedAt, ev.MergedAt, ev.Additions, ev.Deletions,
				ev.ChangedFiles, ev.Commits, ev.Title,
			)
			if err != nil {
				return fmt.Errorf("error inserting event[%d]: %s/%s: %w", i, ev.Org, ev.Repo, err)
			}
		}

		if err = tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}
	}

	// Save state once after all sub-batches complete.
	stateStmt, err := db.PrepareContext(ctx, insertStateSQL)
	if err != nil {
		return fmt.Errorf("failed to prepare state insert statement: %w", err)
	}
	defer stateStmt.Close()

	for t, p := range state {
		since := p.Since.Unix()
		var backfillUnix sql.NullInt64
		if p.BackfillUntil != nil {
			backfillUnix = sql.NullInt64{Int64: p.BackfillUntil.Unix(), Valid: true}
		}
		_, err = stateStmt.ExecContext(ctx, t, e.owner, e.repo, p.Page, since, backfillUnix,
			p.Page, since, backfillUnix)
		if err != nil {
			return fmt.Errorf("error inserting state[%s]: %s/%s with page:%d and since:%s: %w",
				t, e.owner, e.repo, p.Page, p.Since.Format("2006-01-02"), err)
		}
	}

	e.mu.Lock()
	e.flushed += len(events)
	total := e.flushed
	e.mu.Unlock()

	slog.Info("events progress",
		"org", e.owner,
		"repo", e.repo,
		"batch", len(events),
		"total", total,
		"developers", len(users),
		"duration_sec", time.Since(start).Seconds())

	return nil
}
```

**Step 6: Run tests**

Run: `make test`
Expected: PASS

**Step 7: Run qualify**

Run: `make qualify`
Expected: PASS (lint + tests + coverage)

**Step 8: Commit**

```bash
git add pkg/data/postgres/event.go pkg/data/postgres/event_test.go
git commit -S -m "feat: sub-batch DB writes during event flush

Split 500-event API batches into configurable sub-batches (default 100)
for DB writes. Each sub-batch is a separate transaction with only its
referenced developers, preventing the progressive upsert degradation
seen on large repos (1s -> 280s per batch on kubernetes)."
```

---

### Task 4: Refactor `ImportEvents` to accept a time window

**Files:**
- Modify: `pkg/data/postgres/event.go:149-252` (ImportEvents signature and body)
- Modify: `pkg/data/store.go:78-83` (EventStore interface)
- Modify: `pkg/importer/importer.go:356-375` (caller)

**Step 1: Change `ImportEvents` signature**

The current signature uses `days int` to compute `minEventTime`. Change it to accept explicit window boundaries so the caller controls the time range:

In `pkg/data/store.go`, update `EventStore`:

```go
type EventStore interface {
	ImportEvents(ctx context.Context, tokenFn TokenFunc, exhaustFn ExhaustFunc, owner, repo string, windowStart, windowEnd time.Time) (map[string]int, *ImportSummary, error)
	UpdateEvents(ctx context.Context, events []*Event) error
	GetMaxEventTime(ctx context.Context, org, repo string) (time.Time, error)
}
```

**Step 2: Update `ImportEvents` implementation**

In `pkg/data/postgres/event.go`, replace lines 149-177:

```go
func (s *Store) ImportEvents(ctx context.Context, tokenFn data.TokenFunc, exhaustFn data.ExhaustFunc, owner, repo string, windowStart, windowEnd time.Time) (map[string]int, *data.ImportSummary, error) {
	if tokenFn == nil || owner == "" || repo == "" {
		return nil, nil, errors.New("tokenFn, owner, and repo are required")
	}

	if windowStart.IsZero() {
		windowStart = time.Now().AddDate(0, 0, -data.EventAgeDaysDefault).UTC()
	}
	if windowEnd.IsZero() {
		windowEnd = time.Now().UTC()
	}

	token := tokenFn()
	if token == "" {
		return nil, nil, errors.New("token pool exhausted")
	}
	client := github.NewClient(net.GetOAuthClient(ctx, token))

	imp := &eventImporter{
		tokenFn:      tokenFn,
		exhaustFn:    exhaustFn,
		curToken:     token,
		client:       client,
		store:        s,
		owner:        owner,
		repo:         repo,
		list:         make([]*data.Event, 0),
		counts:       make(map[string]int),
		users:        make(map[string]*github.User),
		state:        make(map[string]*data.State),
		minEventTime: windowStart,
	}
```

The rest of `ImportEvents` (lines 179-252) stays the same — `loadState` already uses `e.minEventTime` to clamp.

Update the summary to use `windowStart`:

```go
	summary := &data.ImportSummary{
		Repo:       owner + "/" + repo,
		Since:      windowStart.Format("2006-01-02"),
		Events:     total,
		Developers: len(imp.users),
	}
```

**Step 3: Update caller in `importer.go`**

In `pkg/importer/importer.go`, replace lines 366-371. The current call:

```go
_, _, importErr := store.ImportEvents(ctx, eventTokenFn, eventExhaustFn, org, repo, data.EventAgeDaysDefault)
```

Becomes (for now, using the same 90-day window — the two-pass logic comes in Task 5):

```go
windowStart := time.Now().AddDate(0, 0, -data.EventAgeDaysDefault).UTC()
windowEnd := time.Now().UTC()
_, _, importErr := store.ImportEvents(ctx, eventTokenFn, eventExhaustFn, org, repo, windowStart, windowEnd)
```

**Step 4: Search for any other callers of ImportEvents**

Run: `grep -rn "ImportEvents" pkg/ cmd/` and update any other call sites with the same window conversion.

**Step 5: Run tests**

Run: `make test`
Expected: PASS (behavior unchanged, just different parameter shape)

**Step 6: Commit**

```bash
git add pkg/data/store.go pkg/data/postgres/event.go pkg/importer/importer.go
git commit -S -m "refactor: ImportEvents accepts explicit time window

Replace days parameter with windowStart/windowEnd to support
the two-pass import model where fresh pass and backfill pass
have different time windows."
```

---

### Task 5: Implement two-pass event import in `importRepo`

**Files:**
- Modify: `pkg/importer/importer.go:329-442` (importRepo function)

**Step 1: Add fresh pass + backfill pass logic**

Replace the events phase (lines 356-375) and move backfill after insights (after line 434). The new `importRepo` function structure:

```go
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

	// Phase 1: Metadata
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

	var freshCounts map[string]int
	if err := retryRL(func() error {
		var importErr error
		freshCounts, _, importErr = store.ImportEvents(ctx, eventTokenFn, eventExhaustFn, org, repo, freshStart, freshEnd)
		return importErr
	}); err != nil {
		slog.Error("importing events (fresh)", "org", org, "repo", repo, "error", err)
		errs++
	} else {
		freshTotal := 0
		for _, v := range freshCounts {
			freshTotal += v
		}
		slog.Info("fresh pass complete", "org", org, "repo", repo,
			"events", freshTotal,
			"window_start", freshStart.Format("2006-01-02"),
			"window_end", freshEnd.Format("2006-01-02"))
	}

	// Set backfill_until on first import (when it's NULL).
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

	// Phase 3-8: Non-event phases (releases, metrics, containers, reputation, insights)
	// These run after the fresh pass so the dashboard has complete data for the recent window.

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
			return importErr
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
```

**Step 2: Run tests**

Run: `make test`
Expected: PASS

**Step 3: Run qualify**

Run: `make qualify`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/importer/importer.go
git commit -S -m "feat: two-pass event import with progressive backfill

Fresh pass (3 weeks) runs first, then all non-event phases
(releases, metrics, reputation, insights), then backfill pass
(1-week chunk) extends historical coverage. Backfill is best
effort — runs with remaining time budget after the fast train."
```

---

### Task 6: Update ARCHITECTURE.md and remove proposed status

**Files:**
- Modify: `docs/ARCHITECTURE.md:119-146`

**Step 1: Replace the "Progressive Backfill (Proposed)" section**

Replace the entire section (lines 119-146) with the implemented design:

```markdown
### Progressive Backfill

Event import uses a two-pass model to prioritize fresh data:

1. **Fresh pass** — fetches events from the most recent `IMPORT_FRESH_DAYS` (default 21 days), newest-first. All 5 event types run concurrently. Completes quickly even for the largest repos.
2. **Non-event phases** — releases, metrics, containers, reputation, deep reputation, insights all run after the fresh pass, ensuring the dashboard has complete data for the recent window.
3. **Backfill pass** — extends historical coverage by `IMPORT_BACKFILL_CHUNK_DAYS` (default 7 days) per run, marching backward until reaching `EventAgeDaysDefault` (90 days). Runs last as best effort.

**State:** `backfill_until` column in `devpulse_state` tracks the oldest date covered. NULL means fresh repo. Updated only on successful chunk completion — killed runs retry the same chunk.

**DB writes:** API fetches 500-event batches, but flushes to PostgreSQL in sub-batches of `IMPORT_DB_BATCH_SIZE` (default 100). Each sub-batch is a separate transaction, keeping developer upsert conflict sets small and write times flat (~1-3s regardless of import progress).

**Self-healing:** Killed jobs resume cleanly — flushed sub-batches are persisted via idempotent upserts, and `backfill_until` only advances on chunk completion. GitHub API 500s on one event type don't block backfill progress — the failing window ages out.

**Configuration:** `IMPORT_FRESH_DAYS` (default 21), `IMPORT_BACKFILL_CHUNK_DAYS` (default 7), `IMPORT_DB_BATCH_SIZE` (default 100). All optional with sensible defaults.
```

**Step 2: Commit**

```bash
git add docs/ARCHITECTURE.md
git commit -S -m "docs: update progressive backfill section with implemented design"
```

---

### Task 7: Final qualification

**Step 1: Full qualify run**

Run: `make qualify`
Expected: PASS (test-coverage + lint + govulncheck + e2e)

**Step 2: Review git log**

Run: `git log --oneline -10`
Verify: 6 clean commits covering config, state, sub-batching, window refactor, two-pass import, docs.

---

## Unresolved Questions

1. **Existing repos on first deploy:** When the new code runs for a repo that already has 90 days of data, `GetBackfillUntil` returns nil. The fresh pass runs (mostly no-ops via upsert), then `backfill_until` is set to `today - 21d`. The backfill pass will then try to cover days 21-28, even though data already exists. This is harmless (upserts) but wastes ~1 API page of fetches per event type on the first run. After that, it marches to 90d and stops. **Acceptable trade-off vs adding migration logic to pre-populate `backfill_until` for existing repos.**

2. **Per-event-type backfill tracking:** The current design uses a single `backfill_until` per repo (MIN across all event types). If one event type consistently fails, it holds back the overall boundary. We chose to advance anyway on partial failure. If we later want per-type tracking, the column is already per-row (keyed by `query, org, repo`).

3. **`dbBatchSize` as package var:** Using `var dbBatchSize = config.ImportDBBatchSize()` means the value is read once at init time. This is fine for production (env vars don't change at runtime) but means tests can't easily override it. If test flexibility is needed later, accept it as a parameter to `flush()`.
