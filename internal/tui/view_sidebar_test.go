package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSidebarPanelShowsInboxFooterWhenInboxHasItems(t *testing.T) {
	model := NewModel(DefaultState())
	// Set inbox count so the INBOX node shows the count.
	model.workspace.inboxCount = 2

	panel := model.renderSidebarPanel(56, 12, false)
	if !strings.Contains(panel, "INBOX") {
		t.Fatalf("sidebar INBOX node missing: %q", panel)
	}
	if !strings.Contains(panel, "(2)") {
		t.Fatalf("sidebar INBOX count badge missing: %q", panel)
	}
}

func TestSidebarPanelHidesInboxFooterWhenInboxEmpty(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.inbox = nil
	model.workspace.inboxCount = 0

	panel := model.renderSidebarPanel(56, 12, false)
	// INBOX node is always shown; just no count badge.
	if !strings.Contains(panel, "INBOX") {
		t.Fatalf("sidebar INBOX node should always be present: %q", panel)
	}
	if strings.Contains(panel, "(0)") {
		t.Fatalf("sidebar INBOX should not show (0) count: %q", panel)
	}
}

func TestSidebarPanelTrimsNodesBeforeInboxFooter(t *testing.T) {
	model := NewModel(DefaultState())
	// Seed several nodes so that some are trimmed at small height.
	model.workspace.rebuildSidebar(
		"",
		[]SidebarChatItem{
			{SessionID: "s1", DisplayName: "Chat 1"},
			{SessionID: "s2", DisplayName: "Chat 2"},
			{SessionID: "s3", DisplayName: "Chat 3"},
		},
		[]SidebarProjectItem{
			{ID: "p1", DisplayName: "Project 1"},
			{ID: "p2", DisplayName: "Project 2"},
		},
	)

	// Render at a height that can't fit all nodes.
	panel := model.renderSidebarPanel(56, 6, false)
	if !strings.Contains(panel, "+") && !strings.Contains(panel, "more") {
		t.Fatalf("expected truncated sidebar nodes indicator at constrained height: %q", panel)
	}
}

func TestSidebarPanelShowsChatSessionCount(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.rebuildSidebar(
		"",
		[]SidebarChatItem{
			{SessionID: "s1", DisplayName: "Blog Site"},
			{SessionID: "s2", DisplayName: "API Work"},
			{SessionID: "s3", DisplayName: "Design"},
		},
		nil,
	)

	panel := model.renderSidebarPanel(56, 20, false)
	// CHATS header should show the count of non-Frank sessions.
	if !strings.Contains(panel, "CHATS") {
		t.Fatalf("sidebar CHATS header missing: %q", panel)
	}
	if !strings.Contains(panel, "(3)") {
		t.Fatalf("sidebar CHATS count badge missing, want (3): %q", panel)
	}
}

func TestSidebarPanelKeepsCompactLabelsReadableAtTabletWidthEX253(t *testing.T) {
	model := NewModel(DefaultState())
	model.workspace.rebuildSidebar(
		"",
		[]SidebarChatItem{{SessionID: "s1", DisplayName: "Frank / General"}},
		[]SidebarProjectItem{{ID: "p1", DisplayName: "Product Rebuild"}},
	)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 34})
	model = updated.(Model)
	model.setFocus(SidebarPanel)

	layout := model.CurrentLayout()
	panel := model.renderSidebarPanel(layout.widths[SidebarPanel]-2, 12, true)
	if !strings.Contains(panel, "INBOX") {
		t.Fatalf("sidebar should keep INBOX readable at tablet width: %q", panel)
	}
	if !strings.Contains(panel, "CHATS") {
		t.Fatalf("sidebar should keep CHATS readable at tablet width: %q", panel)
	}
	if !strings.Contains(panel, "Frank") {
		t.Fatalf("sidebar should keep session labels readable at tablet width: %q", panel)
	}
	if strings.Contains(panel, "PRJ") || strings.Contains(panel, "GEN") {
		t.Fatalf("sidebar should avoid opaque compact tokens at tablet width: %q", panel)
	}
}

// TestSidebarScrollsToKeepCursorVisible verifies EX-195: when the cursor is
// beyond the visible window, the sidebar viewport scrolls so the cursor row
// is always rendered.
func TestSidebarScrollsToKeepCursorVisible(t *testing.T) {
	t.Parallel()
	model := NewModel(DefaultState())
	// Seed 4 recent chat sessions (max allowed by rebuildSidebar) and 4 projects
	// to produce a sidebar tall enough to overflow a constrained height.
	sessions := []SidebarChatItem{
		{SessionID: "s1", DisplayName: "Alpha chat"},
		{SessionID: "s2", DisplayName: "Beta chat"},
		{SessionID: "s3", DisplayName: "Gamma chat"},
		{SessionID: "s4", DisplayName: "Delta chat"},
	}
	projects := []SidebarProjectItem{
		{ID: "p1", DisplayName: "Project Alpha"},
		{ID: "p2", DisplayName: "Project Beta"},
		{ID: "p3", DisplayName: "Project Gamma"},
		{ID: "p4", DisplayName: "Project Delta"},
	}
	model.workspace.rebuildSidebar("", sessions, projects)

	// Place cursor at the last visible node.
	visible := model.workspace.visibleSidebarIDs()
	if len(visible) == 0 {
		t.Fatal("no visible sidebar nodes — test setup issue")
	}
	model.workspace.sidebarCursor = len(visible) - 1
	lastNodeID := visible[len(visible)-1]
	lastNode := model.workspace.nodes[lastNodeID]
	if lastNode == nil {
		t.Fatal("last visible node is nil")
	}

	// Render at a height that can only show a few rows (well below total node count).
	panel := model.renderSidebarPanel(56, 8, false)

	// The last node's label should be visible after scrolling.
	if !strings.Contains(panel, lastNode.Label) {
		t.Errorf("EX-195: cursor row not visible after scroll — %q not found in sidebar:\n%s", lastNode.Label, panel)
	}
	// An "above" indicator should appear since we scrolled down.
	if !strings.Contains(panel, "above") {
		t.Errorf("EX-195: expected '↑ N above' scroll indicator in sidebar:\n%s", panel)
	}
}
