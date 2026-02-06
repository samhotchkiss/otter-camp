package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouterSetup(t *testing.T) {
	t.Parallel()

	router := NewRouter()

	for _, tc := range []struct {
		name   string
		target string
	}{
		{name: "health", target: "/health"},
		{name: "root", target: "/"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.target, nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected status %d, got %d", tc.name, http.StatusOK, rec.Code)
		}
	}
}

func TestCORSMiddleware(t *testing.T) {
	t.Parallel()

	router := NewRouter()
	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, X-Org-ID")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 200 or 204, got %d", rec.Code)
	}

	if allowOrigin := rec.Header().Get("Access-Control-Allow-Origin"); allowOrigin == "" {
		t.Fatalf("expected Access-Control-Allow-Origin to be set")
	}

	if allowMethods := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(allowMethods, http.MethodGet) {
		t.Fatalf("expected Access-Control-Allow-Methods to include GET, got %q", allowMethods)
	}

	if allowHeaders := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(allowHeaders), "x-org-id") {
		t.Fatalf("expected Access-Control-Allow-Headers to include X-Org-ID, got %q", allowHeaders)
	}
}

func TestJSONContentType(t *testing.T) {
	t.Parallel()

	router := NewRouter()

	for _, target := range []string{"/health", "/"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("%s: expected content-type application/json, got %q", target, ct)
		}
	}
}

func TestNotFoundHandler(t *testing.T) {
	t.Parallel()

	router := NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}
