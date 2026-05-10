package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/thingzio/devpulse/pkg/tenant"
)

const csrfCookieName = "csrf_token"

const adminUsersEnvVar = "DEVPULSE_ADMIN_USERS"

var adminUsers []string

func init() {
	loadAdminUsers()
}

func loadAdminUsers() {
	adminUsers = nil
	raw := os.Getenv(adminUsersEnvVar)
	if raw != "" {
		for entry := range strings.SplitSeq(raw, ",") {
			if u := strings.TrimSpace(entry); u != "" {
				adminUsers = append(adminUsers, strings.ToLower(u))
			}
		}
	}
}

// RequireAdmin validates the session cookie and checks the username against
// the DEVPULSE_ADMIN_USERS whitelist. Returns 404 (not 403) to hide route
// existence from non-admins.
func RequireAdmin(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName())
			if err != nil {
				http.NotFound(w, r)
				return
			}

			tn, err := tenant.ValidateSession(r.Context(), db, cookie.Value)
			if err != nil {
				slog.Debug("admin: invalid session", "error", err)
				http.NotFound(w, r)
				return
			}

			if !IsAdmin(tn.Username) {
				slog.Warn("admin access denied",
					"username", tn.Username,
					"path", r.URL.Path,
					"remote", r.RemoteAddr,
				)
				http.NotFound(w, r)
				return
			}

			slog.Info("admin access",
				"username", tn.Username,
				"path", r.URL.Path,
				"method", r.Method,
				"remote", r.RemoteAddr,
			)

			ctx := WithTenantContext(r.Context(), tn)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// IsAdmin checks whether username is in the DEVPULSE_ADMIN_USERS env var
// (comma-separated, whitespace-trimmed). Comparison is case-insensitive
// because GitHub usernames are case-insensitive.
func IsAdmin(username string) bool {
	if username == "" {
		return false
	}
	lower := strings.ToLower(username)
	for _, u := range adminUsers {
		if u == lower {
			return true
		}
	}
	return false
}

// GenerateCSRFToken produces a 32-byte random token encoded as base64url.
func GenerateCSRFToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("csrf: crypto/rand failed: " + err.Error())
	}
	return base64.URLEncoding.EncodeToString(b)
}

// EnsureCSRFToken returns the request's existing CSRF cookie value if present,
// otherwise generates a new token and writes the cookie. Reusing the cookie
// across admin page renders prevents stale form tokens from desyncing with
// the cookie when the user navigates between admin pages.
func EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(csrfCookieName); err == nil && cookie.Value != "" {
		SetCSRFCookie(w, cookie.Value)
		return cookie.Value
	}
	token := GenerateCSRFToken()
	SetCSRFCookie(w, token)
	return token
}

// SetCSRFCookie writes the CSRF token cookie for the double-submit pattern.
// The form value is injected server-side into a hidden field.
func SetCSRFCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/admin",
		MaxAge:   3600,
		Secure:   secure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// ValidateCSRFFromRequest compares the csrf_token form field against the
// csrf_token cookie (double-submit pattern). Returns true if they match.
func ValidateCSRFFromRequest(r *http.Request) bool {
	formToken := r.FormValue("csrf_token")
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" || formToken == "" {
		return false
	}
	return ValidateCSRF(cookie.Value, formToken)
}

// ValidateCSRF performs constant-time comparison of CSRF tokens.
// Returns false if either string is empty.
func ValidateCSRF(expected, actual string) bool {
	if expected == "" || actual == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

// AdminAuditLog logs an admin action with structured fields.
func AdminAuditLog(ctx context.Context, action, path, remoteAddr, detail string) {
	username := ""
	if tn := TenantFromContext(ctx); tn != nil {
		username = tn.Username
	}

	slog.Info("admin action",
		"action", action,
		"admin", username,
		"path", path,
		"remote", remoteAddr,
		"detail", detail,
	)
}
