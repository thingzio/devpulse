package net

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDownload(t *testing.T) {
	t.Run("success writes file content", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("file content"))
		}))
		defer srv.Close()

		dest := filepath.Join(t.TempDir(), "out.txt")
		err := Download(context.Background(), srv.URL, dest)
		require.NoError(t, err)

		got, err := os.ReadFile(dest)
		require.NoError(t, err)
		assert.Equal(t, "file content", string(got))
	})

	t.Run("404 returns ErrURLNotFound", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		dest := filepath.Join(t.TempDir(), "out.txt")
		err := Download(context.Background(), srv.URL, dest)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrURLNotFound))
	})

	t.Run("non-200 non-404 returns error with status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		dest := filepath.Join(t.TempDir(), "out.txt")
		err := Download(context.Background(), srv.URL, dest)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "500")
	})

	t.Run("canceled context returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		dest := filepath.Join(t.TempDir(), "out.txt")
		err := Download(ctx, srv.URL, dest)
		require.Error(t, err)
	})
}
