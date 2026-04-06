package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/health"
	"github.com/thingzio/devpulse/pkg/middleware"
	"github.com/thingzio/devpulse/pkg/plan"
)

const (
	serverCacheTTL        = 5 * time.Minute
	browserCacheMaxAge    = "private, max-age=1800"
	cacheControlHeaderKey = "Cache-Control"
)

// responseCache is a TTL-based in-memory cache for JSON API responses.
type responseCache struct {
	entries sync.Map
}

type cacheEntry struct {
	data    []byte
	expires time.Time
}

var apiCache = &responseCache{}

func (c *responseCache) get(key string) ([]byte, bool) {
	v, ok := c.entries.Load(key)
	if !ok {
		return nil, false
	}
	e := v.(*cacheEntry)
	if time.Now().After(e.expires) {
		c.entries.Delete(key)
		return nil, false
	}
	return e.data, true
}

func (c *responseCache) set(key string, val []byte) {
	c.entries.Store(key, &cacheEntry{
		data:    val,
		expires: time.Now().Add(serverCacheTTL),
	})
}

// dataCacheKey builds a cache key from tenant ID, path, and query string.
func dataCacheKey(r *http.Request) string {
	tid := ""
	if tn := middleware.TenantFromContext(r.Context()); tn != nil {
		tid = tn.ID
	}
	return tid + "|" + r.URL.Path + "?" + r.URL.RawQuery
}

const (
	percentageListLimit           = 9
	repoNamePartsLimit            = 2
	hundredPercent                = 100
	categoryOther                 = "ALL OTHERS"
	arraySelector                 = "|"
	maxRequestBodyBytes     int64 = 1 << 20 // 1 MB
	queryResultLimitDefault       = 500
)

// SeriesData is a generic type for chart series responses.
type SeriesData[T any] struct {
	Labels []string `json:"labels"`
	Data   []T      `json:"data"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func queryParamInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}

	i, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("error converting query string to int", "value", v, "error", err)
		return def
	}

	if i < 14 || i > 3650 {
		return def
	}

	return i
}

func optional(val string) *string {
	if val == "" {
		return nil
	}
	return &val
}

func parseRepo(repo *string) (*string, *string, bool) {
	if repo == nil {
		return nil, nil, false
	}

	repoParts := strings.Split(*repo, "/")
	if len(repoParts) != repoNamePartsLimit {
		return nil, nil, false
	}

	o := strings.TrimSpace(repoParts[0])
	r := strings.TrimSpace(repoParts[1])

	return &o, &r, true
}

type insightParams struct {
	days int
	org  *string
	repo *string
}

func parseInsightParams(r *http.Request) insightParams {
	days := queryParamInt(r, "d", data.EventAgeDaysDefault)
	if tn := middleware.TenantFromContext(r.Context()); tn != nil {
		if limits, ok := plan.Get(tn.Plan); ok && limits.MaxDataRangeMonths > 0 {
			maxDays := limits.MaxDataRangeMonths * 30
			if days > maxDays {
				days = maxDays
			}
		}
	}
	org := r.URL.Query().Get("o")
	repo := r.URL.Query().Get("r")
	if orgStr, repoStr, ok := parseRepo(optional(repo)); ok {
		org = *orgStr
		repo = *repoStr
	}
	return insightParams{days: days, org: optional(org), repo: optional(repo)}
}

func mapCountedItemsToSeries(res []*data.CountedItem) *SeriesData[int] {
	slog.Debug("items", "count", len(res))

	if len(res) > percentageListLimit {
		res = res[:percentageListLimit]
	}

	sum := 0
	d := &SeriesData[int]{
		Labels: make([]string, 0),
		Data:   make([]int, 0),
	}
	for _, v := range res {
		sum += v.Count
		d.Labels = append(d.Labels, v.Name)
		d.Data = append(d.Data, v.Count)
	}

	if sum < hundredPercent {
		d.Labels = append(d.Labels, categoryOther)
		d.Data = append(d.Data, hundredPercent-sum)
	}
	return d
}

type percentageProvider func(ctx context.Context, entity, org, repo *string, ex []string, days int) ([]*data.CountedItem, error)

func percentageAPIHandler(w http.ResponseWriter, r *http.Request, fn percentageProvider) {
	days := queryParamInt(r, "d", data.EventAgeDaysDefault)
	org := r.URL.Query().Get("o")
	repo := r.URL.Query().Get("r")
	entity := r.URL.Query().Get("e")
	var exclude []string
	if x := r.URL.Query().Get("x"); x != "" {
		exclude = strings.Split(x, arraySelector)
	}

	slog.Debug("event type query", "org", org, "repo", repo, "entity", entity, "days", days)

	if orgStr, repoStr, ok := parseRepo(&repo); ok {
		org = *orgStr
		repo = *repoStr
	}

	res, err := fn(r.Context(), optional(entity), optional(org), optional(repo), exclude, days)
	if err != nil {
		slog.Error("failed to get event type series", "error", err)
		writeError(w, http.StatusInternalServerError, "error querying event type series")
		return
	}

	writeJSON(w, http.StatusOK, mapCountedItemsToSeries(res))
}

func minDateAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		p := parseInsightParams(r)
		minDate, err := s.GetMinEventDate(r.Context(), p.org, p.repo)
		if err != nil {
			slog.Error("failed to get min event date", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to get min date")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"min_date": minDate})
	}
}

func queryAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		q := r.URL.Query().Get("q")
		v := r.URL.Query().Get("v")

		var items []*data.ListItem
		var err error

		const (
			scopeOrg  = "org"
			scopeRepo = "repo"
		)

		ctx := r.Context()
		switch v {
		case scopeOrg:
			items, err = s.GetOrgLike(ctx, q, queryResultLimitDefault)
		case scopeRepo:
			items, err = s.GetRepoLike(ctx, q, queryResultLimitDefault)
		case "entity":
			items, err = s.GetEntityLike(ctx, q, queryResultLimitDefault)
		case "all":
			half := queryResultLimitDefault / 2
			orgs, orgErr := s.GetOrgLike(ctx, q, half)
			if orgErr != nil {
				err = orgErr
				break
			}
			for _, o := range orgs {
				o.Type = scopeOrg
			}
			repos, repoErr := s.GetRepoLike(ctx, q, half)
			if repoErr != nil {
				err = repoErr
				break
			}
			for _, r := range repos {
				r.Type = scopeRepo
			}
			items = append(items, orgs...)
			items = append(items, repos...)
		default:
			items = []*data.ListItem{}
		}

		if err != nil {
			slog.Error("failed to get org like data", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying org like data")
			return
		}

		writeJSON(w, http.StatusOK, items)
	}
}

func developerDataAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		percentageAPIHandler(w, r, s.GetDeveloperPercentages)
	}
}

func entityDataAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		percentageAPIHandler(w, r, s.GetEntityPercentages)
	}
}

func eventDataAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		p := parseInsightParams(r)
		entity := r.URL.Query().Get("e")
		res, err := s.GetEventTypeSeries(r.Context(), p.org, p.repo, optional(entity), p.days)
		if err != nil {
			slog.Error("failed to get event type series", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying event type series")
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func eventSearchAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		var q data.EventSearchCriteria
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			slog.Error("error binding json", "error", err)
			writeError(w, http.StatusBadRequest, "error binding json")
			return
		}

		if org, repo, ok := parseRepo(q.Repo); ok {
			q.Org = org
			q.Repo = repo
		}

		if q.Type != nil {
			eType := *q.Type
			switch eType {
			case "PR":
				eType = data.EventTypePR
			case "PR-Review":
				eType = data.EventTypePRReview
			case "Issue":
				eType = data.EventTypeIssue
			case "Issue-Comment":
				eType = data.EventTypeIssueComment
			case "Fork":
				eType = data.EventTypeFork
			default:
				eType = ""
			}
			if eType != "" {
				q.Type = &eType
			}
		}

		slog.Debug("event search query", "query", q)

		res, err := s.SearchEvents(r.Context(), &q)
		if err != nil {
			slog.Error("failed to execute event search", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying event type series")
			return
		}

		writeJSON(w, http.StatusOK, res)
	}
}

func entityDevelopersAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		entity := r.URL.Query().Get("e")
		if entity == "" {
			writeError(w, http.StatusBadRequest, "entity parameter required")
			return
		}

		res, err := s.GetEntity(r.Context(), entity)
		if err != nil {
			slog.Error("failed to get entity developers", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying entity developers")
			return
		}

		writeJSON(w, http.StatusOK, res)
	}
}

// storeFromRequest returns the tenant-scoped Store from context, falling back to the default.
func storeFromRequest(r *http.Request, fallback data.Store) data.Store {
	if s := scopedStoreFromContext(r.Context()); s != nil {
		return s
	}
	return fallback
}

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
			slog.Error("failed to get "+label, "error", err)
			writeError(w, http.StatusInternalServerError, "error querying "+label)
			return
		}

		b, err := json.Marshal(res)
		if err != nil {
			slog.Error("failed to marshal "+label, "error", err)
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
			slog.Error("failed to get "+label, "error", err)
			writeError(w, http.StatusInternalServerError, "error querying "+label)
			return
		}

		b, err := json.Marshal(res)
		if err != nil {
			slog.Error("failed to marshal "+label, "error", err)
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
	return insightWithEntityHandler(store, "reputation distribution", func(ctx context.Context, s data.Store, o, r, e *string, m int) (any, error) {
		return s.GetReputationDistribution(ctx, o, r, e, m)
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
