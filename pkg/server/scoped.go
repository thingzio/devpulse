// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

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
				// Use a bounded context so cleanup doesn't block indefinitely.
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = conn.ExecContext(cleanupCtx, "SELECT set_config('app.tenant_id', '', false)")
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
