# Insight Caching Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Skip LLM insight generation when repo data hasn't changed significantly, reducing Anthropic API cost by ~70-85%.

**Architecture:** Two-gate check before each LLM call: (1) minimum age of 7 days since last generation, (2) event count delta >= 10%. A new `event_count` column on `repo_insights` tracks the count at generation time for comparison.

**Tech Stack:** Go, PostgreSQL, existing Store interface pattern.

---

### Task 1: Add `event_count` column to migration files

**Files:**
- Modify: `pkg/data/postgres/sql/migrations/001_initial.sql:100-108`
- Modify: `pkg/data/postgres/sql/migrations_saas/001_initial.sql` (no CREATE TABLE here, but verify no changes needed)

**Step 1: Add column to base migration**

In `pkg/data/postgres/sql/migrations/001_initial.sql`, change the `repo_insights` CREATE TABLE from:

```sql
CREATE TABLE IF NOT EXISTS repo_insights (
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    insights_json TEXT NOT NULL,
    period_months INTEGER NOT NULL DEFAULT 3,
    model TEXT NOT NULL DEFAULT '',
    generated_at TEXT NOT NULL,
    PRIMARY KEY (org, repo)
);
```

to:

```sql
CREATE TABLE IF NOT EXISTS repo_insights (
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    insights_json TEXT NOT NULL,
    period_months INTEGER NOT NULL DEFAULT 3,
    model TEXT NOT NULL DEFAULT '',
    generated_at TEXT NOT NULL,
    event_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (org, repo)
);
```

**Step 2: Verify SaaS migration**

The SaaS migration only adds RLS policies for `repo_insights` — no CREATE TABLE. Confirm no change needed in `pkg/data/postgres/sql/migrations_saas/001_initial.sql`.

**Step 3: Commit**

```bash
git add pkg/data/postgres/sql/migrations/001_initial.sql
git commit -S -m "feat: add event_count column to repo_insights table"
```

---

### Task 2: Add `EventCount` to `RepoInsights` struct

**Files:**
- Modify: `pkg/data/types.go:499-506`

**Step 1: Add field**

Change `RepoInsights` from:

```go
type RepoInsights struct {
	Org          string             `json:"org" yaml:"org"`
	Repo         string             `json:"repo" yaml:"repo"`
	Insights     *GeneratedInsights `json:"insights" yaml:"insights"`
	PeriodMonths int                `json:"period_months" yaml:"periodMonths"`
	Model        string             `json:"model" yaml:"model"`
	GeneratedAt  string             `json:"generated_at" yaml:"generatedAt"`
}
```

to:

```go
type RepoInsights struct {
	Org          string             `json:"org" yaml:"org"`
	Repo         string             `json:"repo" yaml:"repo"`
	Insights     *GeneratedInsights `json:"insights" yaml:"insights"`
	PeriodMonths int                `json:"period_months" yaml:"periodMonths"`
	Model        string             `json:"model" yaml:"model"`
	GeneratedAt  string             `json:"generated_at" yaml:"generatedAt"`
	EventCount   int                `json:"event_count" yaml:"eventCount"`
}
```

**Step 2: Commit**

```bash
git add pkg/data/types.go
git commit -S -m "feat: add EventCount field to RepoInsights struct"
```

---

### Task 3: Add `GetRepoInsightsEventCount` to Store interface and implement

**Files:**
- Modify: `pkg/data/store.go:146-148`
- Modify: `pkg/data/postgres/repo_insights.go`

**Step 1: Add to Store interface**

In `pkg/data/store.go`, after the existing `GetRepoInsightsGeneratedAt` line (148), add:

```go
	GetRepoInsightsEventCount(ctx context.Context, org, repo string) (int, error)
```

**Step 2: Add SQL constant and implement in postgres package**

In `pkg/data/postgres/repo_insights.go`, add the SQL constant after the existing `selectRepoInsightsGeneratedAtSQL`:

```go
	selectRepoInsightsEventCountSQL = `SELECT COALESCE(event_count, 0)
		FROM repo_insights
		WHERE org = $1 AND repo = $2
	`
```

Add the implementation after `GetRepoInsightsGeneratedAt`:

```go
func (s *Store) GetRepoInsightsEventCount(ctx context.Context, org, repo string) (int, error) {
	if s.db == nil {
		return 0, data.ErrDBNotInitialized
	}

	var count int
	if err := s.db.QueryRowContext(ctx, selectRepoInsightsEventCountSQL, org, repo).Scan(&count); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("querying repo insights event_count for %s/%s: %w", org, repo, err)
	}

	return count, nil
}
```

**Step 3: Commit**

```bash
git add pkg/data/store.go pkg/data/postgres/repo_insights.go
git commit -S -m "feat: add GetRepoInsightsEventCount to Store interface"
```

---

### Task 4: Update `SaveRepoInsights` and `GetRepoInsights` for `event_count`

**Files:**
- Modify: `pkg/data/postgres/repo_insights.go:12-30` (SQL constants)
- Modify: `pkg/data/postgres/repo_insights.go:32-52` (SaveRepoInsights)
- Modify: `pkg/data/postgres/repo_insights.go:54-84` (GetRepoInsights)

**Step 1: Update SQL constants**

Replace `upsertRepoInsightsSQL`:

```go
	upsertRepoInsightsSQL = `INSERT INTO repo_insights (org, repo, insights_json, period_months, model, generated_at, event_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT(org, repo) DO UPDATE SET
			insights_json = $8, period_months = $9, model = $10, generated_at = $11, event_count = $12
	`
```

Replace `selectRepoInsightsSQL`:

```go
	selectRepoInsightsSQL = `SELECT org, repo, insights_json, period_months, model, generated_at, event_count
		FROM repo_insights
		WHERE org = COALESCE($1, org)
		  AND repo = COALESCE($2, repo)
		ORDER BY org, repo
	`
```

**Step 2: Update SaveRepoInsights**

Replace the `ExecContext` call in `SaveRepoInsights`:

```go
	_, err = s.db.ExecContext(ctx, upsertRepoInsightsSQL,
		org, repo, j, ri.PeriodMonths, ri.Model, ri.GeneratedAt, ri.EventCount,
		j, ri.PeriodMonths, ri.Model, ri.GeneratedAt, ri.EventCount,
	)
```

**Step 3: Update GetRepoInsights scan**

Replace the `Scan` call in the `GetRepoInsights` loop:

```go
		if err := rows.Scan(&ri.Org, &ri.Repo, &j, &ri.PeriodMonths, &ri.Model, &ri.GeneratedAt, &ri.EventCount); err != nil {
```

**Step 4: Run tests**

```bash
make test
```

**Step 5: Commit**

```bash
git add pkg/data/postgres/repo_insights.go
git commit -S -m "feat: persist event_count in repo_insights upsert and select"
```

---

### Task 5: Write `shouldRegenerateInsights` test

**Files:**
- Modify: `pkg/importer/importer.go` (will add function in Task 6)
- Create: `pkg/importer/insights_test.go`

**Step 1: Write the test file**

```go
package importer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestShouldRegenerateInsights(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name        string
		generatedAt string
		savedCount  int
		currentCount int
		want        bool
		wantReason  string
	}{
		{
			name:        "first generation (no prior insights)",
			generatedAt: "",
			savedCount:  0,
			currentCount: 100,
			want:        true,
			wantReason:  "first generation",
		},
		{
			name:        "too recent (1 day old)",
			generatedAt: now.Add(-24 * time.Hour).Format("2006-01-02T15:04:05Z"),
			savedCount:  1000,
			currentCount: 1200,
			want:        false,
			wantReason:  "insights are only 1 days old",
		},
		{
			name:        "old enough but delta below threshold",
			generatedAt: now.Add(-8 * 24 * time.Hour).Format("2006-01-02T15:04:05Z"),
			savedCount:  1000,
			currentCount: 1050,
			want:        false,
			wantReason:  "event delta 5.0% below threshold",
		},
		{
			name:        "old enough and delta above threshold",
			generatedAt: now.Add(-8 * 24 * time.Hour).Format("2006-01-02T15:04:05Z"),
			savedCount:  1000,
			currentCount: 1150,
			want:        true,
			wantReason:  "event delta 15.0% exceeds threshold",
		},
		{
			name:        "old enough with zero saved count (treat as first gen)",
			generatedAt: now.Add(-8 * 24 * time.Hour).Format("2006-01-02T15:04:05Z"),
			savedCount:  0,
			currentCount: 50,
			want:        true,
			wantReason:  "no prior event count",
		},
		{
			name:        "old enough with events decreasing beyond threshold",
			generatedAt: now.Add(-8 * 24 * time.Hour).Format("2006-01-02T15:04:05Z"),
			savedCount:  1000,
			currentCount: 850,
			want:        true,
			wantReason:  "event delta 15.0% exceeds threshold",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := checkInsightStaleness(tc.generatedAt, tc.savedCount, tc.currentCount)
			assert.Equal(t, tc.want, got, "shouldRegenerate mismatch")
			assert.Contains(t, reason, tc.wantReason)
		})
	}
}
```

**Step 2: Run the test (expect compile failure)**

```bash
go test ./pkg/importer/ -run TestShouldRegenerateInsights -v
```

Expected: compilation error — `checkInsightStaleness` not defined.

**Step 3: Commit**

```bash
git add pkg/importer/insights_test.go
git commit -S -m "test: add shouldRegenerateInsights test cases"
```

---

### Task 6: Implement `checkInsightStaleness` and wire into `generateRepoInsights`

**Files:**
- Modify: `pkg/importer/importer.go:239-267`

**Step 1: Add constants**

After the existing `insightsPeriodMonths` constant (line 240), add:

```go
	insightsMinAgeDays    = 7
	insightsEventDeltaPct = 0.10
```

**Step 2: Add `checkInsightStaleness` function**

Add after the constants block (after line 242):

```go
// checkInsightStaleness is a pure function that determines whether insights should be regenerated
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
```

**Step 3: Update `generateRepoInsights`**

Replace the existing `generateRepoInsights` function:

```go
func generateRepoInsights(ctx context.Context, store data.Store, cfg *data.LLMConfig, org, repo string) error {
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
	summary, err := store.GetInsightsSummary(ctx, &org, &repo, nil, insightsPeriodMonths)
	if err != nil {
		return fmt.Errorf("getting insights summary for staleness check: %w", err)
	}

	shouldRegen, reason := checkInsightStaleness(generatedAt, savedCount, summary.Events)
	if !shouldRegen {
		slog.Debug("skipping insights generation", "org", org, "repo", repo, "reason", reason)
		return nil
	}
	slog.Info("regenerating insights", "org", org, "repo", repo, "reason", reason)

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
		EventCount:   summary.Events,
	}

	if err := store.SaveRepoInsights(ctx, org, repo, ri); err != nil {
		return fmt.Errorf("saving insights: %w", err)
	}

	slog.Info("insights generated", "org", org, "repo", repo, "model", model,
		"event_count", summary.Events, "prev_count", savedCount)
	return nil
}
```

**Step 4: Run the test**

```bash
go test ./pkg/importer/ -run TestShouldRegenerateInsights -v
```

Expected: all 6 test cases PASS.

**Step 5: Run full test suite**

```bash
make test
```

**Step 6: Commit**

```bash
git add pkg/importer/importer.go
git commit -S -m "feat: add insight caching with age + event delta gates"
```

---

### Task 7: Run qualify and verify

**Step 1: Run full qualify**

```bash
make qualify
```

Fix any lint or vet issues.

**Step 2: Final commit (if any fixes)**

```bash
git add -A
git commit -S -m "fix: address lint issues from insight caching"
```

---

## File Change Summary

| File | Action | What |
|------|--------|------|
| `pkg/data/postgres/sql/migrations/001_initial.sql` | Modify | Add `event_count` column |
| `pkg/data/types.go` | Modify | Add `EventCount` to `RepoInsights` |
| `pkg/data/store.go` | Modify | Add `GetRepoInsightsEventCount` to interface |
| `pkg/data/postgres/repo_insights.go` | Modify | New query, update upsert/select/scan |
| `pkg/importer/importer.go` | Modify | Add caching constants, `checkInsightStaleness`, update `generateRepoInsights` |
| `pkg/importer/insights_test.go` | Create | Test cases for staleness logic |
