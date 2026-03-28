package middleware

import (
	"database/sql"
	"log/slog"
	"net/http"
)

// InjectTenantScope acquires a dedicated database connection, sets the
// PostgreSQL session variable app.tenant_id via parameterized set_config,
// and ensures the scope is reset when the request completes.
// Must be applied after RequireAuth middleware.
func InjectTenantScope(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tn := TenantFromContext(r.Context())
			if tn == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			if db == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Acquire a dedicated connection to prevent tenant scope
			// from bleeding to other requests via connection pooling.
			conn, err := db.Conn(r.Context())
			if err != nil {
				slog.Error("acquiring connection", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			defer conn.Close()

			// Use parameterized set_config to prevent SQL injection.
			// Third param false = session-scoped (reset when connection returns to pool).
			if _, err := conn.ExecContext(r.Context(),
				"SELECT set_config('app.tenant_id', $1, false)", tn.ID); err != nil {
				slog.Error("setting tenant scope", "error", err, "tenant_id", tn.ID)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
