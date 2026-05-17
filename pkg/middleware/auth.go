package middleware

import (
	"context"
	"database/sql"
	"errors"
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

// init reads BASE_URL at package load time; env var must be set before import.
func init() {
	secure = strings.HasPrefix(os.Getenv("BASE_URL"), "https://")
	cookieName = cookieNameFor(secure)
}

func cookieNameFor(isSecure bool) string {
	if isSecure {
		return "__Host-session"
	}
	return "session"
}

// SessionCookieName returns the session cookie name based on the BASE_URL scheme.
func SessionCookieName() string {
	return cookieName
}

// OAuthStateCookieName returns the OAuth state cookie name, using the __Host-
// prefix in HTTPS mode for consistency with the session cookie.
func OAuthStateCookieName() string {
	if secure {
		return "__Host-oauth_state"
	}
	return "oauth_state"
}

// IsSecure returns true when the BASE_URL uses HTTPS.
func IsSecure() bool {
	return secure
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
				ClearSessionCookie(w)
				if errors.Is(err, tenant.ErrAccountSuspended) {
					http.Redirect(w, r, "/suspended", http.StatusFound)
					return
				}
				slog.Debug("invalid session", "error", err)
				http.Redirect(w, r, loginURL, http.StatusFound)
				return
			}

			ctx := context.WithValue(r.Context(), tenantContextKey, tn)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WithTenantContext injects a tenant into the context. Intended for tests.
func WithTenantContext(ctx context.Context, tn *tenant.Tenant) context.Context {
	return context.WithValue(ctx, tenantContextKey, tn)
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
	//nolint:gosec // G124: Secure/HttpOnly/SameSite are set; gosec can't trace 'secure' package var
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
