package digest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"html/template"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/postgres"
	devnet "github.com/thingzio/devpulse/pkg/net"
	"github.com/thingzio/devpulse/pkg/tenant"
)

const (
	digestDays       = 180
	maxSignals       = 5
	fromEmail        = "DevPulse <noreply@thingz.io>"
	emailSubject     = "Your weekly DevPulse digest"
	sendTimeout      = 30 * time.Second
	perTenantTimeout = 15 * time.Second

	severityCritical = "critical"
	severityWarning  = "warning"
)

// Config holds configuration for the digest sender.
type Config struct {
	ResendAPIKey string
	BaseURL      string
	HMACSecret   string

	// TestUsername restricts sending to a single tenant (by username).
	// When set, only that tenant receives the digest; all others are skipped.
	// Derived from DEVPULSE_ADMIN_USERS when DIGEST_ADMIN_ONLY is true.
	TestUsername string
}

// NewConfigFromEnv creates a Config from environment variables.
// Returns nil if any required variable (SEND_API_KEY, BASE_URL,
// DIGEST_HMAC_SECRET) is missing.
func NewConfigFromEnv() *Config {
	apiKey := os.Getenv("SEND_API_KEY")
	if apiKey == "" {
		return nil
	}
	baseURL := os.Getenv("BASE_URL")
	if baseURL == "" {
		return nil
	}
	secret := os.Getenv("DIGEST_HMAC_SECRET")
	if secret == "" {
		return nil
	}
	var testUser string
	if os.Getenv("DIGEST_ADMIN_ONLY") != "false" {
		if admins := os.Getenv("DEVPULSE_ADMIN_USERS"); admins != "" {
			testUser, _, _ = strings.Cut(admins, ",")
		}
	}

	return &Config{
		ResendAPIKey: apiKey,
		BaseURL:      baseURL,
		HMACSecret:   secret,
		TestUsername: testUser,
	}
}

// HMACSecret returns the DIGEST_HMAC_SECRET env var. Used by the
// unsubscribe handler to read the secret at request time rather than
// caching it at startup.
func HMACSecret() string {
	return os.Getenv("DIGEST_HMAC_SECRET")
}

// digestDay returns a deterministic day-of-week (0=Sunday..6=Saturday) for a tenant.
func digestDay(tenantID string) int {
	h := fnv.New32a()
	h.Write([]byte(tenantID))
	return int(h.Sum32() % 7)
}

// UnsubscribeToken generates an HMAC-SHA256 token for one-click unsubscribe.
func UnsubscribeToken(secret, tenantID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(tenantID))
	return hex.EncodeToString(mac.Sum(nil))
}

// ValidateUnsubscribeToken checks an unsubscribe token.
func ValidateUnsubscribeToken(secret, tenantID, token string) bool {
	expected := UnsubscribeToken(secret, tenantID)
	return hmac.Equal([]byte(expected), []byte(token))
}

// digestCooldown is the minimum time between digest sends for a single tenant.
// Set below 7 days so the deterministic weekday rotation isn't blocked by
// clock drift or scheduler jitter.
const digestCooldown = 6 * 24 * time.Hour

// Run sends weekly digest emails to eligible tenants whose deterministic
// send-day matches today's day of week.
//
// When Config.TestUsername is set, only that tenant receives a digest
// (day-of-week check is skipped).
func Run(ctx context.Context, db *sql.DB, cfg *Config) error {
	now := time.Now()
	today := int(now.Weekday())

	if cfg.TestUsername != "" {
		slog.Info("digest running in test mode", "test_username", cfg.TestUsername)
	}

	tenants, err := tenant.ListDigestTenants(ctx, db)
	if err != nil {
		return fmt.Errorf("listing digest tenants: %w", err)
	}

	var sent, skipped int
	for _, dt := range tenants {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// In test mode, only send to the specified user.
		if cfg.TestUsername != "" && dt.Username != cfg.TestUsername {
			skipped++
			continue
		}

		// In normal mode, only send on the tenant's deterministic day.
		if cfg.TestUsername == "" && digestDay(dt.ID) != today {
			skipped++
			continue
		}

		// Skip if already sent within the cooldown window.
		if dt.LastSentAt != nil && now.Sub(*dt.LastSentAt) < digestCooldown {
			slog.Debug("digest already sent recently, skipping",
				"tenant", dt.ID, "last_sent", dt.LastSentAt.Format(time.RFC3339))
			skipped++
			continue
		}

		if sendErr := sendDigest(ctx, db, cfg, dt); sendErr != nil {
			slog.Error("sending digest", "tenant", dt.ID, "email", dt.Email, "error", sendErr)
			continue
		}
		sent++
	}

	slog.Info("digest run complete", "sent", sent, "skipped", skipped, "total", len(tenants))
	return nil
}

// sendDigest gathers data and sends the email for a single tenant.
func sendDigest(ctx context.Context, db *sql.DB, cfg *Config, dt tenant.DigestTenant) error {
	tCtx, cancel := context.WithTimeout(ctx, perTenantTimeout)
	defer cancel()

	conn, err := db.Conn(tCtx)
	if err != nil {
		return fmt.Errorf("acquiring connection: %w", err)
	}
	defer conn.Close()

	if _, setErr := conn.ExecContext(tCtx,
		"SELECT set_config('app.tenant_id', $1, false)", dt.ID); setErr != nil {
		return fmt.Errorf("setting tenant scope: %w", setErr)
	}
	defer func() {
		// Use WithoutCancel so the RLS reset still runs even if the parent
		// ctx is canceled. Bound with an independent timeout because the
		// underlying conn.Close() will release the conn to the pool next.
		cleanupCtx, c := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer c()
		_, _ = conn.ExecContext(cleanupCtx, "SELECT set_config('app.tenant_id', '', false)")
	}()

	store := postgres.NewFromConn(conn)

	signals, err := store.GetSignals(tCtx, nil, maxSignals)
	if err != nil {
		return fmt.Errorf("getting signals: %w", err)
	}

	portfolio, err := store.GetPortfolioSummary(tCtx, nil, nil, digestDays)
	if err != nil {
		return fmt.Errorf("getting portfolio summary: %w", err)
	}

	if len(signals) == 0 && portfolio == nil {
		slog.Debug("no digest content, skipping", "tenant", dt.ID)
		return nil
	}

	unsubToken := UnsubscribeToken(cfg.HMACSecret, dt.ID)
	unsubURL := fmt.Sprintf("%s/digest/unsubscribe?tenant=%s&token=%s", cfg.BaseURL, dt.ID, unsubToken)
	dashURL := cfg.BaseURL

	htmlBody := renderHTML(portfolio, signals, dashURL, unsubURL)
	textBody := renderText(portfolio, signals, dashURL, unsubURL)

	sendCtx, sendCancel := context.WithTimeout(ctx, sendTimeout)
	defer sendCancel()

	if err := devnet.SendEmail(sendCtx, cfg.ResendAPIKey, fromEmail, dt.Email, emailSubject, htmlBody, textBody, ""); err != nil {
		return err
	}

	if updateErr := tenant.UpdateDigestLastSent(ctx, db, dt.ID); updateErr != nil {
		slog.Error("recording digest sent timestamp", "tenant", dt.ID, "error", updateErr)
	}
	return nil
}

// digestData holds all values needed by the HTML digest template.
type digestData struct {
	Portfolio *data.PortfolioSummary
	Signals   []*data.Signal
	DashURL   string
	UnsubURL  string
	KPIs      []kpi
}

type kpi struct {
	Label string
	Value string
	Delta template.HTML
}

func severityColor(sev string) string {
	switch sev {
	case severityCritical:
		return "#ef4444"
	case severityWarning:
		return "#f59e0b"
	default:
		return "#4ade80"
	}
}

//nolint:lll // HTML email template lines are long by nature
var digestTmpl = template.Must(template.New("digest").Funcs(template.FuncMap{
	"severityColor": severityColor,
}).Parse(`<!DOCTYPE html><html><head><meta charset="utf-8"></head>
<body style="margin:0;padding:0;background:#0c1017;color:#f0f0f0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
<div style="max-width:600px;margin:0 auto;padding:24px;">
<div style="text-align:center;margin-bottom:24px;">
<h1 style="color:#f0f0f0;font-size:20px;margin:0;">DevPulse Weekly Digest</h1>
<p style="color:#999;font-size:13px;margin:4px 0 0;">Your project health highlights this week</p>
</div>
{{- if .Portfolio}}
<div style="background:#171a22;border:1px solid #242836;border-radius:6px;padding:16px;margin-bottom:16px;">
<h2 style="color:#f0f0f0;font-size:15px;margin:0 0 12px;">Portfolio Pulse</h2>
<table style="width:100%;color:#f0f0f0;font-size:13px;" cellpadding="0" cellspacing="0">
{{- range .KPIs}}
<tr><td style="padding:4px 0;color:#999;">{{.Label}}</td><td style="padding:4px 0;text-align:right;color:#f0f0f0;font-weight:600;">{{.Value}}</td>{{if .Delta}}<td style="padding:4px 0;text-align:right;color:#999;font-size:12px;">{{.Delta}}</td>{{else}}<td style="padding:4px 0;"></td>{{end}}</tr>
{{- end}}
</table></div>
{{- end}}
{{- if .Signals}}
<div style="background:#171a22;border:1px solid #242836;border-radius:6px;padding:16px;margin-bottom:16px;">
<h2 style="color:#f0f0f0;font-size:15px;margin:0 0 12px;">Notable Changes</h2>
{{- range .Signals}}
<div style="padding:8px 0;border-bottom:1px solid #242836;">
<div style="color:{{severityColor .Severity}};font-size:12px;font-weight:600;">{{.Org}}/{{.Repo}} &middot; {{.Metric}}</div>
<div style="color:#f0f0f0;font-size:13px;margin-top:2px;">{{.Message}}</div>
</div>
{{- end}}
</div>
{{- end}}
<div style="text-align:center;margin-top:24px;">
<a href="{{.DashURL}}" style="display:inline-block;background:#4a9eff;color:#fff;text-decoration:none;padding:10px 24px;border-radius:6px;font-size:14px;font-weight:600;">View Dashboard</a>
</div>
<div style="text-align:center;margin-top:32px;padding-top:16px;border-top:1px solid #242836;">
<p style="color:#999;font-size:11px;margin:0;">
You're receiving this because you have a DevPulse account with weekly digests enabled.<br>
<a href="{{.UnsubURL}}" style="color:#999;text-decoration:underline;">Unsubscribe</a>
</p>
</div>
</div></body></html>`))

// renderHTML generates the HTML version of the digest email.
func renderHTML(ps *data.PortfolioSummary, signals []*data.Signal, dashURL, unsubURL string) string {
	d := digestData{
		Portfolio: ps,
		Signals:   signals,
		DashURL:   dashURL,
		UnsubURL:  unsubURL,
	}

	if ps != nil {
		d.KPIs = buildKPIs(ps)
	}

	var buf bytes.Buffer
	if err := digestTmpl.Execute(&buf, d); err != nil {
		slog.Error("rendering digest HTML", "error", err)
		return ""
	}
	return buf.String()
}

func buildKPIs(ps *data.PortfolioSummary) []kpi {
	kpis := []kpi{
		{Label: "Stars", Value: fmt.Sprintf("%d", ps.TotalStars), Delta: template.HTML(formatDelta(ps.StarsDelta, ps.StarsDeltaPct))}, //nolint:gosec // computed internally, not user input
		{Label: "Forks", Value: fmt.Sprintf("%d", ps.TotalForks), Delta: template.HTML(formatDelta(ps.ForksDelta, ps.ForksDeltaPct))}, //nolint:gosec // computed internally, not user input
		{Label: "Open Issues", Value: fmt.Sprintf("%d", ps.TotalOpenIssues)},
		{Label: "Contributors", Value: fmt.Sprintf("%d", ps.TotalContributors)},
	}
	if ps.MedianMergeHours > 0 {
		kpis = append(kpis, kpi{Label: "Median Merge Time", Value: fmt.Sprintf("%.1fh", ps.MedianMergeHours)})
	}
	return kpis
}

func formatDelta(delta int, pct float64) string {
	if delta == 0 {
		return ""
	}
	sign := "+"
	if delta < 0 {
		sign = ""
	}
	return fmt.Sprintf("%s%d (%.1f%%)", sign, delta, pct)
}

// renderText generates the plain-text version of the digest email.
func renderText(ps *data.PortfolioSummary, signals []*data.Signal, dashURL, unsubURL string) string {
	text := "DevPulse Weekly Digest\n"
	text += "Your project health highlights this week\n\n"

	if ps != nil {
		text += "PORTFOLIO PULSE\n"
		text += fmt.Sprintf("  Stars:        %d %s\n", ps.TotalStars, formatDelta(ps.StarsDelta, ps.StarsDeltaPct))
		text += fmt.Sprintf("  Forks:        %d %s\n", ps.TotalForks, formatDelta(ps.ForksDelta, ps.ForksDeltaPct))
		text += fmt.Sprintf("  Open Issues:  %d\n", ps.TotalOpenIssues)
		text += fmt.Sprintf("  Contributors: %d\n", ps.TotalContributors)
		if ps.MedianMergeHours > 0 {
			text += fmt.Sprintf("  Median Merge: %.1fh\n", ps.MedianMergeHours)
		}
		text += "\n"
	}

	if len(signals) > 0 {
		text += "NOTABLE CHANGES\n"
		for _, s := range signals {
			text += fmt.Sprintf("  [%s] %s/%s - %s: %s\n", s.Severity, s.Org, s.Repo, s.Metric, s.Message)
		}
		text += "\n"
	}

	text += fmt.Sprintf("View Dashboard: %s\n\n", dashURL)
	text += fmt.Sprintf("Unsubscribe: %s\n", unsubURL)
	return text
}
