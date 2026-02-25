package security

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSMiddlewareConfiguredAndUnconfiguredOrigins(t *testing.T) {
	middleware := CORSMiddleware([]string{"https://app.example.com"})
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("preflight configured origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/v1/projects", nil)
		req.Header.Set("Origin", "https://app.example.com")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
			t.Fatalf("allow-origin = %q, want configured origin", got)
		}
	})

	t.Run("request unconfigured origin proceeds without header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/projects", nil)
		req.Header.Set("Origin", "https://evil.example")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("allow-origin = %q, want empty", got)
		}
	})
}
