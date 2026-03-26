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

	url, state := BuildAuthURL(cfg)
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

	url, _ := BuildAuthURL(cfg)
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
