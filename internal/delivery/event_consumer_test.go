package delivery

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
)

type fakeDeployCompletionHandler struct {
	calls []uuid.UUID
}

func (f *fakeDeployCompletionHandler) OnDeployCompleted(_ context.Context, deployTaskID uuid.UUID) error {
	f.calls = append(f.calls, deployTaskID)
	return nil
}

func TestEventConsumerHandlesTaskStatusChangedDone(t *testing.T) {
	taskID := uuid.New()
	h := &fakeDeployCompletionHandler{}
	consumer := NewEventConsumer(EventConsumerOptions{Updater: h})

	payload, err := json.Marshal(map[string]any{
		"task_id":   taskID.String(),
		"to_status": "done",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if err := consumer.handle(context.Background(), eventbus.DomainEvent{EventType: "task.status_changed", Payload: payload}); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(h.calls) != 1 || h.calls[0] != taskID {
		t.Fatalf("calls = %v, want [%s]", h.calls, taskID)
	}
}

func TestEventConsumerIgnoresNonDeployCompletions(t *testing.T) {
	h := &fakeDeployCompletionHandler{}
	consumer := NewEventConsumer(EventConsumerOptions{Updater: h})

	payload, err := json.Marshal(map[string]any{
		"task_id":   uuid.NewString(),
		"task_type": "build",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if err := consumer.handle(context.Background(), eventbus.DomainEvent{EventType: "task.completed", Payload: payload}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(h.calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(h.calls))
	}
}
