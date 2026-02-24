package audit

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestRecordAsyncDoesNotBlockCaller(t *testing.T) {
	done := make(chan struct{})
	service := NewService(&stubRepo{
		insertFn: func(context.Context, repo.AuditEvent) error {
			time.Sleep(100 * time.Millisecond)
			close(done)
			return nil
		},
	}, nil)

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
	case <-time.After(500 * time.Millisecond):
		t.Fatal("RecordAsync did not complete in time")
	}
}

func TestPrincipalFromContext(t *testing.T) {
	principalID := uuid.New()
	ctx := WithPrincipal(context.Background(), "human", principalID)

	gotType, gotID := PrincipalFromContext(ctx)
	if gotType != "human" {
		t.Fatalf("principal type = %q, want %q", gotType, "human")
	}
	if gotID != principalID {
		t.Fatalf("principal id = %s, want %s", gotID, principalID)
	}

	emptyType, emptyID := PrincipalFromContext(context.Background())
	if emptyType != "" {
		t.Fatalf("empty context principal type = %q, want empty", emptyType)
	}
	if emptyID != uuid.Nil {
		t.Fatalf("empty context principal id = %s, want nil", emptyID)
	}
}

func TestAuthEventConstantsFollowDomainActionPattern(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z]+(?:\.[a-z_]+)+$`)
	values := []string{
		EventAuthLogin,
		EventAuthLogout,
		EventAuthLoginFailed,
		EventAuthSessionRevoked,
		EventAPIKeyIssued,
		EventAPIKeyRevoked,
	}

	for _, value := range values {
		if !pattern.MatchString(value) {
			t.Fatalf("event constant %q does not match domain.action format", value)
		}
	}
}

type stubRepo struct {
	insertFn func(ctx context.Context, event repo.AuditEvent) error
}

func (s *stubRepo) Insert(ctx context.Context, event repo.AuditEvent) error {
	if s.insertFn == nil {
		return nil
	}
	return s.insertFn(ctx, event)
}
