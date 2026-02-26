package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthRoutesAndRequestIDHeader(t *testing.T) {
	handler := NewHandlerWithOptions(HandlerOptions{})

	cases := []struct {
		path       string
		statusCode int
	}{
		{path: "/health/live", statusCode: http.StatusOK},
		{path: "/health", statusCode: http.StatusOK},
		{path: "/health/ready", statusCode: http.StatusServiceUnavailable},
		{path: "/ready", statusCode: http.StatusServiceUnavailable},
		{path: "/metrics", statusCode: http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.statusCode {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, tc.statusCode, rec.Body.String())
			}
			if rec.Header().Get("X-Request-ID") == "" {
				t.Fatal("missing X-Request-ID header")
			}
		})
	}
}
