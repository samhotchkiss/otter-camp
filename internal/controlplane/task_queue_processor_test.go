package controlplane

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
)

func TestTaskQueueProcessorHandleTaskQueuedEventIgnoresNonQueuedEvents(t *testing.T) {
	processor := &TaskQueueProcessor{}

	payload, err := json.Marshal(map[string]any{
		"task_id":   uuid.New(),
		"to_status": "in_progress",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	cases := []eventbus.DomainEvent{
		{
			EventType: "task.created",
			Payload:   payload,
		},
		{
			EventType: "task.status_changed",
			Payload:   payload,
		},
		{
			EventType: "task.status_changed",
			Payload:   []byte(`not-json`),
		},
	}

	for _, event := range cases {
		if err := processor.handleTaskQueuedEvent(context.Background(), event); err != nil {
			t.Fatalf("handleTaskQueuedEvent(%s) error = %v, want nil", event.EventType, err)
		}
	}
}
