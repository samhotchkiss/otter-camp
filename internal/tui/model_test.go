package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestGlobalFocusControls(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyTab})
	if model.FocusedPanel() != ChatPanel {
		t.Fatalf("focus after Tab = %v, want chat", model.FocusedPanel())
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyShiftTab})
	if model.FocusedPanel() != MainPanel {
		t.Fatalf("focus after Shift-Tab = %v, want main", model.FocusedPanel())
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if model.FocusedPanel() != SidebarPanel {
		t.Fatalf("focus after 1 = %v, want sidebar", model.FocusedPanel())
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}, Alt: true})
	if model.FocusedPanel() != MainPanel {
		t.Fatalf("focus after Alt-2 = %v, want main", model.FocusedPanel())
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if model.FocusedPanel() != ChatPanel {
		t.Fatalf("focus after 3 = %v, want chat", model.FocusedPanel())
	}
}

func TestFocusCommandFallback(t *testing.T) {
	model := NewModel(DefaultState())

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	for _, r := range []rune("focus sidebar") {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})

	if model.FocusedPanel() != SidebarPanel {
		t.Fatalf("focus after :focus sidebar = %v, want sidebar", model.FocusedPanel())
	}
}

func TestSpaceKeyWorksInChatInput(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeySpace})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})

	if got := model.ChatInput(); got != "h i" {
		t.Fatalf("chat input after space key = %q, want %q", got, "h i")
	}
}

func TestChatInputAcceptsMultiRuneEvents(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello from dictation")})

	if got := model.ChatInput(); got != "hello from dictation" {
		t.Fatalf("chat input after multi-rune event = %q, want %q", got, "hello from dictation")
	}
}

func TestChatScrollHotkeys(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	for i := 0; i < 40; i++ {
		model.appendMessage("seed", "assistant", "line line line line line", true)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyPgUp})
	if model.chatScrollOffset <= 0 {
		t.Fatalf("chat scroll offset after PgUp = %d, want > 0", model.chatScrollOffset)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyPgDown})
	if model.chatScrollOffset != 0 {
		t.Fatalf("chat scroll offset after PgDown = %d, want 0", model.chatScrollOffset)
	}
}

func TestSendResetsChatScrollOffset(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = ChatPanel
	model.chatScrollOffset = 12
	model.chatInput = "hello"

	model.sendOrQueueInput()

	if model.chatScrollOffset != 0 {
		t.Fatalf("chat scroll offset after send = %d, want 0", model.chatScrollOffset)
	}
}

func TestSpaceKeyWorksInCommandMode(t *testing.T) {
	model := NewModel(DefaultState())

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	for _, r := range []rune("focus") {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeySpace})
	for _, r := range []rune("chat") {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})

	if got := model.FocusedPanel(); got != ChatPanel {
		t.Fatalf("focus after :focus chat with KeySpace = %v, want chat", got)
	}
}

func TestQuitCommandAndCtrlC(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	for _, r := range []rune("quit") {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	if !model.Quitting() {
		t.Fatal("model should be quitting after :quit")
	}

	another := NewModel(DefaultState())
	another = pressKey(another, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !another.Quitting() {
		t.Fatal("model should be quitting after Ctrl-C")
	}
}

func TestFrankJumpControlsPreserveMainView(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.setMainView(ViewInbox)
	model.workspace.activeSessionID = "session-task-2"
	model.activeSession = "session-task-2"

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'0'}})
	if got := model.ActiveChatSession(); got != "session-org-general" {
		t.Fatalf("session after 0 jump = %q, want session-org-general", got)
	}
	if got := model.MainView(); got != ViewInbox {
		t.Fatalf("main view after 0 jump = %s, want %s", got, ViewInbox)
	}

	model.workspace.activeSessionID = "session-task-1"
	model.activeSession = "session-task-1"
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyCtrlG})
	if got := model.ActiveChatSession(); got != "session-org-general" {
		t.Fatalf("session after Ctrl-G jump = %q, want session-org-general", got)
	}
	if got := model.MainView(); got != ViewInbox {
		t.Fatalf("main view after Ctrl-G jump = %s, want %s", got, ViewInbox)
	}
}

func TestZeroFrankFallbackGuardWhenChatInputIsActive(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.activeSessionID = "session-task-2"
	model.activeSession = "session-task-2"
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'0'}})
	if got := model.ChatInput(); got != "0" {
		t.Fatalf("chat input after 0 in chat focus = %q, want 0", got)
	}
	if got := model.ActiveChatSession(); got != "session-task-2" {
		t.Fatalf("session after 0 in chat focus = %q, want session-task-2", got)
	}
}

func TestFrankCommandAlias(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.activeSessionID = "session-task-1"
	model.activeSession = "session-task-1"

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	for _, r := range []rune("frank") {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	if got := model.ActiveChatSession(); got != "session-org-general" {
		t.Fatalf("session after :frank = %q, want session-org-general", got)
	}
}

func TestResizeKeepsFocusValid(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 90, Height: 30})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if model.FocusedPanel() != SidebarPanel {
		t.Fatalf("focus after selecting sidebar = %v, want sidebar", model.FocusedPanel())
	}

	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	if model.SizeClass() != SizeM {
		t.Fatalf("size class after resize = %s, want %s", model.SizeClass(), SizeM)
	}
	layout := model.CurrentLayout()
	if !layout.visible[model.FocusedPanel()] {
		t.Fatalf("focused panel %s is hidden in layout %s", panelLabel(model.FocusedPanel()), model.SizeClass())
	}
}

func TestMainAndChatReachableInTwoActions(t *testing.T) {
	scenarios := []tea.WindowSizeMsg{
		{Width: 69, Height: 22},
		{Width: 90, Height: 30},
		{Width: 120, Height: 30},
		{Width: 160, Height: 34},
		{Width: 220, Height: 40},
	}
	for _, size := range scenarios {
		for _, start := range []rune{'1', '2', '3'} {
			model := NewModel(DefaultState())
			model = pressMsg(model, size)
			model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{start}})

			toMain := pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
			if toMain.FocusedPanel() != MainPanel {
				t.Fatalf("size=%dx%d start=%q did not reach main in one action", size.Width, size.Height, start)
			}

			toChat := pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
			if toChat.FocusedPanel() != ChatPanel {
				t.Fatalf("size=%dx%d start=%q did not reach chat in one action", size.Width, size.Height, start)
			}
		}
	}
}

func TestInitialStatusIncludesFrankHintOnFreshState(t *testing.T) {
	model := NewModel(DefaultState())
	if !strings.Contains(model.StatusMessage(), "Ctrl-G") {
		t.Fatalf("status message missing Frank jump hint: %q", model.StatusMessage())
	}
}

func TestFrankJumpCtrlGPreservesMainViewAndHighlightsGeneral(t *testing.T) {
	model := moveToTaskSession(NewModel(DefaultState()))
	if got := model.MainView(); got != ViewTask {
		t.Fatalf("main view before Ctrl-G = %s, want %s", got, ViewTask)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyCtrlG})
	if got := model.WorkspaceSession(); got != generalSessionID {
		t.Fatalf("workspace session after Ctrl-G = %q, want %q", got, generalSessionID)
	}
	if got := model.MainView(); got != ViewTask {
		t.Fatalf("main view changed on Ctrl-G = %s, want %s", got, ViewTask)
	}
	entries := model.SidebarVisibleEntries()
	if len(entries) == 0 || !strings.HasPrefix(entries[0], "> General / Frank") {
		t.Fatalf("first sidebar entry = %v, want active General / Frank", entries)
	}
}

func TestFrankJumpZeroFallbackOutsideChatInput(t *testing.T) {
	model := moveToTaskSession(NewModel(DefaultState()))
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'0'}})

	if got := model.WorkspaceSession(); got != generalSessionID {
		t.Fatalf("workspace session after 0 fallback = %q, want %q", got, generalSessionID)
	}
}

func TestFrankJumpZeroGuardWhenChatInputActive(t *testing.T) {
	model := moveToTaskSession(NewModel(DefaultState()))
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	sessionBefore := model.WorkspaceSession()

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'0'}})

	if got := model.WorkspaceSession(); got != sessionBefore {
		t.Fatalf("workspace session changed in chat input mode = %q, want %q", got, sessionBefore)
	}
	if got := model.ChatInput(); got != "0" {
		t.Fatalf("chat input after typing 0 in chat panel = %q, want 0", got)
	}
}

func TestFrankCommandAliasMatchesGeneral(t *testing.T) {
	base := moveToTaskSession(NewModel(DefaultState()))

	viaFrank := runCommand(base, "frank")
	viaGeneral := runCommand(base, "general")

	if got := viaFrank.WorkspaceSession(); got != generalSessionID {
		t.Fatalf("workspace session after :frank = %q, want %q", got, generalSessionID)
	}
	if got := viaGeneral.WorkspaceSession(); got != generalSessionID {
		t.Fatalf("workspace session after :general = %q, want %q", got, generalSessionID)
	}
	if viaFrank.MainView() != viaGeneral.MainView() {
		t.Fatalf("main view mismatch :frank=%s :general=%s", viaFrank.MainView(), viaGeneral.MainView())
	}
}

func TestTmuxHelpLineUsesFallbackCommandHints(t *testing.T) {
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{ModifierReliabilityUncertain: true})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	view := model.View()
	// Context-aware help: should show relevant keybindings including : commands
	if !containsAll(view, []string{"Help:", ": commands"}) {
		t.Fatalf("view missing help text: %q", view)
	}
}

func TestForwardHistoryNavigation(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}}) // chat focus

	// Type and send two messages to populate history (direct pointer call since ChatPanel
	// suppresses actual send without a SendChatMessage hook)
	for _, r := range []rune("first message") {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model.sendOrQueueInput() // adds "first message" to chatHistory

	for _, r := range []rune("second message") {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model.sendOrQueueInput() // adds "second message" to chatHistory

	// Up recall works from empty input: gets last sent message
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyUp})
	if got := model.ChatInput(); got != "second message" {
		t.Fatalf("after Up press, chat input = %q, want %q", got, "second message")
	}

	// Down advances history forward, clearing input (since historyIndex == len-1 → goes past end)
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyDown})
	if got := model.ChatInput(); got != "" {
		t.Fatalf("after Down press from last history entry, chat input = %q, want empty", got)
	}

	// Down from empty (not in history mode) does nothing to input
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyDown})
	if got := model.ChatInput(); got != "" {
		t.Fatalf("after Down press from empty non-history input, chat input = %q, want empty", got)
	}
}

func TestScopeCycleShortcutsTraverseAllScopeLevels(t *testing.T) {
	model := NewModel(DefaultState())
	if got := model.ChatScope(); got != ScopeOrg {
		t.Fatalf("initial scope = %s, want %s", got, ScopeOrg)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if got := model.ChatScope(); got != ScopeProject {
		t.Fatalf("scope after ] = %s, want %s", got, ScopeProject)
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if got := model.ChatScope(); got != ScopeTask {
		t.Fatalf("scope after second ] = %s, want %s", got, ScopeTask)
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if got := model.ChatScope(); got != ScopeOrg {
		t.Fatalf("scope after third ] = %s, want %s", got, ScopeOrg)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if got := model.ChatScope(); got != ScopeTask {
		t.Fatalf("scope after [ from org = %s, want %s", got, ScopeTask)
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if got := model.ChatScope(); got != ScopeProject {
		t.Fatalf("scope after second [ = %s, want %s", got, ScopeProject)
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if got := model.ChatScope(); got != ScopeOrg {
		t.Fatalf("scope after third [ = %s, want %s", got, ScopeOrg)
	}
}

func TestQuitKeyClosesHelpView(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	// Open help
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if got := model.MainView(); got != ViewHelp {
		t.Fatalf("main view after ? = %s, want %s", got, ViewHelp)
	}

	// 'q' should close help from main panel
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}) // focus main
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if got := model.MainView(); got != ViewDashboard {
		t.Fatalf("main view after q = %s, want %s", got, ViewDashboard)
	}
}

func TestStatusBarShowsHumanReadableSession(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	view := model.View()
	// Status bar should show "Frank" (default) not "none" or raw session ID
	if strings.Contains(view, "· none ·") {
		t.Fatalf("status bar shows 'none' instead of human-readable session label: %q", view)
	}
	if strings.Contains(view, "· session-") {
		t.Fatalf("status bar shows raw session ID: %q", view)
	}
	if !strings.Contains(view, "Frank") {
		t.Fatalf("status bar missing 'Frank' session label: %q", view)
	}
}

func TestMainViewTitleIsHumanReadable(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	// DASHBOARD stays the same
	if got := model.MainView(); got != ViewDashboard {
		t.Fatalf("initial main view = %s, want dashboard", got)
	}

	// Navigate to task view — title should be "TASK DETAIL"
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.workspace.setMainView(ViewTask)
	view := model.View()
	if !strings.Contains(view, "TASK DETAIL") {
		t.Fatalf("task view title missing 'TASK DETAIL' in: %q", view)
	}
}

func TestTaskDetailViewShowsExtendedFieldsAndFullEventLog(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.setMainView(ViewTask)
	model.workspace.selectedTaskID = "task-1"
	model.workspace.tasks["task-1"].History = []string{
		"created",
		"owner=frank",
		"priority=high",
		"scope=project-alpha",
		"queued review",
		"awaiting operator approval",
	}

	view := strings.Join(model.renderTaskView(120, 40), "\n")
	for _, want := range []string{
		"Description",
		"Acceptance Criteria",
		"Subtasks",
		"Async Session",
		"session-task-1",
		"created",
		"awaiting operator approval",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("task detail missing %q: %q", want, view)
		}
	}
}

func TestEnterOnTaskDetailOpensAsyncSession(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = MainPanel
	model.workspace.setMainView(ViewTask)
	model.workspace.selectedTaskID = "task-2"
	model.workspace.activeSessionID = generalSessionID
	model.activeSession = generalSessionID

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})

	if got := model.WorkspaceSession(); got != "session-task-2" {
		t.Fatalf("workspace session after Enter on task detail = %q, want %q", got, "session-task-2")
	}
	if got := model.ActiveChatSession(); got != "session-task-2" {
		t.Fatalf("active chat session after Enter on task detail = %q, want %q", got, "session-task-2")
	}
	if got := model.State().LastActiveChatSession; got != "session-task-2" {
		t.Fatalf("persisted chat session after Enter on task detail = %q, want %q", got, "session-task-2")
	}
	if got := model.FocusedPanel(); got != ChatPanel {
		t.Fatalf("focus after Enter on task detail = %s, want chat", panelLabel(got))
	}
}

func TestFormatTaskStatus(t *testing.T) {
	cases := []struct{ in, want string }{
		{"todo", "Todo"},
		{"in_progress", "In Progress"},
		{"done", "Done"},
		{"approved", "Approved"},
		{"blocked", "Blocked"},
		{"rejected", "Rejected"},
		{"custom_status", "Custom Status"},
	}
	for _, tc := range cases {
		if got := formatTaskStatus(tc.in); got != tc.want {
			t.Errorf("formatTaskStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func containsAll(raw string, wants []string) bool {
	for _, want := range wants {
		if !strings.Contains(raw, want) {
			return false
		}
	}
	return true
}

func pressKey(model Model, key tea.KeyMsg) Model {
	updated, _ := model.Update(key)
	next, ok := updated.(Model)
	if !ok {
		panic("unexpected model type")
	}
	return next
}

func moveToTaskSession(model Model) Model {
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	return model
}

func runCommand(model Model, command string) Model {
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	for _, r := range []rune(command) {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	return model
}

func pressMsg(model Model, msg tea.Msg) Model {
	updated, _ := model.Update(msg)
	next, ok := updated.(Model)
	if !ok {
		panic("unexpected model type")
	}
	return next
}
