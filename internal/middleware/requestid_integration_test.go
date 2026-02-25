//go:build integration

package middleware_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/server"
)

func TestRequestIDRoundTripHeaderAndLogsIntegration(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	handler := server.NewHandlerWithOptions(server.HandlerOptions{
		Version: "integration-test",
		Logger:  logger,
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health/live")
	if err != nil {
		t.Fatalf("GET /health/live: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	requestID := strings.TrimSpace(resp.Header.Get(api.HeaderRequestID))
	if requestID == "" {
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
