package data

import (
	"context"
	"io"
	"time"
)

// TokenFunc returns a GitHub API token. Used by methods that make many API
// calls in a loop so they can rotate tokens from a pool on each iteration.
type TokenFunc func() string

// ExhaustFunc marks a token as exhausted (e.g. after hitting a rate limit)
// so the token pool skips it in future rotations.
type ExhaustFunc func(token string)

// StateStore manages import state tracking.
type StateStore interface {
	GetState(ctx context.Context, query, org, repo string, min time.Time) (*State, error)
	HasState(ctx context.Context, org, repo string) (bool, error)
	SaveState(ctx context.Context, query, org, repo string, state *State) error
	ClearState(ctx context.Context, org, repo string) error
	GetDataState(ctx context.Context) (map[string]int64, error)
	GetBackfillUntil(ctx context.Context, org, repo string) (*time.Time, error)
	SaveBackfillUntil(ctx context.Context, org, repo string, until time.Time) error
}

// DeleteStore manages data deletion.
type DeleteStore interface {
	DeleteRepoData(ctx context.Context, org, repo string) (*DeleteResult, error)
}

// SubstitutionStore manages developer substitutions.
type SubstitutionStore interface {
	SaveAndApplyDeveloperSub(ctx context.Context, prop, old, new string) (*Substitution, error)
	ApplySubstitutions(ctx context.Context) ([]*Substitution, error)
}

// EntityStore manages entity lookups and queries.
type EntityStore interface {
	GetEntityLike(ctx context.Context, query string, limit int) ([]*ListItem, error)
	GetEntity(ctx context.Context, val string) (*EntityResult, error)
	QueryEntities(ctx context.Context, val string, limit int) ([]*CountedItem, error)
	CleanEntities(ctx context.Context) error
}

// RepoStore manages repository lookups.
type RepoStore interface {
	GetRepoLike(ctx context.Context, query string, limit int) ([]*ListItem, error)
}

// OrgStore manages organization-level queries.
type OrgStore interface {
	GetAllOrgRepos(ctx context.Context) ([]*OrgRepoItem, error)
	GetDeveloperPercentages(ctx context.Context, entity, org, repo *string, ex []string, days int) ([]*CountedItem, error)
	GetEntityPercentages(ctx context.Context, entity, org, repo *string, ex []string, days int) ([]*CountedItem, error)
	SearchDeveloperUsernames(ctx context.Context, query string, org, repo *string, months, limit int) ([]string, error)
	GetOrgLike(ctx context.Context, query string, limit int) ([]*ListItem, error)
}

// DeveloperStore manages developer records.
type DeveloperStore interface {
	GetDeveloperUsernames(ctx context.Context) ([]string, error)
	GetNoFullnameDeveloperUsernames(ctx context.Context) ([]string, error)
	GetUnenrichedDeveloperUsernames(ctx context.Context) ([]string, error)
	SaveDevelopers(ctx context.Context, devs []*Developer) error
	GetDeveloper(ctx context.Context, username string) (*Developer, error)
	SearchDevelopers(ctx context.Context, val string, limit int) ([]*DeveloperListItem, error)
	UpdateDeveloperNames(ctx context.Context, devs map[string]string) error
	EnrichDeveloperEntities(ctx context.Context, token string) error
}

// QueryStore manages event search and aggregation queries.
type QueryStore interface {
	SearchEvents(ctx context.Context, q *EventSearchCriteria) ([]*EventDetails, error)
	GetMinEventDate(ctx context.Context, org, repo *string) (string, error)
	GetEventTypeSeries(ctx context.Context, org, repo, entity *string, days int) (*EventTypeSeries, error)
}

// EventStore manages event imports.
type EventStore interface {
	ImportEvents(ctx context.Context, tokenFn TokenFunc, exhaustFn ExhaustFunc, owner, repo string, days int) (map[string]int, *ImportSummary, error)
	UpdateEvents(ctx context.Context, token string, concurrency int) (map[string]int, error)
	GetMaxEventTime(ctx context.Context, org, repo string) (time.Time, error)
}

// InsightsStore provides analytics and insights queries.
type InsightsStore interface {
	GetInsightsSummary(ctx context.Context, org, repo, entity *string, days int) (*InsightsSummary, error)
	GetDailyActivity(ctx context.Context, org, repo, entity *string, days int) (*DailyActivitySeries, error)
	GetContributorRetention(ctx context.Context, org, repo, entity *string, days int) (*RetentionSeries, error)
	GetPRReviewRatio(ctx context.Context, org, repo, entity *string, days int) (*PRReviewRatioSeries, error)
	GetChangeFailureRate(ctx context.Context, org, repo, entity *string, days int) (*ChangeFailureRateSeries, error)
	GetReviewLatency(ctx context.Context, org, repo, entity *string, days int) (*ReviewLatencySeries, error)
	GetTimeToMerge(ctx context.Context, org, repo, entity *string, days int) (*VelocitySeries, error)
	GetTimeToClose(ctx context.Context, org, repo, entity *string, days int) (*VelocitySeries, error)
	GetTimeToRestoreBugs(ctx context.Context, org, repo, entity *string, days int) (*VelocitySeries, error)
	GetPRSizeDistribution(ctx context.Context, org, repo, entity *string, days int) (*PRSizeSeries, error)
	GetForksAndActivity(ctx context.Context, org, repo, entity *string, days int) (*ForksAndActivitySeries, error)
	GetContributorFunnel(ctx context.Context, org, repo, entity *string, days int) (*ContributorFunnelSeries, error)
	GetContributorMomentum(ctx context.Context, org, repo, entity *string, days int) (*MomentumSeries, error)
	GetContributorProfile(ctx context.Context, username string, org, repo, entity *string, days int) (*ContributorProfileSeries, error)
	GetIssueOpenCloseRatio(ctx context.Context, org, repo, entity *string, days int) (*IssueRatioSeries, error)
	GetTimeToFirstResponse(ctx context.Context, org, repo, entity *string, days int) (*FirstResponseSeries, error)
	GetAgingPRs(ctx context.Context, org, repo, entity *string, days int) (*AgingPRsSeries, error)
	GetUnansweredRate(ctx context.Context, org, repo, entity *string, days int) (*UnansweredSeries, error)
	GetResponseSLO(ctx context.Context, org, repo, entity *string, days int) (*ResponseSLOSeries, error)
	GetPortfolioSummary(ctx context.Context, org, repo *string, days int) (*PortfolioSummary, error)
	GetSignals(ctx context.Context, org *string, limit int) ([]*Signal, error)
}

// ReleaseStore manages release imports and queries.
type ReleaseStore interface {
	ImportReleases(ctx context.Context, token, owner, repo string) error
	ImportAllReleases(ctx context.Context, token string) error
	GetReleaseCadence(ctx context.Context, org, repo, entity *string, days int) (*ReleaseCadenceSeries, error)
	GetReleaseDownloads(ctx context.Context, org, repo *string, days int) (*ReleaseDownloadsSeries, error)
	GetReleaseDownloadsByTag(ctx context.Context, org, repo *string, days int) (*ReleaseDownloadsByTagSeries, error)
}

// ContainerStore manages container version imports and queries.
type ContainerStore interface {
	ImportContainerVersions(ctx context.Context, token, org, repo string) error
	ImportAllContainerVersions(ctx context.Context, token string) error
	GetContainerActivity(ctx context.Context, org, repo *string, days int) (*ContainerActivitySeries, error)
}

// RepoMetaStore manages repository metadata imports and queries.
type RepoMetaStore interface {
	ImportRepoMeta(ctx context.Context, token, owner, repo string) (time.Time, error)
	ImportAllRepoMeta(ctx context.Context, token string) error
	GetRepoMetas(ctx context.Context, org, repo *string) ([]*RepoMeta, error)
	GetRepoOverview(ctx context.Context, org *string, days int) ([]*RepoOverview, error)
}

// MetricHistoryStore manages repository metric history imports and queries.
type MetricHistoryStore interface {
	ImportRepoMetricHistory(ctx context.Context, token, owner, repo string) error
	ImportAllRepoMetricHistory(ctx context.Context, token string) error
	GetRepoMetricHistory(ctx context.Context, org, repo *string, days int) ([]*RepoMetricHistory, error)
}

// ReputationStore manages reputation scoring.
type ReputationStore interface {
	ImportReputation(ctx context.Context, org, repo *string) (*ReputationResult, error)
	ImportDeepReputation(ctx context.Context, tokenFn TokenFunc, exhaustFn ExhaustFunc, limit, staleHours int, org, repo *string) (*DeepReputationResult, error)
	GetOrComputeDeepReputation(ctx context.Context, token, username string) (*UserReputation, error)
	ComputeDeepReputation(ctx context.Context, token, username string) (*UserReputation, error)
	GetReputationComposition(ctx context.Context, org, repo, entity *string, days int) (*ReputationComposition, error)
}

// InsightsGenerationStore manages LLM-generated repo insights.
type InsightsGenerationStore interface {
	GetRepoInsights(ctx context.Context, org, repo *string) ([]*RepoInsights, error)
	SaveRepoInsights(ctx context.Context, org, repo string, insights *RepoInsights) error
	GetRepoInsightsGeneratedAt(ctx context.Context, org, repo string) (string, error)
	GetRepoInsightsEventCount(ctx context.Context, org, repo string) (int, error)
}

// Store is the top-level interface composing all sub-interfaces.
type Store interface {
	io.Closer
	StateStore
	DeleteStore
	SubstitutionStore
	EntityStore
	RepoStore
	OrgStore
	DeveloperStore
	QueryStore
	EventStore
	InsightsStore
	ReleaseStore
	ContainerStore
	RepoMetaStore
	MetricHistoryStore
	ReputationStore
	InsightsGenerationStore
}
