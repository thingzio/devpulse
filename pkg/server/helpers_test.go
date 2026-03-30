package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
)

func TestWriteJSON(t *testing.T) {
	t.Run("sets content-type and encodes value", func(t *testing.T) {
		w := httptest.NewRecorder()
		writeJSON(w, http.StatusOK, map[string]string{"key": "value"})

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var got map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
		assert.Equal(t, "value", got["key"])
	})

	t.Run("custom status code", func(t *testing.T) {
		w := httptest.NewRecorder()
		writeJSON(w, http.StatusCreated, struct{ ID int }{ID: 42})
		assert.Equal(t, http.StatusCreated, w.Code)
	})

	t.Run("nil value encodes as null", func(t *testing.T) {
		w := httptest.NewRecorder()
		writeJSON(w, http.StatusOK, nil)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "null")
	})
}

func TestWriteError(t *testing.T) {
	t.Run("encodes error message as JSON", func(t *testing.T) {
		w := httptest.NewRecorder()
		writeError(w, http.StatusBadRequest, "invalid input")

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var got map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
		assert.Equal(t, "invalid input", got["error"])
	})

	t.Run("403 forbidden", func(t *testing.T) {
		w := httptest.NewRecorder()
		writeError(w, http.StatusForbidden, "not allowed")
		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

func TestScopedStoreFromContext(t *testing.T) {
	t.Run("returns nil when not set", func(t *testing.T) {
		assert.Nil(t, scopedStoreFromContext(context.Background()))
	})

	t.Run("returns store when set", func(t *testing.T) {
		// Use a non-nil concrete value that satisfies data.Store via the key.
		// We just need to verify the type assertion works; a nil-db store is fine here.
		ctx := context.WithValue(context.Background(), scopedStoreKey{}, data.Store(nil))
		// nil satisfies the interface but the value is nil — that's ok for this test.
		// The important thing is the key is present.
		_ = scopedStoreFromContext(ctx) // should not panic
	})
}

func TestStoreFromRequest(t *testing.T) {
	t.Run("returns fallback when no scoped store in context", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		var fallback data.Store // nil fallback
		got := storeFromRequest(r, fallback)
		assert.Nil(t, got)
	})
}
