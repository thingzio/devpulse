package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequireAuth_NoCookie(t *testing.T) {
	handler := RequireAuth(nil, "/auth/github")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/auth/github", rec.Header().Get("Location"))
}

func TestTenantFromContext_Missing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	tn := TenantFromContext(req.Context())
	assert.Nil(t, tn)
}

func TestSetSessionCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, "test-token", 3600)

	cookies := rec.Result().Cookies()
	assert.Len(t, cookies, 1)
	assert.Equal(t, SessionCookieName(), cookies[0].Name)
	assert.Equal(t, "test-token", cookies[0].Value)
	assert.True(t, cookies[0].HttpOnly)
}

func TestClearSessionCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	ClearSessionCookie(rec)

	cookies := rec.Result().Cookies()
	assert.Len(t, cookies, 1)
	assert.Equal(t, "", cookies[0].Value)
	assert.Equal(t, -1, cookies[0].MaxAge)
}

func TestCookieNameFor(t *testing.T) {
	assert.Equal(t, "__Host-session", cookieNameFor(true))
	assert.Equal(t, "session", cookieNameFor(false))
}
