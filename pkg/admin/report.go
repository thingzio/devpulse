package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/thingzio/devpulse/pkg/config"
)

const defaultReportDays = 1

func loadReportConfig() *reportConfig {
	key := os.Getenv("SENDGRID_API_KEY")
	if key == "" {
		return nil
	}
	to := os.Getenv("REPORT_TO_EMAIL")
	if to == "" {
		return nil
	}
	from := os.Getenv("REPORT_FROM_EMAIL")
	if from == "" {
		return nil
	}
	return &reportConfig{
		SendGridAPIKey: key,
		ToEmail:        to,
		FromEmail:      from,
		SubjectPrefix:  config.GetEnv("REPORT_SUBJECT_PREFIX", "DevPulse Daily Report"),
	}
}

// deltaIntStr formats an int delta pointer as "+N (X.Y%)" or returns "—" if nil.
func deltaIntStr(val *int, pct *float64) string {
	if val == nil {
		return "\u2014"
	}
	sign := "+"
	if *val < 0 {
		sign = ""
	}
	if pct != nil {
		return fmt.Sprintf("%s%d (%.1f%%)", sign, *val, *pct)
	}
	return fmt.Sprintf("%s%d", sign, *val)
}

// deltaInt64Str formats an int64 delta pointer similarly.
func deltaInt64Str(val *int64, pct *float64) string {
	if val == nil {
		return "\u2014"
	}
	sign := "+"
	if *val < 0 {
		sign = ""
	}
	if pct != nil {
		return fmt.Sprintf("%s%d (%.1f%%)", sign, *val, *pct)
	}
	return fmt.Sprintf("%s%d", sign, *val)
}

type metricRow struct {
	label    string
	current  string
	dod      string
	wow      string
	mom      string
	invertColor bool // true = positive is bad (e.g. errors)
}

func buildMetricRows(s summaryResponse) []metricRow {
	cur := s.Current
	rows := []metricRow{
		{label: "Tenants", current: fmt.Sprintf("%d", cur.Tenants)},
		{label: "Free", current: fmt.Sprintf("%d", cur.TenantsFree)},
		{label: "Starter", current: fmt.Sprintf("%d", cur.TenantsStarter)},
		{label: "Pro", current: fmt.Sprintf("%d", cur.TenantsPro)},
		{label: "Enterprise", current: fmt.Sprintf("%d", cur.TenantsEnterprise)},
		{label: "Repos", current: fmt.Sprintf("%d", cur.Repos)},
		{label: "Events", current: fmt.Sprintf("%d", cur.Events)},
		{label: "Contributors", current: fmt.Sprintf("%d", cur.Contributors)},
		{label: "Installations", current: fmt.Sprintf("%d", cur.Installations)},
		{label: "Repos w/ Errors", current: fmt.Sprintf("%d", cur.ReposWithErrors), invertColor: true},
	}

	type deltaFields struct {
		d *statsDelta
	}

	extractDelta := func(d *statsDelta, idx int) string {
		if d == nil {
			return "\u2014"
		}
		switch idx {
		case 0:
			return deltaIntStr(d.Tenants, d.TenantsPct)
		case 1, 2, 3, 4:
			// Plan breakdown deltas not tracked individually in statsDelta
			return "\u2014"
		case 5:
			return deltaIntStr(d.Repos, d.ReposPct)
		case 6:
			return deltaInt64Str(d.Events, d.EventsPct)
		case 7:
			return deltaIntStr(d.Contributors, d.ContribPct)
		case 8:
			return deltaIntStr(d.Installations, d.InstallPct)
		case 9:
			return deltaIntStr(d.ReposWithErrors, d.ErrorsPct)
		default:
			return "\u2014"
		}
	}

	for i := range rows {
		rows[i].dod = extractDelta(s.DoD, i)
		rows[i].wow = extractDelta(s.WoW, i)
		rows[i].mom = extractDelta(s.MoM, i)
	}

	return rows
}

// deltaColor returns an inline CSS color for a delta cell.
func deltaColor(cell string, invert bool) string {
	if cell == "\u2014" {
		return "#888"
	}
	positive := strings.HasPrefix(cell, "+")
	if invert {
		positive = !positive
	}
	if positive {
		return "#16a34a"
	}
	return "#dc2626"
}

func renderReportHTML(summary summaryResponse, analysis string) string {
	var b strings.Builder

	b.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"></head><body style="font-family:sans-serif;color:#1a1a1a;max-width:700px;margin:0 auto;padding:16px;">`)
	fmt.Fprintf(&b, `<h2 style="margin-bottom:4px;">DevPulse Daily Report</h2><p style="color:#666;margin-top:0;">%s</p>`, summary.Date)

	// Section 1: Platform Summary
	b.WriteString(`<h3>Platform Summary</h3>`)
	b.WriteString(`<table style="border-collapse:collapse;width:100%;font-size:14px;">`)
	b.WriteString(`<tr style="background:#f3f4f6;"><th style="padding:8px;text-align:left;border-bottom:2px solid #d1d5db;">Metric</th>`)
	b.WriteString(`<th style="padding:8px;text-align:right;border-bottom:2px solid #d1d5db;">Current</th>`)
	b.WriteString(`<th style="padding:8px;text-align:right;border-bottom:2px solid #d1d5db;">DoD</th>`)
	b.WriteString(`<th style="padding:8px;text-align:right;border-bottom:2px solid #d1d5db;">WoW</th>`)
	b.WriteString(`<th style="padding:8px;text-align:right;border-bottom:2px solid #d1d5db;">MoM</th></tr>`)

	rows := buildMetricRows(summary)
	for _, r := range rows {
		b.WriteString(`<tr>`)
		fmt.Fprintf(&b, `<td style="padding:6px 8px;border-bottom:1px solid #e5e7eb;">%s</td>`, r.label)
		fmt.Fprintf(&b, `<td style="padding:6px 8px;text-align:right;border-bottom:1px solid #e5e7eb;font-weight:600;">%s</td>`, r.current)
		fmt.Fprintf(&b, `<td style="padding:6px 8px;text-align:right;border-bottom:1px solid #e5e7eb;color:%s;">%s</td>`, deltaColor(r.dod, r.invertColor), r.dod)
		fmt.Fprintf(&b, `<td style="padding:6px 8px;text-align:right;border-bottom:1px solid #e5e7eb;color:%s;">%s</td>`, deltaColor(r.wow, r.invertColor), r.wow)
		fmt.Fprintf(&b, `<td style="padding:6px 8px;text-align:right;border-bottom:1px solid #e5e7eb;color:%s;">%s</td>`, deltaColor(r.mom, r.invertColor), r.mom)
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</table>`)

	// Section 2: Repos with errors
	if len(summary.ErrorRepos) > 0 {
		b.WriteString(`<h3>Repos with Import Errors</h3>`)
		b.WriteString(`<table style="border-collapse:collapse;width:100%;font-size:14px;">`)
		b.WriteString(`<tr style="background:#f3f4f6;"><th style="padding:8px;text-align:left;border-bottom:2px solid #d1d5db;">Org/Repo</th>`)
		b.WriteString(`<th style="padding:8px;text-align:right;border-bottom:2px solid #d1d5db;">Errors</th>`)
		b.WriteString(`<th style="padding:8px;text-align:left;border-bottom:2px solid #d1d5db;">Last Error</th></tr>`)
		for _, er := range summary.ErrorRepos {
			fmt.Fprintf(&b, `<tr><td style="padding:6px 8px;border-bottom:1px solid #e5e7eb;">%s/%s</td>`, er.Org, er.Repo)
			fmt.Fprintf(&b, `<td style="padding:6px 8px;text-align:right;border-bottom:1px solid #e5e7eb;color:#dc2626;">%d</td>`, er.Errors)
			fmt.Fprintf(&b, `<td style="padding:6px 8px;border-bottom:1px solid #e5e7eb;font-size:12px;color:#666;">%s</td></tr>`, er.LastError)
		}
		b.WriteString(`</table>`)
	}

	// Section 3: Infrastructure Analysis
	b.WriteString(`<h3>Infrastructure Analysis</h3>`)
	if analysis == "" {
		b.WriteString(`<div style="padding:12px;background:#fef3c7;border-radius:6px;color:#92400e;">Metrics analysis unavailable</div>`)
	} else {
		fmt.Fprintf(&b, `<div style="padding:12px;background:#f9fafb;border:1px solid #e5e7eb;border-radius:6px;white-space:pre-wrap;font-size:13px;">%s</div>`, analysis)
	}

	b.WriteString(`</body></html>`)
	return b.String()
}

func renderReportText(summary summaryResponse, analysis string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "DevPulse Daily Report  %s\n", summary.Date)
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	b.WriteString("PLATFORM SUMMARY\n")
	fmt.Fprintf(&b, "%-20s %10s %14s %14s %14s\n", "Metric", "Current", "DoD", "WoW", "MoM")
	b.WriteString(strings.Repeat("-", 72) + "\n")

	rows := buildMetricRows(summary)
	for _, r := range rows {
		fmt.Fprintf(&b, "%-20s %10s %14s %14s %14s\n", r.label, r.current, r.dod, r.wow, r.mom)
	}

	if len(summary.ErrorRepos) > 0 {
		b.WriteString("\nREPOS WITH IMPORT ERRORS\n")
		fmt.Fprintf(&b, "%-30s %8s  %s\n", "Org/Repo", "Errors", "Last Error")
		b.WriteString(strings.Repeat("-", 72) + "\n")
		for _, er := range summary.ErrorRepos {
			fmt.Fprintf(&b, "%-30s %8d  %s\n", er.Org+"/"+er.Repo, er.Errors, er.LastError)
		}
	}

	b.WriteString("\nINFRASTRUCTURE ANALYSIS\n")
	b.WriteString(strings.Repeat("-", 72) + "\n")
	if analysis == "" {
		b.WriteString("Metrics analysis unavailable\n")
	} else {
		b.WriteString(analysis + "\n")
	}

	return b.String()
}

func sendEmail(ctx context.Context, cfg *reportConfig, subject, html, text string) error {
	payload := map[string]any{
		"personalizations": []map[string]any{
			{"to": []map[string]string{{"email": cfg.ToEmail}}},
		},
		"from":    map[string]string{"email": cfg.FromEmail},
		"subject": subject,
		"content": []map[string]string{
			{"type": "text/plain", "value": text},
			{"type": "text/html", "value": html},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling email payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.sendgrid.com/v3/mail/send", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating sendgrid request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.SendGridAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := analysisClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending email via sendgrid: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	truncated := string(respBody)
	if len(truncated) > 200 {
		truncated = truncated[:200]
	}
	return fmt.Errorf("sendgrid returned %d: %s", resp.StatusCode, truncated)
}

func handleReport(db *sql.DB, mcfg *metricsConfig, rcfg *reportConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rcfg == nil {
			http.Error(w, "report not configured", http.StatusServiceUnavailable)
			return
		}

		ctx := r.Context()

		summary, err := collectSummary(ctx, db)
		if err != nil {
			slog.Error("collecting summary for report", "error", err)
			http.Error(w, "error collecting summary", http.StatusInternalServerError)
			return
		}

		var analysis string

		token, err := gcpAccessToken(ctx)
		if err != nil {
			slog.Warn("failed to get GCP token for report, skipping metrics", "error", err)
		} else {
			metrics := collectAllMetrics(ctx, mcfg, token, defaultReportDays)
			a, aErr := analyzeMetrics(ctx, mcfg, metrics)
			if aErr != nil {
				slog.Warn("metrics analysis failed for report, continuing without", "error", aErr)
			} else {
				analysis = a
			}
		}

		subject := rcfg.SubjectPrefix + " \u2014 " + summary.Date
		html := renderReportHTML(summary, analysis)
		text := renderReportText(summary, analysis)

		if err := sendEmail(ctx, rcfg, subject, html, text); err != nil {
			slog.Error("sending report email", "error", err)
			writeJSON(w, http.StatusInternalServerError, reportResponse{
				Error: "failed to send email",
			})
			return
		}

		slog.Info("daily report sent", "to", rcfg.ToEmail, "date", summary.Date)
		writeJSON(w, http.StatusOK, reportResponse{Sent: true})
	}
}
