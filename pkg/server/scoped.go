package server

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/postgres"
	"github.com/thingzio/devpulse/pkg/middleware"
)

type scopedStoreKey struct{}

// ScopedStoreMiddleware acquires a dedicated DB connection, sets the tenant
// scope for RLS via set_config, creates a Store bound to that connection,
// and stores it in the request context. The connection is closed when the
// request completes. All queries through this Store are RLS-scoped.
func ScopedStoreMiddleware(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tn := middleware.TenantFromContext(r.Context())
			if tn == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			conn, err := db.Conn(r.Context())
			if err != nil {
				slog.Error("acquiring scoped connection", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			defer func() {
				// Reset tenant scope before returning connection to pool.
				// set_config with false is session-level; without this reset,
				// the connection would retain a stale app.tenant_id, causing
				// RLS violations for subsequent unscoped queries (e.g. session creation).
				_, _ = conn.ExecContext(context.Background(), "SELECT set_config('app.tenant_id', '', false)")
				conn.Close()
			}()

			if _, err := conn.ExecContext(r.Context(),
				"SELECT set_config('app.tenant_id', $1, false)", tn.ID); err != nil {
				slog.Error("setting tenant scope", "error", err, "tenant_id", tn.ID)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			scopedStore := postgres.NewFromConn(conn)
			ctx := context.WithValue(r.Context(), scopedStoreKey{}, data.Store(scopedStore))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// scopedStoreFromContext returns the tenant-scoped Store from the request context.
func scopedStoreFromContext(ctx context.Context) data.Store {
	if s, ok := ctx.Value(scopedStoreKey{}).(data.Store); ok {
		return s
	}
	return nil
}
