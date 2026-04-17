package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsAdmin(t *testing.T) {
	t.Setenv(adminUsersEnvVar, "alice,bob")

	assert.True(t, IsAdmin("alice"))
	assert.True(t, IsAdmin("bob"))
	assert.False(t, IsAdmin("eve"))
	assert.False(t, IsAdmin(""))
}

func TestIsAdmin_Empty(t *testing.T) {
	t.Setenv(adminUsersEnvVar, "")

	assert.False(t, IsAdmin("alice"))
	assert.False(t, IsAdmin(""))
}

func TestIsAdmin_Whitespace(t *testing.T) {
	t.Setenv(adminUsersEnvVar, " alice , bob ")

	assert.True(t, IsAdmin("alice"))
	assert.True(t, IsAdmin("bob"))
	assert.False(t, IsAdmin("eve"))
}

func TestRequireAdmin_NoCookie(t *testing.T) {
	mw := RequireAdmin(nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGenerateCSRFToken(t *testing.T) {
	token := GenerateCSRFToken()
	require.NotEmpty(t, token)
	// 32 bytes in base64url with padding = 44 chars.
	assert.Len(t, token, 44)

	// Each call produces a unique token.
	assert.NotEqual(t, token, GenerateCSRFToken())
}

func TestValidateCSRF(t *testing.T) {
	token := GenerateCSRFToken()

	assert.True(t, ValidateCSRF(token, token))
	assert.False(t, ValidateCSRF(token, "wrong"))
	assert.False(t, ValidateCSRF("", token))
	assert.False(t, ValidateCSRF(token, ""))
	assert.False(t, ValidateCSRF("", ""))
}
