//go:build integration

package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestResizeTransitionsRemainStable(t *testing.T) {
	model := NewModel(DefaultState())
	sequence := []tea.WindowSizeMsg{
		{Width: 69, Height: 22},
		{Width: 90, Height: 30},
		{Width: 120, Height: 30},
		{Width: 160, Height: 34},
		{Width: 220, Height: 40},
		{Width: 100, Height: 21}, // height-based XS override
		{Width: 140, Height: 30},
	}

	for i, msg := range sequence {
		model = pressMsg(model, msg)
		_ = model.View()
		layout := model.CurrentLayout()
		if !layout.visible[model.FocusedPanel()] {
			t.Fatalf("step %d: focused panel %s hidden for size class %s", i, panelLabel(model.FocusedPanel()), layout.sizeClass)
		}

		model = pressKey(model, tea.KeyMsg{Type: tea.KeyTab})
		_ = model.View()
		layout = model.CurrentLayout()
		if !layout.visible[model.FocusedPanel()] {
			t.Fatalf("step %d after tab: focused panel %s hidden for size class %s", i, panelLabel(model.FocusedPanel()), layout.sizeClass)
		}
	}
}
