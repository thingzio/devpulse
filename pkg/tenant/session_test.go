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

package tenant

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashToken(t *testing.T) {
	t.Run("produces 64-char hex string", func(t *testing.T) {
		h := HashToken("sometoken")
		assert.Len(t, h, 64)
	})

	t.Run("idempotent", func(t *testing.T) {
		assert.Equal(t, HashToken("abc"), HashToken("abc"))
	})

	t.Run("different inputs produce different hashes", func(t *testing.T) {
		assert.NotEqual(t, HashToken("token1"), HashToken("token2"))
	})

	t.Run("known hash", func(t *testing.T) {
		// sha256("") = e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
		h := HashToken("")
		require.Len(t, h, 64)
		assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", h)
	})
}

// TestAuthenticateUser_NewUserGetsProPlan guards against regressions in the
// auto-enroll-to-Pro behavior on the OAuth login path. AuthenticateUser
// shares the upsertTenantSQL constant with UpsertTenant via upsertTenantOn;
// if the parameter list ever drifts, this test fails fast instead of after
// deploy.
func TestAuthenticateUser_NewUserGetsProPlan(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, token, err := AuthenticateUser(ctx, db, 800001, "newuser",
		"new@test.com", "https://avatar.test", "New User", "Acme", "NYC", "Bio",
		7*24*time.Hour)
	require.NoError(t, err)
	require.NotNil(t, tn)
	require.NotEmpty(t, token)

	assert.Equal(t, "pro", tn.Plan, "new users must be auto-enrolled in Pro during the beta preview")
	assert.Equal(t, 25, tn.MaxRepos)
	assert.Equal(t, 15000, tn.MaxEventsPerWeek)

	// Session is valid and resolves back to the same tenant.
	got, err := ValidateSession(ctx, db, token)
	require.NoError(t, err)
	assert.Equal(t, tn.ID, got.ID)
}

// TestAuthenticateUser_ExistingUserKeepsPlan ensures the Pro auto-enroll
// applies on INSERT only — re-authentication must not silently upgrade a
// downgraded tenant back to Pro.
func TestAuthenticateUser_ExistingUserKeepsPlan(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, _, err := AuthenticateUser(ctx, db, 800002, "existing", "", "", "", "", "", "", time.Hour)
	require.NoError(t, err)

	// Admin downgrades the tenant to Free.
	require.NoError(t, UpdatePlan(ctx, db, tn.ID, "free", 1, 500))

	// User signs in again with updated profile fields.
	tn2, _, err := AuthenticateUser(ctx, db, 800002, "existing",
		"e@test.com", "https://a.test", "Existing", "Co", "Loc", "Bio",
		time.Hour)
	require.NoError(t, err)

	assert.Equal(t, "free", tn2.Plan, "re-auth must NOT overwrite an existing tenant's plan")
	assert.Equal(t, 1, tn2.MaxRepos)
	assert.Equal(t, 500, tn2.MaxEventsPerWeek)
	// Profile fields should update on conflict.
	assert.Equal(t, "Existing", tn2.Name)
	assert.Equal(t, "Co", tn2.Company)
}
