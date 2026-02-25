package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestGlobalFocusControls(t *testing.T) {
	model := NewModel(DefaultState())

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyTab})
	if model.FocusedPanel() != ChatPanel {
		t.Fatalf("focus after Tab = %v, want chat", model.FocusedPanel())
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyShiftTab})
	if model.FocusedPanel() != MainPanel {
		t.Fatalf("focus after Shift-Tab = %v, want main", model.FocusedPanel())
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	if model.FocusedPanel() != SidebarPanel {
		t.Fatalf("focus after Alt-1 = %v, want sidebar", model.FocusedPanel())
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}, Alt: true})
	if model.FocusedPanel() != MainPanel {
		t.Fatalf("focus after Alt-2 = %v, want main", model.FocusedPanel())
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}, Alt: true})
	if model.FocusedPanel() != ChatPanel {
		t.Fatalf("focus after Alt-3 = %v, want chat", model.FocusedPanel())
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

func pressKey(model Model, key tea.KeyMsg) Model {
	updated, _ := model.Update(key)
	next, ok := updated.(Model)
	if !ok {
		panic("unexpected model type")
	}
	return next
}
