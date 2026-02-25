//go:build integration

package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestInboxActionUpdatesBoardDetailAndActivity(t *testing.T) {
	t.Parallel()

	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.focus = MainPanel

	before := model.BoardCounts()
	model.workspace.setMainView(ViewInbox)

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})

	after := model.BoardCounts()
	if after.Done <= before.Done {
		t.Fatalf("done count did not increase after approve: before=%+v after=%+v", before, after)
	}
	if got := model.TaskStatus("task-1"); got != "approved" {
		t.Fatalf("task-1 status = %q, want approved", got)
	}

	model.workspace.setMainView(ViewTask)
	taskDetail := model.WorkspaceRender(SizeM)
	if !strings.Contains(taskDetail, "status=approved") {
		t.Fatalf("task detail missing approved status: %q", taskDetail)
	}

	activity := strings.Join(model.ActivityEntries(), " | ")
	if !strings.Contains(activity, "inbox approve task-1") {
		t.Fatalf("activity feed missing inbox approve entry: %q", activity)
	}
}

func TestKeyboardOnlyNavigationOpenInContextFlow(t *testing.T) {
	t.Parallel()

	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 160, Height: 34})

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}}) // sidebar
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}) // project
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}) // first task session
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})                     // select
	if got := model.WorkspaceSession(); got != "session-task-1" {
		t.Fatalf("session after sidebar select = %q, want session-task-1", got)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}) // main
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	for _, r := range []rune("inbox") {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}}) // open in context

	if got := model.MainView(); got != ViewTask {
		t.Fatalf("main view after open in context = %s, want %s", got, ViewTask)
	}
	if got := model.WorkspaceSession(); got != "session-task-1" {
		t.Fatalf("workspace session after open in context = %q, want session-task-1", got)
	}
	if got := model.State().LastActiveChatSession; got != "session-task-1" {
		t.Fatalf("persisted chat session = %q, want session-task-1", got)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyTab}) // chat
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyTab}) // sidebar
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyTab}) // main
	if got := model.FocusedPanel(); got != MainPanel {
		t.Fatalf("focus should cycle back to main; got %s", panelLabel(got))
	}

	model = pressMsg(model, WorkspaceEnvelopeMsg{
		Envelope: EventEnvelope{
			Seq:        20,
			EventID:    "evt-flow",
			EventType:  "task.flow.advanced",
			OccurredAt: time.Now().UTC(),
			OrgID:      "org-1",
			Payload:    mustWorkspaceJSON(t, map[string]any{"task_id": "task-1", "flow_step": 4, "session_id": "session-task-1"}),
		},
	})
	if got := model.TaskFlow("task-1"); got != 4 {
		t.Fatalf("task flow after realtime update = %d, want 4", got)
	}
}
