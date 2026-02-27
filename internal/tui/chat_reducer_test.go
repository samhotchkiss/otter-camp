package tui

import (
	"encoding/json"
	"testing"
	"time"
)

func TestChatReducerDeltaFinalizeSequencing(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true
	model.activeTurn = true

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

func TestChatReducerChunkEventCompatibility(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true
	model.activeTurn = true

	chunkEvent := EventEnvelope{
		Seq:        1,
		EventID:    "evt-1",
		EventType:  "chat.message.chunk",
		OccurredAt: time.Now().UTC(),
		OrgID:      "org-1",
		Payload:    mustJSON(t, map[string]any{"message_id": "msg-1", "delta": "Hello"}),
	}
	model.applyChatEnvelope(chunkEvent)

	if !model.ActiveTurn() {
		t.Fatal("expected active turn after chunk")
	}
	messages := model.ChatMessages()
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	if got := messages[0].Content; got != "Hello" {
		t.Fatalf("chunk content = %q, want %q", got, "Hello")
	}
	if messages[0].Role != "assistant" {
		t.Fatalf("message role = %q, want assistant", messages[0].Role)
	}
}

func TestChatReducerTurnCompletedClearsAndPromotesQueue(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true

	model.chatInput = "first"
	model.sendOrQueueInput()

	model.applyChatEnvelope(EventEnvelope{
		Seq:        1,
		EventID:    "evt-1",
		EventType:  "chat.message.chunk",
		OccurredAt: time.Now().UTC(),
		OrgID:      "org-1",
		Payload:    mustJSON(t, map[string]any{"message_id": "msg-assistant", "delta": "working..."}),
	})

	model.chatInput = "follow up"
	model.sendOrQueueInput()
	if got := model.QueueDepth(); got != 1 {
		t.Fatalf("queue depth before turn completed = %d, want 1", got)
	}

	model.applyChatEnvelope(EventEnvelope{
		Seq:        2,
		EventID:    "evt-2",
		EventType:  "chat.turn.completed",
		OccurredAt: time.Now().UTC(),
		OrgID:      "org-1",
		Payload:    mustJSON(t, map[string]any{"turn_id": "turn-1"}),
	})

	if got := model.QueueDepth(); got != 0 {
		t.Fatalf("queue depth after turn completed = %d, want 0", got)
	}
	if !model.ActiveTurn() {
		t.Fatal("expected active turn after queued promotion")
	}

	messages := model.ChatMessages()
	if len(messages) != 3 {
		t.Fatalf("message count after turn completed = %d, want 3", len(messages))
	}
	if !messages[1].Finalized {
		t.Fatal("assistant message should be finalized on turn completion")
	}
	if got := messages[2].Content; got != "follow up" {
		t.Fatalf("promoted queued content = %q, want %q", got, "follow up")
	}
}

func TestChatReducerIgnoresUnsolicitedChunkWhenNoActiveTurn(t *testing.T) {
	model := NewModel(DefaultState())

	model.applyChatEnvelope(EventEnvelope{
		Seq:        1,
		EventID:    "evt-1",
		EventType:  "chat.message.chunk",
		OccurredAt: time.Now().UTC(),
		OrgID:      "org-1",
		Payload:    mustJSON(t, map[string]any{"message_id": "msg-1", "delta": "ignored"}),
	})

	if model.ActiveTurn() {
		t.Fatal("active turn should remain false for unsolicited chunk")
	}
	if got := len(model.ChatMessages()); got != 0 {
		t.Fatalf("message count = %d, want 0", got)
	}
}

func TestChatReducerHandlesInterjectionRole(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true
	model.activeTurn = true

	model.applyChatEnvelope(EventEnvelope{
		Seq:        1,
		EventID:    "evt-interjection",
		EventType:  "chat.message.finalized",
		OccurredAt: time.Now().UTC(),
		OrgID:      "org-1",
		Payload: mustJSON(t, map[string]any{
			"message_id": "msg-interjection",
			"role":       "interjection",
			"content":    "Side-channel update from supervisor.",
		}),
	})

	messages := model.ChatMessages()
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	if got := messages[0].Role; got != "interjection" {
		t.Fatalf("message role = %q, want interjection", got)
	}
	if got := messages[0].Content; got != "Side-channel update from supervisor." {
		t.Fatalf("message content = %q, want interjection content", got)
	}
	if !messages[0].Finalized {
		t.Fatal("interjection message should be finalized")
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
