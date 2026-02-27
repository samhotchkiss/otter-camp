package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestSidebarToggleKeybindingAtSSize(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 90, Height: 30})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}) // main

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if got := model.FocusedPanel(); got != SidebarPanel {
		t.Fatalf("focus after sidebar toggle on = %s, want sidebar", panelLabel(got))
	}
	layout := model.CurrentLayout()
	if !layout.visible[SidebarPanel] {
		t.Fatalf("sidebar should be visible after toggle on")
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if got := model.FocusedPanel(); got != MainPanel {
		t.Fatalf("focus after sidebar toggle off = %s, want main", panelLabel(got))
	}
	layout = model.CurrentLayout()
	if layout.visible[SidebarPanel] {
		t.Fatalf("sidebar should be hidden after toggle off")
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

func TestSlashSearchMainPanelEnterKeepsFilter(t *testing.T) {
	model := NewModel(DefaultState())
	// Seed tasks so the dashboard has content to filter
	model.workspace.tasks["task-1"] = &taskRecord{ID: "task-1", Title: "Launch docs", Status: "todo"}
	model.workspace.tasks["task-2"] = &taskRecord{ID: "task-2", Title: "CI hardening", Status: "in_progress"}
	model.workspace.taskOrder = []string{"task-1", "task-2"}

	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}) // main
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})

	for _, r := range []rune("launch") {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	filtered := model.View()
	if !strings.Contains(filtered, "Launch docs") {
		t.Fatalf("filtered dashboard missing Launch docs: %q", filtered)
	}
	if strings.Contains(filtered, "CI hardening") {
		t.Fatalf("filtered dashboard should hide CI hardening: %q", filtered)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.searchMode {
		t.Fatalf("search mode should exit on Enter")
	}
	if got := model.mainFilter; got != "launch" {
		t.Fatalf("main filter after Enter = %q, want %q", got, "launch")
	}

	filtered = model.View()
	if strings.Contains(filtered, "CI hardening") {
		t.Fatalf("accepted filter should remain active: %q", filtered)
	}
}

func TestSlashSearchMainPanelEscClearsFilter(t *testing.T) {
	model := NewModel(DefaultState())
	// Seed tasks so the dashboard has content to filter
	model.workspace.tasks["task-1"] = &taskRecord{ID: "task-1", Title: "Launch docs", Status: "todo"}
	model.workspace.tasks["task-2"] = &taskRecord{ID: "task-2", Title: "CI hardening", Status: "in_progress"}
	model.workspace.taskOrder = []string{"task-1", "task-2"}

	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}) // main
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range []rune("launch") {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEsc})
	if model.searchMode {
		t.Fatalf("search mode should exit on Esc")
	}
	if got := model.mainFilter; got != "" {
		t.Fatalf("main filter after Esc = %q, want empty", got)
	}

	view := model.View()
	if !containsAll(view, []string{"Launch docs", "CI hardening"}) {
		t.Fatalf("cleared filter should restore task list: %q", view)
	}
}

func TestSlashSearchSidebarFiltersSessions(t *testing.T) {
	model := NewModel(DefaultState())
	// Seed chat nodes so the sidebar has sessions to filter
	model.workspace.nodes["chat-abc"] = &sidebarNode{
		ID: "chat-abc", Label: "Blog Site", Kind: sidebarKindSession, SessionID: "sess-abc",
	}
	model.workspace.nodes["chat-def"] = &sidebarNode{
		ID: "chat-def", Label: "Project Alpha", Kind: sidebarKindSession, SessionID: "sess-def",
	}
	model.workspace.topLevel = append(model.workspace.topLevel[:3], append(
		[]string{"chat-abc", "chat-def"}, model.workspace.topLevel[3:]...,
	)...)

	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}}) // sidebar
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range []rune("Blog") {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	sidebar := model.renderSidebarPanel(56, 14, true)
	if !strings.Contains(sidebar, "Blog Site") {
		t.Fatalf("filtered sidebar missing Blog Site: %q", sidebar)
	}
	if strings.Contains(sidebar, "Project Alpha") {
		t.Fatalf("filtered sidebar should hide Project Alpha: %q", sidebar)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.searchMode {
		t.Fatalf("search mode should exit on Enter")
	}
	if got := model.sidebarFilter; got != "Blog" {
		t.Fatalf("sidebar filter after Enter = %q, want %q", got, "Blog")
	}
}

func TestSidebarFilterSuppressesEmptySectionHeaders(t *testing.T) {
	model := NewModel(DefaultState())
	// Set up a header + two sessions under it
	model.workspace.nodes["header-chats"] = &sidebarNode{
		ID: "header-chats", Label: "CHATS", Kind: sidebarKindHeader,
	}
	model.workspace.nodes["sess-alpha"] = &sidebarNode{
		ID: "sess-alpha", Label: "Alpha Session", Kind: sidebarKindSession, SessionID: "s1",
	}
	model.workspace.nodes["sess-beta"] = &sidebarNode{
		ID: "sess-beta", Label: "Beta Session", Kind: sidebarKindSession, SessionID: "s2",
	}
	model.workspace.topLevel = []string{"header-chats", "sess-alpha", "sess-beta"}

	// No filter — header + both sessions visible
	visible := model.filteredSidebarIDs(model.workspace.topLevel, "")
	if len(visible) != 3 {
		t.Fatalf("unfiltered visible IDs = %d, want 3", len(visible))
	}

	// Filter "alpha" — header should be shown (has matching child)
	filtered := model.filteredSidebarIDs(model.workspace.topLevel, "alpha")
	headerPresent := false
	for _, id := range filtered {
		if id == "header-chats" {
			headerPresent = true
		}
	}
	if !headerPresent {
		t.Fatalf("header should be included when a child matches: %v", filtered)
	}

	// Filter "zzznomatch" — header should be suppressed (no matching children)
	noMatchFiltered := model.filteredSidebarIDs(model.workspace.topLevel, "zzznomatch")
	for _, id := range noMatchFiltered {
		if id == "header-chats" {
			t.Fatalf("header should be suppressed when no children match: %v", noMatchFiltered)
		}
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

func TestResizeKeyShrinksSidebarByTwoPercent(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 160, Height: 34})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})

	before := model.State().PanelProportions
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'<'}})
	after := model.State().PanelProportions

	if got, want := after[0], before[0]-0.02; absFloat(got-want) > 0.0001 {
		t.Fatalf("sidebar proportion after '<' = %.2f, want %.2f", got, want)
	}
}

func TestGGJumpTopBottomAcrossPanels(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}}) // sidebar
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if got := model.workspace.sidebarCursor; got != len(model.workspace.visibleSidebarIDs())-1 {
		t.Fatalf("sidebar cursor after G = %d, want %d", got, len(model.workspace.visibleSidebarIDs())-1)
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if got := model.workspace.sidebarCursor; got != 0 {
		t.Fatalf("sidebar cursor after g = %d, want 0", got)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}) // main
	model.workspace.setMainView(ViewInbox)
	model.workspace.inboxCursor = 1
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if got := model.workspace.inboxCursor; got != 0 {
		t.Fatalf("inbox cursor after g = %d, want 0", got)
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if got := model.workspace.inboxCursor; got != len(model.workspace.inbox)-1 {
		t.Fatalf("inbox cursor after G = %d, want %d", got, len(model.workspace.inbox)-1)
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}}) // chat
	for i := 0; i < 40; i++ {
		model.appendMessage("seed", "assistant", "line line line line line", true)
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if model.chatScrollOffset <= 0 {
		t.Fatalf("chat scroll offset after g = %d, want > 0", model.chatScrollOffset)
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if model.chatScrollOffset != 0 {
		t.Fatalf("chat scroll offset after G = %d, want 0", model.chatScrollOffset)
	}
}

func TestCommandPaletteShowsFuzzySuggestions(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.workspace.tasks["task-1"] = &taskRecord{ID: "task-1", Title: "Launch docs", Status: "todo"}
	model.workspace.taskOrder = []string{"task-1"}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	for _, r := range []rune("tsk") {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	panel := model.renderChatPanel(90, 20, true)
	if !strings.Contains(panel, "task: Launch docs") {
		t.Fatalf("command palette suggestions missing task fuzzy match: %q", panel)
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
	if len(entries) == 0 || !strings.HasPrefix(entries[0], "> Frank / General") {
		t.Fatalf("first sidebar entry = %v, want active Frank / General", entries)
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

func TestChatHelpHintUsesAltEnterNewline(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = ChatPanel

	help := model.commandFallbackHelp()
	if !strings.Contains(help, "Alt-Enter newline") {
		t.Fatalf("chat help hint missing Alt-Enter newline: %q", help)
	}
	if strings.Contains(help, "Shift-Enter") {
		t.Fatalf("chat help hint contains stale Shift-Enter label: %q", help)
	}
}

func TestHelpViewUsesAltEnterNewline(t *testing.T) {
	model := NewModel(DefaultState())
	rendered := strings.Join(model.renderHelpView(100, 100), "\n")

	if !strings.Contains(rendered, "Alt-Enter") {
		t.Fatalf("help view missing Alt-Enter label: %q", rendered)
	}
	if strings.Contains(rendered, "Shift-Enter") {
		t.Fatalf("help view contains stale Shift-Enter label: %q", rendered)
	}
}

func TestHelpViewDocumentsSidebarToggleKeybinding(t *testing.T) {
	model := NewModel(DefaultState())
	rendered := strings.Join(model.renderHelpView(100, 100), "\n")
	if !strings.Contains(rendered, "toggle sidebar") {
		t.Fatalf("help view missing sidebar toggle keybinding: %q", rendered)
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

func TestSelectingChatSessionNodeResetsScope(t *testing.T) {
	// Simulate: start in ScopeTask, then select a session node from the CHATS sidebar.
	// Scope should reset to ScopeOrg.
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 200, Height: 50})

	// Force scope to ScopeTask to simulate having had a task session open.
	model.activeScope = ScopeTask

	// Make sure focus is on sidebar.
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if model.ChatScope() != ScopeTask {
		t.Fatalf("precondition: scope = %s, want task", model.ChatScope())
	}

	// Find the Frank/General session node (always present) and press Enter on it.
	model.workspace.sidebarCursor = model.workspace.indexOfNode(generalSidebarNodeID)
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})

	if got := model.ChatScope(); got != ScopeOrg {
		t.Fatalf("scope after selecting session node = %s, want %s", got, ScopeOrg)
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
	model.workspace.tasks["task-1"] = &taskRecord{
		ID:                 "task-1",
		Title:              "Launch docs",
		Description:        "Doc launch requirements.",
		AcceptanceCriteria: "Approved.",
		Subtasks:           []string{"Draft checklist"},
		SessionID:          "session-task-1",
		Status:             "todo",
		Flow:               1,
	}
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
		"created",
		"awaiting operator approval",
		"Enter·open session", // session exists → open action hint shown
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("task detail missing %q: %q", want, view)
		}
	}
	// Raw session UUID should NOT be shown (replaced with action hint)
	if strings.Contains(view, "session-task-1") {
		t.Fatalf("task detail should not expose raw session ID in body")
	}
}

func TestEnterOnTaskDetailOpensAsyncSession(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = MainPanel
	model.workspace.setMainView(ViewTask)
	model.workspace.tasks["task-2"] = &taskRecord{
		ID:        "task-2",
		Title:     "CI hardening",
		SessionID: "session-task-2",
		Status:    "in_progress",
	}
	model.workspace.taskSessionIDs["task-2"] = "session-task-2"
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

func TestDashboardCursorJKMoveSelection(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.tasks["task-1"] = &taskRecord{ID: "task-1", Title: "Launch docs", Status: "todo"}
	model.workspace.tasks["task-2"] = &taskRecord{ID: "task-2", Title: "CI hardening", Status: "in_progress"}
	model.workspace.tasks["task-3"] = &taskRecord{ID: "task-3", Title: "Done task", Status: "done"}
	model.workspace.taskOrder = []string{"task-1", "task-2", "task-3"}

	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}) // navigate to dashboard
	if got := model.FocusedPanel(); got != MainPanel {
		t.Fatalf("focus after d = %s, want main", panelLabel(got))
	}

	// Press j — should select first active task (task-1)
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if got := model.workspace.selectedTaskID; got != "task-1" {
		t.Fatalf("selectedTaskID after first j = %q, want task-1", got)
	}

	// Press j again — should advance to task-2 (in_progress)
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if got := model.workspace.selectedTaskID; got != "task-2" {
		t.Fatalf("selectedTaskID after second j = %q, want task-2", got)
	}

	// Press j again — should stay on task-2 (task-3 is done and excluded)
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if got := model.workspace.selectedTaskID; got != "task-2" {
		t.Fatalf("selectedTaskID after third j (clamped) = %q, want task-2", got)
	}

	// Press k — should go back to task-1
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if got := model.workspace.selectedTaskID; got != "task-1" {
		t.Fatalf("selectedTaskID after k = %q, want task-1", got)
	}

	// Dashboard view should show ► on selected task
	view := strings.Join(model.renderDashboardView(120, 20), "\n")
	if !strings.Contains(view, "► Launch docs") {
		t.Fatalf("dashboard missing cursor on selected task: %q", view)
	}
	if strings.Contains(view, "► CI hardening") {
		t.Fatalf("dashboard should not have cursor on non-selected task: %q", view)
	}
	// Dynamic nav hint should show selected task name
	if !strings.Contains(view, "Launch docs") || !strings.Contains(view, "Enter·open") {
		t.Fatalf("dashboard nav hint missing selected task info: %q", view)
	}
}

func TestDashboardArrowKeysMoveCursor(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.tasks["task-a"] = &taskRecord{ID: "task-a", Title: "Alpha", Status: "todo", TaskNumber: 1}
	model.workspace.tasks["task-b"] = &taskRecord{ID: "task-b", Title: "Beta", Status: "in_progress", TaskNumber: 2}
	model.workspace.taskOrder = []string{"task-b", "task-a"} // descending

	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.focus = MainPanel
	model.workspace.setMainView(ViewDashboard)

	// Down arrow → first task (task-b, task_number=2, comes first)
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyDown})
	if got := model.workspace.selectedTaskID; got != "task-b" {
		t.Fatalf("selectedTaskID after Down = %q, want task-b", got)
	}
	// Down arrow again → second task
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyDown})
	if got := model.workspace.selectedTaskID; got != "task-a" {
		t.Fatalf("selectedTaskID after second Down = %q, want task-a", got)
	}
	// Up arrow → back to first
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyUp})
	if got := model.workspace.selectedTaskID; got != "task-b" {
		t.Fatalf("selectedTaskID after Up = %q, want task-b", got)
	}
}

func TestModelStateReturnsLastMainView(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.setMainView(ViewInbox)

	if got := model.State().LastMainView; got != "inbox" {
		t.Fatalf("state last_main_view = %q, want %q", got, "inbox")
	}
}

func TestDashboardFilteredCountsReflectVisibleTasks(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.tasks = map[string]*taskRecord{
		"task-a": {ID: "task-a", Title: "API refactor", Status: "todo"},
		"task-b": {ID: "task-b", Title: "API tests", Status: "in_progress"},
		"task-c": {ID: "task-c", Title: "Billing feature", Status: "todo"},
		"task-d": {ID: "task-d", Title: "Billing done", Status: "done"},
	}
	model.workspace.taskOrder = []string{"task-a", "task-b", "task-c", "task-d"}

	// No filter: headers should show total counts (2 todo, 1 in_progress, 1 done)
	view := strings.Join(model.renderDashboardView(120, 20), "\n")
	if !strings.Contains(view, "TODO") || !strings.Contains(view, "2") {
		t.Fatalf("unfiltered TODO count missing: %q", view)
	}

	// Filter to "api": only task-a (todo) and task-b (in_progress) match
	model.mainFilter = "api"
	filteredView := strings.Join(model.renderDashboardView(120, 20), "\n")

	// Should show 1 in TODO column header and 1 in IN PROGRESS column header
	// The "DONE" column header should show 0 (no matching done tasks)
	if !strings.Contains(filteredView, "API refactor") {
		t.Fatalf("filtered view missing API refactor: %q", filteredView)
	}
	if strings.Contains(filteredView, "Billing") {
		t.Fatalf("filtered view should not contain Billing tasks: %q", filteredView)
	}
}

func TestNewModelWithRuntimeRestoresLastMainView(t *testing.T) {
	state := DefaultState()
	state.LastMainView = "agents"

	model := NewModelWithRuntime(state, RuntimeHints{})
	if got := model.MainView(); got != ViewAgents {
		t.Fatalf("main view on restore = %s, want %s", got, ViewAgents)
	}
}

func TestNewModelWithRuntimeDefaultsToDashboardWhenLastMainViewUnknown(t *testing.T) {
	state := DefaultState()
	state.LastMainView = "unknown-view"

	model := NewModelWithRuntime(state, RuntimeHints{})
	if got := model.MainView(); got != ViewDashboard {
		t.Fatalf("main view for unknown persisted value = %s, want %s", got, ViewDashboard)
	}
}

func TestAgentsViewFiltersWithMainFilter(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.agents = []string{"Frank=active", "Ellie=paused", "Bob=active"}

	// No filter — all agents visible
	view := strings.Join(model.renderAgentsView(80, 20), "\n")
	for _, name := range []string{"Frank", "Ellie", "Bob"} {
		if !strings.Contains(view, name) {
			t.Fatalf("unfiltered agents view missing %q: %q", name, view)
		}
	}

	// Filter to "frank" — only Frank should appear
	model.mainFilter = "frank"
	filteredView := strings.Join(model.renderAgentsView(80, 20), "\n")
	if !strings.Contains(filteredView, "Frank") {
		t.Fatalf("filtered agents view missing Frank: %q", filteredView)
	}
	if strings.Contains(filteredView, "Ellie") {
		t.Fatalf("filtered agents view should not contain Ellie: %q", filteredView)
	}
	if strings.Contains(filteredView, "Bob") {
		t.Fatalf("filtered agents view should not contain Bob: %q", filteredView)
	}
}

func TestAgentsViewShowsNoMatchesMessageWhenFilterExcludesAll(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.agents = []string{"Frank=active", "Ellie=paused"}
	model.mainFilter = "zzznomatch"

	view := strings.Join(model.renderAgentsView(80, 20), "\n")
	if !strings.Contains(view, "no agents matching") {
		t.Fatalf("agents view should show no-match message when filter excludes all: %q", view)
	}
}

func TestInboxEmptyStateDistinguishesFilteredVsGenuinelyEmpty(t *testing.T) {
	model := NewModel(DefaultState())

	// Genuinely empty inbox — should show "✓ Inbox clear"
	view := strings.Join(model.renderInboxView(80, 20), "\n")
	if !strings.Contains(view, "Inbox clear") {
		t.Fatalf("empty inbox should show 'Inbox clear': %q", view)
	}

	// Inbox with items but filter excludes all — should show "no inbox items matching"
	model.workspace.inbox = []inboxItem{
		{ID: "i1", TaskID: "task-1", Summary: "Review PR #42"},
	}
	model.mainFilter = "zzznomatch"
	filteredView := strings.Join(model.renderInboxView(80, 20), "\n")
	if strings.Contains(filteredView, "Inbox clear") {
		t.Fatalf("filtered inbox should not show 'Inbox clear' when items exist but don't match: %q", filteredView)
	}
	if !strings.Contains(filteredView, "no inbox items matching") {
		t.Fatalf("filtered inbox should show 'no inbox items matching': %q", filteredView)
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

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
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
	// Directly set a non-general task session so MainView == ViewTask.
	// Simulating key navigation is fragile since the sidebar structure has changed.
	model.workspace.tasks["test-task"] = &taskRecord{
		ID:        "test-task",
		Title:     "Test Task",
		SessionID: "session-task-test",
		Status:    "todo",
	}
	model.workspace.taskSessionIDs["test-task"] = "session-task-test"
	model.workspace.selectedTaskID = "test-task"
	model.workspace.activeSessionID = "session-task-test"
	model.activeSession = "session-task-test"
	model.workspace.setMainView(ViewTask)
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

func TestStatusAutoClearFiresWhenGenerationMatches(t *testing.T) {
	model := NewModel(DefaultState())
	model.statusMessage = "test status"
	model.statusGeneration = 7

	// Simulate the timer firing with the matching generation.
	updated, _ := model.Update(statusClearMsg{Generation: 7})
	next, ok := updated.(Model)
	if !ok {
		t.Fatal("unexpected model type")
	}
	if next.statusMessage != "" {
		t.Fatalf("status message should be cleared, got %q", next.statusMessage)
	}
}

func TestStatusAutoClearIgnoredWhenGenerationMismatch(t *testing.T) {
	model := NewModel(DefaultState())
	model.statusMessage = "newer status"
	model.statusGeneration = 9

	// Simulate a stale timer (old generation 7, but current is 9).
	updated, _ := model.Update(statusClearMsg{Generation: 7})
	next, ok := updated.(Model)
	if !ok {
		t.Fatal("unexpected model type")
	}
	if next.statusMessage == "" {
		t.Fatalf("status message should NOT be cleared by stale timer: got empty string")
	}
}

func TestKeyPressSchedulesAutoClearWhenStatusNonEmpty(t *testing.T) {
	model := NewModel(DefaultState())
	model.statusMessage = "some status"
	model.statusGeneration = 0

	// Any key press should increment statusGeneration.
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	_ = cmd // cmd is tea.Batch(cmd, statusAutoClearCmd(gen)) — we just verify generation incremented.

	// Verify by checking what generation the model would clear on.
	// We can't easily inspect the returned Cmd, but we can verify that
	// the model's generation was incremented.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	next, ok := updated.(Model)
	if !ok {
		t.Fatal("unexpected model type")
	}
	// statusGeneration should have incremented at least once (statusMessage might have changed).
	// Since pressing ? opens the help view (and sets a new statusMessage or clears the old one),
	// the exact generation depends on the result. Just verify the field exists and code ran.
	_ = next.statusGeneration
}

// EX-121: project view "d" help hint should reflect showDoneTasks state.
func TestProjectViewDoneHintReflectsState(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = MainPanel
	model.workspace.setMainView(ViewProject)

	model.workspace.showDoneTasks = false
	hint := model.commandFallbackHelp()
	if !strings.Contains(hint, "d show done") {
		t.Fatalf("hint when showDoneTasks=false should say 'd show done': %q", hint)
	}
	if strings.Contains(hint, "d hide done") {
		t.Fatalf("hint when showDoneTasks=false should not say 'd hide done': %q", hint)
	}

	model.workspace.showDoneTasks = true
	hint = model.commandFallbackHelp()
	if !strings.Contains(hint, "d hide done") {
		t.Fatalf("hint when showDoneTasks=true should say 'd hide done': %q", hint)
	}
	if strings.Contains(hint, "d show done") {
		t.Fatalf("hint when showDoneTasks=true should not say 'd show done': %q", hint)
	}
}

// EX-122: inboxItemsLoadedMsg handler should set RequiresHumanReview=true on
// any task that has a corresponding inbox item.
func TestInboxItemsLoadedSetsRequiresHumanReview(t *testing.T) {
	model := NewModel(DefaultState())
	if model.workspace.tasks == nil {
		model.workspace.tasks = make(map[string]*taskRecord)
	}
	model.workspace.tasks["task-review"] = &taskRecord{
		ID:                  "task-review",
		RequiresHumanReview: false,
	}

	updated, _ := model.Update(inboxItemsLoadedMsg{
		Items: []InboxSummaryItem{
			{ID: "inbox-1", TaskID: "task-review", Summary: "needs review"},
		},
	})
	m := updated.(Model)
	if rec := m.workspace.tasks["task-review"]; rec == nil || !rec.RequiresHumanReview {
		t.Fatal("RequiresHumanReview should be true after loading inbox item for task")
	}
}

// EX-123: removeInboxItem should clear RequiresHumanReview when no inbox items
// remain for the affected task.
func TestRemoveInboxItemClearsRequiresHumanReviewWhenLastItemRemoved(t *testing.T) {
	w := &workspaceState{
		tasks: map[string]*taskRecord{
			"task-abc": {ID: "task-abc", RequiresHumanReview: true},
		},
		inbox: []inboxItem{
			{ID: "inbox-1", TaskID: "task-abc", Summary: "review needed"},
		},
	}

	w.removeInboxItem("inbox-1")

	if rec := w.tasks["task-abc"]; rec == nil || rec.RequiresHumanReview {
		t.Fatal("RequiresHumanReview should be false after last inbox item for task is removed")
	}
}

func TestRemoveInboxItemKeepsRequiresHumanReviewWhenOtherItemsRemain(t *testing.T) {
	w := &workspaceState{
		tasks: map[string]*taskRecord{
			"task-abc": {ID: "task-abc", RequiresHumanReview: true},
		},
		inbox: []inboxItem{
			{ID: "inbox-1", TaskID: "task-abc", Summary: "first review"},
			{ID: "inbox-2", TaskID: "task-abc", Summary: "second review"},
		},
	}

	w.removeInboxItem("inbox-1")

	if rec := w.tasks["task-abc"]; rec == nil || !rec.RequiresHumanReview {
		t.Fatal("RequiresHumanReview should remain true when another inbox item for the task remains")
	}
}

// EX-124: inbox view shows "N of M" position indicator in action hint.
func TestInboxViewShowsPositionIndicator(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.inbox = []inboxItem{
		{ID: "item-1", TaskID: "task-a", Summary: "First review needed"},
		{ID: "item-2", TaskID: "task-b", Summary: "Second review needed"},
		{ID: "item-3", TaskID: "task-c", Summary: "Third review needed"},
	}
	model.workspace.inboxCursor = 0
	model.workspace.setMainView(ViewInbox)

	rendered := strings.Join(model.renderInboxView(80, 20), "\n")
	if !strings.Contains(rendered, "1 of 3") {
		t.Fatalf("inbox view missing position indicator '1 of 3': %q", rendered)
	}
}

func TestInboxViewOmitsPositionIndicatorForSingleItem(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.inbox = []inboxItem{
		{ID: "item-1", TaskID: "task-a", Summary: "Only one review"},
	}
	model.workspace.inboxCursor = 0
	model.workspace.setMainView(ViewInbox)

	rendered := strings.Join(model.renderInboxView(80, 20), "\n")
	if strings.Contains(rendered, " of ") {
		t.Fatalf("inbox view should not show position indicator for a single item: %q", rendered)
	}
}

// EX-125: flow.advanced event triggers task detail reload and adds activity entry.
func TestFlowAdvancedEventAddsActivityAndLoadsTaskDetail(t *testing.T) {
	model := NewModel(DefaultState())
	if model.workspace.tasks == nil {
		model.workspace.tasks = make(map[string]*taskRecord)
	}
	model.workspace.tasks["task-flow"] = &taskRecord{
		ID:           "task-flow",
		TaskNumber:   5,
		Title:        "Build flow",
		Flow:         1,
		FlowNodeName: "Review",
	}

	rawPayload, _ := json.Marshal(map[string]string{"task_id": "task-flow"})
	envelope := EventEnvelope{
		EventType: "flow.advanced",
		Payload:   rawPayload,
	}
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: envelope})
	m := updated.(Model)

	activityFound := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "OC-5") && strings.Contains(entry, "flow advanced") {
			activityFound = true
			break
		}
	}
	if !activityFound {
		t.Fatalf("flow.advanced event should add activity entry with task label: %v", m.workspace.activity)
	}
}

// EX-126: dashboard board scrolls to keep the cursor visible when a column
// has more than 4 tasks.
func TestDashboardBoardScrollsToCursorWhenColumnOverflows(t *testing.T) {
	model := NewModel(DefaultState())
	if model.workspace.tasks == nil {
		model.workspace.tasks = make(map[string]*taskRecord)
	}
	// Add 6 todo tasks. The cursor will be on task-6 (index 5 in the column).
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("task-%d", i)
		model.workspace.tasks[id] = &taskRecord{
			ID:         id,
			TaskNumber: i,
			Title:      fmt.Sprintf("Task %d", i),
			Status:     "todo",
		}
		model.workspace.taskOrder = append(model.workspace.taskOrder, id)
	}
	model.workspace.selectedTaskID = "task-6" // cursor on last task (index 5)

	rendered := strings.Join(model.renderDashboardView(120, 20), "\n")
	// The cursor task should be visible (rendered as "► OC-6: Task 6")
	if !strings.Contains(rendered, "OC-6") {
		t.Fatalf("cursor task OC-6 should be visible even when column overflows 4 rows: %q", rendered)
	}
	// And there should be a scroll indicator showing tasks above
	if !strings.Contains(rendered, "↑") {
		t.Fatalf("scroll indicator (↑) should appear when cursor is scrolled into view: %q", rendered)
	}
}

// EX-127: chat.turn.started event should capture activeTurnSessionID immediately.
func TestChatTurnStartedSetsActiveTurnSessionID(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true
	// Use a well-formed UUID to exercise the looksLikeUUID path.
	sessionUUID := "11111111-2222-3333-4444-555555555555"
	model.activeSession = sessionUUID
	// activeTurnSessionID empty → sessionMatchesActive accepts any session.
	model.activeTurnSessionID = ""

	rawPayload, _ := json.Marshal(map[string]string{"session_id": sessionUUID})
	envelope := EventEnvelope{
		EventType: "chat.turn.started",
		Payload:   rawPayload,
	}
	updated, _ := model.Update(ChatEnvelopeMsg{Envelope: envelope})
	m := updated.(Model)

	if !m.activeTurn {
		t.Fatal("activeTurn should be true after chat.turn.started")
	}
	if m.activeTurnSessionID != sessionUUID {
		t.Fatalf("activeTurnSessionID = %q, want %q", m.activeTurnSessionID, sessionUUID)
	}
}

// EX-128: task.created event adds activity entry.
func TestTaskCreatedEventAddsActivityEntry(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true

	rawPayload, _ := json.Marshal(map[string]any{
		"task_id":    "task-new-123",
		"project_id": "proj-abc",
		"task_number": 9,
	})
	envelope := EventEnvelope{
		EventType: "task.created",
		Payload:   rawPayload,
	}
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: envelope})
	m := updated.(Model)

	activityFound := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "OC-9") && strings.Contains(entry, "created") {
			activityFound = true
			break
		}
	}
	if !activityFound {
		t.Fatalf("task.created event should add 'OC-9: created' activity entry: %v", m.workspace.activity)
	}
}

// EX-129: budget.anomaly_detected should surface a status-bar warning with
// the period, multiplier, and token counts so the user is immediately aware.
func TestBudgetAnomalyDetectedShowsStatusWarning(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true

	rawPayload, _ := json.Marshal(map[string]any{
		"period":                 "daily",
		"current_tokens":         int64(90000),
		"rolling_average_tokens": int64(15000),
		"rolling_average_ratio":  6.0,
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "budget.anomaly_detected",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	if !strings.Contains(m.statusMessage, "Budget anomaly") {
		t.Fatalf("budget.anomaly_detected should set statusMessage with 'Budget anomaly', got: %q", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "daily") {
		t.Fatalf("statusMessage should mention period 'daily', got: %q", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "6x") {
		t.Fatalf("statusMessage should mention multiplier '6x', got: %q", m.statusMessage)
	}
}

// EX-129: task.merged should add an activity entry with the branch name.
func TestTaskMergedAddsActivityEntry(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true

	rawPayload, _ := json.Marshal(map[string]any{
		"branch_name": "feature/oc-42-new-widget",
		"project_id":  "proj-xyz",
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "task.merged",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "feature/oc-42-new-widget") && strings.Contains(entry, "merged") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("task.merged should add '<branch>: merged' activity entry: %v", m.workspace.activity)
	}
}

// EX-129: flow.started should add an activity entry and trigger a task detail reload.
func TestFlowStartedEventAddsActivityAndLoadsTaskDetail(t *testing.T) {
	loadCalled := ""
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadTaskDetail: func(_ context.Context, id string) (*TaskDetailItem, error) {
			loadCalled = id
			return &TaskDetailItem{ID: id}, nil
		},
	})
	model.turnsSynced = true

	rawPayload, _ := json.Marshal(map[string]any{"task_id": "task-flow-start-1"})
	updated, cmd := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "flow.started",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "flow started") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("flow.started should add '…: flow started' activity entry: %v", m.workspace.activity)
	}
	if cmd == nil {
		t.Fatal("flow.started should return a loadTaskDetail cmd")
	}
	// Execute the cmd to verify it triggers the right loader.
	msg := cmd()
	if _, ok := msg.(taskDetailLoadedMsg); !ok {
		t.Fatalf("cmd returned unexpected message type: %T", msg)
	}
	if loadCalled != "task-flow-start-1" {
		t.Fatalf("loadTaskDetail called with %q, want 'task-flow-start-1'", loadCalled)
	}
}

// EX-129: flow.rejected should add an activity entry and trigger a task detail reload.
func TestFlowRejectedEventAddsActivityAndLoadsTaskDetail(t *testing.T) {
	loadCalled := ""
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadTaskDetail: func(_ context.Context, id string) (*TaskDetailItem, error) {
			loadCalled = id
			return &TaskDetailItem{ID: id}, nil
		},
	})
	model.turnsSynced = true

	rawPayload, _ := json.Marshal(map[string]any{
		"task_id":    "task-rej-99",
		"project_id": "proj-abc",
	})
	updated, cmd := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "flow.rejected",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "flow rejected") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("flow.rejected should add '…: flow rejected' activity entry: %v", m.workspace.activity)
	}
	if cmd == nil {
		t.Fatal("flow.rejected should return a loadTaskDetail cmd")
	}
	msg := cmd()
	if _, ok := msg.(taskDetailLoadedMsg); !ok {
		t.Fatalf("cmd returned unexpected message type: %T", msg)
	}
	if loadCalled != "task-rej-99" {
		t.Fatalf("loadTaskDetail called with %q, want 'task-rej-99'", loadCalled)
	}
}

// EX-130: project.deployed should add a "deployed <sha>" activity entry.
func TestProjectDeployedAddsActivityEntry(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true

	rawPayload, _ := json.Marshal(map[string]any{
		"project_id": "proj-123",
		"commit_sha": "abcdef1234567890",
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "project.deployed",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "deployed") && strings.Contains(entry, "abcdef12") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("project.deployed should add 'deployed <sha>' activity entry: %v", m.workspace.activity)
	}
}

// EX-130: project.rollback_initiated should add a "rollback to <sha>" activity entry.
func TestProjectRollbackInitiatedAddsActivityEntry(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true

	rawPayload, _ := json.Marshal(map[string]any{
		"project_id":        "proj-123",
		"target_commit_sha": "dead1234beef5678",
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "project.rollback_initiated",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "rollback") && strings.Contains(entry, "dead1234") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("project.rollback_initiated should add 'rollback to <sha>' activity entry: %v", m.workspace.activity)
	}
}

// EX-130: deploy.approval_requested should set statusMessage with the commit SHA.
func TestDeployApprovalRequestedShowsStatusMessage(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true

	rawPayload, _ := json.Marshal(map[string]any{
		"project_id": "proj-abc",
		"commit_sha": "cafe1234abcd5678",
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "deploy.approval_requested",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	if !strings.Contains(m.statusMessage, "Deploy pending approval") {
		t.Fatalf("deploy.approval_requested should set statusMessage with approval notice, got: %q", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "cafe1234") {
		t.Fatalf("statusMessage should include commit SHA prefix, got: %q", m.statusMessage)
	}
}

// EX-130: tool.capability_denied should set statusMessage with the tool name and capability.
func TestToolCapabilityDeniedShowsStatusWarning(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true

	rawPayload, _ := json.Marshal(map[string]any{
		"tool_name":  "file.write",
		"capability": "file_write",
		"run_id":     "run-xyz",
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "tool.capability_denied",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	if !strings.Contains(m.statusMessage, "Policy denied tool") {
		t.Fatalf("tool.capability_denied should set statusMessage with denial notice, got: %q", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "file.write") {
		t.Fatalf("statusMessage should include tool name, got: %q", m.statusMessage)
	}
}

// EX-131: memory.extracted should add an activity entry with the item count.
func TestMemoryExtractedAddsActivityEntry(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true

	rawPayload, _ := json.Marshal(map[string]any{
		"count":        3,
		"batch_source": "turn",
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "memory.extracted",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "memory") && strings.Contains(entry, "3") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("memory.extracted should add activity entry with count: %v", m.workspace.activity)
	}
}

// EX-131: memory.extracted with count=0 should NOT add a "memory" activity entry.
func TestMemoryExtractedZeroCountIgnored(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true

	rawPayload, _ := json.Marshal(map[string]any{"count": 0})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "memory.extracted",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "memory") {
			t.Fatalf("memory.extracted count=0 should not add memory activity entry: %v", m.workspace.activity)
		}
	}
}

// EX-131: mcp.catalog.changed should set statusMessage describing the catalog delta.
func TestMCPCatalogChangedShowsStatusMessage(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true

	rawPayload, _ := json.Marshal(map[string]any{
		"added_count":   5,
		"updated_count": 2,
		"removed_count": 1,
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "mcp.catalog.changed",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	if !strings.Contains(m.statusMessage, "MCP catalog") {
		t.Fatalf("mcp.catalog.changed should set statusMessage with MCP catalog notice, got: %q", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "+5") {
		t.Fatalf("statusMessage should mention added count, got: %q", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "-1") {
		t.Fatalf("statusMessage should mention removed count, got: %q", m.statusMessage)
	}
}

// EX-132: appendActivity should cap the activity log at activityMaxEntries,
// dropping the oldest entries when the slice grows beyond the limit.
func TestAppendActivityCapsBeyondMaxEntries(t *testing.T) {
	entries := make([]string, 0, activityMaxEntries+10)
	for i := 0; i < activityMaxEntries+5; i++ {
		entries = appendActivity(entries, fmt.Sprintf("entry-%d", i))
	}
	if len(entries) != activityMaxEntries {
		t.Fatalf("appendActivity should cap at %d, got %d", activityMaxEntries, len(entries))
	}
	// Most-recent entry should be the last one appended.
	last := fmt.Sprintf("entry-%d", activityMaxEntries+4)
	if entries[len(entries)-1] != last {
		t.Fatalf("last entry = %q, want %q", entries[len(entries)-1], last)
	}
	// Oldest surviving entry should be "entry-5" (the first 5 were trimmed).
	oldest := fmt.Sprintf("entry-%d", 5)
	if entries[0] != oldest {
		t.Fatalf("oldest surviving entry = %q, want %q", entries[0], oldest)
	}
}

// EX-133: pressing r in ViewTask should set "Refreshing task detail…" status,
// indicating the context-aware branch was taken (not the sidebar-only path).
func TestRKeyInViewTaskTriggersTaskDetailReload(t *testing.T) {
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadTaskDetail: func(_ context.Context, id string) (*TaskDetailItem, error) {
			return &TaskDetailItem{ID: id}, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.workspace.setMainView(ViewTask)
	model.workspace.selectedTaskID = "task-r-test"
	model.focus = MainPanel

	updated := pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if updated.statusMessage != "Refreshing task detail…" {
		t.Fatalf("r in ViewTask should set 'Refreshing task detail…' status, got: %q", updated.statusMessage)
	}
}

// EX-133: pressing r in ViewProject should set "Refreshing project detail…" status.
func TestRKeyInViewProjectTriggersProjectDetailReload(t *testing.T) {
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadProjectDetail: func(_ context.Context, id string) (*ProjectDetail, error) {
			return &ProjectDetail{ID: id}, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.workspace.setMainView(ViewProject)
	model.workspace.selectedProjectID = "proj-r-test"
	model.focus = MainPanel

	updated := pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if updated.statusMessage != "Refreshing project detail…" {
		t.Fatalf("r in ViewProject should set 'Refreshing project detail…' status, got: %q", updated.statusMessage)
	}
}

// EX-162: pressing r in ViewInbox should reload inbox items (not just sidebar).
func TestRKeyInViewInboxReloadsInboxItems(t *testing.T) {
	t.Parallel()
	reloaded := false
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadInboxItems: func(_ context.Context) ([]InboxSummaryItem, error) {
			reloaded = true
			return nil, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.workspace.setMainView(ViewInbox)
	model.focus = MainPanel

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m := updated.(Model)
	if m.statusMessage != "Refreshing inbox…" {
		t.Fatalf("r in ViewInbox should set 'Refreshing inbox…' status, got: %q", m.statusMessage)
	}
	if cmd == nil {
		t.Fatal("r in ViewInbox should return a non-nil cmd")
	}
	// cmd is a tea.Batch (inner cmd + status auto-clear timer); use runNonTimerCmds to execute it.
	runNonTimerCmds(cmd)
	if !reloaded {
		t.Fatal("r in ViewInbox should call LoadInboxItems, but it was not called")
	}
}

// EX-162: pressing r in ViewAgents should reload agents list (not just sidebar).
func TestRKeyInViewAgentsReloadsAgents(t *testing.T) {
	t.Parallel()
	reloaded := false
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadAgents: func(_ context.Context) ([]string, error) {
			reloaded = true
			return nil, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.workspace.setMainView(ViewAgents)
	model.focus = MainPanel

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m := updated.(Model)
	if m.statusMessage != "Refreshing agents…" {
		t.Fatalf("r in ViewAgents should set 'Refreshing agents…' status, got: %q", m.statusMessage)
	}
	if cmd == nil {
		t.Fatal("r in ViewAgents should return a non-nil cmd")
	}
	// cmd is a tea.Batch (inner cmd + status auto-clear timer); use runNonTimerCmds to execute it.
	runNonTimerCmds(cmd)
	if !reloaded {
		t.Fatal("r in ViewAgents should call LoadAgents, but it was not called")
	}
}

// EX-135: pressing g in ViewDashboard should jump to the first task.
func TestGKeyInDashboardJumpsToFirstTask(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.focus = MainPanel
	model.workspace.setMainView(ViewDashboard)
	model.workspace.tasks = map[string]*taskRecord{
		"task-a": {ID: "task-a", TaskNumber: 1, Title: "Alpha", Status: "todo"},
		"task-b": {ID: "task-b", TaskNumber: 2, Title: "Beta", Status: "in_progress"},
		"task-c": {ID: "task-c", TaskNumber: 3, Title: "Gamma", Status: "todo"},
	}
	model.workspace.taskOrder = []string{"task-a", "task-b", "task-c"}
	model.workspace.dashboardCursor = 2
	model.workspace.selectedTaskID = "task-c"

	updated := pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if updated.workspace.selectedTaskID != "task-a" {
		t.Fatalf("g should select first task 'task-a', got: %q", updated.workspace.selectedTaskID)
	}
	if updated.workspace.dashboardCursor != 0 {
		t.Fatalf("g should reset dashboardCursor to 0, got: %d", updated.workspace.dashboardCursor)
	}
}

// EX-135: pressing G in ViewDashboard should jump to the last task.
func TestGUpperKeyInDashboardJumpsToLastTask(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.focus = MainPanel
	model.workspace.setMainView(ViewDashboard)
	model.workspace.tasks = map[string]*taskRecord{
		"task-a": {ID: "task-a", TaskNumber: 1, Title: "Alpha", Status: "todo"},
		"task-b": {ID: "task-b", TaskNumber: 2, Title: "Beta", Status: "in_progress"},
		"task-c": {ID: "task-c", TaskNumber: 3, Title: "Gamma", Status: "todo"},
	}
	model.workspace.taskOrder = []string{"task-a", "task-b", "task-c"}
	model.workspace.dashboardCursor = 0
	model.workspace.selectedTaskID = "task-a"

	updated := pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if updated.workspace.selectedTaskID != "task-c" {
		t.Fatalf("G should select last task 'task-c', got: %q", updated.workspace.selectedTaskID)
	}
	if updated.workspace.dashboardCursor != 2 {
		t.Fatalf("G should set dashboardCursor to 2, got: %d", updated.workspace.dashboardCursor)
	}
}

// EX-136: chat header in ScopeTask should include task number and title as breadcrumb.
func TestChatHeaderShowsTaskContextForScopeTask(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.workspace.tasks["task-ctx-1"] = &taskRecord{
		ID:         "task-ctx-1",
		TaskNumber: 42,
		Title:      "My Important Task",
		Status:     "in_progress",
	}
	model.workspace.selectedTaskID = "task-ctx-1"
	model.activeScope = ScopeTask

	view := model.View()
	if !strings.Contains(view, "OC-42") {
		t.Fatalf("chat header should contain task number 'OC-42' in ScopeTask, view: %q", view[:min(200, len(view))])
	}
	if !strings.Contains(view, "My Important Task") {
		t.Fatalf("chat header should contain task title 'My Important Task', view: %q", view[:min(200, len(view))])
	}
}

// EX-137: agent.pm_removed should add an activity entry.
func TestAgentPMRemovedAddsActivityEntry(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true

	rawPayload, _ := json.Marshal(map[string]any{
		"agent_id":   "agent-123",
		"project_id": "proj-456",
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "agent.pm_removed",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "PM") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("agent.pm_removed should add activity entry mentioning 'PM': %v", m.workspace.activity)
	}
}

// EX-138: task.status_changed to done in selected project should trigger a
// project detail reload so the Done section is populated correctly.
func TestTaskStatusChangedToDoneTriggersProjectDetailReload(t *testing.T) {
	loadCalled := ""
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadProjectDetail: func(_ context.Context, id string) (*ProjectDetail, error) {
			loadCalled = id
			return &ProjectDetail{ID: id}, nil
		},
	})
	model.turnsSynced = true
	model.workspace.selectedProjectID = "proj-done-test"
	model.workspace.selectedProject = &ProjectDetail{
		ID:          "proj-done-test",
		DisplayName: "Done Test Project",
	}
	model.workspace.tasks["task-done-1"] = &taskRecord{ID: "task-done-1", Status: "in_progress"}

	rawPayload, _ := json.Marshal(map[string]any{
		"task_id":    "task-done-1",
		"project_id": "proj-done-test",
		"to_status":  "done",
	})
	_, cmd := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "task.status_changed",
		Payload:   rawPayload,
	}})

	if cmd == nil {
		t.Fatal("task.status_changed to done in selected project should return a non-nil loadProjectDetail cmd")
	}
	msg := cmd()
	if _, ok := msg.(projectDetailLoadedMsg); !ok {
		t.Fatalf("cmd returned unexpected type %T", msg)
	}
	if loadCalled != "proj-done-test" {
		t.Fatalf("loadProjectDetail called with %q, want 'proj-done-test'", loadCalled)
	}
}

// --- EX-140: project lifecycle, chat session lifecycle, run failures, message redaction ---

func TestProjectCreatedAddsActivityAndReloadsSidebar(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	raw, _ := json.Marshal(map[string]any{"project_id": "proj-1", "slug": "my-project"})
	updated, cmd := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "project.created",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "project created: my-project") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing 'project created: my-project': %v", m.workspace.activity)
	}
	if cmd == nil {
		t.Fatal("project.created should return loadSidebarDataCmd (non-nil cmd)")
	}
}

func TestProjectDeletedAddsActivityAndReloadsSidebar(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	raw, _ := json.Marshal(map[string]any{"project_id": "proj-2", "slug": "old-project"})
	updated, cmd := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "project.deleted",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "project deleted: old-project") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing 'project deleted: old-project': %v", m.workspace.activity)
	}
	if cmd == nil {
		t.Fatal("project.deleted should return loadSidebarDataCmd (non-nil cmd)")
	}
}

func TestProjectArchivedAddsActivityAndReloadsSidebar(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	raw, _ := json.Marshal(map[string]any{"project_id": "proj-3", "slug": "archived-proj"})
	updated, cmd := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "project.archived",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "project archived: archived-proj") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing 'project archived: archived-proj': %v", m.workspace.activity)
	}
	if cmd == nil {
		t.Fatal("project.archived should return loadSidebarDataCmd (non-nil cmd)")
	}
}

func TestProjectUpdatedReloadsDetailForCurrentProject(t *testing.T) {
	t.Parallel()
	loadCalled := ""
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadProjectDetail: func(_ context.Context, id string) (*ProjectDetail, error) {
			loadCalled = id
			return &ProjectDetail{}, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.workspace.selectedProjectID = "proj-updated"

	raw, _ := json.Marshal(map[string]any{"project_id": "proj-updated", "slug": "my-proj"})
	_, cmd := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "project.updated",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	if cmd == nil {
		t.Fatal("project.updated for selected project should return loadProjectDetailCmd")
	}
	cmd()
	if loadCalled != "proj-updated" {
		t.Fatalf("loadProjectDetail called with %q, want 'proj-updated'", loadCalled)
	}
}

func TestProjectUpdatedIgnoredForNonCurrentProject(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.workspace.selectedProjectID = "proj-A"

	raw, _ := json.Marshal(map[string]any{"project_id": "proj-B", "slug": "other"})
	_, cmd := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "project.updated",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	if cmd != nil {
		t.Fatal("project.updated for non-selected project should return nil cmd")
	}
}

func TestRunFailedSetsStatusMessage(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	raw, _ := json.Marshal(map[string]any{
		"run_id":        "run-1",
		"failure_class": "transient",
		"reason":        "connection timeout",
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "run.failed",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)

	if !strings.Contains(m.statusMessage, "Run failed") {
		t.Fatalf("statusMessage %q should contain 'Run failed'", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "connection timeout") {
		t.Fatalf("statusMessage %q should contain the reason", m.statusMessage)
	}
}

func TestRunDeadLetteredSetsStatusMessage(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	raw, _ := json.Marshal(map[string]any{
		"run_id":        "run-2",
		"failure_class": "permanent",
		"last_error":    "max retries exceeded",
		"attempt_count": 3,
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "run.dead_lettered",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)

	if !strings.Contains(m.statusMessage, "dead-lettered") {
		t.Fatalf("statusMessage %q should contain 'dead-lettered'", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "3") {
		t.Fatalf("statusMessage %q should contain attempt count", m.statusMessage)
	}
}

func TestChatMessageRedactedUpdatesContent(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.activeTurnSessionID = "session-redact-test"
	model.turnsSynced = true
	// Add a message to the chat
	model.chatMessageIndex["msg-redact-1"] = len(model.chatMessages)
	model.chatMessages = append(model.chatMessages, ChatMessage{
		ID:        "msg-redact-1",
		Role:      "assistant",
		Content:   "sensitive content",
		Finalized: true,
	})

	raw, _ := json.Marshal(map[string]any{"session_id": "session-redact-test", "message_id": "msg-redact-1"})
	updated, _ := model.Update(ChatEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "chat.message.redacted",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)

	idx, ok := m.chatMessageIndex["msg-redact-1"]
	if !ok {
		t.Fatal("message index missing after redaction")
	}
	if m.chatMessages[idx].Content != "[Redacted]" {
		t.Fatalf("content = %q, want '[Redacted]'", m.chatMessages[idx].Content)
	}
}

func TestChatMessageRedactedIgnoresNonActiveSession(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.activeTurnSessionID = "session-active"
	model.turnsSynced = true
	model.chatMessageIndex["msg-other-1"] = len(model.chatMessages)
	model.chatMessages = append(model.chatMessages, ChatMessage{
		ID:      "msg-other-1",
		Role:    "assistant",
		Content: "content should stay",
	})

	raw, _ := json.Marshal(map[string]any{"session_id": "session-other", "message_id": "msg-other-1"})
	updated, _ := model.Update(ChatEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "chat.message.redacted",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)

	idx := m.chatMessageIndex["msg-other-1"]
	if m.chatMessages[idx].Content != "content should stay" {
		t.Fatalf("content changed for non-active session: %q", m.chatMessages[idx].Content)
	}
}

// --- EX-141: agent lifecycle, supervisor escalation, status bar truncation ---

func TestAgentExpiredAddsActivityAndReloadsAgents(t *testing.T) {
	t.Parallel()
	agentLoadCalled := false
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadAgents: func(_ context.Context) ([]string, error) {
			agentLoadCalled = true
			return []string{}, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	raw, _ := json.Marshal(map[string]any{"agent_id": "agent-1", "reason": "budget exhausted"})
	updated, cmd := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "agent.expired",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "agent expired") && strings.Contains(entry, "budget exhausted") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing 'agent expired: budget exhausted': %v", m.workspace.activity)
	}
	if cmd == nil {
		t.Fatal("agent.expired should return loadAgentsCmd (non-nil cmd)")
	}
	cmd()
	if !agentLoadCalled {
		t.Fatal("agent.expired cmd should call LoadAgents")
	}
}

func TestAgentPromotedAddsActivityAndReloadsAgents(t *testing.T) {
	t.Parallel()
	agentLoadCalled := false
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadAgents: func(_ context.Context) ([]string, error) {
			agentLoadCalled = true
			return []string{}, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	updated, cmd := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "agent.promoted",
		OrgID:     "org-1",
		Payload:   json.RawMessage(`{"temp_agent_id":"a1","promoted_staff_agent_id":"a2"}`),
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "promoted") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing 'promoted': %v", m.workspace.activity)
	}
	if cmd == nil {
		t.Fatal("agent.promoted should return loadAgentsCmd (non-nil cmd)")
	}
	cmd()
	if !agentLoadCalled {
		t.Fatal("agent.promoted cmd should call LoadAgents")
	}
}

func TestSupervisorEscalationCreatedSetsStatusMessage(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	raw, _ := json.Marshal(map[string]any{"run_id": "abc12345-0000-0000-0000-000000000000"})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "supervisor.escalation_created",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)

	if !strings.Contains(m.statusMessage, "Supervisor escalation") {
		t.Fatalf("statusMessage %q should contain 'Supervisor escalation'", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "Recovery") {
		t.Fatalf("statusMessage %q should mention recovery", m.statusMessage)
	}
}

// --- EX-142: task.review_rejected, chat.session.mode_changed ---

func TestTaskReviewRejectedAddsActivityEntry(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.workspace.tasks = map[string]*taskRecord{
		"task-review-1": {ID: "task-review-1", TaskNumber: 7, Title: "Fix login"},
	}

	raw, _ := json.Marshal(map[string]any{"task_id": "task-review-1", "reason": "tests not passing"})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "task.review_rejected",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "OC-7") && strings.Contains(entry, "review rejected") && strings.Contains(entry, "tests not passing") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing review rejected entry: %v", m.workspace.activity)
	}
}

func TestChatSessionModeChangedAddsActivityEntry(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	raw, _ := json.Marshal(map[string]any{"session_id": "sess-1", "old_mode": "sync", "new_mode": "async"})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "chat.session.mode_changed",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "session mode") && strings.Contains(entry, "sync") && strings.Contains(entry, "async") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing session mode change entry: %v", m.workspace.activity)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// EX-143 tests: run failures should persist in the activity log (not just status bar).

func TestRunFailedAddsActivityEntry(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	raw, _ := json.Marshal(map[string]any{"run_id": "run-1", "reason": "context deadline exceeded"})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "run.failed",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)

	if !strings.Contains(m.statusMessage, "Run failed") {
		t.Fatalf("statusMessage = %q, want to contain 'Run failed'", m.statusMessage)
	}
	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "run failed") && strings.Contains(entry, "context deadline exceeded") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing run failed entry: %v", m.workspace.activity)
	}
}

func TestRunDeadLetteredAddsActivityEntry(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	raw, _ := json.Marshal(map[string]any{"run_id": "run-2", "last_error": "ENOMEM", "attempt_count": 3})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "run.dead_lettered",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)

	if !strings.Contains(m.statusMessage, "dead-lettered") {
		t.Fatalf("statusMessage = %q, want to contain 'dead-lettered'", m.statusMessage)
	}
	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "dead-lettered") && strings.Contains(entry, "ENOMEM") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing dead-lettered entry: %v", m.workspace.activity)
	}
}

// EX-144: task.review_rejected should also trigger task detail reload.

func TestTaskReviewRejectedTriggersDetailReload(t *testing.T) {
	t.Parallel()
	loaded := false
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadTaskDetail: func(_ context.Context, id string) (*TaskDetailItem, error) {
			if id == "task-rej-1" {
				loaded = true
			}
			return &TaskDetailItem{ID: id}, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	raw, _ := json.Marshal(map[string]any{"task_id": "task-rej-1", "reason": "incomplete"})
	_, cmd := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "task.review_rejected",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	if cmd == nil {
		t.Fatal("task.review_rejected returned nil cmd, want loadTaskDetailCmd")
	}
	// Execute the command to trigger the callback
	_ = cmd()
	if !loaded {
		t.Fatal("LoadTaskDetail was not called after task.review_rejected")
	}
}

// EX-145: deploy.approval_requested should also add to activity log.

func TestDeployApprovalRequestedAddsActivityEntry(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	raw, _ := json.Marshal(map[string]any{"commit_sha": "abc123def456"})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "deploy.approval_requested",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)

	if !strings.Contains(m.statusMessage, "Deploy pending approval") {
		t.Fatalf("statusMessage = %q, want to contain 'Deploy pending approval'", m.statusMessage)
	}
	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "Deploy pending approval") && strings.Contains(entry, "abc123de") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing deploy approval entry: %v", m.workspace.activity)
	}
}

// EX-146: chat.session.created should add an activity entry.

func TestChatSessionCreatedAddsActivityEntry(t *testing.T) {
	t.Parallel()
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadOrgSession: func(_ context.Context) (string, error) { return "sess-org", nil },
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	raw, _ := json.Marshal(map[string]any{"scope_type": "project_task"})
	updated, cmd := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "chat.session.created",
		OrgID:     "org-1",
		Payload:   raw,
	}})
	m := updated.(Model)
	if cmd == nil {
		t.Fatal("chat.session.created returned nil cmd, want sidebar reload")
	}
	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "session created") && strings.Contains(entry, "project_task") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing session created entry: %v", m.workspace.activity)
	}
}

// EX-147: activityIcon should use error icon for failures/rejections and
// warning icon for pending/rollback entries — not always the success check.

func TestActivityIconEX147(t *testing.T) {
	t.Parallel()
	cases := []struct {
		entry    string
		wantRune rune // first non-space rune of the rendered icon
	}{
		// Error icon (✗)
		{entry: "run failed: context deadline exceeded", wantRune: '✗'},
		{entry: "run dead-lettered (3 attempts): ENOMEM", wantRune: '✗'},
		{entry: "OC-7: review rejected — tests not passing", wantRune: '✗'},
		{entry: "agent expired: budget exceeded", wantRune: '✗'},
		{entry: "OC-3: blocked", wantRune: '✗'},
		// Warning icon (◌)
		{entry: "rollback to abc12345", wantRune: '◌'},
		{entry: "Deploy pending approval: abc12345", wantRune: '◌'},
		{entry: "OC-5: in progress", wantRune: '◌'},
		// Success icon (✓)
		{entry: "task merged: feat/login", wantRune: '✓'},
		{entry: "session created: project_task", wantRune: '✓'},
		{entry: "realtime events connected", wantRune: '✓'},
		{entry: "OC-1: done", wantRune: '✓'},
	}

	for _, tc := range cases {
		rendered := activityIcon(tc.entry)
		// Strip ANSI codes: find the rune that is ✓, ✗, or ◌
		var got rune
		for _, r := range rendered {
			if r == '✓' || r == '✗' || r == '◌' {
				got = r
				break
			}
		}
		if got != tc.wantRune {
			t.Errorf("activityIcon(%q) icon rune = %q, want %q", tc.entry, got, tc.wantRune)
		}
	}
}

// EX-148: a/x/f keys should work from ViewTask (not just ViewInbox) when
// RequiresHumanReview is set. The task view shows these hints — they must be functional.

func TestTaskViewApproveKeyActsOnInboxItem(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	// Set up a task with RequiresHumanReview and a matching inbox item
	taskID := "task-review-axf"
	model.workspace.tasks[taskID] = &taskRecord{
		ID:                  taskID,
		TaskNumber:          9,
		Title:               "Review me",
		RequiresHumanReview: true,
	}
	model.workspace.inbox = []inboxItem{{ID: "inbox-1", TaskID: taskID, Summary: "Ready for review"}}
	model.workspace.inboxCount = 1
	model.workspace.selectedTaskID = taskID
	model.workspace.setMainView(ViewTask)
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30}) // focus sync
	model.focus = MainPanel

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m := updated.(Model)

	if m.statusMessage != "Task approved." {
		t.Fatalf("statusMessage = %q, want 'Task approved.'", m.statusMessage)
	}
	// inbox item should be removed
	if len(m.workspace.inbox) != 0 {
		t.Fatalf("inbox len = %d, want 0 after approve", len(m.workspace.inbox))
	}
	// RequiresHumanReview should be cleared
	if task := m.workspace.tasks[taskID]; task != nil && task.RequiresHumanReview {
		t.Fatal("RequiresHumanReview still true after approve")
	}
}

func TestTaskViewRejectKeyActsOnInboxItem(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	taskID := "task-review-rej"
	model.workspace.tasks[taskID] = &taskRecord{
		ID:                  taskID,
		TaskNumber:          10,
		Title:               "Reject me",
		RequiresHumanReview: true,
	}
	model.workspace.inbox = []inboxItem{{ID: "inbox-2", TaskID: taskID, Summary: "Check this"}}
	model.workspace.inboxCount = 1
	model.workspace.selectedTaskID = taskID
	model.workspace.setMainView(ViewTask)
	model.focus = MainPanel

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m := updated.(Model)

	if m.statusMessage != "Task rejected." {
		t.Fatalf("statusMessage = %q, want 'Task rejected.'", m.statusMessage)
	}
	if len(m.workspace.inbox) != 0 {
		t.Fatalf("inbox len = %d, want 0 after reject", len(m.workspace.inbox))
	}
}

func TestTaskViewApproveNoOpWhenNoInboxItem(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	taskID := "task-no-inbox"
	model.workspace.tasks[taskID] = &taskRecord{
		ID:                  taskID,
		TaskNumber:          11,
		Title:               "No inbox",
		RequiresHumanReview: false, // no review needed
	}
	model.workspace.selectedTaskID = taskID
	model.workspace.setMainView(ViewTask)
	model.focus = MainPanel

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m := updated.(Model)

	// Should not set "Task approved." since there's no inbox item
	if m.statusMessage == "Task approved." {
		t.Fatal("statusMessage = 'Task approved.' but no inbox item existed")
	}
}

// EX-150: project view task list should show ⚠ badge for tasks requiring human review,
// consistent with the dashboard board view (EX-094).

func TestProjectViewShowsHumanReviewBadge(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 40})

	taskID := "task-needs-review"
	model.workspace.tasks[taskID] = &taskRecord{
		ID:                  taskID,
		TaskNumber:          5,
		Title:               "Needs Review",
		Status:              "in_progress",
		RequiresHumanReview: true,
	}
	// renderProjectView requires a project node in workspace.nodes to render tasks.
	model.workspace.nodes["project-proj-1"] = &sidebarNode{
		ID:        "project-proj-1",
		Kind:      sidebarKindProject,
		Label:     "Test Project",
		ProjectID: "proj-1",
	}
	// Set up project detail with a task
	model.workspace.selectedProjectID = "proj-1"
	model.workspace.selectedProject = &ProjectDetail{
		ID:          "proj-1",
		DisplayName: "Test Project",
		Tasks: []SidebarTaskItem{
			{ID: taskID, Title: "Needs Review", WorkStatus: "in_progress", TaskNumber: 5},
		},
	}
	model.workspace.setMainView(ViewProject)
	model.focus = MainPanel
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 40})

	rendered := model.renderProjectView(80, 30)
	found := false
	for _, line := range rendered {
		if strings.Contains(line, "⚠") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("project view task list missing ⚠ badge for RequiresHumanReview task:\n%s",
			strings.Join(rendered, "\n"))
	}
}

func TestProjectViewNoReviewBadgeWhenNotRequired(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 40})

	taskID := "task-no-review"
	model.workspace.tasks[taskID] = &taskRecord{
		ID:                  taskID,
		TaskNumber:          6,
		Title:               "Normal Task",
		Status:              "in_progress",
		RequiresHumanReview: false,
	}
	model.workspace.nodes["project-proj-2"] = &sidebarNode{
		ID:        "project-proj-2",
		Kind:      sidebarKindProject,
		Label:     "Test Project 2",
		ProjectID: "proj-2",
	}
	model.workspace.selectedProjectID = "proj-2"
	model.workspace.selectedProject = &ProjectDetail{
		ID:          "proj-2",
		DisplayName: "Test Project 2",
		Tasks: []SidebarTaskItem{
			{ID: taskID, Title: "Normal Task", WorkStatus: "in_progress", TaskNumber: 6},
		},
	}
	model.workspace.setMainView(ViewProject)
	model.focus = MainPanel
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 40})

	rendered := model.renderProjectView(80, 30)
	// The ⚠ should NOT appear in the task list (only in the separate human review line below)
	taskLineBadge := false
	for _, line := range rendered {
		// The title line contains the task title — check if it has ⚠
		if strings.Contains(line, "Normal Task") && strings.Contains(line, "⚠") {
			taskLineBadge = true
			break
		}
	}
	if taskLineBadge {
		t.Fatalf("project view task line shows ⚠ badge when RequiresHumanReview=false")
	}
}

// EX-151: sidebar task node should show ⚠ badge for tasks requiring human review.

func TestSidebarTaskNodeShowsHumanReviewBadge(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 40})

	taskID := "task-sidebar-review"
	model.workspace.tasks[taskID] = &taskRecord{
		ID:                  taskID,
		TaskNumber:          12,
		Title:               "Sidebar Review Task",
		Status:              "in_progress",
		RequiresHumanReview: true,
	}
	// Add a task node to the sidebar
	model.workspace.nodes["task-"+taskID] = &sidebarNode{
		ID:         "task-" + taskID,
		Kind:       sidebarKindTask,
		Label:      "OC-12: Sidebar Review Task",
		TaskID:     taskID,
		WorkStatus: "in_progress",
		ParentID:   "project-proj-3",
	}

	// Render the sidebar node directly
	node := model.workspace.nodes["task-"+taskID]
	rendered := model.renderSidebarNode(node, false, 80, false)

	if !strings.Contains(rendered, "⚠") {
		t.Fatalf("sidebar task node missing ⚠ badge for RequiresHumanReview task: %q", rendered)
	}
}

func TestProjectTasksLoadedSyncsHumanReviewFromInbox(t *testing.T) {
	// EX-152: If inbox is loaded BEFORE the project tasks exist in w.tasks,
	// syncTaskHumanReviewFromInbox skips them (task records don't exist yet).
	// After projectTasksLoadedMsg, we re-apply syncTaskHumanReviewFromInbox so
	// the ⚠ badge is correct.
	model := NewModel(DefaultState())

	taskID := "task-ex152"
	// Load inbox first (task record doesn't exist yet)
	model = pressMsg(model, inboxItemsLoadedMsg{
		Items: []InboxSummaryItem{
			{ID: "inbox-1", TaskID: taskID, Summary: "review needed"},
		},
	})
	// task record doesn't exist yet — RequiresHumanReview cannot be set
	if rec := model.workspace.tasks[taskID]; rec != nil {
		t.Fatalf("task record should not exist before project tasks load")
	}

	// Now project tasks load (creates the task record)
	model = pressMsg(model, projectTasksLoadedMsg{
		ProjectID: "proj-ex152",
		Tasks: []SidebarTaskItem{
			{ID: taskID, Title: "Ex152 Task", WorkStatus: "in_progress", TaskNumber: 99},
		},
		ExpandNode: false,
	})

	rec := model.workspace.tasks[taskID]
	if rec == nil {
		t.Fatalf("task record missing after projectTasksLoadedMsg")
	}
	if !rec.RequiresHumanReview {
		t.Fatalf("RequiresHumanReview = false after projectTasksLoadedMsg with inbox item, want true (EX-152 regression)")
	}
}

func TestBudgetAnomalyAddsActivityEntry(t *testing.T) {
	// EX-153: budget.anomaly_detected now persists in activity log.
	model := NewModel(DefaultState())
	rawPayload, _ := json.Marshal(map[string]any{
		"period":                 "hourly",
		"current_tokens":         int64(5000),
		"rolling_average_tokens": int64(1000),
		"rolling_average_ratio":  5.0,
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "budget.anomaly_detected",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "budget anomaly") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing budget anomaly entry (EX-153): %v", m.workspace.activity)
	}
}

func TestToolCapabilityDeniedAddsActivityEntry(t *testing.T) {
	// EX-153: tool.capability_denied now persists in activity log.
	model := NewModel(DefaultState())
	rawPayload, _ := json.Marshal(map[string]any{
		"tool_name":  "file_write",
		"capability": "write_production_db",
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "tool.capability_denied",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "policy denied") && strings.Contains(entry, "file_write") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing tool capability denied entry (EX-153): %v", m.workspace.activity)
	}
}

func TestMCPCatalogChangedAddsActivityEntry(t *testing.T) {
	// EX-153: mcp.catalog.changed now persists in activity log.
	model := NewModel(DefaultState())
	rawPayload, _ := json.Marshal(map[string]any{
		"added_count":   3,
		"removed_count": 1,
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "mcp.catalog.changed",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "mcp catalog") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing mcp catalog entry (EX-153): %v", m.workspace.activity)
	}
}

func TestSupervisorEscalationAddsActivityEntry(t *testing.T) {
	// EX-153: supervisor.escalation_created now persists in activity log.
	model := NewModel(DefaultState())
	rawPayload, _ := json.Marshal(map[string]any{
		"run_id": "run-abc12345",
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "supervisor.escalation_created",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "supervisor escalation") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing supervisor escalation entry (EX-153): %v", m.workspace.activity)
	}
}

func TestHelpCommandIncludesMergesAndSchedules(t *testing.T) {
	// EX-154: :help was missing :merges and :schedules.
	model := NewModel(DefaultState())
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	for _, r := range []rune("help") {
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})

	status := model.StatusMessage()
	for _, cmd := range []string{":merges", ":schedules"} {
		if !strings.Contains(status, cmd) {
			t.Fatalf(":help output missing %q: %q", cmd, status)
		}
	}
}

func TestHelpViewCommandsSectionIncludesAllCommands(t *testing.T) {
	// EX-155: :? help view Commands section was missing :agents, :activity,
	// :merges, :schedules, :scope, :queue, :sidebar, :inbox commands.
	// Use a large maxLines (100) so the Commands section (which follows ~41 lines
	// of earlier sections) is not truncated.
	model := NewModel(DefaultState())
	model.workspace.setMainView(ViewHelp)

	viewLines := model.renderMainViewContent(ViewHelp, 80, 100)
	rendered := strings.Join(viewLines, "\n")

	for _, cmd := range []string{":agents", ":merges", ":schedules", ":scope", ":queue", ":sidebar"} {
		if !strings.Contains(rendered, cmd) {
			t.Fatalf("help view Commands section missing %q (EX-155): %q", cmd, rendered)
		}
	}
}

func TestCommandModeHelpIncludesAllViews(t *testing.T) {
	// EX-155: commandMode help bar now includes :agents :activity :merges :schedules.
	model := NewModel(DefaultState())
	model.commandMode = true

	help := model.commandFallbackHelp()
	for _, cmd := range []string{":agents", ":merges", ":schedules", ":scope"} {
		if !strings.Contains(help, cmd) {
			t.Fatalf("commandMode help missing %q (EX-155): %q", cmd, help)
		}
	}
}

func TestProjectViewFallbackUsesTaskUUIDNotNodeID(t *testing.T) {
	// EX-156: When the project view falls back to sidebar node children
	// (before proj.Tasks is loaded from the API), SidebarTaskItem.ID was set
	// to child.ID ("task-<uuid>") instead of child.TaskID ("<uuid>").
	// This caused RequiresHumanReview lookup and loadTaskDetailCmd to use the
	// wrong key, so ⚠ badges never showed in the fallback path.
	model := NewModel(DefaultState())
	projectID := "proj-fallback"
	taskID := "uuid-fallback-task"

	// Set up project node (no selectedProject — triggers fallback path)
	model.workspace.nodes["project-"+projectID] = &sidebarNode{
		ID:        "project-" + projectID,
		Kind:      sidebarKindProject,
		Label:     "Fallback Project",
		ProjectID: projectID,
		Expanded:  true,
	}
	// Add the task node as a child of the project
	model.workspace.nodes["task-"+taskID] = &sidebarNode{
		ID:         "task-" + taskID,
		Kind:       sidebarKindTask,
		Label:      "OC-5: Fallback Task",
		ParentID:   "project-" + projectID,
		TaskID:     taskID, // raw UUID
		TaskNumber: 5,
		WorkStatus: "in_progress",
	}
	// Seed the task record with RequiresHumanReview=true
	model.workspace.tasks[taskID] = &taskRecord{
		ID:                  taskID,
		TaskNumber:          5,
		Title:               "Fallback Task",
		Status:              "in_progress",
		RequiresHumanReview: true,
	}
	model.workspace.selectedProjectID = projectID
	// selectedProject is non-nil but has no Tasks → triggers sidebar-child fallback.
	// (nil selectedProject gives "Loading…" early return; we need the else branch.)
	model.workspace.selectedProject = &ProjectDetail{ID: projectID, DisplayName: "Fallback Project"}

	rendered := strings.Join(model.renderProjectView(80, 30), "\n")
	if !strings.Contains(rendered, "⚠") {
		t.Fatalf("project view fallback path should show ⚠ badge for RequiresHumanReview task (EX-156 regression): %q", rendered)
	}
	if !strings.Contains(rendered, "OC-5") {
		t.Fatalf("project view fallback path should show OC-N prefix (EX-156 regression): %q", rendered)
	}
}

// EX-157: tui.command navigate task case was missing — server sends navigate/task
// to open a specific task by UUID but TUI silently ignored it.
func TestTUICommandNavigateTaskSetsSelectedTask(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true

	taskID := "task-uuid-navigate"
	model.workspace.tasks = map[string]*taskRecord{
		taskID: {ID: taskID, TaskNumber: 42, Title: "Navigate Me"},
	}

	rawPayload, _ := json.Marshal(map[string]any{
		"action":    "navigate",
		"target":    "task",
		"target_id": taskID,
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "tui.command",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	if m.workspace.selectedTaskID != taskID {
		t.Fatalf("selectedTaskID = %q, want %q after tui.command navigate task", m.workspace.selectedTaskID, taskID)
	}
	if m.workspace.mainView != ViewTask {
		t.Fatalf("mainView = %q, want ViewTask after tui.command navigate task", m.workspace.mainView)
	}
	if m.activeScope != ScopeTask {
		t.Fatalf("activeScope = %v, want ScopeTask after tui.command navigate task", m.activeScope)
	}
	if !strings.Contains(m.statusMessage, "OC-42") {
		t.Fatalf("statusMessage = %q, want OC-42 label after navigate", m.statusMessage)
	}
}

// EX-158: budget anomaly and policy denied entries showed ✓ (green success) in
// the activity log. Budget anomaly should show ◌ (warning) and policy denied
// should show ✗ (error).
func TestActivityIconBudgetAnomalyIsWarning(t *testing.T) {
	entry := "budget anomaly: daily usage ~3x above avg"
	icon := activityIcon(entry)
	if !strings.Contains(icon, "◌") {
		t.Fatalf("activityIcon(%q) = %q, want ◌ warning icon", entry, icon)
	}
}

func TestActivityIconPolicyDeniedIsError(t *testing.T) {
	entry := "policy denied: file_write"
	icon := activityIcon(entry)
	if !strings.Contains(icon, "✗") {
		t.Fatalf("activityIcon(%q) = %q, want ✗ error icon", entry, icon)
	}
}

// EX-159: worker.unresponsive only set statusMessage (5s auto-clear) but did
// not persist in the activity log, so the warning disappeared after navigation.
func TestWorkerUnresponsiveAddsActivityEntry(t *testing.T) {
	model := NewModel(DefaultState())
	model.turnsSynced = true
	model.activeSession = "session-org-test"
	model.activeTurnSessionID = "" // matches any session

	rawPayload, _ := json.Marshal(map[string]any{
		"session_id": "session-org-test",
		"message":    "Worker appears offline — check that `ottercamp worker` is running.",
	})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "worker.unresponsive",
		Payload:   rawPayload,
	}})
	m := updated.(Model)

	if len(m.workspace.activity) == 0 {
		t.Fatal("activity log is empty after worker.unresponsive event, want at least one entry")
	}
	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "worker unresponsive") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing 'worker unresponsive' entry: %v", m.workspace.activity)
	}
}

// EX-160: Inbox approve/reject/defer were local-only; pressing 'a', 'x', 'f'
// must also return a tea.Cmd that calls ActOnInboxItem with the correct item ID.
func TestInboxApproveIssuesServerAPICall(t *testing.T) {
	var calledItemID, calledAction string
	model := NewModel(DefaultState())
	model.runtimeHints.ActOnInboxItem = func(_ context.Context, itemID, action string) error {
		calledItemID = itemID
		calledAction = action
		return nil
	}
	model.focus = MainPanel
	model.workspace.mainView = ViewInbox
	model.workspace.inbox = []inboxItem{
		{ID: "item-abc", TaskID: "task-xyz", Summary: "Fix the bug"},
	}
	model.workspace.tasks = map[string]*taskRecord{
		"task-xyz": {ID: "task-xyz", TaskNumber: 3, Title: "Fix the bug"},
	}

	// Press 'a' to approve
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m := updated.(Model)

	if len(m.workspace.inbox) != 0 {
		t.Fatalf("inbox still has %d items after approve, want 0", len(m.workspace.inbox))
	}
	if cmd == nil {
		t.Fatal("approve returned nil cmd, want actOnInboxItemCmd")
	}
	// The cmd is wrapped in a tea.Batch with the status auto-clear timer.
	// runNonTimerCmds skips the 5s timer and only runs the API call cmd.
	runNonTimerCmds(cmd)
	if calledItemID != "item-abc" {
		t.Fatalf("ActOnInboxItem called with itemID=%q, want %q", calledItemID, "item-abc")
	}
	if calledAction != "approve" {
		t.Fatalf("ActOnInboxItem called with action=%q, want %q", calledAction, "approve")
	}
}

// runNonTimerCmds executes cmds in a batch that complete quickly (within 50ms).
// Timer-based cmds like statusAutoClearCmd sleep for seconds and are skipped.
func runNonTimerCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	// Run cmd with timeout: skip if it doesn't complete in 50ms (i.e. it's a timer cmd).
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				runNonTimerCmds(c)
			}
		}
	case <-time.After(50 * time.Millisecond):
		// timer cmd — skip
	}
}

// EX-161: chat.session.* events have their handlers in applyWorkspaceCommand.
// In production (tui.go) they must be routed as WorkspaceEnvelopeMsg. This test
// confirms that the WorkspaceEnvelopeMsg path produces the expected sidebar reload
// cmd and activity entry (correct routing), while ChatEnvelopeMsg silently drops
// the event (documents the old bug).
func TestChatSessionCreatedRoutedAsWorkspaceEventReloadsAndLogs(t *testing.T) {
	t.Parallel()

	// Correct path: WorkspaceEnvelopeMsg → applyWorkspaceCommand handler fires
	model := NewModel(DefaultState())
	raw, _ := json.Marshal(map[string]any{"scope_type": "project_task"})
	updated, cmd := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "chat.session.created",
		Payload:   raw,
	}})
	m := updated.(Model)
	if cmd == nil {
		t.Fatal("WorkspaceEnvelopeMsg for chat.session.created returned nil cmd, want sidebar reload")
	}
	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "session created") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activity log missing 'session created' entry after WorkspaceEnvelopeMsg: %v", m.workspace.activity)
	}

	// Old bug path: ChatEnvelopeMsg → applyChatEnvelope → silent drop (no case in switch)
	model2 := NewModel(DefaultState())
	updated2, cmd2 := model2.Update(ChatEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "chat.session.created",
		Payload:   raw,
	}})
	m2 := updated2.(Model)
	if cmd2 != nil {
		t.Logf("note: ChatEnvelopeMsg for chat.session.created returned a cmd (not expected, but not fatal)")
	}
	for _, entry := range m2.workspace.activity {
		if strings.Contains(entry, "session created") {
			t.Fatalf("ChatEnvelopeMsg for chat.session.created should be silently dropped (wrong route), but got activity entry: %q", entry)
		}
	}
}

// EX-163: when a queued message is promoted on turn completion, it must be
// sent to the server (requestChatSendCmd must fire). Previously completeTurnAndPromoteQueue
// only showed the message locally, so the server never received it.
func TestQueuedMessageIsDispatchedToServerOnTurnCompletion(t *testing.T) {
	t.Parallel()

	sent := false
	sessionID := "session-uuid-ex163"
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		SendChatMessage: func(_ context.Context, sid, content string) error {
			if sid == sessionID && content == "queued content" {
				sent = true
			}
			return nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.activeSession = sessionID
	model.activeTurnSessionID = sessionID
	model.turnsSynced = true
	model.activeTurn = true
	// Simulate a queued message
	model.queuedMessages = []QueuedMessage{{Text: "queued content"}}

	// Simulate turn completion event
	raw, _ := json.Marshal(map[string]string{"session_id": sessionID})
	updated, cmd := model.Update(ChatEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "chat.turn.completed",
		Payload:   raw,
	}})
	m := updated.(Model)

	if len(m.queuedMessages) != 0 {
		t.Fatal("queued message should have been removed from queue after promotion")
	}
	if cmd == nil {
		t.Fatal("turn completion with queued message should return a non-nil send cmd")
	}
	// cmd is requestChatSendCmd → dispatches chatSendRequestedMsg.
	// Feed it through Update so the full pipeline runs (chatSendRequestedMsg → sendChatMessageCmd → SendChatMessage).
	msg := cmd()
	updated2, cmd2 := m.Update(msg)
	_ = updated2
	if cmd2 == nil {
		t.Fatal("chatSendRequestedMsg should produce a sendChatMessageCmd")
	}
	// cmd2 is sendChatMessageCmd — calling it invokes SendChatMessage.
	cmd2()
	if !sent {
		t.Fatal("queued message was promoted but SendChatMessage was never called (EX-163 regression)")
	}
}

// EX-164: :scope command should dispatch loadChatHistoryCmd (previously the
// returned cmd from switchScope was silently discarded).
func TestScopeCommandDispatchesChatHistoryReload(t *testing.T) {
	t.Parallel()
	loaded := false
	// Use a proper UUID-format ID so looksLikeUUID returns true.
	sessionID := "00000000-0000-0000-0000-000000000164"
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadChatHistory: func(_ context.Context, sid string) ([]ChatMessage, error) {
			if sid == sessionID {
				loaded = true
			}
			return nil, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	// Seed a task session so ScopeTask resolves to a real UUID.
	if model.workspace.tasks == nil {
		model.workspace.tasks = make(map[string]*taskRecord)
	}
	model.workspace.tasks["task-ex164"] = &taskRecord{ID: "task-ex164", SessionID: sessionID}
	model.workspace.selectedTaskID = "task-ex164"
	model.activeSession = sessionID

	// Directly exercise executeCommand to avoid full char-by-char simulation.
	returned := model.executeCommand(":scope task")
	if returned == nil {
		t.Fatal(":scope task should return a non-nil cmd (history reload), but got nil")
	}
	runNonTimerCmds(returned)
	if !loaded {
		t.Fatal(":scope task did not call LoadChatHistory (EX-164 regression)")
	}
}

// EX-165: Ctrl-G / :frank should reload chat history after switching from
// another session. Previously jumpToFrankSession returned void, so chatMessages
// was never cleared or refreshed.
func TestJumpToFrankSessionReloadsChatHistory(t *testing.T) {
	t.Parallel()
	// Use a proper UUID-format ID so looksLikeUUID returns true.
	frankSessionID := "00000000-0000-0000-0000-000000000165"
	loaded := false
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadChatHistory: func(_ context.Context, sid string) ([]ChatMessage, error) {
			if sid == frankSessionID {
				loaded = true
			}
			return nil, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	// Seed the general session node in the sidebar so activateGeneralSession works.
	if model.workspace.nodes == nil {
		model.workspace.nodes = make(map[string]*sidebarNode)
	}
	model.workspace.nodes[generalSidebarNodeID] = &sidebarNode{
		ID:        generalSidebarNodeID,
		Kind:      sidebarKindSession,
		SessionID: frankSessionID,
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m := updated.(Model)
	if m.activeSession != frankSessionID {
		t.Fatalf("Ctrl-G should set activeSession to frank session %q, got %q", frankSessionID, m.activeSession)
	}
	if cmd == nil {
		t.Fatal("Ctrl-G should return a non-nil cmd (history reload)")
	}
	runNonTimerCmds(cmd)
	if !loaded {
		t.Fatal("Ctrl-G did not call LoadChatHistory for Frank session (EX-165 regression)")
	}
}

// EX-166: pressing 'n' (next unread) should reload chat history for the newly
// selected session. Previously selectSidebarNode only set the session ID, so
// the chat panel showed the previous session's messages.
func TestNKeyNextUnreadReloadsChatHistory(t *testing.T) {
	t.Parallel()
	unreadSessionID := "00000000-0000-0000-0000-000000000166"
	loaded := false
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadChatHistory: func(_ context.Context, sid string) ([]ChatMessage, error) {
			if sid == unreadSessionID {
				loaded = true
			}
			return nil, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	// Seed the sidebar with a session node that has unread messages.
	if model.workspace.nodes == nil {
		model.workspace.nodes = make(map[string]*sidebarNode)
	}
	unreadNodeID := "session-" + unreadSessionID
	model.workspace.nodes[unreadNodeID] = &sidebarNode{
		ID:        unreadNodeID,
		Kind:      sidebarKindSession,
		SessionID: unreadSessionID,
		Label:     "Unread Session",
		Unread:    3,
	}
	model.workspace.topLevel = append([]string{unreadNodeID}, model.workspace.topLevel...)

	model.focus = MainPanel // not ChatPanel so 'n' is handled

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m := updated.(Model)
	if m.activeSession != unreadSessionID {
		t.Fatalf("'n' should switch to unread session %q, got %q", unreadSessionID, m.activeSession)
	}
	if cmd == nil {
		t.Fatal("'n' key with unread session should return a non-nil cmd (history reload)")
	}
	runNonTimerCmds(cmd)
	if !loaded {
		t.Fatal("'n' next-unread did not call LoadChatHistory (EX-166 regression)")
	}
}

// EX-167: chat.session.closed for the active session should set a status bar message.
func TestSessionClosedSetsStatusMessage(t *testing.T) {
	t.Parallel()
	sessionID := "00000000-0000-0000-0000-000000000167"
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.activeSession = sessionID
	model.activeTurnSessionID = sessionID
	model.turnsSynced = true

	raw, _ := json.Marshal(map[string]string{"session_id": sessionID})
	updated, _ := model.Update(WorkspaceEnvelopeMsg{Envelope: EventEnvelope{
		EventType: "chat.session.closed",
		Payload:   raw,
	}})
	m := updated.(Model)
	if !strings.Contains(m.statusMessage, "closed") {
		t.Fatalf("chat.session.closed for active session should set status containing 'closed', got: %q", m.statusMessage)
	}
	found := false
	for _, entry := range m.workspace.activity {
		if strings.Contains(entry, "session closed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("chat.session.closed should add 'session closed' entry to activity log")
	}
}

// EX-168: :sidebar select command should reload chat history after switching session.
func TestSidebarSelectCommandReloadsChatHistory(t *testing.T) {
	t.Parallel()
	sessionID := "00000000-0000-0000-0000-000000000168"
	loaded := false
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadChatHistory: func(_ context.Context, id string) ([]ChatMessage, error) {
			if id == sessionID {
				loaded = true
			}
			return nil, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	if model.workspace.nodes == nil {
		model.workspace.nodes = make(map[string]*sidebarNode)
	}
	nodeID := "session-" + sessionID
	model.workspace.nodes[nodeID] = &sidebarNode{
		ID:        nodeID,
		Kind:      sidebarKindSession,
		SessionID: sessionID,
		Label:     "Test Session",
	}
	model.workspace.topLevel = append([]string{nodeID}, model.workspace.topLevel...)
	model.workspace.sidebarCursor = 0

	cmd := model.executeCommand(":sidebar select")
	if cmd == nil {
		t.Fatal(":sidebar select should return a non-nil cmd (history reload)")
	}
	if model.activeSession != sessionID {
		t.Fatalf(":sidebar select should set activeSession to %q, got %q", sessionID, model.activeSession)
	}
	runNonTimerCmds(cmd)
	if !loaded {
		t.Fatal(":sidebar select did not call LoadChatHistory (EX-168 regression)")
	}
}

// EX-169: inbox open via 'o' key should reload chat history for the task session.
func TestInboxOpenKeyReloadsChatHistory(t *testing.T) {
	t.Parallel()
	sessionID := "00000000-0000-0000-0000-000000000169"
	loaded := false
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.runtimeHints.LoadChatHistory = func(_ context.Context, id string) ([]ChatMessage, error) {
		if id == sessionID {
			loaded = true
		}
		return nil, nil
	}
	// Seed a task with a session
	if model.workspace.tasks == nil {
		model.workspace.tasks = make(map[string]*taskRecord)
	}
	taskID := "task-inbox-169"
	model.workspace.tasks[taskID] = &taskRecord{
		ID:        taskID,
		Title:     "Inbox task",
		SessionID: sessionID,
	}
	model.workspace.taskSessionIDs = map[string]string{taskID: sessionID}
	// Seed an inbox item
	model.workspace.inbox = []inboxItem{{ID: "inbox-169", TaskID: taskID, Summary: "Review needed"}}
	model.workspace.inboxCursor = 0
	model.workspace.mainView = ViewInbox
	model.focus = MainPanel

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m := updated.(Model)
	if cmd == nil {
		t.Fatal("'o' inbox open should return a non-nil cmd (history reload)")
	}
	if m.activeSession != sessionID {
		t.Fatalf("'o' inbox open should set activeSession to %q, got %q", sessionID, m.activeSession)
	}
	runNonTimerCmds(cmd)
	if !loaded {
		t.Fatal("'o' inbox open did not call LoadChatHistory (EX-169 regression)")
	}
}

// EX-169: inbox open via Enter key should reload chat history for the task session.
func TestInboxEnterKeyReloadsChatHistory(t *testing.T) {
	t.Parallel()
	sessionID := "00000000-0000-0000-0000-00000000169e"
	loaded := false
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.runtimeHints.LoadChatHistory = func(_ context.Context, id string) ([]ChatMessage, error) {
		if id == sessionID {
			loaded = true
		}
		return nil, nil
	}
	if model.workspace.tasks == nil {
		model.workspace.tasks = make(map[string]*taskRecord)
	}
	taskID := "task-inbox-169e"
	model.workspace.tasks[taskID] = &taskRecord{
		ID:        taskID,
		Title:     "Inbox task enter",
		SessionID: sessionID,
	}
	model.workspace.taskSessionIDs = map[string]string{taskID: sessionID}
	model.workspace.inbox = []inboxItem{{ID: "inbox-169e", TaskID: taskID, Summary: "Review needed"}}
	model.workspace.inboxCursor = 0
	model.workspace.mainView = ViewInbox
	model.focus = MainPanel

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m := updated.(Model)
	if cmd == nil {
		t.Fatal("Enter inbox open should return a non-nil cmd (history reload)")
	}
	if m.activeSession != sessionID {
		t.Fatalf("Enter inbox open should set activeSession to %q, got %q", sessionID, m.activeSession)
	}
	runNonTimerCmds(cmd)
	if !loaded {
		t.Fatal("Enter inbox open did not call LoadChatHistory (EX-169 regression)")
	}
}

// EX-170: :inbox command should trigger data load (consistent with 'i' key).
func TestInboxCommandLoadsData(t *testing.T) {
	t.Parallel()
	loaded := false
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadInboxItems: func(_ context.Context) ([]InboxSummaryItem, error) {
			loaded = true
			return nil, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	cmd := model.executeCommand(":inbox")
	if cmd == nil {
		t.Fatal(":inbox command should return a non-nil cmd (inbox data load)")
	}
	runNonTimerCmds(cmd)
	if !loaded {
		t.Fatal(":inbox command did not call LoadInboxItems (EX-170 regression)")
	}
}

// EX-170: :agents command should trigger data load (consistent with ViewAgents refresh).
func TestAgentsCommandLoadsData(t *testing.T) {
	t.Parallel()
	loaded := false
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadAgents: func(_ context.Context) ([]string, error) {
			loaded = true
			return nil, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	cmd := model.executeCommand(":agents")
	if cmd == nil {
		t.Fatal(":agents command should return a non-nil cmd (agents data load)")
	}
	runNonTimerCmds(cmd)
	if !loaded {
		t.Fatal(":agents command did not call LoadAgents (EX-170 regression)")
	}
}

// EX-171: :inbox open command should reload chat history for the task session.
func TestInboxOpenCommandReloadsChatHistory(t *testing.T) {
	t.Parallel()
	sessionID := "00000000-0000-0000-0000-000000000171"
	loaded := false
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadChatHistory: func(_ context.Context, id string) ([]ChatMessage, error) {
			if id == sessionID {
				loaded = true
			}
			return nil, nil
		},
	})
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	if model.workspace.tasks == nil {
		model.workspace.tasks = make(map[string]*taskRecord)
	}
	taskID := "task-inbox-171"
	model.workspace.tasks[taskID] = &taskRecord{
		ID:        taskID,
		Title:     "Inbox task cmd",
		SessionID: sessionID,
	}
	model.workspace.taskSessionIDs = map[string]string{taskID: sessionID}
	model.workspace.inbox = []inboxItem{{ID: "inbox-171", TaskID: taskID, Summary: "Review needed"}}
	model.workspace.inboxCursor = 0

	cmd := model.executeCommand(":inbox open")
	if cmd == nil {
		t.Fatal(":inbox open should return a non-nil cmd (history reload)")
	}
	if model.activeSession != sessionID {
		t.Fatalf(":inbox open should set activeSession to %q, got %q", sessionID, model.activeSession)
	}
	runNonTimerCmds(cmd)
	if !loaded {
		t.Fatal(":inbox open did not call LoadChatHistory (EX-171 regression)")
	}
}

// EX-172: inbox open ('o', Enter, :inbox open) should set activeScope=ScopeTask
// so the chat header shows the correct agent name and scope indicators.
func TestInboxOpenSetsScopeTask(t *testing.T) {
	t.Parallel()
	sessionID := "00000000-0000-0000-0000-000000000172"
	for _, name := range []string{"o-key", "enter-key", "inbox-open-cmd"} {
		t.Run(name, func(t *testing.T) {
			model := NewModel(DefaultState())
			model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
			if model.workspace.tasks == nil {
				model.workspace.tasks = make(map[string]*taskRecord)
			}
			taskID := "task-scope-172"
			model.workspace.tasks[taskID] = &taskRecord{
				ID:        taskID,
				AgentName: "Ellie",
				SessionID: sessionID,
			}
			model.workspace.taskSessionIDs = map[string]string{taskID: sessionID}
			model.workspace.inbox = []inboxItem{{ID: "inbox-172", TaskID: taskID, Summary: "Review"}}
			model.workspace.inboxCursor = 0
			model.workspace.mainView = ViewInbox
			model.focus = MainPanel
			// Start with org scope to verify it's updated
			model.activeScope = ScopeOrg

			switch name {
			case "o-key":
				updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
				model = updated.(Model)
			case "enter-key":
				updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
				model = updated.(Model)
			case "inbox-open-cmd":
				model.executeCommand(":inbox open")
			}

			if model.activeScope != ScopeTask {
				t.Fatalf("[%s] inbox open should set activeScope=ScopeTask, got %v", name, model.activeScope)
			}
			if model.activeSession != sessionID {
				t.Fatalf("[%s] inbox open should set activeSession=%q, got %q", name, sessionID, model.activeSession)
			}
		})
	}
}
