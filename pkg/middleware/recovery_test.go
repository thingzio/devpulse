package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRecovery_NoPanic(t *testing.T) {
	handler := Recovery(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRecovery_Panic(t *testing.T) {
	handler := Recovery(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequest(http.MethodGet, "/crash", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "internal error")
}

func TestRecovery_PanicNilValue(t *testing.T) {
	handler := Recovery(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(nil)
	}))

	req := httptest.NewRequest(http.MethodGet, "/nil-panic", nil)
	rec := httptest.NewRecorder()

	// panic(nil) in Go 1.21+ is a runtime.PanicNilError which recover() catches.
	// In older Go, recover() returns nil and we don't intercept.
	// Either way the server should not crash.
	assert.NotPanics(t, func() {
		handler.ServeHTTP(rec, req)
	})
}
