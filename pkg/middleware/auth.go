package middleware

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/thingzio/devpulse/pkg/tenant"
)

type contextKey string

const tenantContextKey contextKey = "tenant"

var (
	secure     bool
	cookieName string
)

func init() {
	secure = strings.HasPrefix(os.Getenv("BASE_URL"), "https://")
	if secure {
		cookieName = "__Host-session"
	} else {
		cookieName = "session"
	}
}

// SessionCookieName returns the session cookie name based on the BASE_URL scheme.
func SessionCookieName() string {
	return cookieName
}

// RequireAuth validates the session cookie and injects the tenant into context.
// Redirects to loginURL if no valid session is found.
func RequireAuth(db *sql.DB, loginURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName())
			if err != nil {
				http.Redirect(w, r, loginURL, http.StatusFound)
				return
			}

			tn, err := tenant.ValidateSession(r.Context(), db, cookie.Value)
			if err != nil {
				slog.Debug("invalid session", "error", err)
				ClearSessionCookie(w)
				http.Redirect(w, r, loginURL, http.StatusFound)
				return
			}

			ctx := context.WithValue(r.Context(), tenantContextKey, tn)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// TenantFromContext extracts the tenant from the request context.
func TenantFromContext(ctx context.Context) *tenant.Tenant {
	if t, ok := ctx.Value(tenantContextKey).(*tenant.Tenant); ok {
		return t
	}
	return nil
}

// SetSessionCookie sets the session cookie with security attributes.
func SetSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	// Lax is required for OAuth redirects (cross-site GET from github.com).
	// Strict would block the cookie on the callback redirect.
	sameSite := http.SameSiteLaxMode
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName(),
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   secure,
		HttpOnly: true,
		SameSite: sameSite,
	})
}

// ClearSessionCookie removes the session cookie.
func ClearSessionCookie(w http.ResponseWriter) {
	SetSessionCookie(w, "", -1)
}
