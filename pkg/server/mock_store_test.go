package server

import (
	"context"
	"time"

	"github.com/thingzio/devpulse/pkg/data"
)

// mockStore satisfies data.Store via zero-returning stubs for every method.
// Override individual function fields per-test to control behavior.
type mockStore struct {
	// InsightsStore overrides
	getInsightsSummaryFn        func(ctx context.Context, org, repo, entity *string, days int) (*data.InsightsSummary, error)
	getRepoMetricHistoryFn      func(ctx context.Context, org, repo *string, days int) ([]*data.RepoMetricHistory, error)
	getRepoInsightsFn           func(ctx context.Context, org, repo *string) ([]*data.RepoInsights, error)
	getPortfolioSummaryFn       func(ctx context.Context, org, repo *string, days int) (*data.PortfolioSummary, error)
	getEventTypeSeriesFn        func(ctx context.Context, org, repo, entity *string, days int) (*data.EventTypeSeries, error)
	getDeveloperPercentagesFn   func(ctx context.Context, entity, org, repo *string, ex []string, days int) ([]*data.CountedItem, error)
	getReputationDistributionFn func(ctx context.Context, org, repo, entity *string, days int) (*data.ReputationDistribution, error)
	searchEventsFn              func(ctx context.Context, q *data.EventSearchCriteria) ([]*data.EventDetails, error)
	getRepoInsightsEventCountFn func(ctx context.Context, org, repo string) (int, error)
}

// Close implements io.Closer.
func (m *mockStore) Close() error { return nil }

// --- StateStore ---
func (m *mockStore) GetState(_ context.Context, _, _, _ string, _ time.Time) (*data.State, error) {
	return nil, nil
}
func (m *mockStore) SaveState(_ context.Context, _, _, _ string, _ *data.State) error { return nil }
func (m *mockStore) ClearState(_ context.Context, _, _ string) error                  { return nil }
func (m *mockStore) GetDataState(_ context.Context) (map[string]int64, error)         { return nil, nil }

// --- DeleteStore ---
func (m *mockStore) DeleteRepoData(_ context.Context, _, _ string) (*data.DeleteResult, error) {
	return nil, nil
}

// --- SubstitutionStore ---
func (m *mockStore) SaveAndApplyDeveloperSub(_ context.Context, _, _, _ string) (*data.Substitution, error) {
	return nil, nil
}
func (m *mockStore) ApplySubstitutions(_ context.Context) ([]*data.Substitution, error) {
	return nil, nil
}

// --- EntityStore ---
func (m *mockStore) GetEntityLike(_ context.Context, _ string, _ int) ([]*data.ListItem, error) {
	return nil, nil
}
func (m *mockStore) GetEntity(_ context.Context, _ string) (*data.EntityResult, error) {
	return nil, nil
}
func (m *mockStore) QueryEntities(_ context.Context, _ string, _ int) ([]*data.CountedItem, error) {
	return nil, nil
}
func (m *mockStore) CleanEntities(_ context.Context) error { return nil }

// --- RepoStore ---
func (m *mockStore) GetRepoLike(_ context.Context, _ string, _ int) ([]*data.ListItem, error) {
	return nil, nil
}

// --- OrgStore ---
func (m *mockStore) GetAllOrgRepos(_ context.Context) ([]*data.OrgRepoItem, error) { return nil, nil }
func (m *mockStore) GetDeveloperPercentages(ctx context.Context, entity, org, repo *string, ex []string, days int) ([]*data.CountedItem, error) {
	if m.getDeveloperPercentagesFn != nil {
		return m.getDeveloperPercentagesFn(ctx, entity, org, repo, ex, days)
	}
	return nil, nil
}
func (m *mockStore) GetEntityPercentages(_ context.Context, _, _, _ *string, _ []string, _ int) ([]*data.CountedItem, error) {
	return nil, nil
}
func (m *mockStore) SearchDeveloperUsernames(_ context.Context, _ string, _, _ *string, _, _ int) ([]string, error) {
	return nil, nil
}
func (m *mockStore) GetOrgLike(_ context.Context, _ string, _ int) ([]*data.ListItem, error) {
	return nil, nil
}

// --- DeveloperStore ---
func (m *mockStore) GetDeveloperUsernames(_ context.Context) ([]string, error) { return nil, nil }
func (m *mockStore) GetNoFullnameDeveloperUsernames(_ context.Context) ([]string, error) {
	return nil, nil
}
func (m *mockStore) GetUnenrichedDeveloperUsernames(_ context.Context) ([]string, error) {
	return nil, nil
}
func (m *mockStore) SaveDevelopers(_ context.Context, _ []*data.Developer) error { return nil }
func (m *mockStore) GetDeveloper(_ context.Context, _ string) (*data.Developer, error) {
	return nil, nil
}
func (m *mockStore) SearchDevelopers(_ context.Context, _ string, _ int) ([]*data.DeveloperListItem, error) {
	return nil, nil
}
func (m *mockStore) UpdateDeveloperNames(_ context.Context, _ map[string]string) error { return nil }
func (m *mockStore) EnrichDeveloperEntities(_ context.Context, _ string) error         { return nil }

// --- QueryStore ---
func (m *mockStore) SearchEvents(ctx context.Context, q *data.EventSearchCriteria) ([]*data.EventDetails, error) {
	if m.searchEventsFn != nil {
		return m.searchEventsFn(ctx, q)
	}
	return nil, nil
}
func (m *mockStore) GetMinEventDate(_ context.Context, _, _ *string) (string, error) { return "", nil }
func (m *mockStore) GetEventTypeSeries(ctx context.Context, org, repo, entity *string, days int) (*data.EventTypeSeries, error) {
	if m.getEventTypeSeriesFn != nil {
		return m.getEventTypeSeriesFn(ctx, org, repo, entity, days)
	}
	return nil, nil
}

// --- EventStore ---
func (m *mockStore) ImportEvents(_ context.Context, _, _, _ string, _ int) (map[string]int, *data.ImportSummary, error) {
	return nil, nil, nil
}
func (m *mockStore) UpdateEvents(_ context.Context, _ string, _ int) (map[string]int, error) {
	return nil, nil
}

// --- InsightsStore ---
func (m *mockStore) GetInsightsSummary(ctx context.Context, org, repo, entity *string, days int) (*data.InsightsSummary, error) {
	if m.getInsightsSummaryFn != nil {
		return m.getInsightsSummaryFn(ctx, org, repo, entity, days)
	}
	return nil, nil
}
func (m *mockStore) GetDailyActivity(_ context.Context, _, _, _ *string, _ int) (*data.DailyActivitySeries, error) {
	return nil, nil
}
func (m *mockStore) GetContributorRetention(_ context.Context, _, _, _ *string, _ int) (*data.RetentionSeries, error) {
	return nil, nil
}
func (m *mockStore) GetPRReviewRatio(_ context.Context, _, _, _ *string, _ int) (*data.PRReviewRatioSeries, error) {
	return nil, nil
}
func (m *mockStore) GetChangeFailureRate(_ context.Context, _, _, _ *string, _ int) (*data.ChangeFailureRateSeries, error) {
	return nil, nil
}
func (m *mockStore) GetReviewLatency(_ context.Context, _, _, _ *string, _ int) (*data.ReviewLatencySeries, error) {
	return nil, nil
}
func (m *mockStore) GetTimeToMerge(_ context.Context, _, _, _ *string, _ int) (*data.VelocitySeries, error) {
	return nil, nil
}
func (m *mockStore) GetTimeToClose(_ context.Context, _, _, _ *string, _ int) (*data.VelocitySeries, error) {
	return nil, nil
}
func (m *mockStore) GetTimeToRestoreBugs(_ context.Context, _, _, _ *string, _ int) (*data.VelocitySeries, error) {
	return nil, nil
}
func (m *mockStore) GetPRSizeDistribution(_ context.Context, _, _, _ *string, _ int) (*data.PRSizeSeries, error) {
	return nil, nil
}
func (m *mockStore) GetForksAndActivity(_ context.Context, _, _, _ *string, _ int) (*data.ForksAndActivitySeries, error) {
	return nil, nil
}
func (m *mockStore) GetContributorFunnel(_ context.Context, _, _, _ *string, _ int) (*data.ContributorFunnelSeries, error) {
	return nil, nil
}
func (m *mockStore) GetContributorMomentum(_ context.Context, _, _, _ *string, _ int) (*data.MomentumSeries, error) {
	return nil, nil
}
func (m *mockStore) GetContributorProfile(_ context.Context, _ string, _, _, _ *string, _ int) (*data.ContributorProfileSeries, error) {
	return nil, nil
}
func (m *mockStore) GetIssueOpenCloseRatio(_ context.Context, _, _, _ *string, _ int) (*data.IssueRatioSeries, error) {
	return nil, nil
}
func (m *mockStore) GetTimeToFirstResponse(_ context.Context, _, _, _ *string, _ int) (*data.FirstResponseSeries, error) {
	return nil, nil
}
func (m *mockStore) GetAgingPRs(_ context.Context, _, _, _ *string, _ int) (*data.AgingPRsSeries, error) {
	return nil, nil
}
func (m *mockStore) GetUnansweredRate(_ context.Context, _, _, _ *string, _ int) (*data.UnansweredSeries, error) {
	return nil, nil
}
func (m *mockStore) GetResponseSLO(_ context.Context, _, _, _ *string, _ int) (*data.ResponseSLOSeries, error) {
	return nil, nil
}
func (m *mockStore) GetPortfolioSummary(ctx context.Context, org, repo *string, days int) (*data.PortfolioSummary, error) {
	if m.getPortfolioSummaryFn != nil {
		return m.getPortfolioSummaryFn(ctx, org, repo, days)
	}
	return nil, nil
}
func (m *mockStore) GetSignals(_ context.Context, _ *string, _ int) ([]*data.Signal, error) {
	return nil, nil
}

// --- ReleaseStore ---
func (m *mockStore) ImportReleases(_ context.Context, _, _, _ string) error { return nil }
func (m *mockStore) ImportAllReleases(_ context.Context, _ string) error    { return nil }
func (m *mockStore) GetReleaseCadence(_ context.Context, _, _, _ *string, _ int) (*data.ReleaseCadenceSeries, error) {
	return nil, nil
}
func (m *mockStore) GetReleaseDownloads(_ context.Context, _, _ *string, _ int) (*data.ReleaseDownloadsSeries, error) {
	return nil, nil
}
func (m *mockStore) GetReleaseDownloadsByTag(_ context.Context, _, _ *string, _ int) (*data.ReleaseDownloadsByTagSeries, error) {
	return nil, nil
}

// --- ContainerStore ---
func (m *mockStore) ImportContainerVersions(_ context.Context, _, _, _ string) error { return nil }
func (m *mockStore) ImportAllContainerVersions(_ context.Context, _ string) error    { return nil }
func (m *mockStore) GetContainerActivity(_ context.Context, _, _ *string, _ int) (*data.ContainerActivitySeries, error) {
	return nil, nil
}

// --- RepoMetaStore ---
func (m *mockStore) ImportRepoMeta(_ context.Context, _, _, _ string) (time.Time, error) {
	return time.Time{}, nil
}
func (m *mockStore) ImportAllRepoMeta(_ context.Context, _ string) error { return nil }
func (m *mockStore) GetRepoMetas(_ context.Context, _, _ *string) ([]*data.RepoMeta, error) {
	return nil, nil
}
func (m *mockStore) GetRepoOverview(_ context.Context, _ *string, _ int) ([]*data.RepoOverview, error) {
	return nil, nil
}

// --- MetricHistoryStore ---
func (m *mockStore) ImportRepoMetricHistory(_ context.Context, _, _, _ string) error { return nil }
func (m *mockStore) ImportAllRepoMetricHistory(_ context.Context, _ string) error    { return nil }
func (m *mockStore) GetRepoMetricHistory(ctx context.Context, org, repo *string, days int) ([]*data.RepoMetricHistory, error) {
	if m.getRepoMetricHistoryFn != nil {
		return m.getRepoMetricHistoryFn(ctx, org, repo, days)
	}
	return nil, nil
}

// --- ReputationStore ---
func (m *mockStore) ImportReputation(_ context.Context, _, _ *string) (*data.ReputationResult, error) {
	return nil, nil
}
func (m *mockStore) ImportDeepReputation(_ context.Context, _ data.TokenFunc, _ data.ExhaustFunc, _, _ int, _, _ *string) (*data.DeepReputationResult, error) {
	return nil, nil
}
func (m *mockStore) GetOrComputeDeepReputation(_ context.Context, _, _ string) (*data.UserReputation, error) {
	return nil, nil
}
func (m *mockStore) ComputeDeepReputation(_ context.Context, _, _ string) (*data.UserReputation, error) {
	return nil, nil
}
func (m *mockStore) GetReputationDistribution(ctx context.Context, org, repo, entity *string, days int) (*data.ReputationDistribution, error) {
	if m.getReputationDistributionFn != nil {
		return m.getReputationDistributionFn(ctx, org, repo, entity, days)
	}
	return nil, nil
}

// --- InsightsGenerationStore ---
func (m *mockStore) GetRepoInsights(ctx context.Context, org, repo *string) ([]*data.RepoInsights, error) {
	if m.getRepoInsightsFn != nil {
		return m.getRepoInsightsFn(ctx, org, repo)
	}
	return nil, nil
}
func (m *mockStore) SaveRepoInsights(_ context.Context, _, _ string, _ *data.RepoInsights) error {
	return nil
}
func (m *mockStore) GetRepoInsightsGeneratedAt(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (m *mockStore) GetRepoInsightsEventCount(ctx context.Context, org, repo string) (int, error) {
	if m.getRepoInsightsEventCountFn != nil {
		return m.getRepoInsightsEventCountFn(ctx, org, repo)
	}
	return 0, nil
}
