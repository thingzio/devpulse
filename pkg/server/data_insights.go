package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/health"
	"github.com/thingzio/devpulse/pkg/middleware"
	"github.com/thingzio/devpulse/pkg/plan"
)

func insightWithEntityHandler(defaultStore data.Store, label string, fn func(context.Context, data.Store, *string, *string, *string, int) (any, error)) http.HandlerFunc {
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
		res, err := fn(r.Context(), s, p.org, p.repo, entity, p.days)
		if err != nil {
			slog.Error("insight query failed", "type", label, "error", err)
			writeError(w, http.StatusInternalServerError, "error querying "+label)
			return
		}

		b, err := json.Marshal(res)
		if err != nil {
			slog.Error("insight marshal failed", "type", label, "error", err)
			writeError(w, http.StatusInternalServerError, "error encoding "+label)
			return
		}
		apiCache.set(key, b)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(cacheControlHeaderKey, browserCacheMaxAge)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}
}

func insightHandler(defaultStore data.Store, label string, fn func(context.Context, data.Store, *string, *string, int) (any, error)) http.HandlerFunc {
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
		res, err := fn(r.Context(), s, p.org, p.repo, p.days)
		if err != nil {
			slog.Error("insight query failed", "type", label, "error", err)
			writeError(w, http.StatusInternalServerError, "error querying "+label)
			return
		}

		b, err := json.Marshal(res)
		if err != nil {
			slog.Error("insight marshal failed", "type", label, "error", err)
			writeError(w, http.StatusInternalServerError, "error encoding "+label)
			return
		}
		apiCache.set(key, b)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(cacheControlHeaderKey, browserCacheMaxAge)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}
}

func insightsSummaryAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "insights summary", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetInsightsSummary(ctx, o, r, e, m)
	})
}

func insightsDailyActivityAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "daily activity", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetDailyActivity(ctx, o, r, e, m)
	})
}

func insightsRetentionAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "contributor retention", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetContributorRetention(ctx, o, r, e, m)
	})
}

func insightsPRRatioAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "PR review ratio", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetPRReviewRatio(ctx, o, r, e, m)
	})
}

func insightsTimeToMergeAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "time to merge", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetTimeToMerge(ctx, o, r, e, m)
	})
}

func insightsTimeToCloseAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "time to close", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetTimeToClose(ctx, o, r, e, m)
	})
}

func insightsTimeToRestoreAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "time to restore", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetTimeToRestoreBugs(ctx, o, r, e, m)
	})
}

func insightsChangeFailureRateAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "change failure rate", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetChangeFailureRate(ctx, o, r, e, m)
	})
}

func insightsReviewLatencyAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "review latency", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetReviewLatency(ctx, o, r, e, m)
	})
}

func insightsPRSizeAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "PR size distribution", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetPRSizeDistribution(ctx, o, r, e, m)
	})
}

func insightsContributorMomentumAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "contributor momentum", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetContributorMomentum(ctx, o, r, e, m)
	})
}

func insightsContributorFunnelAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "contributor funnel", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetContributorFunnel(ctx, o, r, e, m)
	})
}

func insightsContributorProfileAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		p := parseInsightParams(r)
		entity := optional(r.URL.Query().Get("e"))
		username := r.URL.Query().Get("u")
		if username == "" {
			writeError(w, http.StatusBadRequest, "username parameter (u) is required")
			return
		}
		res, err := s.GetContributorProfile(r.Context(), username, p.org, p.repo, entity, p.days)
		if err != nil {
			slog.Error("failed to get contributor profile", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying contributor profile")
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func developerSearchAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		p := parseInsightParams(r)
		q := r.URL.Query().Get("q")
		if q == "" {
			writeError(w, http.StatusBadRequest, "query parameter (q) is required")
			return
		}
		res, err := s.SearchDeveloperUsernames(r.Context(), q, p.org, p.repo, p.days, 10)
		if err != nil {
			slog.Error("failed to search developers", "error", err)
			writeError(w, http.StatusInternalServerError, "error searching developers")
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func insightsTimeToFirstResponseAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "time to first response", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetTimeToFirstResponse(ctx, o, r, e, m)
	})
}

func insightsIssueRatioAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "issue open/close ratio", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetIssueOpenCloseRatio(ctx, o, r, e, m)
	})
}

func insightsForksAndActivityAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "forks and activity", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetForksAndActivity(ctx, o, r, e, m)
	})
}

func insightsReputationAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "reputation composition", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetReputationComposition(ctx, o, r, e, m)
	})
}

func insightsRepoMetaAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		p := parseInsightParams(r)
		res, err := s.GetRepoMetas(r.Context(), p.org, p.repo)
		if err != nil {
			slog.Error("failed to get repo metadata", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying repo metadata")
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func insightsRepoOverviewAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		p := parseInsightParams(r)
		res, err := s.GetRepoOverview(r.Context(), p.org, p.days)
		if err != nil {
			slog.Error("failed to get repo overview", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying repo overview")
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func insightsRepoMetricHistoryAPIHandler(store data.Store) http.HandlerFunc {
	return insightHandler(store, "repo metric history", func(ctx context.Context, s data.Store, o, r *string, m int) (any, error) {
		return s.GetRepoMetricHistory(ctx, o, r, m)
	})
}

func insightsReleaseCadenceAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler(store, "release cadence", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetReleaseCadence(ctx, o, r, e, m)
	})
}

func insightsReleaseDownloadsAPIHandler(store data.Store) http.HandlerFunc {
	return insightHandler(store, "release downloads", func(ctx context.Context, s data.Store, o, r *string, m int) (any, error) {
		return s.GetReleaseDownloads(ctx, o, r, m)
	})
}

func insightsReleaseDownloadsByTagAPIHandler(store data.Store) http.HandlerFunc {
	return insightHandler(store, "release downloads by tag", func(ctx context.Context, s data.Store, o, r *string, m int) (any, error) {
		return s.GetReleaseDownloadsByTag(ctx, o, r, m)
	})
}

func insightsContainerActivityAPIHandler(store data.Store) http.HandlerFunc {
	return insightHandler(store, "container activity", func(ctx context.Context, s data.Store, o, r *string, m int) (any, error) {
		return s.GetContainerActivity(ctx, o, r, m)
	})
}

func insightsGeneratedAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		p := parseInsightParams(r)
		res, err := s.GetRepoInsights(r.Context(), p.org, p.repo)
		if err != nil {
			slog.Error("failed to get generated insights", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying generated insights")
			return
		}

		// Strip action items for plans below Pro (AILevel < 2)
		if tn := middleware.TenantFromContext(r.Context()); tn != nil {
			if limits, ok := plan.Get(tn.Plan); ok && limits.AILevel < 2 {
				for _, ri := range res {
					if ri != nil && ri.Insights != nil {
						ri.Insights.Actions = nil
					}
				}
			}
		}

		writeJSON(w, http.StatusOK, res)
	}
}

func insightsSignalsHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		p := parseInsightParams(r)
		limit := queryParamInt(r, "n", 10)
		res, err := s.GetSignals(r.Context(), p.org, limit)
		if err != nil {
			slog.Error("failed to get signals", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying signals")
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func insightsPortfolioSummaryHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		p := parseInsightParams(r)
		res, err := s.GetPortfolioSummary(r.Context(), p.org, p.repo, p.days)
		if err != nil {
			slog.Error("failed to get portfolio summary", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying portfolio summary")
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func insightsHealthScorecardHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		p := parseInsightParams(r)
		entity := optional(r.URL.Query().Get("e"))
		ctx := r.Context()

		// Gather sub-metrics (individual failures are non-fatal).
		momentum, _ := s.GetContributorMomentum(ctx, p.org, p.repo, entity, p.days)
		ttm, _ := s.GetTimeToMerge(ctx, p.org, p.repo, entity, p.days)
		ttfr, _ := s.GetTimeToFirstResponse(ctx, p.org, p.repo, entity, p.days)
		metricHistory, _ := s.GetRepoMetricHistory(ctx, p.org, p.repo, p.days)
		aging, _ := s.GetAgingPRs(ctx, p.org, p.repo, entity, p.days)
		unanswered, _ := s.GetUnansweredRate(ctx, p.org, p.repo, entity, p.days)
		slo, _ := s.GetResponseSLO(ctx, p.org, p.repo, entity, p.days)
		prRatio, _ := s.GetPRReviewRatio(ctx, p.org, p.repo, entity, p.days)
		issueRatio, _ := s.GetIssueOpenCloseRatio(ctx, p.org, p.repo, entity, p.days)

		sc := buildScorecard(momentum, ttm, ttfr, metricHistory, aging, unanswered, slo, prRatio, issueRatio)
		writeJSON(w, http.StatusOK, sc)
	}
}

func buildDemandInput(
	metricHistory []*data.RepoMetricHistory,
	momentum *data.MomentumSeries,
	prRatio *data.PRReviewRatioSeries,
	issueRatio *data.IssueRatioSeries,
) health.DemandInput {
	di := health.DemandInput{}
	if len(metricHistory) >= 2 {
		latest := metricHistory[len(metricHistory)-1]
		refIdx := len(metricHistory) - 31
		if refIdx < 0 {
			refIdx = 0
		}
		ref := metricHistory[refIdx]
		if ref.Stars >= 10 {
			di.StarGrowthPct = float64(latest.Stars-ref.Stars) / float64(ref.Stars) * 100
		} else if latest.Stars > ref.Stars {
			gain := float64(latest.Stars - ref.Stars)
			if gain > 100 {
				gain = 100
			}
			di.StarGrowthPct = gain
		}
	}
	if momentum != nil && len(momentum.Active) >= 2 {
		prev := momentum.Active[len(momentum.Active)-2]
		curr := momentum.Active[len(momentum.Active)-1]
		if prev > 0 {
			di.ExternalContributorDelta = float64(curr-prev) / float64(prev) * 100
		}
	}
	di.NewPRDelta = monthOverMonthDelta(prRatio != nil, func() (int, int) {
		if prRatio == nil || len(prRatio.PRs) < 2 {
			return 0, 0
		}
		return prRatio.PRs[len(prRatio.PRs)-2], prRatio.PRs[len(prRatio.PRs)-1]
	})
	di.NewIssueDelta = monthOverMonthDelta(issueRatio != nil, func() (int, int) {
		if issueRatio == nil || len(issueRatio.Opened) < 2 {
			return 0, 0
		}
		return issueRatio.Opened[len(issueRatio.Opened)-2], issueRatio.Opened[len(issueRatio.Opened)-1]
	})
	return di
}

func buildThroughputInput(ttm *data.VelocitySeries, aging *data.AgingPRsSeries) health.ThroughputInput {
	ti := health.ThroughputInput{}
	if ttm != nil && len(ttm.AvgDays) >= 1 {
		ti.MedianMergeHours = ttm.AvgDays[len(ttm.AvgDays)-1] * 24
	}
	if aging != nil {
		ti.AgingPRsPct = aging.AgingPct
	}
	ti.PRBacklogDelta = monthOverMonthDelta(ttm != nil, func() (int, int) {
		if ttm == nil || len(ttm.Count) < 2 {
			return 0, 0
		}
		return ttm.Count[len(ttm.Count)-2], ttm.Count[len(ttm.Count)-1]
	})
	return ti
}

func buildResponsivenessInput(
	ttfr *data.FirstResponseSeries,
	slo *data.ResponseSLOSeries,
	unanswered *data.UnansweredSeries,
) health.ResponsivenessInput {
	ri := health.ResponsivenessInput{}
	if ttfr != nil && len(ttfr.PRAvg) >= 1 {
		ri.FirstResponsePRHours = ttfr.PRAvg[len(ttfr.PRAvg)-1]
	}
	if ttfr != nil && len(ttfr.IssueAvg) >= 1 {
		ri.FirstResponseIssueHours = ttfr.IssueAvg[len(ttfr.IssueAvg)-1]
	}
	if slo != nil {
		ri.RespondedWithin48hPct = slo.WithinSLOPct
	}
	if unanswered != nil {
		ri.UnansweredPct = unanswered.UnansweredPct
	}
	return ri
}

func monthOverMonthDelta(valid bool, vals func() (int, int)) float64 {
	if !valid {
		return 0
	}
	prev, curr := vals()
	if prev > 0 {
		return float64(curr-prev) / float64(prev) * 100
	}
	return 0
}

func buildScorecard(
	momentum *data.MomentumSeries,
	ttm *data.VelocitySeries,
	ttfr *data.FirstResponseSeries,
	metricHistory []*data.RepoMetricHistory,
	aging *data.AgingPRsSeries,
	unanswered *data.UnansweredSeries,
	slo *data.ResponseSLOSeries,
	prRatio *data.PRReviewRatioSeries,
	issueRatio *data.IssueRatioSeries,
) *data.HealthScorecard {
	di := buildDemandInput(metricHistory, momentum, prRatio, issueRatio)
	demandScore := health.DemandScore(di)

	ti := buildThroughputInput(ttm, aging)
	throughputScore := health.ThroughputScore(ti)

	ri := buildResponsivenessInput(ttfr, slo, unanswered)
	responsivenessScore := health.ResponsivenessScore(ri)

	overallScore := health.Overall(demandScore, throughputScore, responsivenessScore)

	return &data.HealthScorecard{
		Overall:      health.Grade(overallScore),
		OverallScore: overallScore,
		Demand: data.HealthCategory{
			Name:  "Demand",
			Grade: health.Grade(demandScore),
			Score: demandScore,
			Metrics: map[string]any{
				"Star Growth (%)":        di.StarGrowthPct,
				"Contributor Growth (%)": di.ExternalContributorDelta,
				"New PR Delta (%)":       di.NewPRDelta,
				"New Issue Delta (%)":    di.NewIssueDelta,
			},
		},
		Throughput: data.HealthCategory{
			Name:  "Throughput",
			Grade: health.Grade(throughputScore),
			Score: throughputScore,
			Metrics: map[string]any{
				"Median Merge (hrs)":   ti.MedianMergeHours,
				"PR Backlog Delta (%)": ti.PRBacklogDelta,
				"Aging PRs (%)":        ti.AgingPRsPct,
			},
		},
		Responsiveness: data.HealthCategory{
			Name:  "Responsiveness",
			Grade: health.Grade(responsivenessScore),
			Score: responsivenessScore,
			Metrics: map[string]any{
				"PR Response (hrs)":    ri.FirstResponsePRHours,
				"Issue Response (hrs)": ri.FirstResponseIssueHours,
				"Responded <48h (%)":   ri.RespondedWithin48hPct,
				"Unanswered (%)":       ri.UnansweredPct,
			},
		},
	}
}
