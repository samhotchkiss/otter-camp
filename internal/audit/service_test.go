package audit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestRecordAsyncDoesNotBlockCaller(t *testing.T) {
	done := make(chan struct{}, 1)
	repository := &stubRepository{
		insertFn: func(context.Context, repo.AuditEvent) error {
			time.Sleep(100 * time.Millisecond)
			done <- struct{}{}
			return nil
		},
	}
	service := NewService(repository, slog.New(slog.NewTextHandler(io.Discard, nil)))

	start := time.Now()
	service.RecordAsync(context.Background(), Event{
		OrgID:         uuid.New(),
		EventType:     EventAuthLogin,
		PrincipalType: "human",
		PrincipalID:   uuid.New(),
	})
	elapsed := time.Since(start)
	if elapsed >= 10*time.Millisecond {
		t.Fatalf("RecordAsync elapsed = %s, want < 10ms", elapsed)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async audit insert")
	}
}

func TestRecordAsyncLogsWarnOnFailure(t *testing.T) {
	var logBuffer bytes.Buffer
	repository := &stubRepository{
		insertFn: func(context.Context, repo.AuditEvent) error {
			return errors.New("insert failure")
		},
	}
	logger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelWarn}))
	service := NewService(repository, logger)

	service.RecordAsync(context.Background(), Event{
		OrgID:         uuid.New(),
		EventType:     EventAuthLoginFailed,
		PrincipalType: "human",
		PrincipalID:   uuid.New(),
	})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logBuffer.String(), "audit record async insert failed") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("expected warning log entry, got: %q", logBuffer.String())
}

type stubRepository struct {
	insertFn func(ctx context.Context, event repo.AuditEvent) error
}

func (s *stubRepository) Insert(ctx context.Context, event repo.AuditEvent) error {
	if s.insertFn == nil {
		return nil
	}
	return s.insertFn(ctx, event)
}
