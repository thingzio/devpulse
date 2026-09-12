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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestVerifyWebhookSignature(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{"action":"created"}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	tests := []struct {
		name      string
		payload   []byte
		signature string
		secret    string
		want      bool
	}{
		{
			name:      "valid signature",
			payload:   payload,
			signature: validSig,
			secret:    secret,
			want:      true,
		},
		{
			name:      "wrong secret",
			payload:   payload,
			signature: validSig,
			secret:    "wrong-secret",
			want:      false,
		},
		{
			name:      "missing sha256 prefix",
			payload:   payload,
			signature: hex.EncodeToString(mac.Sum(nil)),
			secret:    secret,
			want:      false,
		},
		{
			name:      "invalid hex encoding",
			payload:   payload,
			signature: "sha256=notvalidhex!!!",
			secret:    secret,
			want:      false,
		},
		{
			name:      "empty signature",
			payload:   payload,
			signature: "",
			secret:    secret,
			want:      false,
		},
		{
			name:      "tampered payload",
			payload:   []byte(`{"action":"deleted"}`),
			signature: validSig,
			secret:    secret,
			want:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := verifyWebhookSignature(tc.payload, tc.signature, tc.secret)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestWebhookHandler(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{"action":"created"}`)

	validSig := func(body []byte) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	t.Run("empty secret returns 503", func(t *testing.T) {
		h := WebhookHandler(nil, "")
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(payload))
		h(w, r)
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})

	t.Run("invalid signature returns 401", func(t *testing.T) {
		h := WebhookHandler(nil, secret)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(payload))
		r.Header.Set("X-Hub-Signature-256", "sha256=badsig")
		h(w, r)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("valid sig with unknown event returns 200", func(t *testing.T) {
		h := WebhookHandler(nil, secret)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(payload))
		r.Header.Set("X-Hub-Signature-256", validSig(payload))
		r.Header.Set("X-GitHub-Event", "push") // not handled, ignored
		h(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("duplicate delivery id is dropped", func(t *testing.T) {
		// Use a fresh cache so other tests don't interfere.
		deliverySeen = &deliverySeenCache{entries: make(map[string]time.Time)}

		h := WebhookHandler(nil, secret)
		send := func() *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(payload))
			r.Header.Set("X-Hub-Signature-256", validSig(payload))
			r.Header.Set("X-GitHub-Event", "push")
			r.Header.Set("X-GitHub-Delivery", "dup-delivery-1")
			h(w, r)
			return w
		}

		assert.Equal(t, http.StatusOK, send().Code)
		// Second delivery with the same id is silently dropped (still 200).
		assert.Equal(t, http.StatusOK, send().Code)
	})
}

func TestDeliverySeenCache(t *testing.T) {
	c := &deliverySeenCache{entries: make(map[string]time.Time)}
	now := time.Unix(0, 0)

	assert.False(t, c.seenOrRecord("abc", now), "first time should not be seen")
	assert.True(t, c.seenOrRecord("abc", now), "second time within window should be seen")

	// After window expires, the id is re-accepted.
	later := now.Add(webhookDeliveryWindow + time.Hour)
	assert.False(t, c.seenOrRecord("abc", later), "after window, id is fresh again")

	// Empty id is never deduped.
	assert.False(t, c.seenOrRecord("", now))
	assert.False(t, c.seenOrRecord("", now))
}
