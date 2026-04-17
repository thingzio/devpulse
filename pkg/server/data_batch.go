package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/thingzio/devpulse/pkg/data"
)

// --- Batch response types ---

type batchHealthResponse struct {
	DailyActivity     any `json:"daily_activity"`
	RepoMeta          any `json:"repo_meta"`
	HealthScorecard   any `json:"health_scorecard"`
	RepoMetricHistory any `json:"repo_metric_history"`
}

type batchActivityResponse struct {
	EventTypes       any `json:"event_types"`
	PRSize           any `json:"pr_size"`
	ForksAndActivity any `json:"forks_and_activity"`
	IssueRatio       any `json:"issue_ratio"`
}

type batchVelocityResponse struct {
	TimeToFirstResponse   any `json:"time_to_first_response"`
	TimeToMerge           any `json:"time_to_merge"`
	ChangeFailureRate     any `json:"change_failure_rate"`
	ReleaseCadence        any `json:"release_cadence"`
	ReleaseDownloads      any `json:"release_downloads"`
	ReleaseDownloadsByTag any `json:"release_downloads_by_tag"`
	ContainerActivity     any `json:"container_activity"`
}

type batchQualityResponse struct {
	PRRatio                any `json:"pr_ratio"`
	ReviewLatency          any `json:"review_latency"`
	TimeToClose            any `json:"time_to_close"`
	TimeToRestore          any `json:"time_to_restore"`
	ContributorComposition any `json:"contributor_composition"`
}

type batchCommunityResponse struct {
	Retention            any `json:"retention"`
	ContributorMomentum  any `json:"contributor_momentum"`
	ContributorFunnel    any `json:"contributor_funnel"`
	EntityPercentages    any `json:"entity_percentages"`
	DeveloperPercentages any `json:"developer_percentages"`
}

// --- Batch handlers ---

func batchHealthHandler(defaultStore data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := dataCacheKey(r)
		if cached, ok := apiCache.get(key); ok {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(cacheControlHeaderKey, browserCacheMaxAge)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached) //nolint:gosec // cached bytes are from our own json.Marshal
			return
		}

		s := storeFromRequest(r, defaultStore)
		p := parseInsightParams(r)
		entity := optional(r.URL.Query().Get("e"))
		ctx := r.Context()

		resp := batchHealthResponse{}

		if v, err := s.GetDailyActivity(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch health: daily activity", "error", err)
		} else {
			resp.DailyActivity = v
		}

		if v, err := s.GetRepoMetas(ctx, p.org, p.repo); err != nil {
			slog.Warn("batch health: repo meta", "error", err)
		} else {
			resp.RepoMeta = v
		}

		// Health scorecard: gather sub-metrics and build scorecard.
		momentum, _ := s.GetContributorMomentum(ctx, p.org, p.repo, entity, p.days)
		ttm, _ := s.GetTimeToMerge(ctx, p.org, p.repo, entity, p.days)
		ttfr, _ := s.GetTimeToFirstResponse(ctx, p.org, p.repo, entity, p.days)
		metricHistory, _ := s.GetRepoMetricHistory(ctx, p.org, p.repo, p.days)
		aging, _ := s.GetAgingPRs(ctx, p.org, p.repo, entity, p.days)
		unanswered, _ := s.GetUnansweredRate(ctx, p.org, p.repo, entity, p.days)
		slo, _ := s.GetResponseSLO(ctx, p.org, p.repo, entity, p.days)
		prRatio, _ := s.GetPRReviewRatio(ctx, p.org, p.repo, entity, p.days)
		issueRatio, _ := s.GetIssueOpenCloseRatio(ctx, p.org, p.repo, entity, p.days)
		resp.HealthScorecard = buildScorecard(momentum, ttm, ttfr, metricHistory, aging, unanswered, slo, prRatio, issueRatio)

		resp.RepoMetricHistory = metricHistory

		b, err := json.Marshal(resp)
		if err != nil {
			slog.Error("batch health: marshal failed", "error", err)
			writeError(w, http.StatusInternalServerError, "error encoding batch health")
			return
		}
		apiCache.setWithTTL(key, b, requestCacheTTL(r))

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(cacheControlHeaderKey, browserCacheMaxAge)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}
}

func batchActivityHandler(defaultStore data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := dataCacheKey(r)
		if cached, ok := apiCache.get(key); ok {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(cacheControlHeaderKey, browserCacheMaxAge)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached) //nolint:gosec // cached bytes are from our own json.Marshal
			return
		}

		s := storeFromRequest(r, defaultStore)
		p := parseInsightParams(r)
		entity := optional(r.URL.Query().Get("e"))
		ctx := r.Context()

		resp := batchActivityResponse{}

		if v, err := s.GetEventTypeSeries(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch activity: event types", "error", err)
		} else {
			resp.EventTypes = v
		}

		if v, err := s.GetPRSizeDistribution(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch activity: pr size", "error", err)
		} else {
			resp.PRSize = v
		}

		if v, err := s.GetForksAndActivity(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch activity: forks and activity", "error", err)
		} else {
			resp.ForksAndActivity = v
		}

		if v, err := s.GetIssueOpenCloseRatio(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch activity: issue ratio", "error", err)
		} else {
			resp.IssueRatio = v
		}

		b, err := json.Marshal(resp)
		if err != nil {
			slog.Error("batch activity: marshal failed", "error", err)
			writeError(w, http.StatusInternalServerError, "error encoding batch activity")
			return
		}
		apiCache.setWithTTL(key, b, requestCacheTTL(r))

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(cacheControlHeaderKey, browserCacheMaxAge)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}
}

func batchVelocityHandler(defaultStore data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := dataCacheKey(r)
		if cached, ok := apiCache.get(key); ok {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(cacheControlHeaderKey, browserCacheMaxAge)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached) //nolint:gosec // cached bytes are from our own json.Marshal
			return
		}

		s := storeFromRequest(r, defaultStore)
		p := parseInsightParams(r)
		entity := optional(r.URL.Query().Get("e"))
		ctx := r.Context()

		resp := batchVelocityResponse{}

		if v, err := s.GetTimeToFirstResponse(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch velocity: time to first response", "error", err)
		} else {
			resp.TimeToFirstResponse = v
		}

		if v, err := s.GetTimeToMerge(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch velocity: time to merge", "error", err)
		} else {
			resp.TimeToMerge = v
		}

		if v, err := s.GetChangeFailureRate(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch velocity: change failure rate", "error", err)
		} else {
			resp.ChangeFailureRate = v
		}

		if v, err := s.GetReleaseCadence(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch velocity: release cadence", "error", err)
		} else {
			resp.ReleaseCadence = v
		}

		if v, err := s.GetReleaseDownloads(ctx, p.org, p.repo, p.days); err != nil {
			slog.Warn("batch velocity: release downloads", "error", err)
		} else {
			resp.ReleaseDownloads = v
		}

		if v, err := s.GetReleaseDownloadsByTag(ctx, p.org, p.repo, p.days); err != nil {
			slog.Warn("batch velocity: release downloads by tag", "error", err)
		} else {
			resp.ReleaseDownloadsByTag = v
		}

		if v, err := s.GetContainerActivity(ctx, p.org, p.repo, p.days); err != nil {
			slog.Warn("batch velocity: container activity", "error", err)
		} else {
			resp.ContainerActivity = v
		}

		b, err := json.Marshal(resp)
		if err != nil {
			slog.Error("batch velocity: marshal failed", "error", err)
			writeError(w, http.StatusInternalServerError, "error encoding batch velocity")
			return
		}
		apiCache.setWithTTL(key, b, requestCacheTTL(r))

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(cacheControlHeaderKey, browserCacheMaxAge)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}
}

func batchQualityHandler(defaultStore data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := dataCacheKey(r)
		if cached, ok := apiCache.get(key); ok {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(cacheControlHeaderKey, browserCacheMaxAge)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached) //nolint:gosec // cached bytes are from our own json.Marshal
			return
		}

		s := storeFromRequest(r, defaultStore)
		p := parseInsightParams(r)
		entity := optional(r.URL.Query().Get("e"))
		ctx := r.Context()

		resp := batchQualityResponse{}

		if v, err := s.GetPRReviewRatio(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch quality: pr ratio", "error", err)
		} else {
			resp.PRRatio = v
		}

		if v, err := s.GetReviewLatency(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch quality: review latency", "error", err)
		} else {
			resp.ReviewLatency = v
		}

		if v, err := s.GetTimeToClose(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch quality: time to close", "error", err)
		} else {
			resp.TimeToClose = v
		}

		if v, err := s.GetTimeToRestoreBugs(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch quality: time to restore", "error", err)
		} else {
			resp.TimeToRestore = v
		}

		if v, err := s.GetContributorComposition(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch quality: contributor composition", "error", err)
		} else {
			resp.ContributorComposition = v
		}

		b, err := json.Marshal(resp)
		if err != nil {
			slog.Error("batch quality: marshal failed", "error", err)
			writeError(w, http.StatusInternalServerError, "error encoding batch quality")
			return
		}
		apiCache.setWithTTL(key, b, requestCacheTTL(r))

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(cacheControlHeaderKey, browserCacheMaxAge)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}
}

func batchCommunityHandler(defaultStore data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := dataCacheKey(r)
		if cached, ok := apiCache.get(key); ok {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(cacheControlHeaderKey, browserCacheMaxAge)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached) //nolint:gosec // cached bytes are from our own json.Marshal
			return
		}

		s := storeFromRequest(r, defaultStore)
		p := parseInsightParams(r)
		entity := optional(r.URL.Query().Get("e"))
		ctx := r.Context()

		// Parse exclude list from "x" query param (same as percentageAPIHandler).
		var exclude []string
		if x := r.URL.Query().Get("x"); x != "" {
			exclude = strings.Split(x, arraySelector)
		}

		resp := batchCommunityResponse{}

		if v, err := s.GetContributorRetention(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch community: retention", "error", err)
		} else {
			resp.Retention = v
		}

		if v, err := s.GetContributorMomentum(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch community: contributor momentum", "error", err)
		} else {
			resp.ContributorMomentum = v
		}

		if v, err := s.GetContributorFunnel(ctx, p.org, p.repo, entity, p.days); err != nil {
			slog.Warn("batch community: contributor funnel", "error", err)
		} else {
			resp.ContributorFunnel = v
		}

		if v, err := s.GetEntityPercentages(ctx, entity, p.org, p.repo, exclude, p.days); err != nil {
			slog.Warn("batch community: entity percentages", "error", err)
		} else {
			resp.EntityPercentages = mapCountedItemsToSeries(v)
		}

		if v, err := s.GetDeveloperPercentages(ctx, entity, p.org, p.repo, exclude, p.days); err != nil {
			slog.Warn("batch community: developer percentages", "error", err)
		} else {
			resp.DeveloperPercentages = mapCountedItemsToSeries(v)
		}

		b, err := json.Marshal(resp)
		if err != nil {
			slog.Error("batch community: marshal failed", "error", err)
			writeError(w, http.StatusInternalServerError, "error encoding batch community")
			return
		}
		apiCache.setWithTTL(key, b, requestCacheTTL(r))

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(cacheControlHeaderKey, browserCacheMaxAge)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}
}
