package data

import (
	"encoding/json"
	"time"
)

// ---------------------------------------------------------------------------
// Constants and configuration
// ---------------------------------------------------------------------------

const (
	EventAgeDaysDefault int = 90

	EventTypePR           string = "pr"
	EventTypePRReview     string = "pr_review"
	EventTypeIssue        string = "issue"
	EventTypeIssueComment string = "issue_comment"
	EventTypeFork         string = "fork"
)

// UpdatableProperties lists developer fields that can be substituted.
var UpdatableProperties = []string{
	"entity",
}

// ---------------------------------------------------------------------------
// Database state types
// ---------------------------------------------------------------------------

type State struct {
	Since time.Time `json:"since" yaml:"since"`
	Page  int       `json:"page" yaml:"page"`
}

type DeleteResult struct {
	Org           string `json:"org" yaml:"org"`
	Repo          string `json:"repo" yaml:"repo"`
	Events        int64  `json:"events" yaml:"events"`
	RepoMeta      int64  `json:"repo_meta" yaml:"repo_meta"`
	Releases      int64  `json:"releases" yaml:"releases"`
	ReleaseAssets int64  `json:"release_assets" yaml:"release_assets"`
	State         int64  `json:"state" yaml:"state"`
}

// ---------------------------------------------------------------------------
// Substitution types
// ---------------------------------------------------------------------------

type Substitution struct {
	Prop    string `json:"prop" yaml:"prop"`
	Old     string `json:"old" yaml:"old"`
	New     string `json:"new" yaml:"new"`
	Records int64  `json:"records" yaml:"records"`
}

// ---------------------------------------------------------------------------
// Entity types
// ---------------------------------------------------------------------------

type EntityResult struct {
	Entity         string               `json:"entity,omitempty" yaml:"entity,omitempty"`
	DeveloperCount int                  `json:"developer_count,omitempty" yaml:"developerCount,omitempty"`
	Developers     []*DeveloperListItem `json:"developers,omitempty" yaml:"developers,omitempty"`
}

// ---------------------------------------------------------------------------
// Repo and org types
// ---------------------------------------------------------------------------

type CountedItem struct {
	Name  string `json:"name" yaml:"name"`
	Count int    `json:"count,omitempty" yaml:"count,omitempty"`
}

type ListItem struct {
	Value string `json:"value,omitempty" yaml:"value,omitempty"`
	Text  string `json:"text,omitempty" yaml:"text,omitempty"`
	Type  string `json:"type,omitempty" yaml:"type,omitempty"`
}

type Org struct {
	URL         string `json:"url,omitempty" yaml:"url,omitempty"`
	Name        string `json:"name,omitempty" yaml:"name,omitempty"`
	Company     string `json:"company,omitempty" yaml:"company,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

type OrgRepoItem struct {
	Org  string `json:"org,omitempty" yaml:"org,omitempty"`
	Repo string `json:"repo,omitempty" yaml:"repo,omitempty"`
}

// ---------------------------------------------------------------------------
// Developer types
// ---------------------------------------------------------------------------

type Developer struct {
	Username      string `json:"username,omitempty" yaml:"username,omitempty"`
	FullName      string `json:"full_name,omitempty" yaml:"fullName,omitempty"`
	Email         string `json:"email,omitempty" yaml:"email,omitempty"`
	AvatarURL     string `json:"avatar,omitempty" yaml:"avatar,omitempty"`
	ProfileURL    string `json:"url,omitempty" yaml:"url,omitempty"`
	Entity        string `json:"entity,omitempty" yaml:"entity,omitempty"`
	Organizations []*Org `json:"organizations,omitempty" yaml:"organizations,omitempty"`
}

type DeveloperListItem struct {
	Username string `json:"username,omitempty" yaml:"username,omitempty"`
	Entity   string `json:"entity,omitempty" yaml:"entity,omitempty"`
}

// ---------------------------------------------------------------------------
// Event types
// ---------------------------------------------------------------------------

type Event struct {
	Org          string  `json:"org,omitempty" yaml:"org,omitempty"`
	Repo         string  `json:"repo,omitempty" yaml:"repo,omitempty"`
	Username     string  `json:"username,omitempty" yaml:"username,omitempty"`
	Type         string  `json:"type,omitempty" yaml:"type,omitempty"`
	Date         string  `json:"date,omitempty" yaml:"date,omitempty"`
	URL          string  `json:"url,omitempty" yaml:"url,omitempty"`
	Mentions     string  `json:"mentions,omitempty" yaml:"mentions,omitempty"`
	Labels       string  `json:"labels,omitempty" yaml:"labels,omitempty"`
	State        *string `json:"state,omitempty" yaml:"state,omitempty"`
	Number       *int    `json:"number,omitempty" yaml:"number,omitempty"`
	CreatedAt    *string `json:"created_at,omitempty" yaml:"createdAt,omitempty"`
	ClosedAt     *string `json:"closed_at,omitempty" yaml:"closedAt,omitempty"`
	MergedAt     *string `json:"merged_at,omitempty" yaml:"mergedAt,omitempty"`
	Additions    *int    `json:"additions,omitempty" yaml:"additions,omitempty"`
	Deletions    *int    `json:"deletions,omitempty" yaml:"deletions,omitempty"`
	ChangedFiles *int    `json:"changed_files,omitempty" yaml:"changed_files,omitempty"`
	Commits      *int    `json:"commits,omitempty" yaml:"commits,omitempty"`
	Title        string  `json:"title,omitempty" yaml:"title,omitempty"`
}

// ImportSummary contains per-repo import metadata.
type ImportSummary struct {
	Repo       string `json:"repo" yaml:"repo"`
	Since      string `json:"since" yaml:"since"`
	Events     int    `json:"events" yaml:"events"`
	Developers int    `json:"developers" yaml:"developers"`
}

// ---------------------------------------------------------------------------
// Event query types
// ---------------------------------------------------------------------------

type EventTypeSeries struct {
	Dates         []string  `json:"dates" yaml:"dates"`
	PRs           []int     `json:"pr" yaml:"pr"`
	PRReviews     []int     `json:"pr_review" yaml:"prReview"`
	Issues        []int     `json:"issue" yaml:"issue"`
	IssueComments []int     `json:"issue_comment" yaml:"issueComment"`
	Forks         []int     `json:"fork" yaml:"fork"`
	Total         []int     `json:"total" yaml:"total"`
	Trend         []float32 `json:"trend" yaml:"trend"`
}

type EventDetails struct {
	Event     *Event     `json:"event,omitempty" yaml:"event,omitempty"`
	Developer *Developer `json:"developer,omitempty" yaml:"developer,omitempty"`
}

type EventSearchCriteria struct {
	FromDate *string `json:"from,omitempty" yaml:"from,omitempty"`
	ToDate   *string `json:"to,omitempty" yaml:"to,omitempty"`
	Type     *string `json:"type,omitempty" yaml:"type,omitempty"`
	Org      *string `json:"org,omitempty" yaml:"org,omitempty"`
	Repo     *string `json:"repo,omitempty" yaml:"repo,omitempty"`
	Username *string `json:"user,omitempty" yaml:"user,omitempty"`
	Entity   *string `json:"entity,omitempty" yaml:"entity,omitempty"`
	Mention  *string `json:"mention,omitempty" yaml:"mention,omitempty"`
	Label    *string `json:"label,omitempty" yaml:"label,omitempty"`
	Page     int     `json:"page,omitempty" yaml:"page,omitempty"`
	PageSize int     `json:"page_size,omitempty" yaml:"pageSize,omitempty"`
}

func (c EventSearchCriteria) String() string {
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Insights series types
// ---------------------------------------------------------------------------

type InsightsSummary struct {
	BusFactor    int    `json:"bus_factor" yaml:"busFactor"`
	PonyFactor   int    `json:"pony_factor" yaml:"ponyFactor"`
	Orgs         int    `json:"orgs" yaml:"orgs"`
	Repos        int    `json:"repos" yaml:"repos"`
	Events       int    `json:"events" yaml:"events"`
	Contributors int    `json:"contributors" yaml:"contributors"`
	LastImport   string `json:"last_import" yaml:"lastImport"`
}

type DailyActivitySeries struct {
	Dates  []string `json:"dates"`
	Counts []int    `json:"counts"`
}

type VelocitySeries struct {
	Labels  []string  `json:"labels" yaml:"labels"`
	Count   []int     `json:"count" yaml:"count"`
	AvgDays []float64 `json:"avg_days" yaml:"avgDays"`
}

type IssueRatioSeries struct {
	Labels []string `json:"labels" yaml:"labels"`
	Opened []int    `json:"opened" yaml:"opened"`
	Closed []int    `json:"closed" yaml:"closed"`
}

type FirstResponseSeries struct {
	Labels   []string  `json:"labels" yaml:"labels"`
	IssueAvg []float64 `json:"issue_avg" yaml:"issueAvg"`
	PRAvg    []float64 `json:"pr_avg" yaml:"prAvg"`
}

// AgingPRsSeries holds counts of open PRs by age bucket.
type AgingPRsSeries struct {
	TotalOpen  int     `json:"total_open"`
	Over30Days int     `json:"over_30_days"`
	Over90Days int     `json:"over_90_days"`
	AgingPct   float64 `json:"aging_pct"`
}

// UnansweredSeries holds counts of unanswered issues and PRs.
type UnansweredSeries struct {
	TotalItems    int     `json:"total_items"`
	Unanswered    int     `json:"unanswered"`
	UnansweredPct float64 `json:"unanswered_pct"`
}

// ResponseSLOSeries tracks percentage of issues/PRs responded to within a threshold.
type ResponseSLOSeries struct {
	TotalItems    int     `json:"total_items"`
	WithinSLO     int     `json:"within_slo"`
	WithinSLOPct  float64 `json:"within_slo_pct"`
	SLOThresholdH int     `json:"slo_threshold_hours"`
}

// HealthCategory is a single scored category in the health scorecard.
type HealthCategory struct {
	Name    string         `json:"name"`
	Grade   string         `json:"grade"`
	Score   float64        `json:"score"`
	Metrics map[string]any `json:"metrics"`
}

// HealthScorecard is the composite health assessment.
type HealthScorecard struct {
	Overall        string         `json:"overall"`
	OverallScore   float64        `json:"overall_score"`
	Demand         HealthCategory `json:"demand"`
	Throughput     HealthCategory `json:"throughput"`
	Responsiveness HealthCategory `json:"responsiveness"`
}

// PortfolioSummary holds aggregated KPIs across all repos.
type PortfolioSummary struct {
	TotalStars        int     `json:"total_stars"`
	TotalForks        int     `json:"total_forks"`
	TotalOpenIssues   int     `json:"total_open_issues"`
	TotalClosedPRs    int     `json:"total_closed_prs"`
	TotalContributors int     `json:"total_contributors"`
	StarsDelta        int     `json:"stars_delta"`
	StarsDeltaPct     float64 `json:"stars_delta_pct"`
	ForksDelta        int     `json:"forks_delta"`
	ForksDeltaPct     float64 `json:"forks_delta_pct"`
	AvgMergeHours     float64 `json:"avg_merge_hours"`
	MedianMergeHours  float64 `json:"median_merge_hours"`
}

// Signal represents a notable week-over-week change detected in the data.
type Signal struct {
	Org      string  `json:"org"`
	Repo     string  `json:"repo"`
	Metric   string  `json:"metric"`
	Message  string  `json:"message"`
	Delta    int     `json:"delta"`
	DeltaPct float64 `json:"delta_pct"`
	Severity string  `json:"severity"`
}

type RetentionSeries struct {
	Labels    []string `json:"labels" yaml:"labels"`
	New       []int    `json:"new" yaml:"new"`
	Returning []int    `json:"returning" yaml:"returning"`
}

type PRReviewRatioSeries struct {
	Labels  []string  `json:"labels" yaml:"labels"`
	PRs     []int     `json:"prs" yaml:"prs"`
	Reviews []int     `json:"reviews" yaml:"reviews"`
	Ratio   []float64 `json:"ratio" yaml:"ratio"`
}

type ChangeFailureRateSeries struct {
	Labels      []string  `json:"labels" yaml:"labels"`
	Failures    []int     `json:"failures" yaml:"failures"`
	Deployments []int     `json:"deployments" yaml:"deployments"`
	Rate        []float64 `json:"rate" yaml:"rate"`
}

type ReviewLatencySeries struct {
	Labels   []string  `json:"labels" yaml:"labels"`
	Count    []int     `json:"count" yaml:"count"`
	AvgHours []float64 `json:"avg_hours" yaml:"avgHours"`
}

type PRSizeSeries struct {
	Labels []string `json:"labels" yaml:"labels"`
	Small  []int    `json:"small" yaml:"small"`
	Medium []int    `json:"medium" yaml:"medium"`
	Large  []int    `json:"large" yaml:"large"`
	XLarge []int    `json:"xlarge" yaml:"xlarge"`
}

type MomentumSeries struct {
	Labels []string `json:"labels" yaml:"labels"`
	Active []int    `json:"active" yaml:"active"`
	Delta  []int    `json:"delta" yaml:"delta"`
}

type ForksAndActivitySeries struct {
	Labels []string `json:"labels" yaml:"labels"`
	Forks  []int    `json:"forks" yaml:"forks"`
	Events []int    `json:"events" yaml:"events"`
}

type ContributorFunnelSeries struct {
	Labels       []string `json:"labels" yaml:"labels"`
	FirstComment []int    `json:"first_comment" yaml:"firstComment"`
	FirstPR      []int    `json:"first_pr" yaml:"firstPR"`
	FirstMerge   []int    `json:"first_merge" yaml:"firstMerge"`
}

type ContributorProfileSeries struct {
	Metrics    []string  `json:"metrics"`
	Values     []int     `json:"values"`
	Averages   []float64 `json:"averages"`
	Reputation *float64  `json:"reputation,omitempty"`
}

// ---------------------------------------------------------------------------
// Release series types
// ---------------------------------------------------------------------------

type ReleaseCadenceSeries struct {
	Labels      []string `json:"labels" yaml:"labels"`
	Total       []int    `json:"total" yaml:"total"`
	Stable      []int    `json:"stable" yaml:"stable"`
	Deployments []int    `json:"deployments" yaml:"deployments"`
}

type ReleaseDownloadsSeries struct {
	Labels    []string `json:"labels" yaml:"labels"`
	Downloads []int    `json:"downloads" yaml:"downloads"`
}

type ReleaseDownloadsByTagSeries struct {
	Tags      []string `json:"tags" yaml:"tags"`
	Downloads []int    `json:"downloads" yaml:"downloads"`
}

// ---------------------------------------------------------------------------
// Container series types
// ---------------------------------------------------------------------------

// ContainerActivitySeries is the chart data for container version publishes per month.
type ContainerActivitySeries struct {
	Labels   []string `json:"labels" yaml:"labels"`
	Versions []int    `json:"versions" yaml:"versions"`
}

// ---------------------------------------------------------------------------
// Reputation types
// ---------------------------------------------------------------------------

// ReputationResult is returned by the shallow bulk import.
type ReputationResult struct {
	Updated int `json:"updated" yaml:"updated"`
	Skipped int `json:"skipped" yaml:"skipped"`
	Errors  int `json:"errors" yaml:"errors"`
}

// DeepReputationResult is returned by the bulk deep scoring step.
type DeepReputationResult struct {
	Scored  int `json:"scored" yaml:"scored"`
	Skipped int `json:"skipped" yaml:"skipped"`
	Errors  int `json:"errors" yaml:"errors"`
}

// ReputationComposition is the dashboard chart data for repo-wide reputation.
type ReputationComposition struct {
	Alert          int `json:"alert" yaml:"alert"`
	Standard       int `json:"standard" yaml:"standard"`
	HighConfidence int `json:"high_confidence" yaml:"high_confidence"`
	Deep           int `json:"deep" yaml:"deep"`
	Scored         int `json:"scored" yaml:"scored"`
	Total          int `json:"total" yaml:"total"`
}

// UserReputation is returned by the on-demand deep score endpoint.
type UserReputation struct {
	Username   string         `json:"username" yaml:"username"`
	Reputation float64        `json:"reputation" yaml:"reputation"`
	Deep       bool           `json:"deep" yaml:"deep"`
	Signals    *SignalSummary `json:"signals,omitempty" yaml:"signals,omitempty"`
}

// SignalSummary exposes gathered signals to the UI.
type SignalSummary struct {
	AgeDays           int64  `json:"age_days" yaml:"ageDays"`
	Followers         int64  `json:"followers" yaml:"followers"`
	Following         int64  `json:"following" yaml:"following"`
	PublicRepos       int64  `json:"public_repos" yaml:"publicRepos"`
	Suspended         bool   `json:"suspended" yaml:"suspended"`
	OrgMember         bool   `json:"org_member" yaml:"orgMember"`
	Commits           int64  `json:"commits" yaml:"commits"`
	TotalCommits      int64  `json:"total_commits" yaml:"totalCommits"`
	TotalContributors int    `json:"total_contributors" yaml:"totalContributors"`
	LastCommitDays    int64  `json:"last_commit_days" yaml:"lastCommitDays"`
	AuthorAssociation string `json:"author_association" yaml:"authorAssociation"`
	HasBio            bool   `json:"has_bio" yaml:"hasBio"`
	HasCompany        bool   `json:"has_company" yaml:"hasCompany"`
	HasLocation       bool   `json:"has_location" yaml:"hasLocation"`
	HasWebsite        bool   `json:"has_website" yaml:"hasWebsite"`
	PRsMerged         int64  `json:"prs_merged" yaml:"prsMerged"`
	PRsClosed         int64  `json:"prs_closed" yaml:"prsClosed"`
	RecentPRRepoCount int64  `json:"recent_pr_repo_count" yaml:"recentPRRepoCount"`
	ForkedRepos       int64  `json:"forked_repos" yaml:"forkedRepos"`
	TrustedOrgMember  bool   `json:"trusted_org_member" yaml:"trustedOrgMember"`
}

// ---------------------------------------------------------------------------
// Repo metric history types
// ---------------------------------------------------------------------------

type RepoMetricHistory struct {
	Org   string `json:"org"`
	Repo  string `json:"repo"`
	Date  string `json:"date"`
	Stars int    `json:"stars"`
	Forks int    `json:"forks"`
}

// ---------------------------------------------------------------------------
// Repo metadata types
// ---------------------------------------------------------------------------

type RepoMeta struct {
	Org                string `json:"org" yaml:"org"`
	Repo               string `json:"repo" yaml:"repo"`
	Stars              int    `json:"stars" yaml:"stars"`
	Forks              int    `json:"forks" yaml:"forks"`
	OpenIssues         int    `json:"open_issues" yaml:"openIssues"`
	Language           string `json:"language" yaml:"language"`
	License            string `json:"license" yaml:"license"`
	Archived           bool   `json:"archived" yaml:"archived"`
	HasCoC             bool   `json:"has_coc" yaml:"hasCoc"`
	HasContributing    bool   `json:"has_contributing" yaml:"hasContributing"`
	HasReadme          bool   `json:"has_readme" yaml:"hasReadme"`
	HasIssueTemplate   bool   `json:"has_issue_template" yaml:"hasIssueTemplate"`
	HasPRTemplate      bool   `json:"has_pr_template" yaml:"hasPrTemplate"`
	CommunityHealthPct int    `json:"community_health_pct" yaml:"communityHealthPct"`
	UpdatedAt          string `json:"updated_at" yaml:"updatedAt"`
}

type RepoOverview struct {
	Org          string `json:"org"`
	Repo         string `json:"repo"`
	Stars        int    `json:"stars"`
	Forks        int    `json:"forks"`
	OpenIssues   int    `json:"open_issues"`
	Events       int    `json:"events"`
	Contributors int    `json:"contributors"`
	Scored       int    `json:"scored"`
	Language     string `json:"language"`
	License      string `json:"license"`
	Archived     bool   `json:"archived"`
	LastImport   string `json:"last_import"`
}

type InsightBullet struct {
	Headline string `json:"headline" yaml:"headline"`
	Detail   string `json:"detail" yaml:"detail"`
}

type GeneratedInsights struct {
	Observations []InsightBullet `json:"observations" yaml:"observations"`
	Actions      []InsightBullet `json:"actions" yaml:"actions"`
}

type RepoInsights struct {
	Org         string             `json:"org" yaml:"org"`
	Repo        string             `json:"repo" yaml:"repo"`
	Insights    *GeneratedInsights `json:"insights" yaml:"insights"`
	PeriodWeeks int                `json:"period_weeks" yaml:"periodWeeks"`
	Model       string             `json:"model" yaml:"model"`
	GeneratedAt string             `json:"generated_at" yaml:"generatedAt"`
	EventCount  int                `json:"event_count" yaml:"eventCount"`
}
