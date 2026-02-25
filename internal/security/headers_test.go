package security

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeadersMiddlewareSetsHeaders(t *testing.T) {
	handler := SecurityHeadersMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("X-Frame-Options = %q, want %q", got, "SAMEORIGIN")
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Fatalf("Referrer-Policy = %q, want %q", got, "strict-origin-when-cross-origin")
	}
	if got := rec.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("Content-Security-Policy header is empty")
	}
}
