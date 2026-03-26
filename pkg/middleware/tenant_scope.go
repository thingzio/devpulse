package middleware

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
)

// InjectTenantScope sets the PostgreSQL session variable app.tenant_id
// for the current connection, enabling RLS policies to filter data.
// Must be applied after RequireAuth middleware.
func InjectTenantScope(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tn := TenantFromContext(r.Context())
			if tn == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			if db != nil {
				if _, err := db.ExecContext(r.Context(),
					fmt.Sprintf("SET app.tenant_id = '%s'", tn.ID)); err != nil {
					slog.Error("setting tenant scope", "error", err, "tenant_id", tn.ID)
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
