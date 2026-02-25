package middleware

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRequestIDLogsStartAndCompletionWithSameID(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	handler := RequestID(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	requestID := rr.Header().Get(api.HeaderRequestID)
	if strings.TrimSpace(requestID) == "" {
		t.Fatal("expected X-Request-ID response header")
	}
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "request_started") || !strings.Contains(logOutput, "request_completed") {
		t.Fatalf("missing request lifecycle logs: %s", logOutput)
	}
	if !strings.Contains(logOutput, requestID) {
		t.Fatalf("request_id %q missing from logs: %s", requestID, logOutput)
	}
}
