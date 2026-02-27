package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSidebarPanelShowsInboxFooterWhenInboxHasItems(t *testing.T) {
	model := NewModel(DefaultState())

	panel := model.renderSidebarPanel(56, 12, false)
	if !strings.Contains(panel, "▼ 2 inbox") {
		t.Fatalf("sidebar inbox footer missing: %q", panel)
	}
}

func TestSidebarPanelHidesInboxFooterWhenInboxEmpty(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.inbox = nil

	panel := model.renderSidebarPanel(56, 12, false)
	if strings.Contains(panel, "▼ ") && strings.Contains(panel, " inbox") {
		t.Fatalf("sidebar inbox footer should be hidden when empty: %q", panel)
	}
}

func TestSidebarPanelTrimsNodesBeforeInboxFooter(t *testing.T) {
	model := NewModel(DefaultState())

	panel := model.renderSidebarPanel(56, 6, false)
	if !strings.Contains(panel, "▼ 2 inbox") {
		t.Fatalf("sidebar inbox footer missing in constrained height: %q", panel)
	}
	if !strings.Contains(panel, "more") {
		t.Fatalf("expected truncated sidebar nodes indicator before inbox footer: %q", panel)
	}
}

func TestSidebarPanelUsesIconOnlyModeAtMediumNarrowWidths(t *testing.T) {
	model := NewModel(DefaultState())
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	model = updated.(Model)
	model.setFocus(SidebarPanel)

	panel := model.renderSidebarPanel(56, 12, true)
	if strings.Contains(panel, "Task 1 / Launch docs") {
		t.Fatalf("sidebar should not render full session labels in icon-only mode: %q", panel)
	}
	if !strings.Contains(panel, "T1") {
		t.Fatalf("sidebar icon-only mode missing compact task token: %q", panel)
	}
}
