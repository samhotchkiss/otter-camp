package tui

import (
	"encoding/json"
	"testing"
	"time"
)

func TestChatReducerDeltaFinalizeSequencing(t *testing.T) {
	model := NewModel(DefaultState())

	deltaEvent := EventEnvelope{
		Seq:        1,
		EventID:    "evt-1",
		EventType:  "chat.message.delta",
		OccurredAt: time.Now().UTC(),
		OrgID:      "org-1",
		Payload:    mustJSON(t, map[string]any{"message_id": "msg-1", "role": "assistant", "delta": "Hello"}),
	}
	model.applyChatEnvelope(deltaEvent)

	if !model.ActiveTurn() {
		t.Fatal("expected active turn after delta")
	}
	messages := model.ChatMessages()
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	if got := messages[0].Content; got != "Hello" {
		t.Fatalf("delta content = %q, want %q", got, "Hello")
	}
	if messages[0].Finalized {
		t.Fatal("delta message should not be finalized")
	}

	finalizedEvent := EventEnvelope{
		Seq:        2,
		EventID:    "evt-2",
		EventType:  "chat.message.finalized",
		OccurredAt: time.Now().UTC(),
		OrgID:      "org-1",
		Payload:    mustJSON(t, map[string]any{"message_id": "msg-1", "role": "assistant", "content": "Hello world"}),
	}
	model.applyChatEnvelope(finalizedEvent)

	if model.ActiveTurn() {
		t.Fatal("expected inactive turn after finalize")
	}
	messages = model.ChatMessages()
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	if got := messages[0].Content; got != "Hello world" {
		t.Fatalf("final content = %q, want %q", got, "Hello world")
	}
	if !messages[0].Finalized {
		t.Fatal("message should be finalized")
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	return raw
}
