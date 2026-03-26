package middleware

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/mchmarny/devpulse/pkg/tenant"
)

type contextKey string

const tenantContextKey contextKey = "tenant"

const (
	sessionCookieName = "__Host-session"
	sessionMaxAge     = 7 * 24 * 60 * 60 // 7 days in seconds
)

// RequireAuth validates the session cookie and injects the tenant into context.
// Redirects to loginURL if no valid session is found.
func RequireAuth(db *sql.DB, loginURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil {
				http.Redirect(w, r, loginURL, http.StatusFound)
				return
			}

			tn, err := tenant.ValidateSession(db, cookie.Value)
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
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearSessionCookie removes the session cookie.
func ClearSessionCookie(w http.ResponseWriter) {
	SetSessionCookie(w, "", -1)
}
