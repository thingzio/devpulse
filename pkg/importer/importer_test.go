package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-github/v83/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/tenant"
)

func TestRetryRL(t *testing.T) {
	ctx := context.Background()
	retryRL := newRetryRL(ctx)

	t.Run("success on first call", func(t *testing.T) {
		err := retryRL(func() error { return nil })
		require.NoError(t, err)
	})

	t.Run("non-rate-limit error returned", func(t *testing.T) {
		sentinel := errors.New("db connection failed")
		err := retryRL(func() error { return sentinel })
		require.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
		assert.Contains(t, err.Error(), "retryRL:")
	})

	t.Run("non-transient error fails immediately without retry", func(t *testing.T) {
		calls := 0
		err := retryRL(func() error {
			calls++
			return errors.New("permanent failure")
		})
		require.Error(t, err)
		assert.Equal(t, 1, calls, "non-transient errors must not be retried")
	})

	t.Run("wrapped error preserves chain", func(t *testing.T) {
		inner := errors.New("inner")
		wrapped := fmt.Errorf("outer: %w", inner)
		err := retryRL(func() error { return wrapped })
		require.Error(t, err)
		assert.ErrorIs(t, err, inner)
	})
}

func TestRetryRL_TransientRetry(t *testing.T) {
	ctx := context.Background()
	retryRL := newRetryRL(ctx)

	t.Run("transient 5xx then success", func(t *testing.T) {
		var calls atomic.Int32
		err := retryRL(func() error {
			n := calls.Add(1)
			if n == 1 {
				return &github.ErrorResponse{
					Response: &http.Response{StatusCode: http.StatusBadGateway},
				}
			}
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, int32(2), calls.Load())
	})

	t.Run("transient EOF then success", func(t *testing.T) {
		var calls atomic.Int32
		err := retryRL(func() error {
			n := calls.Add(1)
			if n < 3 {
				return io.EOF
			}
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, int32(3), calls.Load())
	})

	t.Run("exhausts attempts on persistent transient error", func(t *testing.T) {
		var calls atomic.Int32
		err := retryRL(func() error {
			calls.Add(1)
			return io.ErrUnexpectedEOF
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exhausted")
		// Initial attempt + transientRetryAttempts retries.
		assert.Equal(t, int32(transientRetryAttempts+1), calls.Load())
	})
}

func TestRetryRL_ContextCancellation(t *testing.T) {
	t.Run("canceled before first call", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		retryRL := newRetryRL(ctx)
		var calls atomic.Int32
		err := retryRL(func() error {
			calls.Add(1)
			return nil
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(0), calls.Load())
	})

	t.Run("canceled during backoff stops retries", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		retryRL := newRetryRL(ctx)
		var calls atomic.Int32
		// Cancel after first call so backoff sleep returns immediately.
		err := retryRL(func() error {
			calls.Add(1)
			cancel()
			return io.EOF
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(1), calls.Load(), "must not call fn again after ctx cancel")
	})
}

func TestBestPlanForRepo(t *testing.T) {
	t.Parallel()

	t.Run("single free tenant", func(t *testing.T) {
		t.Parallel()
		got := bestPlanForRepo([]TenantRef{{Plan: "free"}})
		assert.Equal(t, "free", got)
	})

	t.Run("enterprise wins immediately", func(t *testing.T) {
		t.Parallel()
		got := bestPlanForRepo([]TenantRef{{Plan: "free"}, {Plan: "enterprise"}})
		assert.Equal(t, "enterprise", got)
	})

	t.Run("pro beats starter", func(t *testing.T) {
		t.Parallel()
		got := bestPlanForRepo([]TenantRef{{Plan: "starter"}, {Plan: "pro"}})
		assert.Equal(t, "pro", got)
	})

	t.Run("nil tenants returns empty", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "", bestPlanForRepo(nil))
	})

	t.Run("empty slice returns empty", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "", bestPlanForRepo([]TenantRef{}))
	})
}

func TestBuildAndShardIntegration(t *testing.T) {
	t.Parallel()

	// Simulate 2 tenants sharing NVIDIA/aicr, plus unique repos with event counts.
	rows := []tenant.ImportWorkRow{
		{TenantRepoID: "tr1", TenantID: "t1", Org: "NVIDIA", Repo: "aicr", Plan: "pro", EventCount: 1000},
		{TenantRepoID: "tr2", TenantID: "t2", Org: "NVIDIA", Repo: "aicr", Plan: "enterprise", EventCount: 1000},
		{TenantRepoID: "tr3", TenantID: "t1", Org: "NVIDIA", Repo: "cccl", Plan: "pro", EventCount: 5000},
		{TenantRepoID: "tr4", TenantID: "t1", Org: "ai-dynamo", Repo: "dynamo", Plan: "pro", EventCount: 12000},
		{TenantRepoID: "tr5", TenantID: "t2", Org: "ai-dynamo", Repo: "nixl", Plan: "enterprise", EventCount: 3600},
	}

	workList := BuildWorkList(rows)
	// NVIDIA/aicr deduped: 4 unique repos from 5 rows.
	require.Len(t, workList, 4)

	// aicr should have 2 tenants and correct weight.
	for _, rw := range workList {
		if rw.Org == "NVIDIA" && rw.Repo == "aicr" {
			assert.Len(t, rw.Tenants, 2)
			assert.Equal(t, 1000, rw.Weight)
		}
	}

	// Shard across 2 tasks — all repos covered, no overlap.
	all := make([]RepoWork, 0, len(workList))
	for idx := range 2 {
		all = append(all, ShardRepos(workList, 2, idx)...)
	}
	assert.Len(t, all, 4)
	seen := map[string]bool{}
	for _, rw := range all {
		key := rw.Org + "/" + rw.Repo
		assert.False(t, seen[key], "duplicate: %s", key)
		seen[key] = true
	}

	// With weighted sharding, dynamo (12000) and cccl (5000) should be
	// in different shards for better balance.
	has := func(shard []RepoWork, name string) bool {
		for _, r := range shard {
			if r.Repo == name {
				return true
			}
		}
		return false
	}
	shard0 := ShardRepos(workList, 2, 0)
	shard1 := ShardRepos(workList, 2, 1)
	if has(shard0, "dynamo") {
		assert.False(t, has(shard0, "cccl"), "dynamo and cccl should be in different shards")
		assert.True(t, has(shard1, "cccl"), "cccl should be in the other shard")
	} else {
		assert.True(t, has(shard1, "dynamo"), "dynamo must be in one shard")
		assert.False(t, has(shard1, "cccl"), "dynamo and cccl should be in different shards")
		assert.True(t, has(shard0, "cccl"), "cccl should be in the other shard")
	}
}

// backfillMockStore implements data.Store with zero-returning stubs.
// Records ImportEvents and SaveBackfillUntil calls for assertion.
type backfillMockStore struct {
	importEventsCalled      bool
	importEventsWindowStart time.Time
	importEventsWindowEnd   time.Time

	saveBackfillCalled bool
	saveBackfillUntil  time.Time

	// Insights catch-up test hooks. When set, override the corresponding
	// stub returns; SaveRepoInsights records the call for assertion.
	insightsSummary     *data.InsightsSummary
	insightsGeneratedAt string
	insightsSavedCount  int
	saveInsightsCalled  bool
}

func (m *backfillMockStore) Close() error { return nil }

// --- StateStore ---
func (m *backfillMockStore) GetState(_ context.Context, _, _, _ string, _ time.Time) (*data.State, error) {
	return nil, nil
}
func (m *backfillMockStore) SaveState(_ context.Context, _, _, _ string, _ *data.State) error {
	return nil
}
func (m *backfillMockStore) HasState(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}
func (m *backfillMockStore) ClearState(_ context.Context, _, _ string) error { return nil }
func (m *backfillMockStore) GetDataState(_ context.Context) (map[string]int64, error) {
	return nil, nil
}
func (m *backfillMockStore) GetBackfillUntil(_ context.Context, _, _ string) (*time.Time, error) {
	return nil, nil
}
func (m *backfillMockStore) SaveBackfillUntil(_ context.Context, _, _ string, until time.Time) error {
	m.saveBackfillCalled = true
	m.saveBackfillUntil = until
	return nil
}

// --- DeleteStore ---
func (m *backfillMockStore) DeleteRepoData(_ context.Context, _, _ string) (*data.DeleteResult, error) {
	return nil, nil
}

// --- SubstitutionStore ---
func (m *backfillMockStore) SaveAndApplyDeveloperSub(_ context.Context, _, _, _ string) (*data.Substitution, error) {
	return nil, nil
}
func (m *backfillMockStore) ApplySubstitutions(_ context.Context) ([]*data.Substitution, error) {
	return nil, nil
}

// --- EntityStore ---
func (m *backfillMockStore) GetEntityLike(_ context.Context, _ string, _ int) ([]*data.ListItem, error) {
	return nil, nil
}
func (m *backfillMockStore) GetEntity(_ context.Context, _ string) (*data.EntityResult, error) {
	return nil, nil
}
func (m *backfillMockStore) QueryEntities(_ context.Context, _ string, _ int) ([]*data.CountedItem, error) {
	return nil, nil
}
func (m *backfillMockStore) CleanEntities(_ context.Context) error { return nil }

// --- RepoStore ---
func (m *backfillMockStore) GetRepoLike(_ context.Context, _ string, _ int) ([]*data.ListItem, error) {
	return nil, nil
}

// --- OrgStore ---
func (m *backfillMockStore) GetAllOrgRepos(_ context.Context) ([]*data.OrgRepoItem, error) {
	return nil, nil
}
func (m *backfillMockStore) GetDeveloperPercentages(_ context.Context, _, _, _ *string, _ []string, _ int) ([]*data.CountedItem, error) {
	return nil, nil
}
func (m *backfillMockStore) GetEntityPercentages(_ context.Context, _, _, _ *string, _ []string, _ int) ([]*data.CountedItem, error) {
	return nil, nil
}
func (m *backfillMockStore) SearchDeveloperUsernames(_ context.Context, _ string, _, _ *string, _, _ int) ([]string, error) {
	return nil, nil
}
func (m *backfillMockStore) GetOrgLike(_ context.Context, _ string, _ int) ([]*data.ListItem, error) {
	return nil, nil
}

// --- DeveloperStore ---
func (m *backfillMockStore) GetDeveloperUsernames(_ context.Context) ([]string, error) {
	return nil, nil
}
func (m *backfillMockStore) GetNoFullnameDeveloperUsernames(_ context.Context) ([]string, error) {
	return nil, nil
}
func (m *backfillMockStore) GetUnenrichedDeveloperUsernames(_ context.Context) ([]string, error) {
	return nil, nil
}
func (m *backfillMockStore) SaveDevelopers(_ context.Context, _ []*data.Developer) error { return nil }
func (m *backfillMockStore) GetDeveloper(_ context.Context, _ string) (*data.Developer, error) {
	return nil, nil
}
func (m *backfillMockStore) SearchDevelopers(_ context.Context, _ string, _ int) ([]*data.DeveloperListItem, error) {
	return nil, nil
}
func (m *backfillMockStore) UpdateDeveloperNames(_ context.Context, _ map[string]string) error {
	return nil
}
func (m *backfillMockStore) EnrichDeveloperEntities(_ context.Context, _ string) error { return nil }

// --- QueryStore ---
func (m *backfillMockStore) SearchEvents(_ context.Context, _ *data.EventSearchCriteria) ([]*data.EventDetails, error) {
	return nil, nil
}
func (m *backfillMockStore) GetMinEventDate(_ context.Context, _, _ *string) (string, error) {
	return "", nil
}
func (m *backfillMockStore) GetEventTypeSeries(_ context.Context, _, _, _ *string, _ int) (*data.EventTypeSeries, error) {
	return nil, nil
}

// --- EventStore ---
func (m *backfillMockStore) ImportEvents(_ context.Context, _ data.TokenFunc, _ data.ExhaustFunc, _, _ string, windowStart, windowEnd time.Time) (map[string]int, *data.ImportSummary, error) {
	m.importEventsCalled = true
	m.importEventsWindowStart = windowStart
	m.importEventsWindowEnd = windowEnd
	return nil, nil, nil
}
func (m *backfillMockStore) UpdateEvents(_ context.Context, _ string, _ int) (map[string]int, error) {
	return nil, nil
}
func (m *backfillMockStore) GetMaxEventTime(_ context.Context, _, _ string) (time.Time, error) {
	return time.Time{}, nil
}
func (m *backfillMockStore) HasForkEvents(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

// --- InsightsStore ---
func (m *backfillMockStore) GetInsightsSummary(_ context.Context, _, _, _ *string, _ int) (*data.InsightsSummary, error) {
	if m.insightsSummary != nil {
		return m.insightsSummary, nil
	}
	return &data.InsightsSummary{}, nil
}
func (m *backfillMockStore) GetDailyActivity(_ context.Context, _, _, _ *string, _ int) (*data.DailyActivitySeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetContributorRetention(_ context.Context, _, _, _ *string, _ int) (*data.RetentionSeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetPRReviewRatio(_ context.Context, _, _, _ *string, _ int) (*data.PRReviewRatioSeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetChangeFailureRate(_ context.Context, _, _, _ *string, _ int) (*data.ChangeFailureRateSeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetReviewLatency(_ context.Context, _, _, _ *string, _ int) (*data.ReviewLatencySeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetTimeToMerge(_ context.Context, _, _, _ *string, _ int) (*data.VelocitySeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetTimeToClose(_ context.Context, _, _, _ *string, _ int) (*data.VelocitySeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetTimeToRestoreBugs(_ context.Context, _, _, _ *string, _ int) (*data.VelocitySeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetPRSizeDistribution(_ context.Context, _, _, _ *string, _ int) (*data.PRSizeSeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetForksAndActivity(_ context.Context, _, _, _ *string, _ int) (*data.ForksAndActivitySeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetContributorFunnel(_ context.Context, _, _, _ *string, _ int) (*data.ContributorFunnelSeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetContributorMomentum(_ context.Context, _, _, _ *string, _ int) (*data.MomentumSeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetContributorProfile(_ context.Context, _ string, _, _, _ *string, _ int) (*data.ContributorProfileSeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetIssueOpenCloseRatio(_ context.Context, _, _, _ *string, _ int) (*data.IssueRatioSeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetTimeToFirstResponse(_ context.Context, _, _, _ *string, _ int) (*data.FirstResponseSeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetAgingPRs(_ context.Context, _, _, _ *string, _ int) (*data.AgingPRsSeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetUnansweredRate(_ context.Context, _, _, _ *string, _ int) (*data.UnansweredSeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetResponseSLO(_ context.Context, _, _, _ *string, _ int) (*data.ResponseSLOSeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetPortfolioSummary(_ context.Context, _, _ *string, _ int) (*data.PortfolioSummary, error) {
	return nil, nil
}
func (m *backfillMockStore) GetSignals(_ context.Context, _ *string, _ int) ([]*data.Signal, error) {
	return nil, nil
}

// --- ReleaseStore ---
func (m *backfillMockStore) ImportReleases(_ context.Context, _, _, _ string) error { return nil }
func (m *backfillMockStore) ImportAllReleases(_ context.Context, _ string) error    { return nil }
func (m *backfillMockStore) GetReleaseCadence(_ context.Context, _, _, _ *string, _ int) (*data.ReleaseCadenceSeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetReleaseDownloads(_ context.Context, _, _ *string, _ int) (*data.ReleaseDownloadsSeries, error) {
	return nil, nil
}
func (m *backfillMockStore) GetReleaseDownloadsByTag(_ context.Context, _, _ *string, _ int) (*data.ReleaseDownloadsByTagSeries, error) {
	return nil, nil
}

// --- ContainerStore ---
func (m *backfillMockStore) ImportContainerVersions(_ context.Context, _, _, _ string) error {
	return nil
}
func (m *backfillMockStore) ImportAllContainerVersions(_ context.Context, _ string) error {
	return nil
}
func (m *backfillMockStore) GetContainerActivity(_ context.Context, _, _ *string, _ int) (*data.ContainerActivitySeries, error) {
	return nil, nil
}

// --- RepoMetaStore ---
func (m *backfillMockStore) ImportRepoMeta(_ context.Context, _, _, _ string) (time.Time, error) {
	return time.Time{}, nil
}
func (m *backfillMockStore) ImportAllRepoMeta(_ context.Context, _ string) error { return nil }
func (m *backfillMockStore) GetRepoMetas(_ context.Context, _, _ *string) ([]*data.RepoMeta, error) {
	return nil, nil
}
func (m *backfillMockStore) GetRepoOverview(_ context.Context, _ *string, _ int) ([]*data.RepoOverview, error) {
	return nil, nil
}

// --- MetricHistoryStore ---
func (m *backfillMockStore) ImportRepoMetricHistory(_ context.Context, _, _, _ string) error {
	return nil
}
func (m *backfillMockStore) ImportAllRepoMetricHistory(_ context.Context, _ string) error {
	return nil
}
func (m *backfillMockStore) GetRepoMetricHistory(_ context.Context, _, _ *string, _ int) ([]*data.RepoMetricHistory, error) {
	return nil, nil
}

// --- ReputationStore ---
func (m *backfillMockStore) ImportReputation(_ context.Context, _, _ *string) (*data.ReputationResult, error) {
	return nil, nil
}
func (m *backfillMockStore) GetContributorComposition(_ context.Context, _, _, _ *string, _ int) (*data.ContributorComposition, error) {
	return nil, nil
}

// --- InsightsGenerationStore ---
func (m *backfillMockStore) GetRepoInsights(_ context.Context, _, _ *string) ([]*data.RepoInsights, error) {
	return nil, nil
}
func (m *backfillMockStore) SaveRepoInsights(_ context.Context, _, _ string, _ *data.RepoInsights) error {
	m.saveInsightsCalled = true
	return nil
}
func (m *backfillMockStore) GetRepoInsightsGeneratedAt(_ context.Context, _, _ string) (string, error) {
	return m.insightsGeneratedAt, nil
}
func (m *backfillMockStore) GetRepoInsightsEventCount(_ context.Context, _, _ string) (int, error) {
	return m.insightsSavedCount, nil
}

func TestRunBackfillPass(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	retryRL := func(fn func() error) error { return fn() }
	tokenFn := func() string { return "test-token" }

	// Default chunk is 7 days, target is 90 days (data.EventAgeDaysDefault).
	// runBackfillPass computes targetDate as time.Now().AddDate(0,0,-90).UTC()
	// so we match that exact order to avoid timezone drift.
	const chunkDays = 7
	now := time.Now()
	targetDate := now.AddDate(0, 0, -data.EventAgeDaysDefault).UTC()

	tests := []struct {
		name             string
		backfillUntil    *time.Time
		wantImportEvents bool
		wantWindowStart  time.Time // approximate, checked with tolerance
		wantWindowEnd    time.Time
		wantSave         bool
		wantSaveValue    time.Time
	}{
		{
			name:             "backfill nil skips",
			backfillUntil:    nil,
			wantImportEvents: false,
			wantSave:         false,
		},
		{
			name:             "backfill at target skips",
			backfillUntil:    timePtr(now.AddDate(0, 0, -data.EventAgeDaysDefault).UTC()),
			wantImportEvents: false,
			wantSave:         false,
		},
		{
			name:             "normal chunk",
			backfillUntil:    timePtr(now.AddDate(0, 0, -21).UTC()),
			wantImportEvents: true,
			wantWindowStart:  now.AddDate(0, 0, -21-chunkDays).UTC(),
			wantWindowEnd:    now.AddDate(0, 0, -21).UTC(),
			wantSave:         true,
			wantSaveValue:    now.AddDate(0, 0, -21-chunkDays).UTC(),
		},
		{
			name:             "final chunk clamped to target",
			backfillUntil:    timePtr(now.AddDate(0, 0, -88).UTC()),
			wantImportEvents: true,
			wantWindowStart:  targetDate, // clamped to 90d, not 95d
			wantWindowEnd:    now.AddDate(0, 0, -88).UTC(),
			wantSave:         true,
			wantSaveValue:    targetDate,
		},
		{
			name:             "chunk within target",
			backfillUntil:    timePtr(now.AddDate(0, 0, -50).UTC()),
			wantImportEvents: true,
			wantWindowStart:  now.AddDate(0, 0, -50-chunkDays).UTC(),
			wantWindowEnd:    now.AddDate(0, 0, -50).UTC(),
			wantSave:         true,
			wantSaveValue:    now.AddDate(0, 0, -50-chunkDays).UTC(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mock := &backfillMockStore{}

			err := runBackfillPass(ctx, mock, retryRL, tokenFn, nil, nil, tokenFn, "test-org", "test-repo", tc.backfillUntil)
			require.NoError(t, err)

			assert.Equal(t, tc.wantImportEvents, mock.importEventsCalled, "ImportEvents called")
			assert.Equal(t, tc.wantSave, mock.saveBackfillCalled, "SaveBackfillUntil called")

			if tc.wantImportEvents {
				assert.WithinDuration(t, tc.wantWindowStart, mock.importEventsWindowStart, 2*time.Second, "window start")
				assert.WithinDuration(t, tc.wantWindowEnd, mock.importEventsWindowEnd, 2*time.Second, "window end")
			}
			if tc.wantSave {
				assert.WithinDuration(t, tc.wantSaveValue, mock.saveBackfillUntil, 2*time.Second, "save value")
			}
		})
	}
}

func timePtr(t time.Time) *time.Time { return &t }
