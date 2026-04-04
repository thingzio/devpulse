# Insight Caching Design

**Date:** 2026-04-04
**Status:** Approved

## Problem

`generateRepoInsights()` calls the Claude API on every hourly import for every paid-plan repo. At scale (1K tenants, ~3K repos), this costs ~$510/mo. Most calls produce nearly identical output because 3-month trends don't shift hour-to-hour.

## Solution

Add a two-gate check before calling the LLM:

1. **Minimum age gate** -- skip if insights were generated < 7 days ago
2. **Event delta gate** -- skip if event count changed < 10% since last generation

Both gates must pass before regenerating. First-time generation (no existing insights) always proceeds.

## Data Model Change

Add `event_count INTEGER NOT NULL DEFAULT 0` to `repo_insights` table. Populated at generation time with the current event count for the repo's 3-month window (from `InsightsSummary.Events`).

Migration: single `ALTER TABLE` in the existing migrations file.

## Code Changes

### `pkg/importer/importer.go`

- New `shouldRegenerateInsights()` function that:
  1. Calls `GetRepoInsightsGeneratedAt()` (already exists, unused) -- if empty, return true (first generation)
  2. Parses timestamp, checks if > 7 days old -- if not, return false
  3. Calls `GetRepoInsightsEventCount()` (new store method) to get the saved count
  4. Calls `GetInsightsSummary()` to get current event count
  5. Computes `abs(current - saved) / saved` -- if >= 0.10, return true
- `generateRepoInsights()` calls `shouldRegenerateInsights()` first, logs skip reason
- Save `summary.Events` into `RepoInsights.EventCount` when generating

### `pkg/data/types.go`

- Add `EventCount int` field to `RepoInsights`

### `pkg/data/store.go`

- Add `GetRepoInsightsEventCount(ctx, org, repo string) (int, error)` to Store interface

### `pkg/data/postgres/repo_insights.go`

- Add `selectRepoInsightsEventCountSQL` query
- Implement `GetRepoInsightsEventCount()`
- Update `upsertRepoInsightsSQL` to include `event_count`
- Update `SaveRepoInsights()` to persist `EventCount`
- Update `GetRepoInsights()` scan to include `event_count`

### `pkg/data/postgres/sql/migrations_saas/001_initial.sql`

- Add `event_count INTEGER NOT NULL DEFAULT 0` to `repo_insights` CREATE TABLE

### Constants (in `importer.go`)

```go
insightsMinAgeDays    = 7
insightsEventDeltaPct = 0.10
```

## Cost Impact

At 1K tenants (~3K paid repos), reduces from ~720 LLM calls/day to ~60-100 calls/day (repos that cross the 7-day + 10% threshold). Estimated savings: ~70-85% of Anthropic API cost.

## What's NOT Changing

- `insightsPeriodMonths = 3` stays the same
- LLM prompt, model, and response format unchanged
- Free-plan gating unchanged (AILevel check stays)
- No new env vars
