package server

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/thingzio/devpulse/pkg/middleware"
	devnet "github.com/thingzio/devpulse/pkg/net"
	"github.com/thingzio/devpulse/pkg/plan"
	"github.com/thingzio/devpulse/pkg/tenant"
)

type helpData struct {
	Title    string
	PlanMap  map[string]plan.Plan
	Plans    []plan.Plan
	Features []plan.Feature
	Username string
	Name     string
	Email    string
	Sent     bool
	Error    string
	Plan     string
	Repos    int
	Created  string
}

// tryGetTenant attempts to read the session cookie and validate it.
// Returns nil if not authenticated — does not redirect.
func tryGetTenant(r *http.Request, db *sql.DB) *tenant.Tenant {
	cookie, err := r.Cookie(middleware.SessionCookieName())
	if err != nil {
		return nil
	}
	tn, err := tenant.ValidateSession(r.Context(), db, cookie.Value)
	if err != nil {
		return nil
	}
	return tn
}

func helpPageHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d := helpData{
			Title:   "Help",
			PlanMap: plan.All, Plans: plan.DisplayPlans(), Features: plan.DisplayFeatures(),
		}
		if tn := tryGetTenant(r, db); tn != nil {
			populateHelpTenant(r.Context(), &d, tn, db)
		}
		renderTemplate(w, "help.html", d)
	}
}

func populateHelpTenant(ctx context.Context, d *helpData, tn *tenant.Tenant, db *sql.DB) {
	d.Username = tn.Username
	d.Name = tn.Name
	d.Email = tn.Email
	d.Plan = tn.Plan
	d.Created = tn.CreatedAt.Format("2006-01-02")
	if count, err := tenant.CountTenantRepos(ctx, db, tn.ID); err == nil {
		d.Repos = count
	}
}

func helpContactHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := tryGetTenant(r, db)
		if tn == nil || tn.Email == "" {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}

		apiKey := os.Getenv("SEND_API_KEY")
		supportEmail := os.Getenv("SUPPORT_EMAIL")
		if apiKey == "" || supportEmail == "" {
			slog.Error("support email not configured")
			renderHelpWithError(w, r, db, tn, "Contact form is not available at this time.")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		message := strings.TrimSpace(r.FormValue("message"))
		if message == "" {
			renderHelpWithError(w, r, db, tn, "Please enter a message.")
			return
		}

		repoCount, _ := tenant.CountTenantRepos(r.Context(), db, tn.ID)
		created := tn.CreatedAt.Format("2006-01-02")

		subject := fmt.Sprintf("DevPulse Support Request - %s (%s)", tn.Username, tn.Name)
		text := fmt.Sprintf("From: %s (%s)\nEmail: %s\nPlan: %s\nRepos: %d\nMember since: %s\n\n%s",
			tn.Username, tn.Name, tn.Email, tn.Plan, repoCount, created, message)
		htmlBody := fmt.Sprintf(
			`<p><strong>From:</strong> %s (%s)<br><strong>Email:</strong> %s<br>`+
				`<strong>Plan:</strong> %s<br><strong>Repos:</strong> %d<br>`+
				`<strong>Member since:</strong> %s</p><hr><p style="white-space:pre-wrap;">%s</p>`,
			html.EscapeString(tn.Username), html.EscapeString(tn.Name),
			html.EscapeString(tn.Email), html.EscapeString(tn.Plan),
			repoCount, created, html.EscapeString(message),
		)

		if err := devnet.SendEmail(r.Context(), apiKey, supportEmail, supportEmail, subject, htmlBody, text, tn.Email); err != nil {
			slog.Error("sending support email", "username", tn.Username, "error", err)
			renderHelpWithError(w, r, db, tn, "Failed to send message. Please try again later.")
			return
		}

		slog.Info("support email sent", "from", tn.Email, "username", tn.Username)

		d := helpData{
			Title:   "Help",
			PlanMap: plan.All, Plans: plan.DisplayPlans(), Features: plan.DisplayFeatures(),
			Sent: true,
		}
		populateHelpTenant(r.Context(), &d, tn, db)
		renderTemplate(w, "help.html", d)
	}
}

func renderHelpWithError(w http.ResponseWriter, r *http.Request, db *sql.DB, tn *tenant.Tenant, msg string) {
	d := helpData{
		Title:   "Help",
		PlanMap: plan.All, Plans: plan.DisplayPlans(), Features: plan.DisplayFeatures(),
		Error: msg,
	}
	populateHelpTenant(r.Context(), &d, tn, db)
	renderTemplate(w, "help.html", d)
}
