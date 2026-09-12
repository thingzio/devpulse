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

package net

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOAuthClient(t *testing.T) {
	ctx := context.Background()
	client := GetOAuthClient(ctx, "test-token")
	assert.NotNil(t, client)
}

func TestGetOAuthClientSetsAuthHeader(t *testing.T) {
	ctx := context.Background()

	// Start a test server that echoes the Authorization header.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	defer srv.Close()

	tests := []struct {
		name  string
		token string
		want  string
	}{
		{
			name:  "single token",
			token: "ghp_abc123",
			want:  "Bearer ghp_abc123",
		},
		{
			name:  "comma-separated tokens produce invalid header",
			token: "ghp_abc123,ghp_def456",
			want:  "Bearer ghp_abc123,ghp_def456",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := GetOAuthClient(ctx, tc.token)
			resp, err := client.Get(srv.URL)
			require.NoError(t, err)
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(body))
		})
	}
}

func TestPrintHTTPResponse_Nil(t *testing.T) {
	// should not panic
	PrintHTTPResponse(nil)
}

func TestPrintHTTPResponse_WithResponse(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"X-Test": {"value"}},
		Body:       http.NoBody,
	}
	// should not panic
	PrintHTTPResponse(resp)
}
