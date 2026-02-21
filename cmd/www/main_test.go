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
	require.NoError(t, os.WriteFile(
		filepath.Join(staticDir, "index.html"),
		[]byte(`<html><body>landing-page<form id="waitlist-form"><input type="email" id="email-input" required /></form></body></html>`),
		0o644,
	))

	handler := newServerHandler(staticDir, joinConfig{InviteCodes: map[string]struct{}{"valid-code": {}}})

	t.Run("valid invite code serves join page", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/join/valid-code", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), "Join Otter Camp")
	})

	t.Run("invalid invite code serves marketing waitlist form", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/join/bad-code", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), `id="waitlist-form"`)
		require.Contains(t, rec.Body.String(), `id="email-input"`)
	})

	t.Run("missing invite code returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/join/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("invalid invite code reuses existing static marketing content", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/join/not-real", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		body, _ := io.ReadAll(rec.Body)
		require.Contains(t, string(body), "landing-page")
	})
}

func TestJoinSignupPage(t *testing.T) {
	staticDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html><body>landing-page</body></html>"), 0o644))

	handler := newServerHandler(staticDir, joinConfig{InviteCodes: map[string]struct{}{"valid-code": {}}})

	req := httptest.NewRequest(http.MethodGet, "/join/valid-code", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `id="join-form"`)
	require.Contains(t, body, `name="name"`)
	require.Contains(t, body, `name="email"`)
	require.Contains(t, body, `name="organization_name"`)
	require.Contains(t, body, `name="subdomain"`)
	require.Contains(t, body, `https://api.otter.camp/api/onboarding/bootstrap`)
	require.Contains(t, body, `^[a-z0-9-]{3,32}$`)
	require.Contains(t, body, `id="copy-command"`)
	require.Contains(t, body, `curl -sSL otter.camp/install | bash -s -- --token`)
}

func TestInstallRoute(t *testing.T) {
	staticDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html><body>landing-page</body></html>"), 0o644))

	handler := newServerHandler(staticDir, joinConfig{})

	t.Run("get returns hosted install script", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/install", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Header().Get("Content-Type"), "application/x-sh")

		body := rec.Body.String()
		require.Contains(t, body, "#!/usr/bin/env bash")
		require.Contains(t, body, "init --mode hosted --token \"$TOKEN\" --url \"$URL\"")
		require.Contains(t, body, "missing required --token")
		require.Contains(t, body, "missing required --url")
	})

	t.Run("non-get returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/install", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestAPIHealthRouteReturnsUpstreamJSON(t *testing.T) {
	staticDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html><body>landing-page</body></html>"), 0o644))

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/health", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	t.Setenv("OTTER_API_HEALTH_URL", upstream.URL+"/health")
	handler := newServerHandler(staticDir, joinConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	require.Contains(t, rec.Body.String(), `"status":"ok"`)
	require.NotContains(t, rec.Body.String(), "landing-page")
}

func TestAPIHealthRouteReturnsServiceUnavailableWhenUpstreamDown(t *testing.T) {
	staticDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html><body>landing-page</body></html>"), 0o644))

	t.Setenv("OTTER_API_HEALTH_URL", "http://127.0.0.1:1/health")
	handler := newServerHandler(staticDir, joinConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	require.Contains(t, rec.Body.String(), `"status":"unavailable"`)
	require.NotContains(t, rec.Body.String(), "landing-page")
}
