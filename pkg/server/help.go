package server

import (
	"database/sql"
	"fmt"
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
	Plans    map[string]plan.Limits
	Username string
	Name     string
	Email    string
	Sent     bool
	Error    string
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
			Title: "Help",
			Plans: plan.All,
		}
		if tn := tryGetTenant(r, db); tn != nil {
			d.Username = tn.Username
			d.Name = tn.Name
			d.Email = tn.Email
		}
		renderTemplate(w, "help.html", d)
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
			renderHelpWithError(w, tn, "Contact form is not available at this time.")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		message := strings.TrimSpace(r.FormValue("message"))
		if message == "" {
			renderHelpWithError(w, tn, "Please enter a message.")
			return
		}

		subject := fmt.Sprintf("DevPulse Support Request - %s (%s)", tn.Username, tn.Name)
		text := fmt.Sprintf("From: %s (%s)\nEmail: %s\n\n%s", tn.Username, tn.Name, tn.Email, message)
		html := fmt.Sprintf(
			`<p><strong>From:</strong> %s (%s)<br><strong>Email:</strong> %s</p><hr><p style="white-space:pre-wrap;">%s</p>`,
			tn.Username, tn.Name, tn.Email, message,
		)

		if err := devnet.SendEmail(r.Context(), apiKey, supportEmail, supportEmail, subject, html, text, tn.Email); err != nil {
			slog.Error("sending support email", "username", tn.Username, "error", err)
			renderHelpWithError(w, tn, "Failed to send message. Please try again later.")
			return
		}

		slog.Info("support email sent", "from", tn.Email, "username", tn.Username)

		d := helpData{
			Title:    "Help",
			Plans:    plan.All,
			Username: tn.Username,
			Name:     tn.Name,
			Email:    tn.Email,
			Sent:     true,
		}
		renderTemplate(w, "help.html", d)
	}
}

func renderHelpWithError(w http.ResponseWriter, tn *tenant.Tenant, msg string) {
	d := helpData{
		Title:    "Help",
		Plans:    plan.All,
		Username: tn.Username,
		Name:     tn.Name,
		Email:    tn.Email,
		Error:    msg,
	}
	renderTemplate(w, "help.html", d)
}
