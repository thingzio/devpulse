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

const adminUsersEnvVar = "DEVPULSE_ADMIN_USERS"

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

			ctx := WithTenantContext(r.Context(), tn)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// IsAdmin checks whether username is in the DEVPULSE_ADMIN_USERS env var
// (comma-separated, whitespace-trimmed).
func IsAdmin(username string) bool {
	raw := os.Getenv(adminUsersEnvVar)
	if raw == "" || username == "" {
		return false
	}

	for entry := range strings.SplitSeq(raw, ",") {
		if strings.TrimSpace(entry) == username {
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

// ValidateCSRF performs constant-time comparison of CSRF tokens.
// Returns false if either string is empty.
func ValidateCSRF(expected, actual string) bool {
	if expected == "" || actual == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

// AdminAuditLog logs an admin action with structured fields.
func AdminAuditLog(action string, ctx context.Context, path, remoteAddr, detail string) {
	username := ""
	if tn := TenantFromContext(ctx); tn != nil {
		username = tn.Username
	}

	slog.Warn("admin action",
		"action", action,
		"admin", username,
		"path", path,
		"remote", remoteAddr,
		"detail", detail,
	)
}
