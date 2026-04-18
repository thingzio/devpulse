package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/thingzio/devpulse/pkg/tenant"
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

func TestWithTenantContext(t *testing.T) {
	ctx := context.Background()
	tn := &tenant.Tenant{ID: "t-123", Username: "testuser"}

	ctx = WithTenantContext(ctx, tn)
	got := TenantFromContext(ctx)
	assert.NotNil(t, got)
	assert.Equal(t, "t-123", got.ID)
	assert.Equal(t, "testuser", got.Username)
}

func TestOAuthStateCookieName(t *testing.T) {
	// In test env, BASE_URL is not set, so secure=false.
	assert.Equal(t, "oauth_state", OAuthStateCookieName())
}

func TestIsSecure(t *testing.T) {
	// In test env, BASE_URL is not set, so secure=false.
	assert.False(t, IsSecure())
}

func TestRequireAuth_EmptyCookieValue(t *testing.T) {
	// Empty cookie value — middleware should redirect.
	handler := RequireAuth(nil, "/login")(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "not-the-session", Value: "anything"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Wrong cookie name means r.Cookie() returns ErrNoCookie → redirect.
	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
}
