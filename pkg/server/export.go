package server

import (
	"archive/zip"
	"context"
	"database/sql"
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

func csvExportHandler(defaultStore data.Store, db *sql.DB) http.HandlerFunc {
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

		repos, err := tenant.ListTenantRepos(ctx, db, tn.ID)
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
		filename := fmt.Sprintf("devpulse-export-%s.zip", today)

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

		zw := zip.NewWriter(w)
		defer zw.Close()

		for _, rp := range active {
			prefix := fmt.Sprintf("export-%s/%s-%s/", today, rp.Org, rp.Repo)
			exportRepoCSVs(ctx, zw, s, rp.Org, rp.Repo, p.months, prefix)
		}
	}
}

func exportRepoCSVs(ctx context.Context, zw *zip.Writer, s data.Store, org, repo string, months int, prefix string) {
	o := &org
	r := &repo
	exportSummaryCSV(ctx, zw, s, o, r, months, prefix)
	exportEventsCSV(ctx, zw, s, o, r, months, prefix)
	exportDevelopersCSV(ctx, zw, s, o, r, months, prefix)
	exportInsightsCSV(ctx, zw, s, o, r, prefix)
	exportReputationCSV(ctx, zw, s, o, r, months, prefix)
}

func exportSummaryCSV(ctx context.Context, zw *zip.Writer, s data.Store, o, r *string, months int, prefix string) {
	summary, err := s.GetInsightsSummary(ctx, o, r, nil, months)
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

func exportEventsCSV(ctx context.Context, zw *zip.Writer, s data.Store, o, r *string, months int, prefix string) {
	criteria := &data.EventSearchCriteria{
		Org:      o,
		Repo:     r,
		Page:     1,
		PageSize: 1000,
	}
	if months > 0 {
		from := time.Now().UTC().AddDate(0, -months, 0).Format("2006-01-02")
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

func exportDevelopersCSV(ctx context.Context, zw *zip.Writer, s data.Store, o, r *string, months int, prefix string) {
	devs, err := s.GetDeveloperPercentages(ctx, nil, o, r, nil, months)
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

func exportReputationCSV(ctx context.Context, zw *zip.Writer, s data.Store, o, r *string, months int, prefix string) {
	rep, err := s.GetReputationDistribution(ctx, o, r, nil, months)
	if err != nil || rep == nil || len(rep.Labels) == 0 {
		return
	}
	rows := make([][]string, 0, 1+len(rep.Labels))
	rows = append(rows, []string{"range", "count"})
	for i, label := range rep.Labels {
		val := ""
		if i < len(rep.Data) {
			val = fmt.Sprintf("%.0f", rep.Data[i])
		}
		rows = append(rows, []string{label, val})
	}
	writeCSVFile(zw, prefix+"reputation.csv", rows)
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
