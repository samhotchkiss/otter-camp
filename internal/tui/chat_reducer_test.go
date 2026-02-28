package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

// EX-420: after chat.turn.cancelled with queued messages, pressing 'd' in the
// chat panel should delete the first queued message (not navigate to dashboard).
// Previously handleChatRunes required activeTurn=true for the queue actions,
// so 'd' fell through to handleWorkspaceRune which did dashboard navigation.
func TestTurnCancelledDKeyDeletesQueueEX420(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = ChatPanel
	model.turnsSynced = true
	model.activeTurn = true
	model.activeSession = "session-org-abc"
	model.queuedMessages = []QueuedMessage{
		{Text: "queued-1"},
		{Text: "queued-2"},
	}

	// External cancellation: activeTurn goes false, queue remains.
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
		t.Fatalf("queue depth before d = %d, want 2", got)
	}

	// Press 'd' — should delete first queued message, NOT navigate to dashboard.
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if got := model.QueueDepth(); got != 1 {
		t.Fatalf("queue depth after d = %d, want 1 (first item deleted)", got)
	}
	if model.workspace.mainView == ViewDashboard && !strings.Contains(model.statusMessage, "deleted") {
		t.Fatalf("d navigated to dashboard instead of deleting: mainView=%v statusMessage=%q", model.workspace.mainView, model.statusMessage)
	}
	if !strings.Contains(model.statusMessage, "deleted") {
		t.Fatalf("statusMessage = %q, want 'deleted' confirmation", model.statusMessage)
	}
}

// EX-421: after chat.turn.cancelled with queued messages, pressing 's' (steer)
// in the chat panel should say "Steer requires an active turn." rather than
// typing 's' into the input. Previously, the 's' case in handleChatRunes
// checked m.activeTurn before applying steer, and when activeTurn=false it
// fell through without returning — causing 's' to type into chatInput silently.
func TestTurnCancelledSKeyExplainsSteerEX421(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = ChatPanel
	model.turnsSynced = true
	model.activeTurn = true
	model.activeSession = "session-org-abc"
	model.queuedMessages = []QueuedMessage{
		{Text: "queued-1"},
	}

	// External cancellation: activeTurn goes false, queue remains.
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

	// Press 's' — should give feedback, NOT type 's' into the chat input.
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if got := model.ChatInput(); got != "" {
		t.Fatalf("chatInput after s = %q, want empty (s should not type into input in queue-action context)", got)
	}
	if got := model.QueueDepth(); got != 1 {
		t.Fatalf("queue depth after s = %d, want 1 (steer should not delete the queued message)", got)
	}
	if !strings.Contains(model.statusMessage, "active turn") {
		t.Fatalf("statusMessage = %q, want mention of 'active turn' requirement", model.statusMessage)
	}
}

// EX-422: the queue management hint rendered in the chat panel should show
// "e·edit · s·steer · d·delete" when activeTurn=true but only
// "e·edit · d·delete" when activeTurn=false (e.g. after external cancellation).
// Previously, "s·steer" was always shown even when steer would give an error.
func TestQueueHintDropsSteerWhenNoActiveTurnEX422(t *testing.T) {
	t.Run("active-turn-shows-steer", func(t *testing.T) {
		model := NewModel(DefaultState())
		model.focus = ChatPanel
		model.activeTurn = true
		model.queuedMessages = []QueuedMessage{{Text: "queued-1"}}
		panel := model.renderChatPanel(90, 20, true)
		if !strings.Contains(panel, "s·steer") {
			t.Fatalf("queue hint missing s·steer when activeTurn=true: %q", panel)
		}
	})

	t.Run("inactive-turn-omits-steer", func(t *testing.T) {
		model := NewModel(DefaultState())
		model.focus = ChatPanel
		model.activeTurn = false
		model.queuedMessages = []QueuedMessage{{Text: "queued-1"}}
		panel := model.renderChatPanel(90, 20, true)
		if strings.Contains(panel, "s·steer") {
			t.Fatalf("queue hint should not show s·steer when activeTurn=false, got: %q", panel)
		}
		if !strings.Contains(panel, "e·edit") {
			t.Fatalf("queue hint missing e·edit when activeTurn=false: %q", panel)
		}
		if !strings.Contains(panel, "d·delete") {
			t.Fatalf("queue hint missing d·delete when activeTurn=false: %q", panel)
		}
	})
}

// EX-424: pressing 'e' dequeues the first queued message into chatInput and
// sets editingQueued=true. If the user then cancels with Esc or Ctrl-U, the
// editingQueued flag must be cleared so the next sent message is not incorrectly
// marked Edited=true. Previously editingQueued remained true after clearing
// chatInput, causing a fresh message to carry Edited=true even though it had
// never been in the queue.
func TestEditQueuedThenCancelClearsEditingQueuedEX424(t *testing.T) {
	t.Run("esc-clears-editingQueued", func(t *testing.T) {
		model := NewModel(DefaultState())
		model.focus = ChatPanel
		model.activeSession = "11111111-2222-3333-4444-555555555555"
		model.activeTurn = false
		model.queuedMessages = []QueuedMessage{{Text: "original-msg"}}

		// 'e' pops the message into chatInput.
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
		if model.ChatInput() != "original-msg" {
			t.Fatalf("chatInput = %q, want %q", model.ChatInput(), "original-msg")
		}
		if !model.editingQueued {
			t.Fatal("editingQueued should be true after 'e'")
		}

		// Esc cancels the edit — editingQueued must be cleared.
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyEsc})
		if model.ChatInput() != "" {
			t.Fatalf("chatInput = %q, want empty after Esc cancel", model.ChatInput())
		}
		if model.editingQueued {
			t.Fatal("EX-424: editingQueued should be false after Esc cancels edit")
		}
		if !strings.Contains(model.statusMessage, "cancelled") {
			t.Fatalf("statusMessage = %q, want 'cancelled' mention", model.statusMessage)
		}
	})

	t.Run("ctrl-u-clears-editingQueued", func(t *testing.T) {
		model := NewModel(DefaultState())
		model.focus = ChatPanel
		model.activeSession = "11111111-2222-3333-4444-555555555555"
		model.activeTurn = false
		model.queuedMessages = []QueuedMessage{{Text: "queued-msg"}}

		// 'e' pops the message.
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
		if !model.editingQueued {
			t.Fatal("editingQueued should be true after 'e'")
		}

		// Ctrl-U clears the input — editingQueued must be cleared.
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyCtrlU})
		if model.editingQueued {
			t.Fatal("EX-424: editingQueued should be false after Ctrl-U (EX-424)")
		}
		if !strings.Contains(model.statusMessage, "cancelled") {
			t.Fatalf("statusMessage = %q, want 'cancelled' mention", model.statusMessage)
		}
	})

	t.Run("fresh-send-not-marked-edited", func(t *testing.T) {
		// After editing a queued message and cancelling, the next message sent
		// must NOT carry Edited=true even though editingQueued was temporarily set.
		model := NewModel(DefaultState())
		model.focus = ChatPanel
		model.activeSession = "11111111-2222-3333-4444-555555555555"
		model.activeTurn = true
		model.queuedMessages = []QueuedMessage{{Text: "queued-msg"}}

		// 'e' pops the message.
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
		if !model.editingQueued {
			t.Fatal("editingQueued should be true after 'e'")
		}

		// Cancel the edit by clearing the input (activeTurn=true so Esc would cancel
		// the turn; use Ctrl-U to test clearing input directly).
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyCtrlU})
		if model.editingQueued {
			t.Fatal("EX-424: editingQueued should be false after Ctrl-U")
		}

		// Now re-queue a fresh message — it must NOT be marked Edited.
		model.chatInput = "brand-new-message"
		model.sendOrQueueInput()
		queue := model.QueueSnapshot()
		if len(queue) == 0 {
			t.Fatal("expected 1 queued message after re-send")
		}
		if queue[0].Edited {
			t.Fatal("EX-424: fresh message must NOT be marked Edited after cancelled edit")
		}
	})
}

func TestEditQueuedThenEraseEmptiesClearsEditingQueuedEX425(t *testing.T) {
	t.Run("backspace-to-empty-clears-editingQueued", func(t *testing.T) {
		model := NewModel(DefaultState())
		model.focus = ChatPanel
		model.activeSession = "11111111-2222-3333-4444-555555555555"
		model.activeTurn = false
		model.queuedMessages = []QueuedMessage{{Text: "x"}}

		// 'e' pops "x" into chatInput.
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
		if model.ChatInput() != "x" {
			t.Fatalf("chatInput = %q, want %q", model.ChatInput(), "x")
		}
		if !model.editingQueued {
			t.Fatal("editingQueued should be true after 'e'")
		}

		// One backspace erases the single char — input is now empty.
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyBackspace})
		if model.ChatInput() != "" {
			t.Fatalf("chatInput = %q, want empty after backspace", model.ChatInput())
		}
		if model.editingQueued {
			t.Fatal("EX-425: editingQueued should be false after backspace empties input")
		}
	})

	t.Run("backspace-partial-keeps-editingQueued", func(t *testing.T) {
		model := NewModel(DefaultState())
		model.focus = ChatPanel
		model.activeSession = "11111111-2222-3333-4444-555555555555"
		model.activeTurn = false
		model.queuedMessages = []QueuedMessage{{Text: "hi"}}

		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
		// One backspace: "hi" → "h". editingQueued must stay true (input not empty yet).
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyBackspace})
		if model.ChatInput() != "h" {
			t.Fatalf("chatInput = %q, want %q after one backspace", model.ChatInput(), "h")
		}
		if !model.editingQueued {
			t.Fatal("EX-425: editingQueued should still be true after partial backspace (non-empty input)")
		}

		// Second backspace: "h" → "". editingQueued must now be cleared.
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyBackspace})
		if model.ChatInput() != "" {
			t.Fatalf("chatInput = %q, want empty after second backspace", model.ChatInput())
		}
		if model.editingQueued {
			t.Fatal("EX-425: editingQueued should be false after backspace empties input")
		}
	})

	t.Run("ctrl-w-to-empty-clears-editingQueued", func(t *testing.T) {
		model := NewModel(DefaultState())
		model.focus = ChatPanel
		model.activeSession = "11111111-2222-3333-4444-555555555555"
		model.activeTurn = false
		model.queuedMessages = []QueuedMessage{{Text: "some message"}}

		// 'e' pops "some message" into chatInput.
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
		if !model.editingQueued {
			t.Fatal("editingQueued should be true after 'e'")
		}

		// Ctrl-W repeatedly until input is empty.
		for model.ChatInput() != "" {
			model = pressKey(model, tea.KeyMsg{Type: tea.KeyCtrlW})
		}
		if model.editingQueued {
			t.Fatal("EX-425: editingQueued should be false after Ctrl-W empties input")
		}
		if !strings.Contains(model.statusMessage, "erased") {
			t.Fatalf("statusMessage = %q, want 'erased' mention when Ctrl-W clears queued edit", model.statusMessage)
		}
	})

	t.Run("backspace-to-empty-fresh-send-not-marked-edited", func(t *testing.T) {
		// After backspacing through a dequeued message, a freshly-typed message
		// must NOT carry Edited=true.
		model := NewModel(DefaultState())
		model.focus = ChatPanel
		model.activeSession = "11111111-2222-3333-4444-555555555555"
		model.activeTurn = true
		model.queuedMessages = []QueuedMessage{{Text: "ok"}}

		// 'e' pops the message.
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
		// Backspace twice to erase "ok".
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyBackspace})
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyBackspace})
		if model.editingQueued {
			t.Fatal("EX-425: editingQueued should be false after backspace-to-empty")
		}

		// Type fresh content and re-queue.
		model.chatInput = "fresh-content"
		model.sendOrQueueInput()
		queue := model.QueueSnapshot()
		if len(queue) == 0 {
			t.Fatal("expected 1 queued message after re-send")
		}
		if queue[0].Edited {
			t.Fatal("EX-425: fresh message must NOT be marked Edited after backspace-to-empty cancelled edit")
		}
	})
}

// TestTurnCompletionWithNoQueueSetsResponseReceivedEX426 verifies that when a
// chat turn completes with no queued messages, completeTurnAndPromoteQueue sets
// statusMessage to "Response received." instead of silently clearing it to "".
// Previously the blank status left users with no confirmation after a successful
// assistant response (EX-426).
func TestTurnCompletionWithNoQueueSetsResponseReceivedEX426(t *testing.T) {
	t.Run("turn-completed-event", func(t *testing.T) {
		model := NewModel(DefaultState())
		model.turnsSynced = true
		model.activeTurn = true
		model.activeSession = "11111111-2222-3333-4444-555555555555"
		model.activeTurnSessionID = "11111111-2222-3333-4444-555555555555"

		event := EventEnvelope{
			EventType: "chat.turn.completed",
			Payload:   mustJSON(t, map[string]any{"session_id": "11111111-2222-3333-4444-555555555555"}),
		}
		model.applyChatEnvelope(event)

		if model.ActiveTurn() {
			t.Fatal("EX-426: activeTurn should be false after turn.completed")
		}
		if model.StatusMessage() != "Response received." {
			t.Fatalf("EX-426: statusMessage = %q, want \"Response received.\"", model.StatusMessage())
		}
	})

	t.Run("message-finalized-event", func(t *testing.T) {
		model := NewModel(DefaultState())
		model.turnsSynced = true
		model.activeTurn = true
		model.activeSession = "11111111-2222-3333-4444-555555555555"
		model.activeTurnSessionID = "11111111-2222-3333-4444-555555555555"

		event := EventEnvelope{
			EventType: "chat.message.finalized",
			Payload: mustJSON(t, map[string]any{
				"message_id": "msg-1",
				"session_id": "11111111-2222-3333-4444-555555555555",
				"role":       "assistant",
				"content":    "Hello from assistant",
			}),
		}
		model.applyChatEnvelope(event)

		if model.ActiveTurn() {
			t.Fatal("EX-426: activeTurn should be false after message.finalized with no queue")
		}
		if model.StatusMessage() != "Response received." {
			t.Fatalf("EX-426: statusMessage = %q, want \"Response received.\"", model.StatusMessage())
		}
	})

	t.Run("queued-message-still-promotes", func(t *testing.T) {
		// Regression guard: when there ARE queued messages, the promote-status
		// message is used (not "Response received."), and activeTurn stays true.
		model := NewModel(DefaultState())
		model.turnsSynced = true
		model.activeTurn = true
		model.activeSession = "11111111-2222-3333-4444-555555555555"
		model.activeTurnSessionID = "11111111-2222-3333-4444-555555555555"
		model.queuedMessages = []QueuedMessage{{Text: "second message"}}

		event := EventEnvelope{
			EventType: "chat.turn.completed",
			Payload:   mustJSON(t, map[string]any{"session_id": "11111111-2222-3333-4444-555555555555"}),
		}
		model.applyChatEnvelope(event)

		if !model.ActiveTurn() {
			t.Fatal("EX-426 regression: activeTurn should be true after promoting queued message")
		}
		if model.QueueDepth() != 0 {
			t.Fatalf("EX-426 regression: queue depth = %d, want 0", model.QueueDepth())
		}
		if model.StatusMessage() == "Response received." {
			t.Fatal("EX-426 regression: promote path should NOT show \"Response received.\"")
		}
	})
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	return raw
}
