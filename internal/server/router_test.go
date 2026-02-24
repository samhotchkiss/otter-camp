package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/config"
)

func TestTestResetRouteRegisteredInTestMode(t *testing.T) {
	resetter := &stubResetter{}
	handler := NewHandlerWithOptions(HandlerOptions{
		Mode:         config.ModeTest,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		TestResetter: resetter,
	})

	req := httptest.NewRequest(http.MethodPost, "/test/reset", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if resetter.calls != 1 {
		t.Fatalf("reset calls = %d, want %d", resetter.calls, 1)
	}
}

func TestTestResetRouteReturns500OnResetFailure(t *testing.T) {
	resetter := &stubResetter{err: errors.New("boom")}
	handler := NewHandlerWithOptions(HandlerOptions{
		Mode:         config.ModeTest,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		TestResetter: resetter,
	})

	req := httptest.NewRequest(http.MethodPost, "/test/reset", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

func TestTestResetRouteNotRegisteredOutsideTestMode(t *testing.T) {
	handler := NewHandlerWithOptions(HandlerOptions{
		Mode:   config.ModeProduction,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	req := httptest.NewRequest(http.MethodPost, "/test/reset", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

type stubResetter struct {
	calls int
	err   error
}

func (s *stubResetter) Reset(context.Context) error {
	s.calls++
	return s.err
}
