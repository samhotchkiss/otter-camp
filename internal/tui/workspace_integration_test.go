//go:build integration

package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"

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

func TestTaskJumpDuplicateTitlesRequireExplicitDisambiguationEX257(t *testing.T) {
	t.Parallel()

	loadCalledWith := ""
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadTaskDetail: func(_ context.Context, id string) (*TaskDetailItem, error) {
			loadCalledWith = id
			return &TaskDetailItem{ID: id, Title: "Shared task"}, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 160, Height: 34})
	model.focus = MainPanel
	model.workspace.mainView = ViewDashboard
	model.workspace.nodes["project-proj-257-alpha"] = &sidebarNode{
		ID:        "project-proj-257-alpha",
		Kind:      sidebarKindProject,
		Label:     "Alpha Project",
		ProjectID: "proj-257-alpha",
	}
	model.workspace.nodes["project-proj-257-beta"] = &sidebarNode{
		ID:        "project-proj-257-beta",
		Kind:      sidebarKindProject,
		Label:     "Beta Project",
		ProjectID: "proj-257-beta",
	}
	model.workspace.tasks = map[string]*taskRecord{
		"11111111-1111-1111-1111-111111111111": {
			ID:         "11111111-1111-1111-1111-111111111111",
			ProjectID:  "proj-257-alpha",
			TaskNumber: 12,
			Title:      "Shared task",
		},
		"22222222-2222-2222-2222-222222222222": {
			ID:         "22222222-2222-2222-2222-222222222222",
			ProjectID:  "proj-257-beta",
			TaskNumber: 7,
			Title:      "Shared task",
		},
	}
	model.workspace.taskOrder = []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
	}

	cmd := model.executeCommand(":task Shared task")
	if cmd != nil {
		t.Fatal("EX-257: ambiguous :task jump returned cmd; want nil")
	}

	if got := model.MainView(); got != ViewDashboard {
		t.Fatalf("EX-257: ambiguous :task jump changed main view to %v", got)
	}
	if got := model.workspace.selectedTaskID; got != "" {
		t.Fatalf("EX-257: ambiguous :task jump selected task %q", got)
	}
	if loadCalledWith != "" {
		t.Fatalf("EX-257: ambiguous :task jump loaded task detail for %q", loadCalledWith)
	}
	for _, want := range []string{
		`Ambiguous task "Shared task".`,
		"Alpha Project",
		"Beta Project",
	} {
		if !strings.Contains(model.statusMessage, want) {
			t.Fatalf("EX-257: ambiguous status %q missing %q", model.statusMessage, want)
		}
	}

	cmd = model.executeCommand(":task OC-7")
	if cmd == nil {
		t.Fatal("EX-257: task-number :task jump returned nil cmd")
	}
	_ = cmd()

	if got := model.MainView(); got != ViewTask {
		t.Fatalf("EX-257: task-number :task jump main view = %v, want %v", got, ViewTask)
	}
	if got := model.workspace.selectedTaskID; got != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("EX-257: task-number :task jump selected %q, want beta task", got)
	}
	if loadCalledWith != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("EX-257: task-number :task jump loaded %q, want beta task", loadCalledWith)
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

func TestTaskDetailRendersBoundDiscussionAndExecutionSessionsEX254(t *testing.T) {
	t.Parallel()

	executionSessionID := "00000000-0000-0000-0000-000000002541"
	discussionSessionID := "00000000-0000-0000-0000-000000002542"
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadChatHistory: func(_ context.Context, sessionID string) ([]ChatMessage, error) {
			switch sessionID {
			case executionSessionID:
				return []ChatMessage{{ID: "exec-marker", Role: "assistant", Content: "EXECUTION MARKER: async execution session.", Finalized: true}}, nil
			case discussionSessionID:
				return []ChatMessage{{ID: "discussion-marker", Role: "assistant", Content: "DISCUSSION MARKER: sync task discussion session.", Finalized: true}}, nil
			default:
				return nil, nil
			}
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 140, Height: 34})
	model.workspace.setMainView(ViewTask)
	model.workspace.selectedTaskID = "task-254-live"
	model.activeScope = ScopeTask

	updated, cmd := model.Update(taskDetailLoadedMsg{Detail: TaskDetailItem{
		ID:                  "task-254-live",
		Title:               "UX Task Detail Sandbox",
		SessionID:           executionSessionID,
		ActiveExecutionID:   executionSessionID,
		RecentExecutionID:   executionSessionID,
		DiscussionSessionID: discussionSessionID,
	}})
	model = updated.(Model)
	model = applyImmediateCmdMessages(model, cmd)

	task := model.workspace.tasks["task-254-live"]
	if task == nil {
		t.Fatal("task detail record missing after load")
	}
	if task.DiscussionSessionID != discussionSessionID {
		t.Fatalf("DiscussionSessionID = %q, want %q", task.DiscussionSessionID, discussionSessionID)
	}
	if task.ActiveExecutionID != executionSessionID {
		t.Fatalf("ActiveExecutionID = %q, want %q", task.ActiveExecutionID, executionSessionID)
	}
	if task.RecentExecutionID != executionSessionID {
		t.Fatalf("RecentExecutionID = %q, want %q", task.RecentExecutionID, executionSessionID)
	}
	if model.ActiveChatSession() != executionSessionID {
		t.Fatalf("active session = %q, want execution session", model.ActiveChatSession())
	}
	if model.taskPaneTab != taskPaneTabJournal {
		t.Fatalf("task pane tab = %s, want %s", model.taskPaneTab, taskPaneTabJournal)
	}

	journalPanel := model.renderChatPanel(56, 18, true)
	if !renderedPanelContains(journalPanel, "EXECUTION MARKER: async execution session.") {
		t.Fatalf("journal panel missing execution marker: %q", journalPanel)
	}

	model = applyImmediateCmdMessages(model, model.applyTaskPaneSelection(task, ScopeTask, taskPaneTabDiscussion, false))
	if model.ActiveChatSession() != discussionSessionID {
		t.Fatalf("active session after discussion switch = %q, want %q", model.ActiveChatSession(), discussionSessionID)
	}

	discussionPanel := model.renderChatPanel(56, 18, true)
	if !renderedPanelContains(discussionPanel, "DISCUSSION MARKER: sync task discussion session.") {
		t.Fatalf("discussion panel missing discussion marker: %q", discussionPanel)
	}
}

func TestJumpToSessionByNameKeepsTaskDetailInSyncEX254(t *testing.T) {
	t.Parallel()

	taskID := "task-254-sync"
	executionSessionID := "00000000-0000-0000-0000-000000002543"
	discussionSessionID := "00000000-0000-0000-0000-000000002544"
	nodeID := "session-" + discussionSessionID

	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadTaskDetail: func(_ context.Context, id string) (*TaskDetailItem, error) {
			return &TaskDetailItem{
				ID:                  id,
				Title:               "UX Task Detail Sandbox",
				SessionID:           executionSessionID,
				ActiveExecutionID:   executionSessionID,
				RecentExecutionID:   executionSessionID,
				DiscussionSessionID: discussionSessionID,
			}, nil
		},
		LoadChatHistory: func(_ context.Context, sessionID string) ([]ChatMessage, error) {
			switch sessionID {
			case executionSessionID:
				return []ChatMessage{{ID: "exec-sync", Role: "assistant", Content: "EXECUTION MARKER: async execution session.", Finalized: true}}, nil
			case discussionSessionID:
				return []ChatMessage{{ID: "discussion-sync", Role: "assistant", Content: "DISCUSSION MARKER 3: discussion session intentionally made newest.", Finalized: true}}, nil
			default:
				return nil, nil
			}
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 140, Height: 34})
	model.workspace.nodes[nodeID] = &sidebarNode{
		ID:           nodeID,
		Kind:         sidebarKindSession,
		SessionID:    discussionSessionID,
		SessionScope: "project_task",
		TaskID:       taskID,
		Label:        "Sandbox Discussion",
	}
	model.workspace.topLevel = append(model.workspace.topLevel, nodeID)

	model = applyImmediateCmdMessages(model, model.jumpToSessionByName("Sandbox Discussion"))

	if model.MainView() != ViewTask {
		t.Fatalf("main view = %s, want %s", model.MainView(), ViewTask)
	}
	if model.workspace.selectedTaskID != taskID {
		t.Fatalf("selectedTaskID = %q, want %q", model.workspace.selectedTaskID, taskID)
	}
	if model.ActiveChatSession() != discussionSessionID {
		t.Fatalf("active session = %q, want %q", model.ActiveChatSession(), discussionSessionID)
	}
	if model.taskPaneTab != taskPaneTabDiscussion {
		t.Fatalf("task pane tab = %s, want %s", model.taskPaneTab, taskPaneTabDiscussion)
	}

	panel := model.renderChatPanel(56, 18, true)
	if !renderedPanelContains(panel, "DISCUSSION MARKER 3: discussion session intentionally made newest.") {
		t.Fatalf("discussion panel missing synced session marker: %q", panel)
	}
}

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func renderedPanelContains(rendered string, want string) bool {
	plain := ansiEscapePattern.ReplaceAllString(rendered, "")
	plain = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			return r
		case unicode.IsSpace(r):
			return ' '
		case strings.ContainsRune(" .,:;!?-_/'\"", r):
			return r
		default:
			return ' '
		}
	}, plain)
	plain = strings.Join(strings.Fields(plain), " ")
	want = strings.Join(strings.Fields(want), " ")
	return strings.Contains(plain, want)
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

func TestTaskPaneScopeCycleKeepsContentAndTabsInSyncEX259(t *testing.T) {
	t.Parallel()

	const (
		orgSession        = "00000000-0000-0000-0000-000000002593"
		projectSession    = "00000000-0000-0000-0000-000000002594"
		executionSession  = "00000000-0000-0000-0000-000000002595"
		discussionSession = "00000000-0000-0000-0000-000000002596"
	)

	historyBySession := map[string][]ChatMessage{
		orgSession: {{
			ID:        "org-msg",
			Role:      "assistant",
			Content:   "ORG MARKER: org chat session.",
			Timestamp: time.Now().UTC(),
		}},
		projectSession: {{
			ID:        "project-msg",
			Role:      "assistant",
			Content:   "PROJECT MARKER: project-scoped chat session.",
			Timestamp: time.Now().UTC(),
		}},
		executionSession: {{
			ID:        "execution-msg",
			Role:      "assistant",
			Content:   "EXECUTION MARKER: task execution session.",
			Timestamp: time.Now().UTC(),
		}},
		discussionSession: {{
			ID:        "discussion-msg",
			Role:      "assistant",
			Content:   "DISCUSSION MARKER: task discussion session.",
			Timestamp: time.Now().UTC(),
		}},
	}

	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadChatHistory: func(_ context.Context, sessionID string) ([]ChatMessage, error) {
			return historyBySession[sessionID], nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 140, Height: 34})
	model.workspace.setMainView(ViewTask)
	model.workspace.selectedTaskID = "task-259-cycle"
	model.workspace.selectedProjectID = "project-stale"
	model.workspace.activeSessionID = orgSession
	model.activeScope = ScopeOrg
	model.activeSession = orgSession
	model.workspace.nodes["project-project-259"] = &sidebarNode{
		ID:        "project-project-259",
		Kind:      sidebarKindProject,
		Label:     "Project 259",
		ProjectID: "project-259",
	}
	model.workspace.nodes["session-project-259"] = &sidebarNode{
		ID:           "session-project-259",
		Kind:         sidebarKindSession,
		Label:        "Project 259 chat",
		SessionScope: "project",
		ProjectID:    "project-259",
		SessionID:    projectSession,
	}

	updated, cmd := model.Update(taskDetailLoadedMsg{Detail: TaskDetailItem{
		ID:                  "task-259-cycle",
		ProjectID:           "project-259",
		Title:               "Cycle right pane",
		SessionID:           executionSession,
		ActiveExecutionID:   executionSession,
		DiscussionSessionID: discussionSession,
	}})
	model = applyImmediateCmdMessages(updated.(Model), cmd)

	if model.activeScope != ScopeTask {
		t.Fatalf("initial task scope = %v, want %v", model.activeScope, ScopeTask)
	}
	if model.taskPaneTab != taskPaneTabJournal {
		t.Fatalf("initial task tab = %s, want journal", model.taskPaneTab)
	}
	if body := strings.Join(model.renderTaskSurface(70), "\n"); !strings.Contains(body, "EXECUTION MARKER") {
		t.Fatalf("task journal content should show execution marker: %q", body)
	}

	model.focus = ChatPanel
	model.taskPaneMode = taskPaneModeNavigate
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = applyImmediateCmdMessages(updated.(Model), cmd)
	if model.activeScope != ScopeProject {
		t.Fatalf("scope after first Up = %v, want %v", model.activeScope, ScopeProject)
	}
	if model.taskPaneTab != taskPaneTabDiscussion {
		t.Fatalf("tab after first Up = %s, want discussion", model.taskPaneTab)
	}
	if model.activeSession != projectSession {
		t.Fatalf("session after first Up = %q, want project session", model.activeSession)
	}
	projectPanel := model.renderChatPanel(56, 18, true)
	if strings.Contains(projectPanel, "[ Journal ]") || !strings.Contains(projectPanel, "(Journal)") {
		t.Fatalf("project scope tabs should disable Journal: %q", projectPanel)
	}
	if body := strings.Join(model.renderTaskSurface(70), "\n"); !strings.Contains(body, "PROJECT MARKER") {
		t.Fatalf("project scope content should show project marker: %q", body)
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = applyImmediateCmdMessages(updated.(Model), cmd)
	if model.activeScope != ScopeOrg {
		t.Fatalf("scope after second Up = %v, want %v", model.activeScope, ScopeOrg)
	}
	if model.activeSession != orgSession {
		t.Fatalf("session after second Up = %q, want org session", model.activeSession)
	}
	if body := strings.Join(model.renderTaskSurface(70), "\n"); !strings.Contains(body, "ORG MARKER") {
		t.Fatalf("org scope content should show org marker: %q", body)
	}
}

func TestTaskPaneProjectScopeWithoutSessionShowsExplicitStateEX259(t *testing.T) {
	t.Parallel()

	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadChatHistory: func(_ context.Context, _ string) ([]ChatMessage, error) {
			return nil, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 140, Height: 34})
	model.workspace.setMainView(ViewTask)
	model.workspace.selectedTaskID = "task-259-missing-project"
	model.workspace.activeSessionID = "00000000-0000-0000-0000-000000002597"
	model.activeSession = "00000000-0000-0000-0000-000000002597"

	updated, cmd := model.Update(taskDetailLoadedMsg{Detail: TaskDetailItem{
		ID:                  "task-259-missing-project",
		ProjectID:           "project-259-missing",
		Title:               "Missing project chat",
		SessionID:           "00000000-0000-0000-0000-000000002598",
		ActiveExecutionID:   "00000000-0000-0000-0000-000000002598",
		DiscussionSessionID: "00000000-0000-0000-0000-000000002599",
	}})
	model = applyImmediateCmdMessages(updated.(Model), cmd)
	model.focus = ChatPanel
	model.taskPaneMode = taskPaneModeNavigate

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = applyImmediateCmdMessages(updated.(Model), cmd)

	if model.activeScope != ScopeProject {
		t.Fatalf("scope after Up = %v, want %v", model.activeScope, ScopeProject)
	}
	if model.activeSession != "" {
		t.Fatalf("project scope activeSession without session = %q, want empty", model.activeSession)
	}
	body := strings.Join(model.renderTaskSurface(70), "\n")
	if !strings.Contains(body, "No project discussion session.") {
		t.Fatalf("missing project session should render explicit placeholder: %q", body)
	}
	if strings.Contains(body, "no messages yet") {
		t.Fatalf("missing project session should not reuse generic empty-chat copy: %q", body)
	}
}

func applyImmediateCmdMessages(model Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return model
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if msg == nil {
			return model
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, child := range batch {
				model = applyImmediateCmdMessages(model, child)
			}
			return model
		}
		updated, next := model.Update(msg)
		return applyImmediateCmdMessages(updated.(Model), next)
	case <-time.After(50 * time.Millisecond):
		return model
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

func TestTaskPaneEnterCancelCommandDoesNotPostLiteralMessageEX260(t *testing.T) {
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
	model.workspace.selectedTaskID = "task-260-stream"
	model.workspace.tasks["task-260-stream"] = &taskRecord{
		ID:                  "task-260-stream",
		Title:               "Cancel via composer",
		Status:              "in_progress",
		SessionID:           "00000000-0000-0000-0000-000000002602",
		ActiveExecutionID:   "00000000-0000-0000-0000-000000002602",
		DiscussionSessionID: "00000000-0000-0000-0000-000000002603",
	}
	model.focus = ChatPanel
	model.activeScope = ScopeTask
	model.activeSession = "00000000-0000-0000-0000-000000002603"
	model.taskPaneTab = taskPaneTabDiscussion
	model.taskPaneTaskID = "task-260-stream"
	model.taskPaneMode = taskPaneModeCompose
	model.turnsSynced = true
	model.activeTurn = true
	model.chatInput = ":cancel"
	model.chatMessages = []ChatMessage{{
		ID:        "existing-msg",
		Role:      "assistant",
		Content:   "DISCUSSION MARKER: task discussion session.",
		Timestamp: time.Now().UTC(),
	}}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("Enter on :cancel should dispatch a cancel request")
	}
	msg := cmd()
	var request chatCancelRequestedMsg
	switch typed := msg.(type) {
	case chatCancelRequestedMsg:
		request = typed
	case tea.BatchMsg:
		found := false
		for _, child := range typed {
			done := make(chan tea.Msg, 1)
			go func(c tea.Cmd) { done <- c() }(child)
			select {
			case childMsg := <-done:
				if cancelMsg, ok := childMsg.(chatCancelRequestedMsg); ok {
					request = cancelMsg
					found = true
				}
			case <-time.After(50 * time.Millisecond):
			}
			if found {
				break
			}
		}
		if !found {
			t.Fatalf("Enter on :cancel should emit chatCancelRequestedMsg inside batch, got %T", msg)
		}
	default:
		t.Fatalf("Enter on :cancel should emit chatCancelRequestedMsg, got %T", msg)
	}
	_ = cancelChatTurnCmd(request, model.runtimeHints.CancelChatTurn)()

	if cancelCalls != 1 {
		t.Fatalf(":cancel should call cancel once; got %d", cancelCalls)
	}
	if model.activeTurn {
		t.Fatal(":cancel should stop the active turn")
	}
	if model.chatInput != "" {
		t.Fatalf(":cancel should clear the composer, got %q", model.chatInput)
	}
	if len(model.chatMessages) != 1 {
		t.Fatalf(":cancel should not append a literal user message; got %d messages", len(model.chatMessages))
	}
	if strings.Contains(model.chatMessages[0].Content, ":cancel") {
		t.Fatalf("existing transcript should not be replaced by literal :cancel content: %+v", model.chatMessages)
	}
}

func TestTaskPanePointerRefocusActivatesComposerEX252(t *testing.T) {
	t.Parallel()

	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 140, Height: 34})
	model.workspace.setMainView(ViewTask)
	model.workspace.selectedTaskID = "task-252-pointer"
	model.workspace.tasks["task-252-pointer"] = &taskRecord{
		ID:                  "task-252-pointer",
		Title:               "Pointer composer",
		Status:              "in_progress",
		SessionID:           "00000000-0000-0000-0000-000000002526",
		ActiveExecutionID:   "00000000-0000-0000-0000-000000002526",
		DiscussionSessionID: "00000000-0000-0000-0000-000000002527",
	}
	model.focus = ChatPanel
	model.activeScope = ScopeTask
	model.activeSession = "00000000-0000-0000-0000-000000002526"
	model.taskPaneTab = taskPaneTabJournal
	model.taskPaneTaskID = "task-252-pointer"
	model.chatInput = "draft"

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEsc})
	if model.taskPaneMode != taskPaneModeNavigate {
		t.Fatalf("task pane mode after Esc = %s, want navigate", model.taskPaneMode)
	}

	clickX, clickY := chatPanelBodyClickPosition(model)
	model = pressMsg(model, tea.MouseMsg{
		X:      clickX,
		Y:      clickY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	if model.taskPaneMode != taskPaneModeCompose {
		t.Fatalf("task pane mode after pointer refocus = %s, want compose", model.taskPaneMode)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	if model.chatInput != "draft!" {
		t.Fatalf("chat input after pointer refocus typing = %q, want draft!", model.chatInput)
	}
}

func TestTaskPanePointerTabsSwitchContentAndRoutingEX252(t *testing.T) {
	t.Parallel()

	executionSession := "00000000-0000-0000-0000-000000002528"
	discussionSession := "00000000-0000-0000-0000-000000002529"
	traceResult := strings.Repeat("trace-", 90) + "TRACE-END"
	toolCall := ToolCallStatus{
		ID:     "call-252-tabs",
		Name:   "builder.trace",
		Status: "success",
		Result: traceResult,
	}

	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 140, Height: 34})
	model.workspace.setMainView(ViewTask)
	model.workspace.selectedTaskID = "task-252-routing"
	model.workspace.tasks["task-252-routing"] = &taskRecord{
		ID:                  "task-252-routing",
		Title:               "Pointer tabs",
		Status:              "in_progress",
		SessionID:           executionSession,
		ActiveExecutionID:   executionSession,
		DiscussionSessionID: discussionSession,
		Events: []TaskEvent{{
			EventType: "task.created",
			ActorType: "system",
			CreatedAt: time.Now().UTC(),
		}},
	}
	model.focus = ChatPanel
	model.activeScope = ScopeTask
	model.taskPaneTab = taskPaneTabJournal
	model.taskPaneTaskID = "task-252-routing"

	executionMessages := []ChatMessage{{
		ID:        "msg-252-execution",
		Role:      "assistant",
		Content:   "Execution journal entry",
		Timestamp: time.Now().UTC(),
		ToolCalls: []ToolCallStatus{toolCall},
	}}
	discussionMessages := []ChatMessage{{
		ID:        "msg-252-discussion",
		Role:      "user",
		Content:   "Discussion thread update",
		Timestamp: time.Now().UTC(),
	}}
	expanded := map[string]map[string]bool{
		"msg-252-execution": {
			toolCallIdentity(toolCall, 0): true,
		},
	}
	cacheChatSession(&model, executionSession, executionMessages, expanded)
	cacheChatSession(&model, discussionSession, discussionMessages, nil)
	model.activeSession = executionSession
	model.restoreSessionState(executionSession)

	clickX, clickY := taskPaneTabClickPosition(t, model, taskPaneTabEvents)
	model = pressMsg(model, tea.MouseMsg{
		X:      clickX,
		Y:      clickY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	if model.taskPaneTab != taskPaneTabEvents {
		t.Fatalf("events tab = %s, want events", model.taskPaneTab)
	}
	if model.activeSession != executionSession {
		t.Fatalf("events session = %q, want execution session", model.activeSession)
	}
	if model.workspace.selectedTaskID != "task-252-routing" {
		t.Fatalf("selected task changed after events click: %q", model.workspace.selectedTaskID)
	}
	if rendered := strings.Join(model.renderTaskSurface(80), "\n"); !strings.Contains(rendered, "Task created") {
		t.Fatalf("events surface missing event content: %q", rendered)
	}

	clickX, clickY = taskPaneTabClickPosition(t, model, taskPaneTabDiscussion)
	model = pressMsg(model, tea.MouseMsg{
		X:      clickX,
		Y:      clickY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	if model.taskPaneTab != taskPaneTabDiscussion {
		t.Fatalf("discussion tab = %s, want discussion", model.taskPaneTab)
	}
	if model.activeSession != discussionSession {
		t.Fatalf("discussion session = %q, want discussion session", model.activeSession)
	}
	if rendered := strings.Join(model.renderTaskSurface(80), "\n"); !strings.Contains(rendered, "Discussion thread update") {
		t.Fatalf("discussion surface missing discussion content: %q", rendered)
	}

	clickX, clickY = taskPaneTabClickPosition(t, model, taskPaneTabTrace)
	model = pressMsg(model, tea.MouseMsg{
		X:      clickX,
		Y:      clickY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	if model.taskPaneTab != taskPaneTabTrace {
		t.Fatalf("trace tab = %s, want trace", model.taskPaneTab)
	}
	if model.activeSession != executionSession {
		t.Fatalf("trace session = %q, want execution session", model.activeSession)
	}
	if rendered := strings.Join(model.renderTaskSurface(80), "\n"); !strings.Contains(rendered, "TRACE-END") {
		t.Fatalf("trace surface missing full trace content: %q", rendered)
	}
}

func TestTaskTraceShowsFullToolResultDetailEX252(t *testing.T) {
	t.Parallel()

	result := strings.Repeat("trace-", 90) + "TRACE-END"
	toolCall := ToolCallStatus{
		ID:     "call-252-trace",
		Name:   "builder.trace",
		Status: "success",
		Result: result,
	}

	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 140, Height: 34})
	model.workspace.setMainView(ViewTask)
	model.workspace.selectedTaskID = "task-252-trace"
	model.workspace.tasks["task-252-trace"] = &taskRecord{
		ID:                  "task-252-trace",
		Title:               "Trace detail",
		Status:              "in_progress",
		SessionID:           "00000000-0000-0000-0000-000000002530",
		ActiveExecutionID:   "00000000-0000-0000-0000-000000002530",
		DiscussionSessionID: "00000000-0000-0000-0000-000000002531",
	}
	model.focus = ChatPanel
	model.activeScope = ScopeTask
	model.activeSession = "00000000-0000-0000-0000-000000002530"
	model.taskPaneTab = taskPaneTabTrace
	model.taskPaneTaskID = "task-252-trace"
	model.chatMessages = []ChatMessage{{
		ID:        "msg-252-trace",
		Role:      "assistant",
		Timestamp: time.Now().UTC(),
		ToolCalls: []ToolCallStatus{toolCall},
	}}
	model.chatMessageIndex = chatMessageIndex(model.chatMessages)
	model.setToolCallExpanded("msg-252-trace", toolCallIdentity(toolCall, 0), true)

	rendered := strings.Join(model.renderTaskSurface(80), "\n")
	if !strings.Contains(rendered, "TRACE-END") {
		t.Fatalf("trace surface should expose the full tool result tail: %q", rendered)
	}
	if strings.Contains(rendered, "400 of") {
		t.Fatalf("trace surface should not use the preview truncation notice: %q", rendered)
	}
}
