package server

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/middleware"
	"github.com/thingzio/devpulse/pkg/plan"
	"github.com/thingzio/devpulse/pkg/tenant"
)

func csvExportHandler(defaultStore data.Store, listRepos func(ctx context.Context, tenantID string) ([]tenant.TenantRepo, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		limits, ok := plan.Get(tn.Plan)
		if !ok || !limits.CSVExport {
			writeError(w, http.StatusForbidden, "CSV export is not available on your plan")
			return
		}

		s := storeFromRequest(r, defaultStore)
		p := parseInsightParams(r)
		ctx := r.Context()

		repos, err := listRepos(ctx, tn.ID)
		if err != nil {
			slog.Error("listing repos for export", "error", err)
			writeError(w, http.StatusInternalServerError, "error listing repos")
			return
		}

		// Filter to active repos, and to specific repo if requested
		var active []tenant.TenantRepo
		for _, rp := range repos {
			if !rp.Active {
				continue
			}
			if p.org != nil && p.repo != nil {
				if rp.Org == *p.org && rp.Repo == *p.repo {
					active = append(active, rp)
					break
				}
				continue
			}
			active = append(active, rp)
		}

		if len(active) == 0 {
			writeError(w, http.StatusNotFound, "no repos to export")
			return
		}

		today := time.Now().UTC().Format("2006-01-02")
		var filename string
		if len(active) == 1 {
			filename = fmt.Sprintf("devpulse-%s-%s-%s.zip", active[0].Org, active[0].Repo, today)
		} else {
			filename = fmt.Sprintf("devpulse-export-%s.zip", today)
		}

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

		// Streaming exports can take longer than the default WriteTimeout.
		// Extend the deadline using ResponseController so a large multi-repo
		// export is not killed mid-stream by the server's write timeout.
		rc := http.NewResponseController(w)
		// Best-effort: errors here just mean the underlying conn does not
		// support deadlines (e.g. test recorders), in which case the default
		// WriteTimeout still applies.
		_ = rc.SetWriteDeadline(time.Now().Add(csvExportWriteDeadline))

		zw := zip.NewWriter(w)
		defer zw.Close()

		for _, rp := range active {
			prefix := fmt.Sprintf("export-%s/%s-%s/", today, rp.Org, rp.Repo)
			exportRepoCSVs(ctx, zw, s, rp.Org, rp.Repo, p.days, prefix)
		}
	}
}

// csvExportWriteDeadline bounds the streaming response so a slow client or
// a large export cannot hold a connection open indefinitely.
const csvExportWriteDeadline = 10 * time.Minute

func exportRepoCSVs(ctx context.Context, zw *zip.Writer, s data.Store, org, repo string, days int, prefix string) {
	o := &org
	r := &repo
	exportSummaryCSV(ctx, zw, s, o, r, days, prefix)
	exportEventsCSV(ctx, zw, s, o, r, days, prefix)
	exportDevelopersCSV(ctx, zw, s, o, r, days, prefix)
	exportInsightsCSV(ctx, zw, s, o, r, prefix)
	exportContributorCompositionCSV(ctx, zw, s, o, r, days, prefix)
}

func exportSummaryCSV(ctx context.Context, zw *zip.Writer, s data.Store, o, r *string, days int, prefix string) {
	summary, err := s.GetInsightsSummary(ctx, o, r, nil, days)
	if err != nil || summary == nil {
		return
	}
	writeCSVFile(zw, prefix+"summary.csv", [][]string{
		{"metric", "value"},
		{"total_events", fmt.Sprintf("%d", summary.Events)},
		{"contributors", fmt.Sprintf("%d", summary.Contributors)},
		{"repos", fmt.Sprintf("%d", summary.Repos)},
	})
}

func exportEventsCSV(ctx context.Context, zw *zip.Writer, s data.Store, o, r *string, days int, prefix string) {
	criteria := &data.EventSearchCriteria{
		Org:      o,
		Repo:     r,
		Page:     1,
		PageSize: 1000,
	}
	if days > 0 {
		from := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
		criteria.FromDate = &from
	}
	events, err := s.SearchEvents(ctx, criteria)
	if err != nil || len(events) == 0 {
		return
	}
	rows := make([][]string, 0, 1+len(events))
	rows = append(rows, []string{"date", "type", "username", "org", "repo", "url"})
	for _, e := range events {
		if e.Event == nil {
			continue
		}
		rows = append(rows, []string{e.Event.Date, e.Event.Type, e.Event.Username, e.Event.Org, e.Event.Repo, e.Event.URL})
	}
	writeCSVFile(zw, prefix+"events.csv", rows)
}

func exportDevelopersCSV(ctx context.Context, zw *zip.Writer, s data.Store, o, r *string, days int, prefix string) {
	devs, err := s.GetDeveloperPercentages(ctx, nil, o, r, nil, days)
	if err != nil || len(devs) == 0 {
		return
	}
	rows := make([][]string, 0, 1+len(devs))
	rows = append(rows, []string{"developer", "events"})
	for _, d := range devs {
		rows = append(rows, []string{d.Name, fmt.Sprintf("%d", d.Count)})
	}
	writeCSVFile(zw, prefix+"developers.csv", rows)
}

func exportInsightsCSV(ctx context.Context, zw *zip.Writer, s data.Store, o, r *string, prefix string) {
	insights, err := s.GetRepoInsights(ctx, o, r)
	if err != nil || len(insights) == 0 {
		return
	}
	rows := [][]string{{"type", "headline", "detail", "generated_at", "model"}}
	for _, ri := range insights {
		if ri.Insights == nil {
			continue
		}
		for _, obs := range ri.Insights.Observations {
			rows = append(rows, []string{"observation", obs.Headline, obs.Detail, ri.GeneratedAt, ri.Model})
		}
		for _, act := range ri.Insights.Actions {
			rows = append(rows, []string{"action", act.Headline, act.Detail, ri.GeneratedAt, ri.Model})
		}
	}
	if len(rows) > 1 {
		writeCSVFile(zw, prefix+"insights.csv", rows)
	}
}

func exportContributorCompositionCSV(ctx context.Context, zw *zip.Writer, s data.Store, o, r *string, days int, prefix string) {
	comp, err := s.GetContributorComposition(ctx, o, r, nil, days)
	if err != nil || comp == nil || comp.Total == 0 {
		return
	}
	rows := [][]string{
		{"role", "count"},
		{"reviewers", fmt.Sprintf("%d", comp.Reviewers)},
		{"authors", fmt.Sprintf("%d", comp.Authors)},
		{"commenters", fmt.Sprintf("%d", comp.Commenters)},
		{"observers", fmt.Sprintf("%d", comp.Observers)},
	}
	writeCSVFile(zw, prefix+"contributor_composition.csv", rows)
}

func writeCSVFile(zw *zip.Writer, name string, rows [][]string) {
	f, err := zw.Create(name)
	if err != nil {
		slog.Error("creating zip entry", "name", name, "error", err)
		return
	}
	w := csv.NewWriter(f)
	if err := w.WriteAll(rows); err != nil {
		slog.Error("writing csv", "name", name, "error", err)
	}
}
