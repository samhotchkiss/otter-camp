package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestToolCallCollapseStateToggling(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = ChatPanel
	model.chatMessages = []ChatMessage{
		{
			ID:        "msg-1",
			Role:      "assistant",
			Finalized: true,
			ToolCalls: []ToolCallStatus{
				{ID: "tc-1", Name: "search_docs", Status: "running", Result: "tool output"},
			},
		},
	}

	collapsed := strings.Join(model.renderChatMessages(80), "\n")
	if !strings.Contains(collapsed, "▶ ⚙ search_docs (running)") {
		t.Fatalf("expected collapsed tool call marker: %q", collapsed)
	}
	if strings.Contains(collapsed, "tool output") {
		t.Fatalf("tool result should be hidden while collapsed: %q", collapsed)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	expanded := strings.Join(model.renderChatMessages(80), "\n")
	if !strings.Contains(expanded, "▼ ⚙ search_docs (running)") {
		t.Fatalf("expected expanded tool call marker: %q", expanded)
	}
	if !strings.Contains(expanded, "tool output") {
		t.Fatalf("tool result should be visible when expanded: %q", expanded)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	collapsedAgain := strings.Join(model.renderChatMessages(80), "\n")
	if !strings.Contains(collapsedAgain, "▶ ⚙ search_docs (running)") {
		t.Fatalf("expected collapsed marker after second toggle: %q", collapsedAgain)
	}
}

func TestToolResultTextStoredAndDisplayedOnExpand(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = ChatPanel
	model.turnsSynced = true
	model.activeTurn = true

	model.applyChatEnvelope(EventEnvelope{
		Seq:        1,
		EventID:    "evt-tool-status",
		EventType:  "chat.tool_call.status",
		OccurredAt: time.Now().UTC(),
		OrgID:      "org-1",
		Payload: mustToolJSON(t, map[string]any{
			"message_id":   "msg-1",
			"tool_call_id": "tc-1",
			"name":         "search_docs",
			"status":       "success",
		}),
	})

	longResult := "tool result: " + strings.Repeat("x", 400)
	model.applyChatEnvelope(EventEnvelope{
		Seq:        2,
		EventID:    "evt-tool-result",
		EventType:  "chat.message.finalized",
		OccurredAt: time.Now().UTC(),
		OrgID:      "org-1",
		Payload: mustToolJSON(t, map[string]any{
			"message_id":   "msg-tool-1",
			"role":         "tool_result",
			"tool_call_id": "tc-1",
			"content":      longResult,
		}),
	})

	messages := model.ChatMessages()
	if len(messages) != 1 || len(messages[0].ToolCalls) != 1 {
		t.Fatalf("unexpected tool call state: %+v", messages)
	}
	if got := messages[0].ToolCalls[0].Result; got != longResult {
		t.Fatalf("stored tool result = %q, want %q", got, longResult)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	expanded := strings.Join(model.renderChatMessages(80), "\n")
	if !strings.Contains(expanded, "▼ ⚙ search_docs (success)") {
		t.Fatalf("expanded tool call marker missing: %q", expanded)
	}
	if !strings.Contains(expanded, "tool result:") {
		t.Fatalf("expanded tool result text missing: %q", expanded)
	}
	if !strings.Contains(expanded, "(result truncated)") {
		t.Fatalf("large tool result should show truncation hint: %q", expanded)
	}
}

func mustToolJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	return raw
}
