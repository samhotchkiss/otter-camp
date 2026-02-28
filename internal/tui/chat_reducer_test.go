package tui

import (
	"encoding/json"
	"strings"
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
	// EX-400: seed a real UUID session so the placeholder guard does not block sends.
	model.activeSession = "11111111-2222-3333-4444-555555555555"

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

// EX-410: worker.unresponsive in the chat SSE envelope sets statusMessage on
// the active session and is a no-op when turnsSynced is false or the session
// does not match.
func TestWorkerUnresponsiveChatEnvelopeEX410(t *testing.T) {
	t.Run("sets-status-message", func(t *testing.T) {
		model := NewModel(DefaultState())
		model.turnsSynced = true
		model.activeSession = "session-org-abc"
		// activeTurnSessionID="" → sessionMatchesActive always returns true.

		model.applyChatEnvelope(EventEnvelope{
			Seq:       1,
			EventID:   "evt-wu",
			EventType: "worker.unresponsive",
			OrgID:     "org-1",
			Payload:   mustJSON(t, map[string]any{"session_id": "session-org-abc", "message": "Worker offline."}),
		})

		if got := model.statusMessage; got != "Worker offline." {
			t.Fatalf("statusMessage = %q, want %q", got, "Worker offline.")
		}
	})

	t.Run("uses-default-message-when-blank", func(t *testing.T) {
		model := NewModel(DefaultState())
		model.turnsSynced = true
		model.activeSession = "session-org-abc"

		model.applyChatEnvelope(EventEnvelope{
			Seq:       1,
			EventID:   "evt-wu2",
			EventType: "worker.unresponsive",
			OrgID:     "org-1",
			Payload:   mustJSON(t, map[string]any{"session_id": "session-org-abc", "message": "   "}),
		})

		if got := model.statusMessage; !strings.Contains(got, "ottercamp worker") {
			t.Fatalf("statusMessage = %q, want default worker warning", got)
		}
	})

	t.Run("ignored-when-not-synced", func(t *testing.T) {
		model := NewModel(DefaultState())
		model.turnsSynced = false
		before := model.statusMessage

		model.applyChatEnvelope(EventEnvelope{
			Seq:       1,
			EventID:   "evt-wu3",
			EventType: "worker.unresponsive",
			OrgID:     "org-1",
			Payload:   mustJSON(t, map[string]any{"session_id": "session-org-abc", "message": "Worker offline."}),
		})

		if got := model.statusMessage; got != before {
			t.Fatalf("statusMessage changed when not synced: %q → %q", before, got)
		}
	})

	t.Run("ignored-for-other-session", func(t *testing.T) {
		model := NewModel(DefaultState())
		model.turnsSynced = true
		model.activeTurnSessionID = "session-org-mine"
		before := model.statusMessage

		model.applyChatEnvelope(EventEnvelope{
			Seq:       1,
			EventID:   "evt-wu4",
			EventType: "worker.unresponsive",
			OrgID:     "org-1",
			Payload:   mustJSON(t, map[string]any{"session_id": "session-org-OTHER", "message": "Worker offline."}),
		})

		if got := model.statusMessage; got != before {
			t.Fatalf("statusMessage changed for wrong session: %q → %q", before, got)
		}
	})
}

// EX-412: when chat.turn.cancelled arrives with queued messages, the status
// bar should mention the queue so the user knows they have messages waiting.
func TestTurnCancelledWithQueueMentionsQueueEX412(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true
	model.activeTurn = true
	model.activeSession = "session-org-abc"
	// Seed two queued messages.
	model.queuedMessages = []QueuedMessage{
		{Text: "follow-up 1"},
		{Text: "follow-up 2"},
	}

	model.applyChatEnvelope(EventEnvelope{
		Seq:       1,
		EventID:   "evt-cancel",
		EventType: "chat.turn.cancelled",
		OrgID:     "org-1",
		Payload:   mustJSON(t, map[string]any{"session_id": "session-org-abc"}),
	})

	if model.ActiveTurn() {
		t.Fatal("activeTurn should be false after turn cancelled")
	}
	if got := model.QueueDepth(); got != 2 {
		// Queue is intentionally NOT cleared — user must decide what to do.
		t.Fatalf("queue depth = %d, want 2 (queue preserved after external cancel)", got)
	}
	if !strings.Contains(model.statusMessage, "2 queued") {
		t.Fatalf("statusMessage = %q, want mention of queued count", model.statusMessage)
	}
}

// EX-412: with no queued messages, turn cancellation keeps the original message.
func TestTurnCancelledWithoutQueueEX412(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true
	model.activeTurn = true
	model.activeSession = "session-org-abc"

	model.applyChatEnvelope(EventEnvelope{
		Seq:       1,
		EventID:   "evt-cancel2",
		EventType: "chat.turn.cancelled",
		OrgID:     "org-1",
		Payload:   mustJSON(t, map[string]any{"session_id": "session-org-abc"}),
	})

	if model.ActiveTurn() {
		t.Fatal("activeTurn should be false after turn cancelled")
	}
	if got := model.statusMessage; got != "Active turn cancelled." {
		t.Fatalf("statusMessage = %q, want %q", got, "Active turn cancelled.")
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
