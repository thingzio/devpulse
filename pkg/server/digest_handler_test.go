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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/thingzio/devpulse/pkg/digest"
)

func TestDigestToggleHandler_NoTenant(t *testing.T) {
	handler := digestToggleHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/settings/digest", strings.NewReader("enabled=true"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Contains(t, rec.Header().Get("Location"), "/auth/github")
}

func TestDigestUnsubscribeHandler_MissingParams(t *testing.T) {
	t.Setenv("DIGEST_HMAC_SECRET", "test-secret")
	handler := digestUnsubscribeHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/digest/unsubscribe", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDigestUnsubscribeHandler_MissingToken(t *testing.T) {
	t.Setenv("DIGEST_HMAC_SECRET", "test-secret")
	handler := digestUnsubscribeHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/digest/unsubscribe?tenant=abc", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDigestUnsubscribeHandler_InvalidToken(t *testing.T) {
	t.Setenv("DIGEST_HMAC_SECRET", "test-secret")
	handler := digestUnsubscribeHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/digest/unsubscribe?tenant=abc&token=wrong", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestDigestUnsubscribeHandler_NoSecret(t *testing.T) {
	t.Setenv("DIGEST_HMAC_SECRET", "")
	t.Setenv("SEND_API_KEY", "")
	handler := digestUnsubscribeHandler(nil)

	token := digest.UnsubscribeToken("some-secret", "tenant-id")
	req := httptest.NewRequest(http.MethodGet, "/digest/unsubscribe?tenant=tenant-id&token="+token, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
