package push

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
)

type fakeDeliveryGate struct {
	should bool
}

func (f fakeDeliveryGate) ShouldDeliver(context.Context, uuid.UUID, *uuid.UUID, string, string) (bool, error) {
	return f.should, nil
}

type fakeTokenStore struct {
	tokens []PushToken
}

func (f fakeTokenStore) GetTokens(context.Context, uuid.UUID) ([]PushToken, error) {
	return append([]PushToken{}, f.tokens...), nil
}

type fakePushAdapter struct {
	sent int
}

func (f *fakePushAdapter) Send(context.Context, PushToken, PushPayload) error {
	f.sent++
	return nil
}

func TestBuildPayloadTaskBlocked(t *testing.T) {
	taskID := uuid.New()
	payload, _ := json.Marshal(map[string]any{"task_id": taskID.String()})
	consumer := &DeliveryConsumer{}

	built := consumer.BuildPayload(eventbus.DomainEvent{EventType: "task.blocked", Payload: payload}, TierHigh)
	if !strings.Contains(strings.ToLower(built.Title), "blocked") {
		t.Fatalf("title = %q, want contains blocked", built.Title)
	}
	if !strings.Contains(built.DeepLink, taskID.String()) {
		t.Fatalf("deep link = %q, want contains task id %s", built.DeepLink, taskID)
	}
}

func TestConsumeSkipsWhenPreferenceBlocks(t *testing.T) {
	userID := uuid.New()
	payload, _ := json.Marshal(map[string]any{
		"event_type":     "run.dead_lettered",
		"target_user_id": userID.String(),
		"project_id":     uuid.NewString(),
	})
	adapter := &fakePushAdapter{}
	consumer := &DeliveryConsumer{
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		preferences: fakeDeliveryGate{should: false},
		tokens: fakeTokenStore{tokens: []PushToken{{
			Token:    "token-1",
			Platform: "apns",
			DeviceID: "device-1",
		}}},
		adapter: adapter,
	}

	err := consumer.Consume(context.Background(), eventbus.DomainEvent{
		OrganizationID: uuid.New(),
		EventType:      "run.dead_lettered",
		ActorType:      "system",
		Payload:        payload,
	})
	if err != nil {
		t.Fatalf("Consume error: %v", err)
	}
	if adapter.sent != 0 {
		t.Fatalf("adapter send count = %d, want 0", adapter.sent)
	}
}
