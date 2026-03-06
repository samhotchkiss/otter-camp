package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestStatusBarHiddenPaneHintsStaySingleLineEX253(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 72, Height: 26})
	model.statusMessage = "snapshot"

	layout := model.CurrentLayout()
	bar := model.renderStatusBar(layout, model.FocusedPanel())

	if strings.Contains(bar, "\n") {
		t.Fatalf("status bar should remain single-line at XS width: %q", bar)
	}
	if !strings.Contains(bar, "Show: 1 sidebar") || !strings.Contains(bar, "3 chat") {
		t.Fatalf("status bar should keep hidden-pane hints actionable: %q", bar)
	}
	if strings.Contains(bar, "Hidden panes:") {
		t.Fatalf("status bar should use compact hidden-pane hints: %q", bar)
	}
}
