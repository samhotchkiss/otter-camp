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
