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
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/tenant"
)

const e2eWebhookSecret = "test-webhook-secret"

func signWebhook(t *testing.T, secret string, body []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// TestWebhookDelivery_InstallationCreated verifies a properly-signed
// "installation" event with action="created" creates the installation
// row pointed at the right tenant.
func TestWebhookDelivery_InstallationCreated(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	tn, err := tenant.UpsertTenant(ctx, db, 9001, "wh-create-user", "", "", "", "", "", "")
	require.NoError(t, err)

	payload := []byte(fmt.Sprintf(`{
		"action": "created",
		"installation": {"id": 5001, "app_id": 100, "account": {"login": "alice-org", "type": "Organization"}},
		"sender": {"id": %d}
	}`, tn.GitHubID))

	r := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(payload))
	r.Header.Set("X-GitHub-Event", "installation")
	r.Header.Set("X-GitHub-Delivery", "test-delivery-1")
	r.Header.Set("X-Hub-Signature-256", signWebhook(t, e2eWebhookSecret, payload))
	w := httptest.NewRecorder()
	WebhookHandler(db, e2eWebhookSecret).ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	installs, err := tenant.ListInstallations(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, installs, 1)
	assert.Equal(t, int64(5001), installs[0].InstallationID)
	assert.Equal(t, "Organization", installs[0].TargetType)
	assert.Equal(t, "alice-org", installs[0].TargetLogin)
	assert.Nil(t, installs[0].SuspendedAt, "freshly created install must not be suspended")
}

// TestWebhookDelivery_InvalidSignatureRejected ensures payloads with a
// bad HMAC are rejected with 401 and produce no DB side effects.
func TestWebhookDelivery_InvalidSignatureRejected(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	tn, err := tenant.UpsertTenant(ctx, db, 9002, "wh-bad-sig", "", "", "", "", "", "")
	require.NoError(t, err)

	payload := []byte(fmt.Sprintf(`{
		"action": "created",
		"installation": {"id": 5002, "app_id": 100, "account": {"login": "x", "type": "User"}},
		"sender": {"id": %d}
	}`, tn.GitHubID))

	r := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(payload))
	r.Header.Set("X-GitHub-Event", "installation")
	r.Header.Set("X-GitHub-Delivery", "test-delivery-bad")
	r.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	w := httptest.NewRecorder()
	WebhookHandler(db, e2eWebhookSecret).ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)

	installs, err := tenant.ListInstallations(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Empty(t, installs, "no installation should have been created from an unsigned payload")
}

// TestWebhookDelivery_RetryPreservesSuspendedAt is the end-to-end
// version of the unit-level guard added in commit d75f245: GitHub
// re-delivers suspend webhooks; the retry must not rewrite suspended_at.
func TestWebhookDelivery_RetryPreservesSuspendedAt(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	tn, err := tenant.UpsertTenant(ctx, db, 9003, "wh-retry", "", "", "", "", "", "")
	require.NoError(t, err)
	require.NoError(t, tenant.SaveInstallation(ctx, db, tn.ID, 5003, "Organization", "wh-retry-org", nil, 100))

	suspendPayload := []byte(fmt.Sprintf(`{
		"action": "suspend",
		"installation": {"id": 5003, "app_id": 100, "account": {"login": "wh-retry-org", "type": "Organization"}},
		"sender": {"id": %d}
	}`, tn.GitHubID))

	deliver := func(deliveryID string) {
		r := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(suspendPayload))
		r.Header.Set("X-GitHub-Event", "installation")
		r.Header.Set("X-GitHub-Delivery", deliveryID)
		r.Header.Set("X-Hub-Signature-256", signWebhook(t, e2eWebhookSecret, suspendPayload))
		w := httptest.NewRecorder()
		WebhookHandler(db, e2eWebhookSecret).ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	}

	deliver("delivery-suspend-1")
	first, err := tenant.ListInstallations(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.NotNil(t, first[0].SuspendedAt)
	originalSuspendedAt := *first[0].SuspendedAt

	// Retry — must keep the original timestamp. Use a different delivery
	// ID so the in-memory dedup doesn't short-circuit the test.
	deliver("delivery-suspend-1-retry")
	second, err := tenant.ListInstallations(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.NotNil(t, second[0].SuspendedAt)

	assert.Equal(t, originalSuspendedAt.UnixNano(), second[0].SuspendedAt.UnixNano(),
		"retried suspend webhook must preserve the original suspended_at")
}
