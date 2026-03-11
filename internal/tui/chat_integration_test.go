//go:build integration

package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestChatStreamSimulationWithToolCallEvents(t *testing.T) {
	t.Parallel()

	model := NewModel(DefaultState())
	model.turnsSynced = true
	model.activeSession = generalSessionID
	model.workspace.activeSessionID = generalSessionID
	now := time.Now().UTC()
	for _, event := range []EventEnvelope{
		{Seq: 1, EventID: "evt-1", EventType: "chat.turn.started", OccurredAt: now, Payload: mustWorkspaceJSON(t, map[string]any{"session_id": generalSessionID})},
		{Seq: 2, EventID: "evt-2", EventType: "chat.message.delta", OccurredAt: now, Payload: mustWorkspaceJSON(t, map[string]any{"message_id": "msg-1", "session_id": generalSessionID, "role": "assistant", "delta": "Hello "})},
		{Seq: 3, EventID: "evt-3", EventType: "chat.tool_call.status", OccurredAt: now, Payload: mustWorkspaceJSON(t, map[string]any{"message_id": "msg-1", "name": "search_docs", "status": "running"})},
		{Seq: 4, EventID: "evt-4", EventType: "chat.message.finalized", OccurredAt: now, Payload: mustWorkspaceJSON(t, map[string]any{"message_id": "msg-1", "session_id": generalSessionID, "role": "assistant", "content": "Hello world"})},
	} {
		model = pressChatIntegrationMsg(model, ChatEnvelopeMsg{Envelope: event})
	}

	view := model.View()
	if !strings.Contains(view, "Frank") {
		t.Fatalf("view missing assistant role styling: %q", view)
	}
	if !strings.Contains(view, "▶ ⚙ search_docs (running)") {
		t.Fatalf("view missing inline tool-call status: %q", view)
	}
	messages := model.ChatMessages()
	if len(messages) == 0 || messages[len(messages)-1].Content != "Hello world" {
		t.Fatalf("chat messages missing finalized content: %+v view=%q", messages, view)
	}
	if model.ActiveTurn() {
		t.Fatal("active turn should be false after finalized event")
	}
}

func TestScopeSwitchPreservesMainViewState(t *testing.T) {
	t.Parallel()

	model := seededWorkspaceModel()
	initialMainView := model.MainView()
	initialSession := model.ActiveChatSession()

	model = pressChatIntegrationMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if model.MainView() != initialMainView {
		t.Fatalf("main view changed via ] scope switch: got=%q want=%q", model.MainView(), initialMainView)
	}
	if model.ActiveChatSession() != initialSession {
		t.Fatalf("chat session changed on deprecated ] binding: got %q want %q", model.ActiveChatSession(), initialSession)
	}

	model = pressChatIntegrationMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	for _, r := range []rune("scope task") {
		model = pressChatIntegrationMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = pressChatIntegrationMsg(model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.ChatScope() != ScopeTask {
		t.Fatalf("scope after :scope task = %s, want %s", model.ChatScope(), ScopeTask)
	}
	if model.MainView() != initialMainView {
		t.Fatalf("main view changed via :scope command: got=%q want=%q", model.MainView(), initialMainView)
	}
}

func TestFrankJumpPreservesMainViewState(t *testing.T) {
	t.Parallel()

	model := moveToTaskSession(NewModel(DefaultState()))
	model.workspace.setMainView(ViewInbox)
	initialMainView := model.MainView()

	model = pressChatIntegrationMsg(model, tea.KeyMsg{Type: tea.KeyCtrlG})
	if got := model.WorkspaceSession(); got != generalSessionID {
		t.Fatalf("workspace session after Ctrl-G = %q, want %q", got, generalSessionID)
	}
	if got := model.MainView(); got != initialMainView {
		t.Fatalf("main view changed via Ctrl-G: got=%q want=%q", got, initialMainView)
	}

	model = moveToTaskSession(model)
	model.workspace.setMainView(initialMainView)
	model = runCommand(model, "frank")
	if got := model.WorkspaceSession(); got != generalSessionID {
		t.Fatalf("workspace session after :frank = %q, want %q", got, generalSessionID)
	}
	if got := model.MainView(); got != initialMainView {
		t.Fatalf("main view changed via :frank: got=%q want=%q", got, initialMainView)
	}
}

func TestFrankJumpFailureShowsRetryMessage(t *testing.T) {
	t.Parallel()

	model := moveToTaskSession(NewModel(DefaultState()))
	delete(model.workspace.nodes, generalSidebarNodeID)
	model.workspace.topLevel = []string{"project-alpha"}
	beforeSession := model.WorkspaceSession()

	model = pressChatIntegrationMsg(model, tea.KeyMsg{Type: tea.KeyCtrlG})

	if got := model.WorkspaceSession(); got != beforeSession {
		t.Fatalf("workspace session changed on failed Frank jump = %q, want %q", got, beforeSession)
	}
	status := model.StatusMessage()
	if !strings.Contains(status, "Unable to load Frank session") {
		t.Fatalf("status missing failure message: %q", status)
	}
	if !strings.Contains(status, "retry") {
		t.Fatalf("status missing retry action: %q", status)
	}
}

func TestTmuxFallbackCommandsCoverCoreWorkflow(t *testing.T) {
	t.Parallel()

	model := seededWorkspaceModel()
	model.runtimeHints = RuntimeHints{ModifierReliabilityUncertain: true}
	model = pressChatIntegrationMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	model = runPaletteCommand(model, "focus sidebar")
	model = runPaletteCommand(model, "sidebar end")
	model = runPaletteCommand(model, "sidebar select")
	if got := model.ActiveChatSession(); got != "session-task-1" {
		t.Fatalf("session after sidebar select = %q, want session-task-1", got)
	}
	if got := model.MainView(); got != ViewTask {
		t.Fatalf("main view after sidebar select = %s, want %s", got, ViewTask)
	}
	model.activeSession = "00000000-0000-0000-0000-000000000001"
	model.workspace.activeSessionID = model.activeSession

	model = runPaletteCommand(model, "focus chat")
	for _, r := range []rune("first tmux message") {
		model = pressChatIntegrationMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = runPaletteCommand(model, "send")
	if !model.ActiveTurn() {
		t.Fatal("active turn should be true after :send")
	}

	for _, r := range []rune("queued follow-up") {
		model = pressChatIntegrationMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = runPaletteCommand(model, "send")
	if got := model.QueueDepth(); got != 1 {
		t.Fatalf("queue depth after second :send = %d, want 1", got)
	}

	model = runPaletteCommand(model, "queue steer")
	queue := model.QueueSnapshot()
	if len(queue) != 1 || !queue[0].Steer {
		t.Fatalf("queue steer not applied: %+v", queue)
	}

	model = runPaletteCommand(model, "cancel-turn")
	if model.ActiveTurn() {
		t.Fatal("active turn should be false after :cancel-turn")
	}

	initialMain := model.MainView()
	model = runPaletteCommand(model, "frank")
	if got := model.ActiveChatSession(); got != "session-org-general" {
		t.Fatalf("session after :frank = %q, want session-org-general", got)
	}
	if got := model.MainView(); got != initialMain {
		t.Fatalf("main view after :frank changed: got=%s want=%s", got, initialMain)
	}
}

func TestFrankJumpFailureSurfacesRetryHint(t *testing.T) {
	t.Parallel()

	model := NewModelWithRuntime(DefaultState(), RuntimeHints{ModifierReliabilityUncertain: true})
	delete(model.workspace.nodes, "session-general")

	model = pressChatIntegrationMsg(model, tea.KeyMsg{Type: tea.KeyCtrlG})
	view := model.View()
	if !strings.Contains(view, "Unable to load Frank session") || !strings.Contains(view, ":frank") {
		t.Fatalf("view missing frank jump retry hint: %q", view)
	}
}

func TestTmuxFallbackSidebarAndInboxSubcommands(t *testing.T) {
	t.Parallel()

	t.Run("sidebar home end expand collapse", func(t *testing.T) {
		model := seededWorkspaceModel()
		model.runtimeHints = RuntimeHints{ModifierReliabilityUncertain: true}
		model = pressChatIntegrationMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

		model = runPaletteCommand(model, "sidebar end")
		if got := model.workspace.currentSidebarID(); got != "session-alpha-task-1" {
			t.Fatalf("sidebar after :sidebar end = %q, want session-alpha-task-1", got)
		}

		model = runPaletteCommand(model, "sidebar home")
		if got := model.workspace.currentSidebarID(); got != "inbox" {
			t.Fatalf("sidebar after :sidebar home = %q, want inbox", got)
		}

		model = runPaletteCommand(model, "sidebar down")
		model = runPaletteCommand(model, "sidebar down")
		model = runPaletteCommand(model, "sidebar down")
		model = runPaletteCommand(model, "sidebar down")
		if got := model.workspace.currentSidebarID(); got != "project-alpha" {
			t.Fatalf("sidebar after :sidebar down = %q, want project-alpha", got)
		}

		model = runPaletteCommand(model, "sidebar collapse")
		if model.workspace.nodes["project-alpha"].Expanded {
			t.Fatal("project-alpha should be collapsed after :sidebar collapse")
		}

		model = runPaletteCommand(model, "sidebar expand")
		if !model.workspace.nodes["project-alpha"].Expanded {
			t.Fatal("project-alpha should be expanded after :sidebar expand")
		}
	})

	t.Run("inbox approve reject defer", func(t *testing.T) {
		model := seededWorkspaceModel()
		model.runtimeHints = RuntimeHints{ModifierReliabilityUncertain: true}
		model = pressChatIntegrationMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
		model = runPaletteCommand(model, "inbox approve")
		if got := model.TaskStatus("task-1"); got != "approved" {
			t.Fatalf("task-1 status after :inbox approve = %q, want approved", got)
		}
		if !strings.Contains(strings.Join(model.ActivityEntries(), " | "), "approved: OC-1") {
			t.Fatalf("activity missing approve entry: %q", strings.Join(model.ActivityEntries(), " | "))
		}

		model = seededWorkspaceModel()
		model.runtimeHints = RuntimeHints{ModifierReliabilityUncertain: true}
		model = pressChatIntegrationMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
		model = runPaletteCommand(model, "inbox reject")
		if got := model.TaskStatus("task-1"); got != "rejected" {
			t.Fatalf("task-1 status after :inbox reject = %q, want rejected", got)
		}
		if !strings.Contains(strings.Join(model.ActivityEntries(), " | "), "rejected: OC-1") {
			t.Fatalf("activity missing reject entry: %q", strings.Join(model.ActivityEntries(), " | "))
		}

		model = seededWorkspaceModel()
		model.runtimeHints = RuntimeHints{ModifierReliabilityUncertain: true}
		model = pressChatIntegrationMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
		model = runPaletteCommand(model, "inbox defer")
		if got := model.TaskStatus("task-1"); got != "deferred" {
			t.Fatalf("task-1 status after :inbox defer = %q, want deferred", got)
		}
		if !strings.Contains(strings.Join(model.ActivityEntries(), " | "), "deferred: OC-1") {
			t.Fatalf("activity missing defer entry: %q", strings.Join(model.ActivityEntries(), " | "))
		}
	})

	t.Run("inbox home end", func(t *testing.T) {
		model := seededWorkspaceModel()
		model.runtimeHints = RuntimeHints{ModifierReliabilityUncertain: true}
		model = pressChatIntegrationMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
		model = runPaletteCommand(model, "inbox end")
		if got := model.workspace.inboxCursor; got != 1 {
			t.Fatalf("inbox cursor after :inbox end = %d, want 1", got)
		}
		model = runPaletteCommand(model, "inbox home")
		if got := model.workspace.inboxCursor; got != 0 {
			t.Fatalf("inbox cursor after :inbox home = %d, want 0", got)
		}
	})
}

func pressChatIntegrationMsg(model Model, msg tea.Msg) Model {
	updated, _ := model.Update(msg)
	next, ok := updated.(Model)
	if !ok {
		panic("unexpected model type")
	}
	return next
}

func runPaletteCommand(model Model, command string) Model {
	model = pressChatIntegrationMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	for _, r := range []rune(command) {
		model = pressChatIntegrationMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return pressChatIntegrationMsg(model, tea.KeyMsg{Type: tea.KeyEnter})
}
