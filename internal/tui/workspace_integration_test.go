//go:build integration

package tui

import (
	"context"
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

func TestTaskDetailUsesActiveExecutionSessionInRightPaneEX249(t *testing.T) {
	t.Parallel()

	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadChatHistory: func(_ context.Context, _ string) ([]ChatMessage, error) {
			return nil, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 140, Height: 34})
	model.workspace.setMainView(ViewTask)
	model.workspace.selectedTaskID = "task-249-live"
	model.activeScope = ScopeTask

	updated, cmd := model.Update(taskDetailLoadedMsg{Detail: TaskDetailItem{
		ID:                  "task-249-live",
		Title:               "Live work",
		SessionID:           "00000000-0000-0000-0000-000000002498",
		ActiveExecutionID:   "00000000-0000-0000-0000-000000002498",
		DiscussionSessionID: "00000000-0000-0000-0000-000000002499",
	}})
	model = updated.(Model)
	runNonTimerCmds(cmd)

	if model.taskPaneTab != taskPaneTabJournal {
		t.Fatalf("task pane tab = %s, want journal", model.taskPaneTab)
	}
	if model.ActiveChatSession() != "00000000-0000-0000-0000-000000002498" {
		t.Fatalf("active session = %q, want active execution session", model.ActiveChatSession())
	}

	chatPanel := model.renderChatPanel(56, 18, true)
	if !strings.Contains(chatPanel, "Journal") {
		t.Fatalf("chat panel should expose task tabs: %q", chatPanel)
	}
}

func TestTaskPaneTabSwitchPreservesScrollStateEX249(t *testing.T) {
	t.Parallel()

	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadChatHistory: func(_ context.Context, _ string) ([]ChatMessage, error) {
			return nil, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 140, Height: 34})
	model.workspace.setMainView(ViewTask)
	model.workspace.selectedTaskID = "task-249-scroll"
	model.workspace.tasks["task-249-scroll"] = &taskRecord{
		ID:                  "task-249-scroll",
		Title:               "Preserve scroll",
		Status:              "in_progress",
		SessionID:           "00000000-0000-0000-0000-000000002500",
		ActiveExecutionID:   "00000000-0000-0000-0000-000000002500",
		DiscussionSessionID: "00000000-0000-0000-0000-000000002501",
	}
	model.focus = ChatPanel
	model.activeScope = ScopeTask
	model.activeSession = "00000000-0000-0000-0000-000000002500"
	model.taskPaneTab = taskPaneTabJournal
	model.taskPaneTaskID = "task-249-scroll"
	model.chatScrollOffset = 4

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEsc})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRight}) // events
	model.chatScrollOffset = 2
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRight}) // discussion
	model.chatScrollOffset = 5
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyLeft}) // back to events
	if model.chatScrollOffset != 2 {
		t.Fatalf("events scroll offset = %d, want 2", model.chatScrollOffset)
	}
	if model.workspace.selectedTaskID != "task-249-scroll" {
		t.Fatalf("selected task changed across tab switches: %q", model.workspace.selectedTaskID)
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyLeft}) // back to journal
	if model.chatScrollOffset != 4 {
		t.Fatalf("journal scroll offset = %d, want 4", model.chatScrollOffset)
	}
}

func TestCompletedTaskFallsBackDeterministicallyEX249(t *testing.T) {
	t.Parallel()

	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadChatHistory: func(_ context.Context, _ string) ([]ChatMessage, error) {
			return nil, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 140, Height: 34})
	model.workspace.setMainView(ViewTask)
	model.activeScope = ScopeTask
	model.workspace.selectedTaskID = "task-249-recent-fallback"

	updated, _ := model.Update(taskDetailLoadedMsg{Detail: TaskDetailItem{
		ID:                "task-249-recent-fallback",
		Title:             "Recent execution",
		WorkStatus:        "done",
		SessionID:         "00000000-0000-0000-0000-000000002502",
		RecentExecutionID: "00000000-0000-0000-0000-000000002502",
	}})
	model = updated.(Model)
	if model.taskPaneTab != taskPaneTabJournal {
		t.Fatalf("recent execution fallback tab = %s, want journal", model.taskPaneTab)
	}

	model.workspace.selectedTaskID = "task-249-discussion-fallback"
	updated, _ = model.Update(taskDetailLoadedMsg{Detail: TaskDetailItem{
		ID:                  "task-249-discussion-fallback",
		Title:               "Discussion fallback",
		WorkStatus:          "done",
		DiscussionSessionID: "00000000-0000-0000-0000-000000002503",
	}})
	model = updated.(Model)
	if model.taskPaneTab != taskPaneTabDiscussion {
		t.Fatalf("discussion fallback tab = %s, want discussion", model.taskPaneTab)
	}
}

func TestTaskPaneStreamingEscAndCancelEX249(t *testing.T) {
	t.Parallel()

	cancelCalls := 0
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadChatHistory: func(_ context.Context, _ string) ([]ChatMessage, error) {
			return nil, nil
		},
		CancelChatTurn: func(_ context.Context, _ string) error {
			cancelCalls++
			return nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 140, Height: 34})
	model.workspace.setMainView(ViewTask)
	model.workspace.selectedTaskID = "task-249-stream"
	model.workspace.tasks["task-249-stream"] = &taskRecord{
		ID:                  "task-249-stream",
		Title:               "Streaming work",
		Status:              "in_progress",
		SessionID:           "00000000-0000-0000-0000-000000002506",
		ActiveExecutionID:   "00000000-0000-0000-0000-000000002506",
		DiscussionSessionID: "00000000-0000-0000-0000-000000002507",
	}
	model.focus = ChatPanel
	model.activeScope = ScopeTask
	model.activeSession = "00000000-0000-0000-0000-000000002506"
	model.taskPaneTab = taskPaneTabJournal
	model.taskPaneTaskID = "task-249-stream"
	model.turnsSynced = true
	model.chatInput = "keep draft"

	model = pressMsg(model, ChatEnvelopeMsg{
		Envelope: EventEnvelope{
			EventType: "chat.turn.started",
			Payload:   mustWorkspaceJSON(t, map[string]any{"session_id": model.activeSession}),
		},
	})
	model = pressMsg(model, ChatEnvelopeMsg{
		Envelope: EventEnvelope{
			EventType:  "chat.message.delta",
			OccurredAt: time.Now().UTC(),
			Payload: mustWorkspaceJSON(t, map[string]any{
				"message_id": "msg-stream",
				"session_id": model.activeSession,
				"role":       "assistant",
				"delta":      "working...",
			}),
		},
	})
	if !model.activeTurn {
		t.Fatal("streaming turn should remain active")
	}
	if len(model.ChatMessages()) == 0 {
		t.Fatal("streaming delta should appear in the journal session")
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEsc})
	if model.taskPaneMode != taskPaneModeNavigate {
		t.Fatalf("task pane mode after Esc = %s, want navigate", model.taskPaneMode)
	}
	if !model.activeTurn {
		t.Fatal("Esc should not cancel the active streaming turn")
	}
	if cancelCalls != 0 {
		t.Fatalf("Esc should not call cancel; got %d call(s)", cancelCalls)
	}
	if model.chatInput != "keep draft" {
		t.Fatalf("Esc should preserve draft input, got %q", model.chatInput)
	}

	cmd := model.executeCommand(":cancel")
	if cmd == nil {
		t.Fatal(":cancel should return a cancel cmd during streaming")
	}
	msg := cmd()
	updated, followup := model.Update(msg)
	model = updated.(Model)
	runNonTimerCmds(followup)

	if model.activeTurn {
		t.Fatal(":cancel should stop the active streaming turn")
	}
	if cancelCalls != 1 {
		t.Fatalf(":cancel should call cancel once; got %d", cancelCalls)
	}
}
