package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/thingzio/devpulse/pkg/data"
)

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
		slog.Error("error converting query string to int", "value", v, "error", err)
		return def
	}

	if i < 1 || i > 120 {
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
	months int
	org    *string
	repo   *string
}

func parseInsightParams(r *http.Request) insightParams {
	months := queryParamInt(r, "m", data.EventAgeMonthsDefault)
	org := r.URL.Query().Get("o")
	repo := r.URL.Query().Get("r")
	if orgStr, repoStr, ok := parseRepo(optional(repo)); ok {
		org = *orgStr
		repo = *repoStr
	}
	return insightParams{months: months, org: optional(org), repo: optional(repo)}
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

type percentageProvider func(entity, org, repo *string, ex []string, months int) ([]*data.CountedItem, error)

func percentageAPIHandler(w http.ResponseWriter, r *http.Request, fn percentageProvider) {
	months := queryParamInt(r, "m", data.EventAgeMonthsDefault)
	org := r.URL.Query().Get("o")
	repo := r.URL.Query().Get("r")
	entity := r.URL.Query().Get("e")
	var exclude []string
	if x := r.URL.Query().Get("x"); x != "" {
		exclude = strings.Split(x, arraySelector)
	}

	slog.Debug("event type query", "org", org, "repo", repo, "entity", entity, "months", months)

	if orgStr, repoStr, ok := parseRepo(&repo); ok {
		org = *orgStr
		repo = *repoStr
	}

	res, err := fn(optional(entity), optional(org), optional(repo), exclude, months)
	if err != nil {
		slog.Error("failed to get event type series", "error", err)
		writeError(w, http.StatusInternalServerError, "error querying event type series")
		return
	}

	writeJSON(w, http.StatusOK, mapCountedItemsToSeries(res))
}

func minDateAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := parseInsightParams(r)
		minDate, err := store.GetMinEventDate(p.org, p.repo)
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
		q := r.URL.Query().Get("q")
		v := r.URL.Query().Get("v")

		var items []*data.ListItem
		var err error

		const (
			scopeOrg  = "org"
			scopeRepo = "repo"
		)

		switch v {
		case scopeOrg:
			items, err = store.GetOrgLike(q, queryResultLimitDefault)
		case scopeRepo:
			items, err = store.GetRepoLike(q, queryResultLimitDefault)
		case "entity":
			items, err = store.GetEntityLike(q, queryResultLimitDefault)
		case "all":
			half := queryResultLimitDefault / 2
			orgs, orgErr := store.GetOrgLike(q, half)
			if orgErr != nil {
				err = orgErr
				break
			}
			for _, o := range orgs {
				o.Type = scopeOrg
			}
			repos, repoErr := store.GetRepoLike(q, half)
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
		percentageAPIHandler(w, r, store.GetDeveloperPercentages)
	}
}

func entityDataAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		percentageAPIHandler(w, r, store.GetEntityPercentages)
	}
}

func eventDataAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := parseInsightParams(r)
		entity := r.URL.Query().Get("e")
		res, err := store.GetEventTypeSeries(p.org, p.repo, optional(entity), p.months)
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

		res, err := store.SearchEvents(&q)
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
		entity := r.URL.Query().Get("e")
		if entity == "" {
			writeError(w, http.StatusBadRequest, "entity parameter required")
			return
		}

		res, err := store.GetEntity(entity)
		if err != nil {
			slog.Error("failed to get entity developers", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying entity developers")
			return
		}

		writeJSON(w, http.StatusOK, res)
	}
}

func insightWithEntityHandler(label string, fn func(*string, *string, *string, int) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := parseInsightParams(r)
		entity := optional(r.URL.Query().Get("e"))
		res, err := fn(p.org, p.repo, entity, p.months)
		if err != nil {
			slog.Error("failed to get "+label, "error", err)
			writeError(w, http.StatusInternalServerError, "error querying "+label)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func insightHandler(label string, fn func(*string, *string, int) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := parseInsightParams(r)
		res, err := fn(p.org, p.repo, p.months)
		if err != nil {
			slog.Error("failed to get "+label, "error", err)
			writeError(w, http.StatusInternalServerError, "error querying "+label)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func insightsSummaryAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("insights summary", func(o, r, e *string, m int) (any, error) {
		return store.GetInsightsSummary(o, r, e, m)
	})
}

func insightsDailyActivityAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("daily activity", func(o, r, e *string, m int) (any, error) {
		return store.GetDailyActivity(o, r, e, m)
	})
}

func insightsRetentionAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("contributor retention", func(o, r, e *string, m int) (any, error) {
		return store.GetContributorRetention(o, r, e, m)
	})
}

func insightsPRRatioAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("PR review ratio", func(o, r, e *string, m int) (any, error) {
		return store.GetPRReviewRatio(o, r, e, m)
	})
}

func insightsTimeToMergeAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("time to merge", func(o, r, e *string, m int) (any, error) {
		return store.GetTimeToMerge(o, r, e, m)
	})
}

func insightsTimeToCloseAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("time to close", func(o, r, e *string, m int) (any, error) {
		return store.GetTimeToClose(o, r, e, m)
	})
}

func insightsTimeToRestoreAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("time to restore", func(o, r, e *string, m int) (any, error) {
		return store.GetTimeToRestoreBugs(o, r, e, m)
	})
}

func insightsChangeFailureRateAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("change failure rate", func(o, r, e *string, m int) (any, error) {
		return store.GetChangeFailureRate(o, r, e, m)
	})
}

func insightsReviewLatencyAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("review latency", func(o, r, e *string, m int) (any, error) {
		return store.GetReviewLatency(o, r, e, m)
	})
}

func insightsPRSizeAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("PR size distribution", func(o, r, e *string, m int) (any, error) {
		return store.GetPRSizeDistribution(o, r, e, m)
	})
}

func insightsContributorMomentumAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("contributor momentum", func(o, r, e *string, m int) (any, error) {
		return store.GetContributorMomentum(o, r, e, m)
	})
}

func insightsContributorFunnelAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("contributor funnel", func(o, r, e *string, m int) (any, error) {
		return store.GetContributorFunnel(o, r, e, m)
	})
}

func insightsContributorProfileAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := parseInsightParams(r)
		entity := optional(r.URL.Query().Get("e"))
		username := r.URL.Query().Get("u")
		if username == "" {
			writeError(w, http.StatusBadRequest, "username parameter (u) is required")
			return
		}
		res, err := store.GetContributorProfile(username, p.org, p.repo, entity, p.months)
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
		p := parseInsightParams(r)
		q := r.URL.Query().Get("q")
		if q == "" {
			writeError(w, http.StatusBadRequest, "query parameter (q) is required")
			return
		}
		res, err := store.SearchDeveloperUsernames(q, p.org, p.repo, p.months, 10)
		if err != nil {
			slog.Error("failed to search developers", "error", err)
			writeError(w, http.StatusInternalServerError, "error searching developers")
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func insightsTimeToFirstResponseAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("time to first response", func(o, r, e *string, m int) (any, error) {
		return store.GetTimeToFirstResponse(o, r, e, m)
	})
}

func insightsIssueRatioAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("issue open/close ratio", func(o, r, e *string, m int) (any, error) {
		return store.GetIssueOpenCloseRatio(o, r, e, m)
	})
}

func insightsForksAndActivityAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("forks and activity", func(o, r, e *string, m int) (any, error) {
		return store.GetForksAndActivity(o, r, e, m)
	})
}

func insightsReputationAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("reputation distribution", func(o, r, e *string, m int) (any, error) {
		return store.GetReputationDistribution(o, r, e, m)
	})
}

func insightsRepoMetaAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := parseInsightParams(r)
		res, err := store.GetRepoMetas(p.org, p.repo)
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
		p := parseInsightParams(r)
		res, err := store.GetRepoOverview(p.org, p.months)
		if err != nil {
			slog.Error("failed to get repo overview", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying repo overview")
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func insightsRepoMetricHistoryAPIHandler(store data.Store) http.HandlerFunc {
	return insightHandler("repo metric history", func(o, r *string, m int) (any, error) {
		return store.GetRepoMetricHistory(o, r, m)
	})
}

func insightsReleaseCadenceAPIHandler(store data.Store) http.HandlerFunc {
	return insightWithEntityHandler("release cadence", func(o, r, e *string, m int) (any, error) {
		return store.GetReleaseCadence(o, r, e, m)
	})
}

func insightsReleaseDownloadsAPIHandler(store data.Store) http.HandlerFunc {
	return insightHandler("release downloads", func(o, r *string, m int) (any, error) {
		return store.GetReleaseDownloads(o, r, m)
	})
}

func insightsReleaseDownloadsByTagAPIHandler(store data.Store) http.HandlerFunc {
	return insightHandler("release downloads by tag", func(o, r *string, m int) (any, error) {
		return store.GetReleaseDownloadsByTag(o, r, m)
	})
}

func insightsContainerActivityAPIHandler(store data.Store) http.HandlerFunc {
	return insightHandler("container activity", func(o, r *string, m int) (any, error) {
		return store.GetContainerActivity(o, r, m)
	})
}

func insightsGeneratedAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := parseInsightParams(r)
		res, err := store.GetRepoInsights(p.org, p.repo)
		if err != nil {
			slog.Error("failed to get generated insights", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying generated insights")
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}
