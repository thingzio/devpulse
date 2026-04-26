package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// TestSignoutHandler_LogsEvent verifies sign-out emits a structured audit log
// even when no session cookie is present (anonymous sign-out is still
// recorded so operators can see the action).
func TestSignoutHandler_LogsEvent(t *testing.T) {
	buf := captureSlog(t)

	handler := signoutHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/auth/signout", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusFound, rec.Code)
	out := buf.String()
	assert.Contains(t, out, `"msg":"user signed out"`)
}
