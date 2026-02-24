package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/api"
)

func TestRequestIDInjectsContextAndHeader(t *testing.T) {
	handler := RequestID(slog.New(slog.NewTextHandler(io.Discard, nil)))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, ok := api.RequestIDFromContext(r.Context())
		if !ok || requestID == "" {
			t.Fatal("expected request id in context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set(api.HeaderRequestID, "incoming-id")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if got := rr.Header().Get(api.HeaderRequestID); got != "incoming-id" {
		t.Fatalf("X-Request-ID = %q, want %q", got, "incoming-id")
	}
}
