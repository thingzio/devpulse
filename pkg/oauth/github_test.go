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

package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAuthURL(t *testing.T) {
	cfg := &Config{
		ClientID:    "test-client-id",
		RedirectURL: "https://example.com/auth/github/callback",
	}

	url, state, err := BuildAuthURL(cfg)
	require.NoError(t, err)
	require.NotEmpty(t, state)
	assert.Contains(t, url, "github.com/login/oauth/authorize")
	assert.Contains(t, url, "client_id=test-client-id")
	assert.Contains(t, url, "state="+state)
	assert.Contains(t, url, "scope=read")
}

func TestBuildAuthURL_CustomAuthURL(t *testing.T) {
	cfg := &Config{
		ClientID: "abc",
		AuthURL:  "https://custom.example.com/auth",
	}

	url, _, err := BuildAuthURL(cfg)
	require.NoError(t, err)
	assert.Contains(t, url, "custom.example.com/auth")
}

func TestExchangeCode_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"gho_test123","token_type":"bearer","scope":"read:user"}`))
	}))
	defer server.Close()

	cfg := &Config{
		ClientID:     "test",
		ClientSecret: "secret",
		TokenURL:     server.URL,
	}

	token, err := ExchangeCode(context.Background(), cfg, "valid-code")
	require.NoError(t, err)
	assert.Equal(t, "gho_test123", token)
}

func TestExchangeCode_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":"bad_verification_code"}`))
	}))
	defer server.Close()

	cfg := &Config{
		ClientID:     "test",
		ClientSecret: "secret",
		TokenURL:     server.URL,
	}

	_, err := ExchangeCode(context.Background(), cfg, "invalid-code")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad_verification_code")
}

func TestFetchUser_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":12345,"login":"testuser","email":"test@example.com","avatar_url":"https://avatar.url"}`))
	}))
	defer server.Close()

	cfg := &Config{UserURL: server.URL}

	user, err := FetchUser(context.Background(), cfg, "test-token")
	require.NoError(t, err)
	assert.Equal(t, int64(12345), user.ID)
	assert.Equal(t, "testuser", user.Login)
	assert.Equal(t, "test@example.com", user.Email)
}

func TestFetchUser_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	cfg := &Config{UserURL: server.URL}

	_, err := FetchUser(context.Background(), cfg, "bad-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 401")
}

func TestExchangeCode_EmptyToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":""}`))
	}))
	defer server.Close()

	cfg := &Config{ClientID: "x", ClientSecret: "y", TokenURL: server.URL}
	_, err := ExchangeCode(context.Background(), cfg, "code")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty access token")
}

func TestFetchUser_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	cfg := &Config{UserURL: server.URL}
	_, err := FetchUser(context.Background(), cfg, "token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding user")
}

func TestFetchUser_EmailFallback(t *testing.T) {
	userServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":99,"login":"nomail","email":"","avatar_url":"https://a.url"}`))
	}))
	defer userServer.Close()

	emailsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"email":"secondary@example.com","primary":false,"verified":true},
			{"email":"primary@example.com","primary":true,"verified":true}
		]`))
	}))
	defer emailsServer.Close()

	cfg := &Config{UserURL: userServer.URL, EmailsURL: emailsServer.URL}
	user, err := FetchUser(context.Background(), cfg, "token")
	require.NoError(t, err)
	assert.Equal(t, "primary@example.com", user.Email)
}

func TestFetchUser_EmailFallbackFailure(t *testing.T) {
	userServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":99,"login":"nomail","email":"","avatar_url":"https://a.url"}`))
	}))
	defer userServer.Close()

	emailsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer emailsServer.Close()

	cfg := &Config{UserURL: userServer.URL, EmailsURL: emailsServer.URL}
	user, err := FetchUser(context.Background(), cfg, "token")
	require.NoError(t, err)
	assert.Empty(t, user.Email)
}

func TestExchangeCode_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	cfg := &Config{
		ClientID:     "id",
		ClientSecret: "secret",
		TokenURL:     srv.URL,
	}
	_, err := ExchangeCode(context.Background(), cfg, "code")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 502")
}

func TestBuildAuthURL_StateLength(t *testing.T) {
	cfg := &Config{ClientID: "abc"}
	_, state, err := BuildAuthURL(cfg)
	require.NoError(t, err)
	// randomState() returns hex of 16 bytes = 32 chars
	assert.Len(t, state, 32)
}
