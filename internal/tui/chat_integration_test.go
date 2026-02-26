//go:build integration

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestChatStreamSimulationWithToolCallEvents(t *testing.T) {
	t.Parallel()

	server := newScriptedSSEServer(t, []scriptedResponse{
		{frames: []string{
			encodeEnvelopeFrame(1, "evt-1", "chat.message.delta", map[string]any{"message_id": "msg-1", "role": "assistant", "delta": "Hello "}),
			encodeEnvelopeFrame(2, "evt-2", "chat.tool_call.status", map[string]any{"message_id": "msg-1", "name": "search_docs", "status": "running"}),
			encodeEnvelopeFrame(3, "evt-3", "chat.message.finalized", map[string]any{"message_id": "msg-1", "role": "assistant", "content": "Hello world"}),
		}},
	})
	defer server.Close()

	model := NewModel(DefaultState())
	reducer := NewEventReducer(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := &RealtimeClient{
		Connector: HTTPSSEConnector{URL: server.URL()},
		Snapshots: SnapshotFetcherFunc(func(context.Context, []string) error { return nil }),
		Reducer:   reducer,
		Backoff:   []time.Duration{10 * time.Millisecond},
		OnEvent: func(event EventEnvelope, applied bool) {
			if applied {
				model = pressChatIntegrationMsg(model, ChatEnvelopeMsg{Envelope: event})
			}
			if event.EventType == "chat.message.finalized" {
				cancel()
			}
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()
	if err := <-done; err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	view := model.View()
	if !strings.Contains(view, "[ASSISTANT]") {
		t.Fatalf("view missing assistant role styling: %q", view)
	}
	if !strings.Contains(view, "tool search_docs (running)") {
		t.Fatalf("view missing inline tool-call status: %q", view)
	}
	if !strings.Contains(view, "Hello world") {
		t.Fatalf("view missing finalized content: %q", view)
	}
	if model.ActiveTurn() {
		t.Fatal("active turn should be false after finalized event")
	}
}

func TestScopeSwitchPreservesMainViewState(t *testing.T) {
	t.Parallel()

	model := NewModel(DefaultState())
	initialMainView := model.MainView()
	initialSession := model.ActiveChatSession()

	model = pressChatIntegrationMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if model.MainView() != initialMainView {
		t.Fatalf("main view changed via ] scope switch: got=%q want=%q", model.MainView(), initialMainView)
	}
	if model.ActiveChatSession() == initialSession {
		t.Fatalf("chat session did not change on ] scope switch: still %q", model.ActiveChatSession())
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

	model := NewModelWithRuntime(DefaultState(), RuntimeHints{ModifierReliabilityUncertain: true})
	model = pressChatIntegrationMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	model = runPaletteCommand(model, "focus sidebar")
	model = runPaletteCommand(model, "sidebar down")
	model = runPaletteCommand(model, "sidebar down")
	model = runPaletteCommand(model, "sidebar select")
	if got := model.ActiveChatSession(); got != "session-task-1" {
		t.Fatalf("session after sidebar select = %q, want session-task-1", got)
	}
	if got := model.MainView(); got != ViewTask {
		t.Fatalf("main view after sidebar select = %s, want %s", got, ViewTask)
	}

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
