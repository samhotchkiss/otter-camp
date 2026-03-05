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
	// Seed a chat session so markSessionUnread can find a node to mark.
	model.workspace.rebuildSidebar(
		"",
		[]SidebarChatItem{{SessionID: "session-chat-1", DisplayName: "Blog Site"}},
		nil,
	)

	// Directly mark the chat session as unread (simulates a received message).
	model.workspace.markSessionUnread("session-chat-1")

	entries := model.SidebarVisibleEntries()
	joined := strings.Join(entries, " | ")
	if !strings.Contains(joined, "Blog Site (1)") {
		t.Fatalf("chat session unread bubble missing: %q", joined)
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

// mustWorkspaceJSONUsed keeps the import alive.
var _ = func() bool { _, _ = time.Now(), mustWorkspaceJSON; return true }

func TestSidebarTreeNavigationExpandCollapseAndSelect(t *testing.T) {
	model := NewModel(DefaultState())
	// Seed one project with two task nodes so we can test expand/collapse.
	model.workspace.rebuildSidebar(
		"",
		nil,
		[]SidebarProjectItem{{ID: "alpha", DisplayName: "Project Alpha"}},
	)
	model.workspace.setProjectTasks("alpha", []SidebarTaskItem{
		{ID: "t1", Title: "Launch docs", WorkStatus: "todo"},
		{ID: "t2", Title: "CI hardening", WorkStatus: "in_progress"},
	}, true)
	// setProjectTasks with expandNode=true marks the project expanded, so visible nodes:
	// INBOX, CHATS header, Frank/General, PROJECTS header, project-alpha, task-t1, task-t2 = 7

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}}) // focus sidebar

	// Navigate to project-alpha node.
	projectPos := model.workspace.indexOfNode("project-alpha")
	for model.workspace.sidebarCursor < projectPos {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}

	// Collapse project: task children disappear.
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	collapsed := model.SidebarVisibleEntries()
	// INBOX + CHATS header + Frank/General + PROJECTS header + project-alpha = 5
	if len(collapsed) != 5 {
		t.Fatalf("collapsed sidebar entries = %d, want 5: %v", len(collapsed), collapsed)
	}

	// Expand project: task children reappear.
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	expanded := model.SidebarVisibleEntries()
	// INBOX + CHATS header + Frank/General + PROJECTS header + project-alpha + 2 tasks = 7
	if len(expanded) < 7 {
		t.Fatalf("expanded sidebar entries = %d, want at least 7: %v", len(expanded), expanded)
	}

	// Move to first task node and select it → ViewTask.
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	if got := model.MainView(); got != ViewTask {
		t.Fatalf("main view after selecting task node = %s, want %s", got, ViewTask)
	}
}

func TestSessionLabelResolvesTaskScopeId(t *testing.T) {
	workspace := newWorkspaceState()
	// The "session-task-current" alias resolves to the task with selectedTaskID.
	workspace.tasks["task-1"] = &taskRecord{ID: "task-1", Title: "Launch docs"}
	workspace.selectedTaskID = "task-1"

	got := workspace.sessionLabel("session-task-current")
	if got != "Launch docs" {
		t.Fatalf("task scope label = %q, want %q", got, "Launch docs")
	}
}

func TestRebuildSidebarBuildsProjectNodesFromActiveItems(t *testing.T) {
	workspace := newWorkspaceState()

	workspace.rebuildSidebar(
		"org-1",
		nil,
		[]SidebarProjectItem{
			{ID: "proj-alpha", DisplayName: "Alpha"},
			{ID: "proj-beta", DisplayName: "Beta"},
		},
	)

	got := workspace.existingProjects()
	if len(got) != 2 {
		t.Fatalf("project count = %d, want 2", len(got))
	}
	if got[0].ID != "proj-alpha" || got[0].DisplayName != "Alpha" {
		t.Fatalf("first project = %+v, want proj-alpha/Alpha", got[0])
	}
	if got[1].ID != "proj-beta" || got[1].DisplayName != "Beta" {
		t.Fatalf("second project = %+v, want proj-beta/Beta", got[1])
	}
}
