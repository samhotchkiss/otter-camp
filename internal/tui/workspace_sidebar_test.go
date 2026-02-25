package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSidebarUnreadPropagationFromRealtimeEvents(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, WorkspaceEnvelopeMsg{
		Envelope: EventEnvelope{
			Seq:        10,
			EventID:    "evt-status",
			EventType:  "task.status.changed",
			OccurredAt: time.Now().UTC(),
			OrgID:      "org-1",
			Payload:    mustWorkspaceJSON(t, map[string]any{"task_id": "task-1", "status": "in_progress", "session_id": "session-task-1"}),
		},
	})

	entries := model.SidebarVisibleEntries()
	joined := strings.Join(entries, " | ")
	if !strings.Contains(joined, "Project Alpha (1)") {
		t.Fatalf("project unread bubble missing: %q", joined)
	}
	if !strings.Contains(joined, "Task 1 / Launch docs (1)") {
		t.Fatalf("task unread missing: %q", joined)
	}
}

func mustWorkspaceJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	return raw
}

func TestSidebarTreeNavigationExpandCollapseAndSelect(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}}) // focus sidebar

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}) // project
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}}) // collapse
	collapsed := model.SidebarVisibleEntries()
	if len(collapsed) != 2 {
		t.Fatalf("collapsed sidebar entries = %d, want 2", len(collapsed))
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}}) // expand
	expanded := model.SidebarVisibleEntries()
	if len(expanded) < 4 {
		t.Fatalf("expanded sidebar entries = %d, want at least 4", len(expanded))
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}) // first task session
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})                     // select
	if got := model.WorkspaceSession(); got != "session-task-1" {
		t.Fatalf("selected session = %q, want %q", got, "session-task-1")
	}
	if got := model.MainView(); got != ViewTask {
		t.Fatalf("main view after selecting task session = %s, want %s", got, ViewTask)
	}
}
