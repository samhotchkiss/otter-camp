package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLandingPageUnchanged(t *testing.T) {
	staticDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html><body>landing-page</body></html>"), 0o644))

	handler := newServerHandler(staticDir, joinConfig{InviteCodes: map[string]struct{}{"valid-code": {}}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "landing-page")
}

func TestJoinRoute(t *testing.T) {
	staticDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html><body>landing-page</body></html>"), 0o644))

	handler := newServerHandler(staticDir, joinConfig{InviteCodes: map[string]struct{}{"valid-code": {}}})

	t.Run("valid invite code serves join page", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/join/valid-code", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), "Join Otter Camp")
	})

	t.Run("invalid invite code returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/join/bad-code", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("missing invite code returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/join/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("join route does not leak existence on invalid code", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/join/not-real", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
		body, _ := io.ReadAll(rec.Body)
		require.NotContains(t, string(body), "invite")
	})
}
