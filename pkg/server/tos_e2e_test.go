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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/thingzio/devpulse/pkg/oauth"
)

// TestTosReachableSignedOut guards the footer contract: the footer renders on
// the signed-out landing page, so every link it offers — including TERMS —
// must resolve without a session. GET /tos previously sat behind
// middleware.RequireAuth, so an anonymous visitor clicking TERMS was
// redirected into the GitHub OAuth flow instead of reading the page.
func TestTosReachableSignedOut(t *testing.T) {
	db := setupE2EDB(t)

	oauthRL := newRateLimiter(10, time.Minute)
	defer oauthRL.stop()
	repoSearchRL := newRateLimiter(10, time.Minute)
	defer repoSearchRL.stop()
	unsubRL := newRateLimiter(10, time.Minute)
	defer unsubRL.stop()

	mux := makeRouter(db, nil, &oauth.Config{}, "", Options{Version: "test"}, oauthRL, repoSearchRL, unsubRL, nil, 0)

	req := httptest.NewRequest(http.MethodGet, "/tos", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tos signed out = %d, want 200", rec.Code)
	}
}
