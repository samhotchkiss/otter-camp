package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
)

func TestSubscribeTurnCompletedIgnoresNonTurnEvents(t *testing.T) {
	events := &capturingMemorySubscriber{}
	enq := &capturingMemoryEnqueuer{}
	consumer := &EventConsumer{
		events: events,
		enq:    enq,
	}

	consumer.SubscribeTurnCompleted(nil)
	if events.handler == nil {
		t.Fatal("expected subscription handler")
	}

	payload, _ := json.Marshal(map[string]any{
		"session_id": uuid.NewString(),
		"turn_id":    uuid.NewString(),
	})
	if err := events.handler(context.Background(), eventbus.DomainEvent{
		OrganizationID: uuid.New(),
		EventType:      "task.completed",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if enq.calls != 0 {
		t.Fatalf("enqueue calls = %d, want 0", enq.calls)
	}
}

type capturingMemorySubscriber struct {
	handler eventbus.EventHandler
}

func (s *capturingMemorySubscriber) Subscribe(_ string, _ *uuid.UUID, handler eventbus.EventHandler) eventbus.Subscription {
	s.handler = handler
	return eventbus.Subscription{}
}

type capturingMemoryEnqueuer struct {
	calls int
}

func (e *capturingMemoryEnqueuer) Enqueue(_ context.Context, _ pgx.Tx, _ string, _ int, _ any, _ *time.Time) (uuid.UUID, error) {
	e.calls++
	return uuid.New(), nil
}
