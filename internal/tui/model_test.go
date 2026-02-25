package tui

import (
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

func pressKey(model Model, key tea.KeyMsg) Model {
	updated, _ := model.Update(key)
	next, ok := updated.(Model)
	if !ok {
		panic("unexpected model type")
	}
	return next
}

func pressMsg(model Model, msg tea.Msg) Model {
	updated, _ := model.Update(msg)
	next, ok := updated.(Model)
	if !ok {
		panic("unexpected model type")
	}
	return next
}
