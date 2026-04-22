package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/tenant"
)

func TestIsAdmin(t *testing.T) {
	t.Setenv(adminUsersEnvVar, "alice,bob")
	loadAdminUsers()

	assert.True(t, IsAdmin("alice"))
	assert.True(t, IsAdmin("bob"))
	assert.False(t, IsAdmin("eve"))
	assert.False(t, IsAdmin(""))
}

func TestIsAdmin_CaseInsensitive(t *testing.T) {
	t.Setenv(adminUsersEnvVar, "Alice,BOB")
	loadAdminUsers()

	assert.True(t, IsAdmin("alice"))
	assert.True(t, IsAdmin("ALICE"))
	assert.True(t, IsAdmin("Alice"))
	assert.True(t, IsAdmin("bob"))
	assert.True(t, IsAdmin("Bob"))
	assert.False(t, IsAdmin("eve"))
}

func TestIsAdmin_Empty(t *testing.T) {
	t.Setenv(adminUsersEnvVar, "")
	loadAdminUsers()

	assert.False(t, IsAdmin("alice"))
	assert.False(t, IsAdmin(""))
}

func TestIsAdmin_Whitespace(t *testing.T) {
	t.Setenv(adminUsersEnvVar, " alice , bob ")
	loadAdminUsers()

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

func TestSetCSRFCookie(t *testing.T) {
	w := httptest.NewRecorder()
	token := GenerateCSRFToken()
	SetCSRFCookie(w, token)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, csrfCookieName, cookies[0].Name)
	assert.Equal(t, token, cookies[0].Value)
	assert.Equal(t, "/admin", cookies[0].Path)
	assert.Equal(t, http.SameSiteStrictMode, cookies[0].SameSite)
	assert.True(t, cookies[0].HttpOnly)
}

func TestValidateCSRFFromRequest(t *testing.T) {
	token := GenerateCSRFToken()

	t.Run("valid", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/admin/test",
			strings.NewReader("csrf_token="+token))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
		assert.True(t, ValidateCSRFFromRequest(r))
	})

	t.Run("mismatch", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/admin/test",
			strings.NewReader("csrf_token=wrong"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
		assert.False(t, ValidateCSRFFromRequest(r))
	})

	t.Run("missing_cookie", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/admin/test",
			strings.NewReader("csrf_token="+token))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		assert.False(t, ValidateCSRFFromRequest(r))
	})

	t.Run("missing_form_field", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/admin/test", nil)
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
		assert.False(t, ValidateCSRFFromRequest(r))
	})

	t.Run("empty_cookie", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/admin/test",
			strings.NewReader("csrf_token="+token))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: ""})
		assert.False(t, ValidateCSRFFromRequest(r))
	})
}

func TestAdminAuditLog(t *testing.T) {
	// With tenant in context — should not panic.
	tn := &tenant.Tenant{Username: "alice"}
	ctx := WithTenantContext(context.Background(), tn)
	AdminAuditLog(ctx, "test_action", "/admin", "127.0.0.1", "detail")

	// Without tenant in context — should not panic.
	AdminAuditLog(context.Background(), "test_action", "/admin", "127.0.0.1", "no tenant")
}
