package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	ansitrunc "github.com/muesli/reflow/truncate"
)

// ── Color palette ──────────────────────────────────────────────────────────

const (
	colPrimary   = lipgloss.Color("#7C3AED") // violet-600
	colFocus     = lipgloss.Color("#8B5CF6") // violet-500
	colBorder    = lipgloss.Color("#374151") // gray-700
	colSubtle    = lipgloss.Color("#4B5563") // gray-600
	colText      = lipgloss.Color("#F9FAFB") // gray-50
	colMuted     = lipgloss.Color("#6B7280") // gray-500
	colConnected = lipgloss.Color("#10B981") // emerald-500
	colWarning   = lipgloss.Color("#F59E0B") // amber-500
	colError     = lipgloss.Color("#EF4444") // red-500
	colUser      = lipgloss.Color("#60A5FA") // blue-400
	colAssistant = lipgloss.Color("#A78BFA") // violet-400
	colTool      = lipgloss.Color("#34D399") // emerald-400
	colUnread    = lipgloss.Color("#F97316") // orange-500
	colAccent    = lipgloss.Color("#EC4899") // pink-500
	colStatusBg  = lipgloss.Color("#111827") // gray-900
	colCursor    = lipgloss.Color("#1E1B4B") // indigo-950
)

// ── Style primitives ────────────────────────────────────────────────────────

var (
	styleBold    = lipgloss.NewStyle().Bold(true)
	styleMuted   = lipgloss.NewStyle().Foreground(colMuted)
	styleSubtle  = lipgloss.NewStyle().Foreground(colSubtle)
	styleText    = lipgloss.NewStyle().Foreground(colText)
	stylePrimary = lipgloss.NewStyle().Foreground(colPrimary).Bold(true)

	styleUser      = lipgloss.NewStyle().Foreground(colUser).Bold(true)
	styleAssistant = lipgloss.NewStyle().Foreground(colAssistant).Bold(true)
	styleTool      = lipgloss.NewStyle().Foreground(colTool)
	styleInterject = lipgloss.NewStyle().Foreground(colWarning).Bold(true).Italic(true)
	styleUnread    = lipgloss.NewStyle().Foreground(colUnread).Bold(true)

	styleConnected    = lipgloss.NewStyle().Foreground(colConnected).Bold(true)
	styleReconnecting = lipgloss.NewStyle().Foreground(colWarning).Bold(true)
	styleDisconnected = lipgloss.NewStyle().Foreground(colError).Bold(true)
	styleDegraded     = lipgloss.NewStyle().Foreground(colWarning)

	styleDivider = lipgloss.NewStyle().Foreground(colSubtle)
	styleLabel   = lipgloss.NewStyle().Foreground(colMuted).Bold(true)

	styleSelected = lipgloss.NewStyle().
			Background(colCursor).
			Foreground(colFocus).
			Bold(true)
	styleActive = lipgloss.NewStyle().Foreground(colFocus).Bold(true)
)

// taskStatusColor returns a colour for the given work_status string.
// Used to colour the right-aligned status labels in project and task views.
func taskStatusColor(s string) lipgloss.Color {
	switch strings.ToLower(s) {
	case "in_progress":
		return colWarning
	case "blocked", "rejected", "deferred":
		return colError
	case "done", "approved", "cancelled":
		return colConnected
	default: // draft, todo, unknown
		return colMuted
	}
}

// formatTaskStatus converts raw status strings to human-readable Title Case labels.
func formatTaskStatus(s string) string {
	switch strings.ToLower(s) {
	case "draft":
		return "Draft"
	case "todo":
		return "Todo"
	case "in_progress":
		return "In Progress"
	case "done":
		return "Done"
	case "approved":
		return "Approved"
	case "cancelled":
		return "Cancelled"
	case "blocked":
		return "Blocked"
	case "rejected":
		return "Rejected"
	case "deferred":
		return "Deferred"
	default:
		parts := strings.Split(s, "_")
		for i, p := range parts {
			if len(p) > 0 {
				parts[i] = strings.ToUpper(p[:1]) + p[1:]
			}
		}
		return strings.Join(parts, " ")
	}
}

// mainViewTitle returns the human-readable uppercase title for a main panel view.
func mainViewTitle(view MainView) string {
	switch view {
	case ViewDashboard:
		return "DASHBOARD"
	case ViewProject:
		return "PROJECT"
	case ViewTask:
		return "TASK DETAIL"
	case ViewInbox:
		return "INBOX"
	case ViewActivity:
		return "ACTIVITY"
	case ViewAgents:
		return "AGENTS"
	case ViewMerges:
		return "MERGE QUEUE"
	case ViewSchedules:
		return "SCHEDULES"
	case ViewHelp:
		return "HELP"
	default:
		return strings.ToUpper(string(view))
	}
}

func normalizedFilterQuery(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func matchesFilter(value, query string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(value), query)
}

func panelStyle(innerW, innerH int, focused bool) lipgloss.Style {
	bc := colBorder
	if focused {
		bc = colFocus
	}
	return lipgloss.NewStyle().
		Width(innerW).
		Height(innerH).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(bc)
}

func connectionDot(state ConnectionState, degraded bool) string {
	switch {
	case state == ConnectionConnected && !degraded:
		return styleConnected.Render("●")
	case state == ConnectionReconnecting || (state == ConnectionConnected && degraded):
		return styleReconnecting.Render("◌")
	default:
		return styleDisconnected.Render("○")
	}
}

func divider(width int, label string) string {
	if label == "" {
		return styleDivider.Render(strings.Repeat("─", maxInt(1, width)))
	}
	styledLabel := styleLabel.Render(" " + label + " ")
	labelLen := lipgloss.Width(styledLabel)
	remaining := width - labelLen
	if remaining < 0 {
		remaining = 0
	}
	left := remaining / 2
	right := remaining - left
	return styleDivider.Render(strings.Repeat("─", left)) +
		styledLabel +
		styleDivider.Render(strings.Repeat("─", right))
}

// ── Top-level view ──────────────────────────────────────────────────────────

func (m Model) viewForShell(shell string) string {
	layout := computeLayout(m.width, m.height, m.focus, m.sidebarVisible, m.state.PanelProportions)
	focus := normalizeFocus(layout, m.focus)
	if focus != m.focus {
		layout = computeLayout(m.width, m.height, focus, m.sidebarVisible, m.state.PanelProportions)
	}

	_, termH := normalizeDimensions(m.width, m.height)

	// Lines consumed outside panel rows
	extraLines := 2 // status bar + help line
	if m.coldOpenActive {
		extraLines++
	}
	if m.degradedModeBanner() != "" {
		extraLines++
	}
	if m.tourActive {
		extraLines++
	}

	panelH := termH - extraLines
	if panelH < 6 {
		panelH = 6
	}
	innerH := panelH - 2 // border top+bottom

	// Render each visible panel
	var panelViews []string
	for _, panel := range []Panel{SidebarPanel, MainPanel, ChatPanel} {
		if !layout.visible[panel] {
			continue
		}
		w := layout.widths[panel]
		if w < 6 {
			continue
		}
		innerW := w - 2 // border left+right, padding handled inside
		focused := panel == focus

		switch panel {
		case SidebarPanel:
			panelViews = append(panelViews, m.renderSidebarPanel(innerW, innerH, focused))
		case MainPanel:
			panelViews = append(panelViews, m.renderMainPanel(innerW, innerH, focused, layout))
		case ChatPanel:
			panelViews = append(panelViews, m.renderChatPanel(innerW, innerH, focused))
		}
	}

	// Clamp each panel view to exactly panelH lines. lipgloss may expand content
	// beyond panelH (e.g. due to styled line width recalculation). Clamping
	// preserves the top and bottom border rows and trims excess content lines.
	for i, pv := range panelViews {
		pvLines := strings.Split(pv, "\n")
		if len(pvLines) > panelH {
			clamped := make([]string, panelH)
			clamped[0] = pvLines[0]                          // top border
			copy(clamped[1:panelH-1], pvLines[1:panelH-1])  // content
			clamped[panelH-1] = pvLines[len(pvLines)-1]     // bottom border
			panelViews[i] = strings.Join(clamped, "\n")
		}
	}

	panelRow := lipgloss.JoinHorizontal(lipgloss.Top, panelViews...)

	prefix := ""
	if layout.gutters > 0 {
		prefix = strings.Repeat(" ", layout.gutters)
	}

	var sections []string

	if m.coldOpenActive {
		// EX-108: friendlier first-run banner — "WELCOME" reads better than the
		// technical "Booting operator console..." for new users.
		banner := lipgloss.NewStyle().Foreground(colFocus).Bold(true).Render("  OTTERCAMP") +
			styleMuted.Render(" // WELCOME") +
			"  " + styleMuted.Render("Setting up your workspace…")
		sections = append(sections, prefix+banner)
	}

	if banner := m.degradedModeBanner(); banner != "" {
		sections = append(sections, prefix+banner)
	}

	sections = append(sections, prefixLines(panelRow, prefix))
	sections = append(sections, prefix+m.renderStatusBar(layout, focus))
	sections = append(sections, prefix+m.renderHelpLine())

	if m.tourActive {
		// EX-108: promote ?·help so first-run users discover the full keybinding
		// reference immediately. Dot-separated to match existing hint style.
		tour := styleMuted.Render("Tour: ") +
			styleSubtle.Render("1·sidebar  2·main  3·chat") +
			styleMuted.Render("  ·  ") +
			styleSubtle.Render("i·inbox  ?·help  :tour dismiss")
		sections = append(sections, prefix+tour)
	}

	return strings.Join(sections, "\n")
}

// ── Sidebar panel ───────────────────────────────────────────────────────────

func (m Model) renderSidebarPanel(innerW, innerH int, focused bool) string {
	// inner padding = 1 each side → content width = innerW - 2
	cw := innerW - 2
	if cw < 4 {
		cw = 4
	}

	var lines []string

	// Sidebar nodes — INBOX / CHATS / PROJECTS sections
	iconOnly := m.sidebarIconOnlyMode()
	visible := m.workspace.visibleSidebarIDs()
	visible = m.filteredSidebarIDs(visible, m.sidebarFilter)

	// Reserve rows for search bar if active
	rowsForBody := innerH
	searchLine := m.renderSearchBar(SidebarPanel, cw)
	if searchLine != "" {
		rowsForBody -= 2 // blank line + search bar
	}
	if rowsForBody < 0 {
		rowsForBody = 0
	}

	maxNodeLines := rowsForBody
	if len(visible) > maxNodeLines && maxNodeLines > 0 {
		maxNodeLines--
	}
	if maxNodeLines < 0 {
		maxNodeLines = 0
	}

	// EX-195: scroll the sidebar so the cursor is always within the visible window.
	// Each node occupies 1 display line; section-header dividers (except the very first
	// header) add 1 extra line. We compute the display line index of the cursor and
	// derive a scroll start that centres the cursor inside the window.
	sidebarScrollStart := 0
	if cursor := m.workspace.sidebarCursor; cursor > 0 && maxNodeLines > 0 {
		// Count display lines up to and including the cursor row.
		displayAtCursor := 0
		prevWasContent := false
		for i, id := range visible {
			node := m.workspace.nodes[id]
			if node == nil {
				if i == cursor {
					break
				}
				prevWasContent = true
				continue
			}
			if node.Kind == sidebarKindHeader && prevWasContent {
				displayAtCursor++ // divider line
			}
			displayAtCursor++ // the node itself
			if i == cursor {
				break
			}
			prevWasContent = node.Kind != sidebarKindHeader
		}
		// If cursor display-line exceeds the window, scroll forward.
		if displayAtCursor > maxNodeLines {
			excess := displayAtCursor - maxNodeLines
			// Walk forward from index 0 consuming 'excess' display lines to find startIdx.
			consumed := 0
			prevWasContent2 := false
			for i, id := range visible {
				node := m.workspace.nodes[id]
				if node == nil {
					prevWasContent2 = true
					continue
				}
				if node.Kind == sidebarKindHeader && prevWasContent2 {
					consumed++
				}
				consumed++
				prevWasContent2 = node.Kind != sidebarKindHeader
				if consumed >= excess {
					sidebarScrollStart = i + 1
					break
				}
			}
		}
	}

	displayLines := 0
	lastRendered := 0
	firstRendered := sidebarScrollStart
	for i, id := range visible[sidebarScrollStart:] {
		idx := i + sidebarScrollStart
		if displayLines >= maxNodeLines {
			break
		}
		node := m.workspace.nodes[id]
		if node == nil {
			lastRendered = idx + 1
			continue
		}
		// Add section divider above each header (except when it's the first rendered node)
		if node.Kind == sidebarKindHeader && idx > firstRendered {
			if displayLines >= maxNodeLines {
				break
			}
			lines = append(lines, styleMuted.Render(strings.Repeat("─", maxInt(1, cw-2))))
			displayLines++
			if displayLines >= maxNodeLines {
				break
			}
		}
		lines = append(lines, m.renderSidebarNode(node, idx == m.workspace.sidebarCursor, cw, iconOnly))
		displayLines++
		lastRendered = idx + 1
	}
	// Show above/below overflow indicators (EX-195).
	if sidebarScrollStart > 0 {
		// Replace the first line with an "↑ N above" prefix if possible, else prepend
		aboveCount := sidebarScrollStart
		indicator := styleMuted.Render(fmt.Sprintf("  ↑ %d above", aboveCount))
		if len(lines) > 0 {
			lines[0] = indicator
		}
	}
	if remaining := len(visible) - lastRendered; remaining > 0 && rowsForBody > 0 {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("  +%d more", remaining)))
	}

	// EX-193: empty sidebar placeholder so the user knows data is loading vs. absent.
	if len(lines) == 0 {
		if m.sidebarFilter != "" {
			lines = append(lines, styleMuted.Render(truncate("  No matches for /"+m.sidebarFilter, cw)))
		} else {
			lines = append(lines, styleMuted.Render(truncate("  No projects or chats", cw)))
		}
	}

	// EX-194: search bar hint — show navigation hints when search is active.
	// EX-259: "Search" → "Filter" for consistent terminology.
	// EX-316: include ↑/↓ and Ctrl-U hints (added in EX-313/EX-310).
	if searchLine != "" {
		hint := searchLine
		if m.searchMode && m.searchPanel == SidebarPanel {
			hint = styleMuted.Render(truncate("Filter /"+m.sidebarFilter+"▌  (↑/↓ nav · Ctrl-U clear · Esc cancel)", cw))
		}
		lines = append(lines, "")
		lines = append(lines, hint)
	}

	// Fill remaining space
	content := buildPanelContent(lines, innerH, cw)

	return panelStyle(innerW, innerH, focused).Render(content)
}

func (m Model) renderSidebarNode(node *sidebarNode, cursor bool, width int, iconOnly bool) string {
	isActive := node.Kind == sidebarKindSession &&
		node.SessionID == m.workspace.activeSessionID

	var prefix, label string
	switch node.Kind {
	case sidebarKindInbox:
		label = "INBOX"
		if m.workspace.inboxCount > 0 {
			label += fmt.Sprintf("  (%d)", m.workspace.inboxCount)
		}
		prefix = ""
	case sidebarKindHeader:
		sectionID := sidebarSectionID(strings.TrimPrefix(node.ID, "header-"))
		if m.workspace.sectionCollapsed[sectionID] {
			prefix = "▸ "
		} else {
			prefix = "▾ "
		}
		label = node.Label
		if sectionID == sectionChats {
			chatCount := 0
			for _, id := range m.workspace.topLevel {
				n := m.workspace.nodes[id]
				if n != nil && n.Kind == sidebarKindSession && n.ID != generalSidebarNodeID {
					chatCount++
				}
			}
			if chatCount > 0 {
				label += fmt.Sprintf(" (%d)", chatCount)
			}
			if unread := m.workspace.totalUnreadSessions(); unread > 0 {
				label += fmt.Sprintf("  +%d unread", unread)
			}
		} else if sectionID == sectionProjects {
			if count := m.workspace.projectCount(); count > 0 {
				label += fmt.Sprintf(" (%d)", count)
			}
		}
	case sidebarKindProject:
		if node.Expanded {
			prefix = "  ▾ "
		} else {
			prefix = "  ▸ "
		}
		label = node.Label
		// Show open task count when project has loaded task nodes
		if openCount := len(m.workspace.projectChildren(node.ID)); openCount > 0 {
			label += fmt.Sprintf(" (%d)", openCount)
		}
	case sidebarKindTask:
		switch node.WorkStatus {
		case "done", "approved":
			prefix = "    ✓ "
		case "in_progress":
			prefix = "    ◌ "
		case "blocked", "rejected", "deferred":
			prefix = "    ✗ "
		default:
			prefix = "    ○ "
		}
		label = node.Label
		// EX-151: append ⚠ when the task requires human review, consistent with
		// the dashboard board (EX-094) and project view (EX-150).
		if rec := m.workspace.tasks[node.TaskID]; rec != nil && rec.RequiresHumanReview {
			label = label + " ⚠"
		}
	case sidebarKindSession:
		if node.ParentID != "" {
			prefix = "    › "
		} else if node.SessionScope == "project_task" && node.WorkStatus != "" {
			// Show task work status icon for task-scoped chat sessions.
			// Use ✓ for done/approved (consistent with project view done section),
			// not ● which looks active/connected but means complete.
			switch node.WorkStatus {
			case "done", "approved":
				prefix = "  ✓ "
			case "in_progress":
				prefix = "  ◌ "
			case "blocked", "rejected":
				prefix = "  ⚠ "
			default:
				prefix = "  ○ "
			}
		} else {
			prefix = "  "
		}
		label = node.Label
	}
	if iconOnly {
		label = compactSidebarLabel(node)
	}

	var unreadSuffix string
	if node.Unread > 0 {
		unreadSuffix = " " + styleUnread.Render(fmt.Sprintf("(%d)", node.Unread))
	}

	// Right-align relative time for top-level session nodes
	var timeSuffix string
	timeSuffixW := 0
	if node.Kind == sidebarKindSession && node.ParentID == "" && !node.UpdatedAt.IsZero() {
		timeSuffix = " " + styleSubtle.Render(relativeTime(node.UpdatedAt))
		timeSuffixW = lipgloss.Width(timeSuffix)
	}

	line := prefix + label

	var rendered string
	switch node.Kind {
	case sidebarKindHeader, sidebarKindInbox:
		if cursor {
			rendered = styleSelected.Render(truncate(line, width-2))
		} else {
			rendered = styleBold.Render(truncate(line, width-2))
		}
	default:
		// Reserve space for time suffix so it doesn't overflow to the next line
		maxW := width - 2 - timeSuffixW
		if maxW < 4 {
			maxW = 4
		}
		switch {
		case cursor && isActive:
			// show ✓ check to distinguish active+cursor from cursor-only
			check := " " + styleConnected.Render("✓")
			rendered = styleSelected.Render(truncate(line, maxW-2)) + check
		case cursor:
			rendered = styleSelected.Render(truncate(line, maxW))
		case isActive:
			// Always show ✓ for the active session so it's visible at a glance
			check := " " + styleConnected.Render("✓")
			rendered = styleActive.Render(truncate(line, maxW-2)) + check
		default:
			rendered = styleText.Render(truncate(line, maxW))
		}
	}

	return rendered + timeSuffix + unreadSuffix
}

func (m Model) filteredSidebarIDs(visible []string, rawQuery string) []string {
	query := normalizedFilterQuery(rawQuery)
	if query == "" {
		return visible
	}

	include := make(map[string]struct{}, len(visible))
	for _, id := range visible {
		node := m.workspace.nodes[id]
		if node == nil {
			continue
		}
		// Inbox is a leaf node with its own match logic — keep when label matches.
		if node.Kind == sidebarKindInbox {
			if matchesFilter(node.Label, query) || matchesFilter("inbox", query) {
				include[id] = struct{}{}
			}
			continue
		}
		// Section headers are kept conditionally below once we know their children.
		if node.Kind == sidebarKindHeader {
			continue
		}
		if matchesFilter(node.Label, query) {
			include[id] = struct{}{}
			if node.ParentID != "" {
				include[node.ParentID] = struct{}{}
			}
		}
	}

	// EX-119: include section headers only when at least one content node in
	// their section matched. A header's section contains all subsequent nodes
	// until the next header — detect this by checking the include set.
	for _, id := range visible {
		node := m.workspace.nodes[id]
		if node == nil || node.Kind != sidebarKindHeader {
			continue
		}
		// Find at least one non-header node following this header that is included.
		found := false
		inSection := false
		for _, vid := range visible {
			if vid == id {
				inSection = true
				continue
			}
			if !inSection {
				continue
			}
			vnode := m.workspace.nodes[vid]
			if vnode == nil {
				continue
			}
			if vnode.Kind == sidebarKindHeader {
				break // next section header — stop
			}
			if _, ok := include[vid]; ok {
				found = true
				break
			}
		}
		if found {
			include[id] = struct{}{}
		}
	}

	filtered := make([]string, 0, len(include))
	for _, id := range visible {
		if _, ok := include[id]; ok {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

func (m Model) renderSearchBar(panel Panel, width int) string {
	query := m.filterForPanel(panel)
	editing := m.searchMode && m.searchPanel == panel
	if !editing && strings.TrimSpace(query) == "" {
		return ""
	}

	prompt := "/" + query
	if editing {
		prompt += "▌"
	} else {
		// EX-208: when a filter is applied but not actively being edited,
		// show a hint so the user knows how to change or clear it.
		// EX-258: "Esc to clear" was wrong — Esc only clears while in edit mode
		// (updateSearchInput). Outside edit mode Esc navigates, not clears.
		// "/ to re-filter or clear" is accurate: / opens edit mode → Esc clears.
		prompt += "  (/ to re-filter or clear)"
	}
	// EX-259: "Search /frank" → "Filter /frank" — aligns with EX-254/257 filter terminology.
	return styleMuted.Render(truncate("Filter "+prompt, width))
}

func (m Model) sidebarIconOnlyMode() bool {
	width, _ := normalizeDimensions(m.width, m.height)
	return width >= 100 && width < 120
}

func compactSidebarLabel(node *sidebarNode) string {
	if node == nil {
		return ""
	}
	switch node.Kind {
	case sidebarKindInbox:
		return "IN"
	case sidebarKindHeader:
		if len(node.Label) >= 2 {
			return strings.ToUpper(node.Label[:2])
		}
		return strings.ToUpper(node.Label)
	case sidebarKindProject:
		return "PRJ"
	case sidebarKindTask:
		return "TSK"
	}
	if node.TaskID != "" {
		suffix := strings.TrimPrefix(strings.ToLower(node.TaskID), "task-")
		if suffix == "" {
			suffix = "?"
		}
		return "T" + suffix
	}
	return "GEN"
}

// ── Main panel ──────────────────────────────────────────────────────────────

func (m Model) renderMainPanel(innerW, innerH int, focused bool, layout layoutState) string {
	cw := innerW - 2
	if cw < 4 {
		cw = 4
	}

	var lines []string

	// Title — EX-014: human-readable title; EX-015/EX-019: count badges; EX-039: context names
	titleText := mainViewTitle(m.workspace.mainView)
	switch m.workspace.mainView {
	case ViewInbox:
		if n := len(m.workspace.inbox); n > 0 {
			titleText += fmt.Sprintf(" (%d)", n)
		}
	case ViewActivity:
		if n := len(m.workspace.activity); n > 0 {
			titleText += fmt.Sprintf(" (%d)", n)
		}
	case ViewAgents:
		if n := len(m.workspace.agents); n > 0 {
			titleText += fmt.Sprintf(" (%d)", n)
		}
	// EX-102: count badges for merges and schedules, consistent with inbox/activity/agents.
	case ViewMerges:
		if n := len(m.workspace.mergeQueue); n > 0 {
			titleText += fmt.Sprintf(" (%d)", n)
		}
	case ViewSchedules:
		if n := len(m.workspace.schedules); n > 0 {
			titleText += fmt.Sprintf(" (%d)", n)
		}
	case ViewProject:
		// Show selected project name in title for orientation
		if m.workspace.selectedProject != nil && m.workspace.selectedProject.DisplayName != "" {
			titleText = truncate(strings.ToUpper(m.workspace.selectedProject.DisplayName), cw-2)
		} else if nodeID := "project-" + m.workspace.selectedProjectID; m.workspace.nodes[nodeID] != nil {
			titleText = truncate(strings.ToUpper(m.workspace.nodes[nodeID].Label), cw-2)
		}
	case ViewTask:
		// Show "OC-N: Title" in panel header for orientation
		if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil {
			if task.TaskNumber > 0 {
				full := fmt.Sprintf("OC-%d: %s", task.TaskNumber, strings.ToUpper(task.Title))
				titleText = truncate(full, cw-2)
			} else if task.Title != "" {
				titleText = truncate(strings.ToUpper(task.Title), cw-2)
			}
		}
	}
	titleColor := colMuted
	if focused {
		titleColor = colFocus
	}
	title := lipgloss.NewStyle().Foreground(titleColor).Bold(true).Render(titleText)
	lines = append(lines, title)
	lines = append(lines, styleDivider.Render(strings.Repeat("─", cw)))

	// View-specific content
	viewLines := m.renderMainViewContent(m.workspace.mainView, cw, innerH-4)
	lines = append(lines, viewLines...)

	if searchLine := m.renderSearchBar(MainPanel, cw); searchLine != "" {
		hint := searchLine
		// EX-200: match EX-194 — show Esc hint in main panel search bar while editing.
		// EX-259: "Search" → "Filter" for consistent terminology.
		if m.searchMode && m.searchPanel == MainPanel {
			// EX-316: include ↑/↓ and Ctrl-U hints (added in EX-313/EX-310).
			hint = styleMuted.Render(truncate("Filter /"+m.mainFilter+"▌  (↑/↓ nav · Ctrl-U clear · Esc cancel)", cw))
		}
		lines = append(lines, "")
		lines = append(lines, hint)
	}

	content := buildPanelContent(lines, innerH, cw)

	return panelStyle(innerW, innerH, focused).Render(content)
}

func (m Model) renderMainViewContent(view MainView, width, maxLines int) []string {
	switch view {
	case ViewDashboard:
		return m.renderDashboardView(width, maxLines)
	case ViewProject:
		return m.renderProjectView(width, maxLines)
	case ViewTask:
		return m.renderTaskView(width, maxLines)
	case ViewInbox:
		return m.renderInboxView(width, maxLines)
	case ViewActivity:
		return m.renderActivityView(width, maxLines)
	case ViewAgents:
		return m.renderAgentsView(width, maxLines)
	case ViewMerges:
		return m.renderMergesView(width, maxLines)
	case ViewSchedules:
		return m.renderSchedulesView(width, maxLines)
	case ViewHelp:
		return m.renderHelpView(width, maxLines)
	default:
		return []string{styleMuted.Render(fmt.Sprintf("view: %s", view))}
	}
}

func (m Model) renderDashboardView(width, maxLines int) []string {
	var lines []string
	counts := m.workspace.boardCounts()
	query := normalizedFilterQuery(m.mainFilter)

	// EX-109: when a filter is active, compute per-column counts from the
	// filtered task set so the column headers reflect what's actually visible.
	if query != "" {
		counts = boardCounts{}
		for _, id := range m.workspace.taskOrder {
			task := m.workspace.tasks[id]
			if task == nil {
				continue
			}
			taskLabel := task.Title
			if task.TaskNumber > 0 {
				taskLabel = fmt.Sprintf("OC-%d: %s", task.TaskNumber, task.Title)
			}
			if !matchesFilter(taskLabel, query) && !matchesFilter(task.Status, query) {
				continue
			}
			switch task.Status {
			case "draft", "todo":
				counts.Todo++
			case "done", "approved", "cancelled":
				counts.Done++
			case "blocked", "rejected", "deferred":
				counts.Blocked++
			default:
				counts.InProgress++
			}
		}
	}

	// Board columns
	lines = append(lines, "")
	boardTitle := "Task Board"
	if m.workspace.selectedProjectID != "" {
		if projNode := m.workspace.nodes["project-"+m.workspace.selectedProjectID]; projNode != nil {
			boardTitle = projNode.Label + " — Task Board"
		}
	}
	lines = append(lines, styleLabel.Render(boardTitle))

	// EX-114: use 4-column layout when blocked tasks exist and width allows.
	// colW must be computed based on actual column count to prevent header overflow.
	showBlockedCol := width > 80 && counts.Blocked > 0
	numCols := 3
	if showBlockedCol {
		numCols = 4
	}
	colW := (width - 6) / numCols
	if colW < 8 {
		colW = 8
	}

	todoHdr := buildColumnHeader("TODO", counts.Todo, colW, colConnected)
	inProgHdr := buildColumnHeader("IN PROGRESS", counts.InProgress, colW, colWarning)
	doneHdr := buildColumnHeader("DONE", counts.Done, colW, colMuted)
	blockedHdr := buildColumnHeader("BLOCKED", counts.Blocked, colW, colError)
	todoSep := buildColumnSep(colW)
	inProgSep := buildColumnSep(colW)
	doneSep := buildColumnSep(colW)
	blockedSep := buildColumnSep(colW)

	visibleHdrs := []string{todoHdr, inProgHdr, doneHdr}
	visibleSeps := []string{todoSep, inProgSep, doneSep}
	if showBlockedCol {
		visibleHdrs = append(visibleHdrs, blockedHdr)
		visibleSeps = append(visibleSeps, blockedSep)
	}
	// Render header and separator as two separate lines (no embedded newlines)
	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, visibleHdrs...))
	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, visibleSeps...))

	// Task rows under each column
	cursorID := m.workspace.selectedTaskID
	todoTasks, inProgTasks, doneTasks, blockedTasks := []string{}, []string{}, []string{}, []string{}
	// EX-126: track cursor index within each column so we can scroll to keep it visible.
	todoIdx, inProgIdx, doneIdx, blockedIdx := -1, -1, -1, -1
	for _, id := range m.workspace.taskOrder {
		task := m.workspace.tasks[id]
		if task == nil {
			continue
		}
		taskLabel := task.Title
		if task.TaskNumber > 0 {
			taskLabel = fmt.Sprintf("OC-%d: %s", task.TaskNumber, task.Title)
		}
		if !matchesFilter(taskLabel, query) && !matchesFilter(task.Status, query) {
			continue
		}
		isCursor := id == cursorID
		// EX-094: append ⚠ suffix when a task is awaiting human review so it
		// stands out in the dashboard board without requiring task detail drill-in.
		if task.RequiresHumanReview {
			taskLabel = truncate(taskLabel, colW-2) + " ⚠"
		}
		switch task.Status {
		case "draft", "todo":
			if isCursor {
				todoIdx = len(todoTasks)
				entry := truncate("► "+taskLabel, colW)
				todoTasks = append(todoTasks, styleBold.Foreground(colFocus).Render(entry))
			} else {
				entry := truncate("○ "+taskLabel, colW)
				todoTasks = append(todoTasks, styleText.Render(entry))
			}
		case "done", "approved", "cancelled":
			if isCursor {
				doneIdx = len(doneTasks)
			}
			entry := truncate("✓ "+taskLabel, colW)
			doneTasks = append(doneTasks, styleMuted.Render(entry))
		case "blocked", "rejected", "deferred":
			// EX-114: render in blocked column when the column is visible.
			if isCursor {
				blockedIdx = len(blockedTasks)
			}
			entry := truncate("✗ "+taskLabel, colW)
			blockedTasks = append(blockedTasks, lipgloss.NewStyle().Foreground(colError).Render(entry))
		default: // in_progress and unknown active statuses
			if isCursor {
				inProgIdx = len(inProgTasks)
				entry := truncate("► "+taskLabel, colW)
				inProgTasks = append(inProgTasks, styleBold.Foreground(colFocus).Render(entry))
			} else {
				// Use amber for normal in-progress, use warning/amber with ⚠ badge
				// for review-required so it visually pops.
				icon := "◌ "
				taskStyle := lipgloss.NewStyle().Foreground(colWarning)
				if task.RequiresHumanReview {
					taskStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
				}
				entry := truncate(icon+taskLabel, colW)
				inProgTasks = append(inProgTasks, taskStyle.Render(entry))
			}
		}
	}
	// EX-126: compute per-column scroll start so the cursor row is always
	// visible. For columns without a cursor the start is 0 (top-aligned).
	boardColStart := func(n, cursorAt int) int {
		if cursorAt < 0 || cursorAt < 4 {
			return 0
		}
		// Scroll so cursor appears at the last visible row.
		return cursorAt - 3
	}
	const boardRows = 4
	todoStart := boardColStart(len(todoTasks), todoIdx)
	inProgStart := boardColStart(len(inProgTasks), inProgIdx)
	doneStart := boardColStart(len(doneTasks), doneIdx)
	blockedStart := boardColStart(len(blockedTasks), blockedIdx)
	taskRowCount := maxInt(len(todoTasks), maxInt(len(inProgTasks), maxInt(len(doneTasks), len(blockedTasks))))
	// Render only as many rows as there are tasks (up to boardRows), so the board
	// does not pad with blank rows when columns are short.
	visibleRows := minInt(taskRowCount, boardRows)
	for row := 0; row < visibleRows; row++ {
		r0 := lipgloss.NewStyle().Width(colW).Render(safeIndex(todoTasks, todoStart+row))
		r1 := lipgloss.NewStyle().Width(colW).Render(safeIndex(inProgTasks, inProgStart+row))
		r2 := lipgloss.NewStyle().Width(colW).Render(safeIndex(doneTasks, doneStart+row))
		if showBlockedCol {
			r3 := lipgloss.NewStyle().Width(colW).Render(safeIndex(blockedTasks, blockedStart+row))
			lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, r0, r1, r2, r3))
		} else {
			lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, r0, r1, r2))
		}
	}
	// EX-008/EX-126: show per-column overflow indicator accounting for scroll.
	// "↑ N above" when scroll start > 0; "+N more" when items extend below visible window.
	if taskRowCount > boardRows || todoStart > 0 || inProgStart > 0 || doneStart > 0 || blockedStart > 0 {
		overflowFor := func(col []string, start int) string {
			above := start
			below := len(col) - (start + boardRows)
			if below < 0 {
				below = 0
			}
			switch {
			case above > 0 && below > 0:
				return styleMuted.Render(fmt.Sprintf("  ↑%d ·+%d", above, below))
			case above > 0:
				return styleMuted.Render(fmt.Sprintf("  ↑ %d above", above))
			case below > 0:
				return styleMuted.Render(fmt.Sprintf("  +%d more", below))
			default:
				return ""
			}
		}
		r0 := lipgloss.NewStyle().Width(colW).Render(overflowFor(todoTasks, todoStart))
		r1 := lipgloss.NewStyle().Width(colW).Render(overflowFor(inProgTasks, inProgStart))
		r2 := lipgloss.NewStyle().Width(colW).Render(overflowFor(doneTasks, doneStart))
		if showBlockedCol {
			r3 := lipgloss.NewStyle().Width(colW).Render(overflowFor(blockedTasks, blockedStart))
			lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, r0, r1, r2, r3))
		} else {
			lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, r0, r1, r2))
		}
	}

	// Inbox section
	filteredInbox := make([]inboxItem, 0, len(m.workspace.inbox))
	for _, item := range m.workspace.inbox {
		if matchesFilter(item.Summary, query) || matchesFilter(item.TaskID, query) {
			filteredInbox = append(filteredInbox, item)
		}
	}
	if len(filteredInbox) > 0 {
		lines = append(lines, "")
		lines = append(lines, divider(width, fmt.Sprintf("Inbox  %d", len(filteredInbox))))
		for i, item := range filteredInbox {
			if i >= 3 {
				lines = append(lines, styleMuted.Render(fmt.Sprintf("  +%d more  ·  i·view all", len(filteredInbox)-3)))
				break
			}
			bullet := lipgloss.NewStyle().Foreground(colAccent).Render("▸ ")
			taskBadge := ""
			if task := m.workspace.tasks[item.TaskID]; task != nil && task.TaskNumber > 0 {
				taskBadge = styleMuted.Render(fmt.Sprintf("  OC-%d", task.TaskNumber))
			}
			lines = append(lines, bullet+styleText.Render(truncate(item.Summary, width-12))+taskBadge)
		}
		// EX-107: hint to navigate to the full inbox view.
		// Only show when overflow wasn't already shown (which includes the hint).
		if len(filteredInbox) <= 3 {
			lines = append(lines, styleMuted.Render("  i·open inbox"))
		}
	}

	// Activity section
	filteredActivity := make([]string, 0, len(m.workspace.activity))
	for _, entry := range m.workspace.activity {
		if matchesFilter(entry, query) {
			filteredActivity = append(filteredActivity, entry)
		}
	}
	if len(filteredActivity) > 0 {
		lines = append(lines, "")
		lines = append(lines, divider(width, "Activity"))
		start := 0
		if len(filteredActivity) > 3 {
			start = len(filteredActivity) - 3
		}
		for _, entry := range filteredActivity[start:] {
			// EX-103: use the same context-aware icon as renderActivityView (EX-093).
			// Keep the 2-space indent so the icon lines up with other dashboard sections.
			icon := activityIcon(entry)
			lines = append(lines, "  "+icon+styleMuted.Render(truncate(entry, width-6)))
		}
	}

	// Navigation hint — show selected task name when cursor could be off-screen.
	// EX-117: when a filter is active and no tasks are visible in the board
	// columns, show a "no matches" message instead of a misleading task hint.
	activeTasks := m.workspace.dashboardActiveTasks()
	visibleTaskCount := len(todoTasks) + len(inProgTasks) + len(doneTasks) + len(blockedTasks)
	if query != "" && visibleTaskCount == 0 && len(activeTasks) > 0 {
		lines = append(lines, "")
		lines = append(lines, styleMuted.Render(fmt.Sprintf("  no tasks matching %q  ·  /·clear filter", query)))
	} else if len(activeTasks) > 0 {
		lines = append(lines, "")
		filterHint := "  ·  " + filterActionHint(query)
		if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil {
			var nameLabel string
			if task.TaskNumber > 0 {
				nameLabel = fmt.Sprintf("OC-%d: %s", task.TaskNumber, truncate(task.Title, 34))
			} else {
				nameLabel = truncate(task.Title, 42)
			}
			lines = append(lines, styleBold.Foreground(colFocus).Render("  "+nameLabel)+styleMuted.Render("  ·  Enter·open"+filterHint))
		} else {
			lines = append(lines, styleMuted.Render("  Enter·open"+filterHint))
		}
	} else {
		// EX-219: when no active tasks exist, show a navigation hint so the user
		// knows how to interact with the dashboard (Tab·navigate, :frank to chat, i·inbox).
		lines = append(lines, "")
		lines = append(lines, styleMuted.Render("  Tab·navigate  ·  i·inbox  ·  :frank·chat"))
	}

	return lines
}

// buildColumnHeader returns just the column name+count header (single line, no separator).
// The name is truncated if necessary so the header never exceeds width, preventing lipgloss wrapping.
func buildColumnHeader(name string, count, width int, color lipgloss.Color) string {
	countStr := fmt.Sprintf(" (%d)", count)
	// Ensure name+count fits within width to prevent wrapping
	available := width - len(countStr)
	if available < 1 {
		available = 1
	}
	displayName := truncate(name, available)
	label := lipgloss.NewStyle().Foreground(color).Bold(true).Render(displayName)
	cs := lipgloss.NewStyle().Foreground(color).Render(countStr)
	return lipgloss.NewStyle().Width(width).Render(label+cs) + " "
}

// buildColumnSep returns the separator line for a column (single line).
func buildColumnSep(width int) string {
	return lipgloss.NewStyle().Width(width).Render(styleDivider.Render(strings.Repeat("─", width-1))) + " "
}

func (m Model) renderProjectView(width, maxLines int) []string {
	var lines []string
	lines = append(lines, "")

	// Find the selected project node
	projectNodeID := "project-" + m.workspace.selectedProjectID
	node := m.workspace.nodes[projectNodeID]
	if node == nil {
		// Fall back to showing all projects in a list
		query := normalizedFilterQuery(m.mainFilter)
		for _, id := range m.workspace.topLevel {
			n := m.workspace.nodes[id]
			if n == nil || n.Kind != sidebarKindProject {
				continue
			}
			if !matchesFilter(n.Label, query) {
				continue
			}
			icon := "▸ "
			if n.Expanded {
				icon = "▾ "
			}
			lines = append(lines, lipgloss.NewStyle().Foreground(colFocus).Bold(true).Render(icon+n.Label))
		}
		// EX-232: add a footer hint so users know how to select a project and filter.
		lines = append(lines, "")
		lines = append(lines, styleMuted.Render("  Enter·select project  ·  "+filterActionHint(query)+"  ·  Esc·dashboard"))
		return lines
	}

	// Show selected project detail with tasks
	titleStyle := lipgloss.NewStyle().Foreground(colFocus).Bold(true)
	proj := m.workspace.selectedProject

	// Title row: project name left, delivery mode right
	titleText := node.Label
	if proj != nil && strings.TrimSpace(proj.DeliveryMode) != "" {
		pad := width - len(titleText) - len(proj.DeliveryMode)
		if pad > 1 {
			titleText = titleText + strings.Repeat(" ", pad) + proj.DeliveryMode
		}
	}
	lines = append(lines, titleStyle.Render(titleText))
	lines = append(lines, styleDivider.Render(strings.Repeat("─", maxInt(1, width))))

	// Description
	if proj != nil && strings.TrimSpace(proj.Description) != "" {
		wrapped := wrapText(proj.Description, maxInt(10, width))
		for _, wl := range wrapped {
			lines = append(lines, styleMuted.Render(wl))
		}
		lines = append(lines, "")
	}

	if proj == nil {
		// Still loading — EX-213: add r·retry hint in case the load stalls.
		lines = append(lines, styleMuted.Render("  Loading…"))
		lines = append(lines, "")
		lines = append(lines, styleMuted.Render("  r·retry  ·  Esc·dashboard"))
		return lines
	}

	// Open tasks from selectedProject.Tasks (loaded from API)
	query := normalizedFilterQuery(m.mainFilter)
	var openTasks []SidebarTaskItem
	totalUnfilteredOpenTasks := 0 // EX-118: track total open tasks before filter
	if len(proj.Tasks) > 0 {
		for _, t := range proj.Tasks {
			if t.WorkStatus == "done" || t.WorkStatus == "approved" || t.WorkStatus == "cancelled" {
				continue
			}
			totalUnfilteredOpenTasks++
			taskLabel := t.Title
			if t.TaskNumber > 0 {
				taskLabel = fmt.Sprintf("OC-%d: %s", t.TaskNumber, t.Title)
			}
			if !matchesFilter(taskLabel, query) && !matchesFilter(t.WorkStatus, query) {
				continue
			}
			openTasks = append(openTasks, t)
		}
	} else {
		// Fall back to sidebar task nodes if API detail not yet available.
		// EX-156: use child.TaskID (raw UUID) not child.ID (sidebar node ID "task-<uuid>")
		// so that RequiresHumanReview lookup and loadTaskDetailCmd receive the correct ID.
		// Also include TaskNumber so the "OC-N:" prefix renders correctly in the list.
		children := m.workspace.projectChildren(projectNodeID)
		for _, cid := range children {
			child := m.workspace.nodes[cid]
			if child == nil || child.Kind != sidebarKindTask {
				continue
			}
			if child.WorkStatus == "done" || child.WorkStatus == "approved" {
				continue
			}
			totalUnfilteredOpenTasks++
			taskLabel := child.Label
			if !matchesFilter(taskLabel, query) && !matchesFilter(child.WorkStatus, query) {
				continue
			}
			openTasks = append(openTasks, SidebarTaskItem{
				ID:         child.TaskID,   // raw UUID, not sidebar node ID
				Title:      child.Label,
				WorkStatus: child.WorkStatus,
				TaskNumber: child.TaskNumber,
			})
		}
	}

	if len(openTasks) == 0 {
		lines = append(lines, styleLabel.Render("OPEN TASKS (0)"))
		// EX-118: distinguish between a genuinely empty task list and a filter with no matches.
		if query != "" && totalUnfilteredOpenTasks > 0 {
			lines = append(lines, styleMuted.Render(fmt.Sprintf("  no open tasks matching %q", query)))
		} else if proj.DoneCount > 0 {
			lines = append(lines, lipgloss.NewStyle().Foreground(colConnected).Render(fmt.Sprintf("  ✓  All %d tasks complete", proj.DoneCount)))
		} else {
			lines = append(lines, styleMuted.Render("  No open tasks."))
		}
	} else {
		// EX-223: show per-status breakdown in the OPEN TASKS header so users
		// see at a glance how many tasks are in each state without drilling in.
		taskHeader := fmt.Sprintf("OPEN TASKS (%d)", len(openTasks))
		inProgCount, blockedCount := 0, 0
		for _, t := range openTasks {
			switch t.WorkStatus {
			case "in_progress":
				inProgCount++
			case "blocked", "rejected", "deferred":
				blockedCount++
			}
		}
		if inProgCount > 0 {
			taskHeader += fmt.Sprintf("  ·  %d in-progress", inProgCount)
		}
		if blockedCount > 0 {
			taskHeader += fmt.Sprintf("  ·  %d blocked", blockedCount)
		}
		if proj.DoneCount > 0 {
			taskHeader += fmt.Sprintf("  ·  %d done", proj.DoneCount)
		}
		cursor := m.workspace.projectTaskCursor
		if cursor < 0 {
			cursor = 0
		}
		if cursor >= len(openTasks) {
			cursor = len(openTasks) - 1
		}
		// Build task lines, then trim with "+N more" if they overflow maxLines.
		headerLineCount := len(lines) + 1 // +1 for the OPEN TASKS header we're about to add
		availForTasks := maxLines - headerLineCount
		if availForTasks < 1 {
			availForTasks = 1
		}
		var taskLines []string
		for i, task := range openTasks {
			isCursor := i == cursor
			var icon string
			if isCursor {
				icon = "► "
			} else {
				switch task.WorkStatus {
				case "in_progress":
					icon = "◌ "
				case "blocked", "rejected", "deferred":
					icon = "✗ "
				default:
					icon = "○ "
				}
			}
			statusText := formatTaskStatus(task.WorkStatus)
			statW := len([]rune(statusText))
			taskTitle := task.Title
			if task.TaskNumber > 0 {
				taskTitle = fmt.Sprintf("OC-%d: %s", task.TaskNumber, task.Title)
			}
			// Right-align the status label: compute how much to pad between title and status.
			prefixW := 2 + lipgloss.Width(icon) // "  " + icon
			maxTitleW := width - prefixW - statW - 2
			if maxTitleW < 4 {
				maxTitleW = 4
			}
			// EX-150: append ⚠ when the task requires human review, consistent with
			// the dashboard board view (EX-094) so the badge is visible in both views.
			if rec := m.workspace.tasks[task.ID]; rec != nil && rec.RequiresHumanReview {
				taskTitle = truncate(taskTitle, maxTitleW-2) + " ⚠"
			} else {
				taskTitle = truncate(taskTitle, maxTitleW)
			}
			truncTitle := taskTitle
			leftPart := "  " + icon + truncTitle
			padW := width - lipgloss.Width(leftPart) - statW - 1
			if padW < 1 {
				padW = 1
			}
			spacer := strings.Repeat(" ", padW)
			statusColor := taskStatusColor(task.WorkStatus)
			statusLabel := lipgloss.NewStyle().Foreground(statusColor).Render(statusText)
			var taskLine string
			if isCursor {
				taskLine = styleBold.Foreground(colFocus).Render(leftPart) + lipgloss.NewStyle().Foreground(statusColor).Render(spacer+statusText)
			} else {
				taskLine = styleText.Render(leftPart+spacer) + statusLabel
			}
			taskLines = append(taskLines, taskLine)
		}
		// If task list overflows available space, use a stateless scroll window
		// that keeps the cursor row visible (centered when possible).
		if len(taskLines) > availForTasks {
			visible := availForTasks - 1
			if visible < 1 {
				visible = 1
			}
			// Center the window on the cursor.
			scrollStart := cursor - visible/2
			if scrollStart < 0 {
				scrollStart = 0
			}
			if scrollStart+visible > len(taskLines) {
				scrollStart = len(taskLines) - visible
			}
			above := scrollStart
			below := len(taskLines) - (scrollStart + visible)
			shown := make([]string, visible)
			copy(shown, taskLines[scrollStart:scrollStart+visible])
			var footerText string
			switch {
			case above > 0 && below > 0:
				footerText = fmt.Sprintf("  ↑ %d above  ·  +%d more  ·  j/k", above, below)
			case above > 0:
				footerText = fmt.Sprintf("  ↑ %d above  ·  j/k", above)
			default:
				footerText = fmt.Sprintf("  +%d more tasks  ·  j/k to navigate", below)
			}
			taskLines = append(shown, styleMuted.Italic(true).Render(footerText))
		}
		lines = append(lines, styleLabel.Render(taskHeader))
		lines = append(lines, taskLines...)
		// Done tasks section (toggle with 'd').
		if m.workspace.showDoneTasks && proj != nil && len(proj.DoneTasks) > 0 {
			// EX-115: filter done tasks by the active query, consistent with the open-task section.
			var filteredDone []SidebarTaskItem
			for _, t := range proj.DoneTasks {
				doneLabel := t.Title
				if t.TaskNumber > 0 {
					doneLabel = fmt.Sprintf("OC-%d: %s", t.TaskNumber, t.Title)
				}
				if matchesFilter(doneLabel, query) || matchesFilter(t.WorkStatus, query) {
					filteredDone = append(filteredDone, t)
				}
			}
			if len(filteredDone) > 0 {
				if len(lines) < maxLines {
					lines = append(lines, "")
				}
				if len(lines) < maxLines {
					doneHeader := fmt.Sprintf("DONE (%d)", len(filteredDone))
					lines = append(lines, styleLabel.Render(doneHeader))
				}
				for _, t := range filteredDone {
					if len(lines) >= maxLines {
						break
					}
					taskLabel := t.Title
					if t.TaskNumber > 0 {
						taskLabel = fmt.Sprintf("OC-%d: %s", t.TaskNumber, t.Title)
					}
					statusText := formatTaskStatus(t.WorkStatus)
					statW := len([]rune(statusText))
					maxTitleW := width - 8 - statW
					if maxTitleW < 4 {
						maxTitleW = 4
					}
					truncTitle := truncate(taskLabel, maxTitleW)
					leftPart := "  ✓ " + truncTitle
					padW := width - lipgloss.Width(leftPart) - statW - 1
					if padW < 1 {
						padW = 1
					}
					spacer := strings.Repeat(" ", padW)
					lines = append(lines, styleMuted.Render(leftPart+spacer+statusText))
				}
			}
		}
		// Navigation hint row.
		// EX-233: include filterActionHint so users know / clears an active filter.
		// EX-235: include r·refresh so users know they can reload the task list.
		hintParts := "Enter·open  ·  " + filterActionHint(query) + "  ·  Tab·navigate  ·  Esc·dashboard"
		if proj != nil && proj.DoneCount > 0 {
			if m.workspace.showDoneTasks {
				hintParts += "  ·  d·hide done"
			} else {
				hintParts += fmt.Sprintf("  ·  d·show %d done", proj.DoneCount)
			}
		}
		if len(lines) < maxLines {
			lines = append(lines, styleMuted.Render("  "+hintParts))
		}
	}

	return lines
}

func (m Model) renderTaskView(width, maxLines int) []string {
	task := m.workspace.tasks[m.workspace.selectedTaskID]
	if task == nil {
		// EX-236: distinguish "loading" from "not selected" so the window between
		// pressing Enter on a task card and the API response arriving doesn't look
		// like a dead end.
		if m.workspace.selectedTaskID != "" {
			return []string{
				"",
				lipgloss.NewStyle().Width(width).Align(lipgloss.Center).
					Foreground(colMuted).Render("◌  Loading task detail…"),
				"",
				styleMuted.Render("  r·retry  ·  Esc·back"),
			}
		}
		// EX-212: include a navigation hint so the user isn't left with a dead end.
		return []string{
			styleMuted.Render("  No task selected"),
			"",
			styleMuted.Render("  Select a task from the dashboard or sidebar, then press t"),
			"",
			styleMuted.Render("  Esc·dashboard"),
		}
	}

	var lines []string
	lines = append(lines, "")

	statusColor := taskStatusColor(task.Status)

	taskTitle := task.Title
	if task.TaskNumber > 0 {
		taskTitle = fmt.Sprintf("OC-%d: %s", task.TaskNumber, task.Title)
	}
	titleLine := styleBold.Render(truncate(taskTitle, width-4))
	statusLine := lipgloss.NewStyle().Foreground(statusColor).Render("  Status: " + formatTaskStatus(task.Status))

	lines = append(lines, titleLine)
	lines = append(lines, statusLine)
	// Show parent project name if discoverable via sidebar node graph
	taskNodeID := "task-" + task.ID
	if taskNode := m.workspace.nodes[taskNodeID]; taskNode != nil && taskNode.ParentID != "" {
		if projNode := m.workspace.nodes[taskNode.ParentID]; projNode != nil {
			lines = append(lines, styleMuted.Render("  Project: "+projNode.Label))
		}
	}
	if task.AgentName != "" {
		lines = append(lines, styleMuted.Render("  Agent: "+task.AgentName))
	}
	if task.Flow > 0 || task.FlowNodeName != "" {
		flowLine := "  Flow: "
		if task.FlowNodeName != "" {
			flowLine += task.FlowNodeName
			if task.Flow > 0 {
				flowLine += fmt.Sprintf(" (step %d)", task.Flow)
			}
		} else {
			flowLine += fmt.Sprintf("step %d", task.Flow)
		}
		lines = append(lines, styleMuted.Render(flowLine))
	}
	if task.RequiresHumanReview {
		lines = append(lines, lipgloss.NewStyle().Foreground(colWarning).Render("  ⚠  Human review required"))
	}

	if desc := strings.TrimSpace(task.Description); desc != "" {
		lines = append(lines, "")
		lines = append(lines, divider(width, "Description"))
		for _, wrapped := range wrapText(desc, maxInt(10, width-4)) {
			lines = append(lines, styleText.Render("  "+wrapped))
		}
	}

	if acceptance := strings.TrimSpace(task.AcceptanceCriteria); acceptance != "" {
		lines = append(lines, "")
		lines = append(lines, divider(width, "Acceptance Criteria"))
		for _, wrapped := range wrapText(acceptance, maxInt(10, width-6)) {
			lines = append(lines, styleMuted.Render("  ✓ "+wrapped))
		}
	}

	if len(task.Subtasks) > 0 {
		lines = append(lines, "")
		lines = append(lines, divider(width, "Subtasks"))
		for _, subtask := range task.Subtasks {
			st := strings.TrimSpace(subtask)
			if st == "" {
				continue
			}
			lines = append(lines, styleMuted.Render("  • "+truncate(st, width-6)))
		}
	}

	sessionID := strings.TrimSpace(task.SessionID)
	if sessionID == "" {
		sessionID = m.workspace.taskSessionID(task.ID)
	}

	if len(task.History) > 0 {
		lines = append(lines, "")
		lines = append(lines, divider(width, "Event Log"))
		for _, h := range task.History {
			lines = append(lines, styleMuted.Render("  · "+h))
		}
	}

	lines = append(lines, "")
	if task.RequiresHumanReview {
		lines = append(lines, lipgloss.NewStyle().Foreground(colWarning).Render("  a·approve  ·  x·reject  ·  f·defer  ·  o·open task session"))
	}
	// Build hint parts using "  ·  " separator (consistent with project view).
	var hintParts []string
	if sessionID != "" {
		switch task.Status {
		case "in_progress":
			hintParts = append(hintParts, "Enter·resume session")
		case "done", "approved":
			hintParts = append(hintParts, "Enter·view session log")
		default:
			hintParts = append(hintParts, "Enter·open session")
		}
	}
	// Esc destination depends on whether there is a project context.
	if m.workspace.selectedProjectID != "" {
		hintParts = append(hintParts, "Esc·project")
	} else {
		hintParts = append(hintParts, "Esc·dashboard")
	}
	hintParts = append(hintParts, "Tab·navigate")
	if m.workspace.selectedProjectID != "" {
		hintParts = append(hintParts, "p·project view")
		openTasks := m.workspace.openTasksForProject()
		if len(openTasks) >= 2 {
			// EX-106: show "N of M" position so users know where they are in the list.
			cursor := m.workspace.projectTaskCursor
			if cursor < 0 {
				cursor = 0
			}
			if cursor >= len(openTasks) {
				cursor = len(openTasks) - 1
			}
			hintParts = append(hintParts, fmt.Sprintf("j/k·next/prev task  (%d of %d)", cursor+1, len(openTasks)))
		}
	}
	lines = append(lines, styleMuted.Render("  "+strings.Join(hintParts, "  ·  ")))

	return lines
}

func (m Model) renderInboxView(width, maxLines int) []string {
	query := normalizedFilterQuery(m.mainFilter)
	filteredInbox := make([]inboxItem, 0, len(m.workspace.inbox))
	for _, item := range m.workspace.inbox {
		if matchesFilter(item.Summary, query) || matchesFilter(item.TaskID, query) {
			filteredInbox = append(filteredInbox, item)
		}
	}

	if len(filteredInbox) == 0 {
		// EX-111: distinguish between a genuinely empty inbox and a filter with no matches.
		var emptyMsg string
		if query != "" && len(m.workspace.inbox) > 0 {
			emptyMsg = fmt.Sprintf("no inbox items matching %q", query)
		} else {
			emptyMsg = "✓ Inbox clear"
		}
		// EX-211: add navigation hint so users know how to leave the empty inbox.
		return []string{
			"",
			lipgloss.JoinHorizontal(lipgloss.Center,
				lipgloss.NewStyle().Width(width).Align(lipgloss.Center).
					Foreground(colMuted).Render(emptyMsg),
			),
			"",
			styleMuted.Render("  "+filterActionHint(query)+"  ·  Tab·navigate  ·  Esc·dashboard"),
		}
	}

	var lines []string
	lines = append(lines, "")
	current := m.workspace.currentInboxItem()
	currentID := ""
	if current != nil {
		currentID = current.ID
	}
	for i, item := range filteredInbox {
		isCursor := item.ID == currentID
		if currentID == "" {
			isCursor = i == 0
		}

		var prefix string
		var rowStyle lipgloss.Style
		if isCursor {
			prefix = "► "
			rowStyle = lipgloss.NewStyle().Foreground(colFocus).Bold(true)
		} else {
			prefix = "  "
			rowStyle = styleText
		}

		summary := truncate(item.Summary, width-6)
		// Show OC-N label for task badge instead of raw UUID
		taskBadgeLabel := item.TaskID
		if task := m.workspace.tasks[item.TaskID]; task != nil && task.TaskNumber > 0 {
			taskBadgeLabel = fmt.Sprintf("OC-%d", task.TaskNumber)
		} else if len(item.TaskID) > 8 {
			taskBadgeLabel = item.TaskID[:8] + "…"
		}
		taskBadge := lipgloss.NewStyle().Foreground(colMuted).Render("  " + taskBadgeLabel)
		lines = append(lines, rowStyle.Render(prefix+summary)+taskBadge)

		if isCursor {
			// EX-124: show "N of M" position so users know where they are in
			// the inbox list, consistent with EX-106 for the project view.
			posHint := ""
			if len(filteredInbox) > 1 {
				posHint = fmt.Sprintf("  ·  %d of %d", i+1, len(filteredInbox))
			}
			actions := styleMuted.Render("  a·approve  ·  x·reject  ·  f·defer  ·  o·open" + posHint)
			lines = append(lines, actions)
		}
	}
	// EX-230: footer hint so users know about filter even when the inbox has items.
	lines = append(lines, "")
	lines = append(lines, styleMuted.Render("  "+filterActionHint(query)+"  ·  Tab·navigate  ·  Esc·dashboard"))

	return lines
}

// activityIcon returns a coloured icon appropriate for an activity log entry.
// Task status entries (added by EX-092) get icons that reflect the new status.
// EX-147: extended to handle run failures, rollbacks, review rejections, and
// deploy approvals added in EX-143 through EX-146.
func activityIcon(entry string) string {
	lower := strings.ToLower(entry)
	// Explicit error indicators (run failures, dead-letter, review rejections, task errors).
	for _, marker := range []string{
		": blocked", ": rejected", ": deferred", ": failed",
		"run failed", "dead-lettered", "review rejected",
		"agent expired",
		// EX-158: policy denials are errors (tool was blocked from executing).
		"policy denied",
	} {
		if strings.Contains(lower, marker) {
			return lipgloss.NewStyle().Foreground(colError).Render("✗ ")
		}
	}
	// Warning/pending indicators (in-progress tasks, rollbacks, pending approvals).
	for _, marker := range []string{
		": in progress", ": in_progress", ": started",
		"rollback", "deploy pending approval", "supervisor escalation",
		// EX-158: budget anomalies and worker problems are warnings (not errors, action may not be needed).
		"budget anomaly", "worker unresponsive",
	} {
		if strings.Contains(lower, marker) {
			return lipgloss.NewStyle().Foreground(colWarning).Render("◌ ")
		}
	}
	// Everything else (done, connected, loaded, merged, session created, etc.) uses success check.
	return lipgloss.NewStyle().Foreground(colConnected).Render("✓ ")
}

func (m Model) renderActivityView(width, maxLines int) []string {
	var lines []string
	lines = append(lines, "")
	query := normalizedFilterQuery(m.mainFilter)
	filteredActivity := make([]string, 0, len(m.workspace.activity))
	for _, entry := range m.workspace.activity {
		if matchesFilter(entry, query) {
			filteredActivity = append(filteredActivity, entry)
		}
	}
	// EX-215: reserve lines for the hint footer so it's always visible.
	// Without indicator: 1(blank) + entries + 1(sep) + 1(hint) = entries+3 → cap at maxLines-3
	// With indicator:    1(blank) + 1(indicator) + entries + 1(sep) + 1(hint) = entries+4 → cap at maxLines-4
	entryCap := maxLines - 3
	if entryCap < 1 {
		entryCap = 1
	}
	// If truncation will be needed, tighten cap to also fit the "↑ N above" indicator.
	if len(filteredActivity) > entryCap {
		entryCap = maxLines - 4
		if entryCap < 1 {
			entryCap = 1
		}
	}
	start := 0
	if len(filteredActivity) > entryCap {
		start = len(filteredActivity) - entryCap
	}
	// Show "↑ N older entries hidden" indicator when activity is truncated.
	if start > 0 {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("  ↑ %d older entries hidden  (/ to filter)", start)))
	}
	for _, entry := range filteredActivity[start:] {
		icon := activityIcon(entry)
		lines = append(lines, icon+styleText.Render(truncate(entry, width-4)))
	}
	// EX-112: distinguish filtered-out vs genuinely-empty activity log.
	if len(lines) == 1 {
		if query != "" && len(m.workspace.activity) > 0 {
			lines = append(lines, styleMuted.Render(fmt.Sprintf("  no activity matching %q", query)))
		} else {
			lines = append(lines, styleMuted.Render("  No activity yet"))
		}
	}
	// EX-210: hint footer so users know which keys work in this view.
	lines = append(lines, "")
	lines = append(lines, styleMuted.Render("  "+filterActionHint(query)+"  ·  Tab·navigate  ·  Esc·dashboard"))
	return lines
}

func (m Model) renderAgentsView(width, maxLines int) []string {
	var lines []string
	lines = append(lines, "")
	if len(m.workspace.agents) == 0 {
		lines = append(lines, lipgloss.NewStyle().Width(width).Align(lipgloss.Center).
			Foreground(colSubtle).Render("no agents loaded"))
		// EX-210: hint footer even on empty state.
		lines = append(lines, "")
		lines = append(lines, styleMuted.Render("  Tab·navigate  ·  Esc·dashboard"))
		return lines
	}
	// EX-110: apply search filter so agents view responds to mainFilter like all other views.
	query := normalizedFilterQuery(m.mainFilter)

	// Collect matching agents first so we know if truncation will occur.
	type agentEntry struct{ name, status string }
	var matching []agentEntry
	for _, agent := range m.workspace.agents {
		parts := strings.SplitN(agent, "=", 2)
		name, status := agent, ""
		if len(parts) == 2 {
			name, status = parts[0], parts[1]
		}
		if matchesFilter(name, query) || matchesFilter(status, query) {
			matching = append(matching, agentEntry{name, status})
		}
	}

	// EX-215: reserve 3 lines for hint footer; EX-226-agents: one more when truncating.
	itemCap := maxLines - 3
	if itemCap < 1 {
		itemCap = 1
	}
	hidden := 0
	if len(matching) > itemCap {
		itemCap = maxLines - 4
		if itemCap < 1 {
			itemCap = 1
		}
		hidden = len(matching) - itemCap
	}
	for i, ag := range matching {
		if i >= itemCap {
			break
		}
		var dot string
		switch strings.ToLower(ag.status) {
		case "active", "online":
			dot = styleConnected.Render("● ")
		case "paused", "idle", "draft":
			dot = styleReconnecting.Render("◌ ")
		default: // retired, cancelled, unknown
			dot = styleMuted.Render("○ ")
		}
		agentLine := dot + styleBold.Render(ag.name)
		if ag.status != "" {
			agentLine += styleMuted.Render("  " + ag.status)
		}
		lines = append(lines, agentLine)
	}
	if hidden > 0 {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("  +%d more  (/ to filter)", hidden)))
	}
	if len(matching) == 0 && query != "" {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("  no agents matching %q", query)))
	}
	// EX-210: hint footer so users know which keys work in this view.
	lines = append(lines, "")
	lines = append(lines, styleMuted.Render("  "+filterActionHint(query)+"  ·  Tab·navigate  ·  Esc·dashboard"))
	return lines
}

func (m Model) renderMergesView(width, maxLines int) []string {
	var lines []string
	lines = append(lines, "")
	// EX-113: apply filter to merges and schedules views.
	query := normalizedFilterQuery(m.mainFilter)

	// Collect matching items first so we know if truncation will occur.
	var matching []string
	for _, pr := range m.workspace.mergeQueue {
		if matchesFilter(pr, query) {
			matching = append(matching, pr)
		}
	}

	// EX-215: reserve 3 lines for hint footer (blank + hint); EX-226: one more
	// when a truncation indicator is needed.
	itemCap := maxLines - 3
	if itemCap < 1 {
		itemCap = 1
	}
	hidden := 0
	if len(matching) > itemCap {
		// Tighten cap to make room for the "+N more" indicator line.
		itemCap = maxLines - 4
		if itemCap < 1 {
			itemCap = 1
		}
		hidden = len(matching) - itemCap
	}
	for i, pr := range matching {
		if i >= itemCap {
			break
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(colAccent).Render("⎇ ")+styleText.Render(pr))
	}
	if hidden > 0 {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("  +%d more  (/ to filter)", hidden)))
	}
	if len(lines) == 1 {
		if query != "" && len(m.workspace.mergeQueue) > 0 {
			lines = append(lines, styleMuted.Render(fmt.Sprintf("  no merges matching %q", query)))
		} else {
			lines = append(lines, styleMuted.Render("  No pending merges"))
		}
	}
	// EX-210: hint footer so users know which keys work in this view.
	lines = append(lines, "")
	lines = append(lines, styleMuted.Render("  "+filterActionHint(query)+"  ·  Tab·navigate  ·  Esc·dashboard"))
	return lines
}

func (m Model) renderSchedulesView(width, maxLines int) []string {
	var lines []string
	lines = append(lines, "")
	// EX-113: apply filter to schedules view.
	query := normalizedFilterQuery(m.mainFilter)

	// Collect matching items first so we know if truncation will occur.
	var matching []string
	for _, s := range m.workspace.schedules {
		if matchesFilter(s, query) {
			matching = append(matching, s)
		}
	}

	// EX-215: reserve 3 lines for hint footer; EX-226: one more when truncating.
	itemCap := maxLines - 3
	if itemCap < 1 {
		itemCap = 1
	}
	hidden := 0
	if len(matching) > itemCap {
		itemCap = maxLines - 4
		if itemCap < 1 {
			itemCap = 1
		}
		hidden = len(matching) - itemCap
	}
	for i, s := range matching {
		if i >= itemCap {
			break
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(colPrimary).Render("⏰ ")+styleText.Render(s))
	}
	if hidden > 0 {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("  +%d more  (/ to filter)", hidden)))
	}
	if len(lines) == 1 {
		if query != "" && len(m.workspace.schedules) > 0 {
			lines = append(lines, styleMuted.Render(fmt.Sprintf("  no schedules matching %q", query)))
		} else {
			lines = append(lines, styleMuted.Render("  No schedules"))
		}
	}
	// EX-210: hint footer so users know which keys work in this view.
	lines = append(lines, "")
	lines = append(lines, styleMuted.Render("  "+filterActionHint(query)+"  ·  Tab·navigate  ·  Esc·dashboard"))
	return lines
}

// helpViewLineCount is the total number of content lines in renderHelpView before
// scroll clamping. Must stay in sync with the lines slice built inside that function.
// Verified by TestHelpViewLineCountMatchesEX255; update when adding/removing entries.
const helpViewLineCount = 68

func (m Model) renderHelpView(width, maxLines int) []string {
	header := func(s string) string {
		return lipgloss.NewStyle().Foreground(colFocus).Bold(true).Render(s)
	}
	kw := 16 // key column width
	key := func(k, desc string) string {
		col := lipgloss.NewStyle().Foreground(colPrimary).Bold(true).Render(fmt.Sprintf("%-*s", kw, k))
		return "  " + col + "  " + styleMuted.Render(desc)
	}

	lines := []string{
		"",
		header("Sidebar  (press 1 to focus)"),
		key("j/k  ↑/↓", "move cursor up/down"),
		key("PgUp / PgDn", "page up/down (8 items at a time)"),
		key("g/G  Home/End", "jump to first/last item"),
		key("h/l  ←/→", "collapse/expand section or project"),
		key("Space", "toggle expand project / section"),
		key("Enter", "select: open session or project"),
		key("/ or Esc", "search filter / focus main panel"),
		key("s", "toggle sidebar (below 100 cols)"),
		"",
		header("Main Panel  (press 2 to focus)"),
		key("j/k  ↑/↓", "navigate tasks / next·prev task in task detail"),
		key("PgUp / PgDn", "page up/down (8 items)  — navigate tasks in task detail"),
		key("g/G  Home/End", "jump to first/last in view"),
		key("Enter", "open task detail or inbox item"),
		key("Esc", "back to project (from task) / dashboard"),
		key("d", "toggle done tasks section (project view only)"),
		key("a / x / f / o", "approve/reject/defer/open  (inbox or task view when ⚠ shown)"),
		"",
		header("Chat  (press 3 to focus)"),
		key("Enter", "send message  (or expand/collapse tool call when input is empty)"),
		key("Alt-Enter", "insert newline"),
		key("PgUp / PgDn", "scroll messages"),
		// EX-267: ↑/↓ navigate sent message history (when input is empty), not scroll.
		key("↑ / ↓", "↑ recall / ↓ advance sent message history  (when input is empty)"),
		key("g / G", "jump to oldest / latest message"),
		key("Esc", "cancel turn / clear input / focus main (3 states)"),
		key("Ctrl-U / Ctrl-W", "clear all input / delete last word"),
		key("e / s / d", "queued msg: edit / steer / delete  (when turn active)"),
		"",
		header("Global"),
		key("Tab / Shift-Tab", "cycle panel focus"),
		key("< / >", "resize focused panel (sidebar or chat) narrower / wider"),
		key("1 / 2 / 3", "jump to sidebar/main/chat"),
		key("i", "jump to Inbox"),
		key("d", "jump to Dashboard (or toggle done in project view)"),
		key("p", "return to selected project (if any)"),
		key("t", "return to selected task (if any)"),
		key("n", "jump to next unread session"),
		key("r", "refresh task / project detail (in detail views), or sidebar"),
		// EX-262: [ / ] works from any panel, not just chat — moved to Global.
		key("[ / ]", "cycle chat scope: org | project | task"),
		key("?", "toggle this help screen"),
		// EX-252: Ctrl-G and '0' jump to Frank session; Ctrl-P opens command mode.
		// These are undocumented alternatives that power users find via source.
		key("Ctrl-G / 0", "jump to Frank / General session"),
		key("Ctrl-P / :", "open command palette  (Tab fills top suggestion)"),
		"",
		header("Commands  (press : then Tab to autocomplete)"),
		key(":frank / :general", "switch to Frank or General session"),
		key(":dashboard / :inbox", "navigate to view"),
		key(":project / :task", "navigate to view  (or :project <name> / :task <title> to jump)"),
		// EX-155: previously missing from help view
		key(":agents / :activity", "navigate to agents or activity view"),
		key(":merges / :schedules", "navigate to merges or schedules view"),
		// EX-224/EX-240: dynamic jump commands work both via Tab-autocomplete and
		// by typing the name directly (e.g. :project Acme or :task Deploy frontend).
		key(":session <name>", "jump to a session by name  (Tab autocompletes)"),
		key(":project <name>", "jump to a project by name  (Tab autocompletes)"),
		key(":task <title>", "jump to a task by title  (Tab autocompletes)"),
		key(":scope <level>", "switch chat scope: org | project | task"),
		key(":focus <panel>", "focus sidebar|main|chat"),
		key(":send <message>", "send message to Frank"),
		key(":cancel-turn", "cancel agent turn"),
		key(":queue <action>", "manage queued msgs: edit | steer | delete"),
		key(":sidebar <action>", "sidebar: up|down|home|end|expand|collapse|select"),
		key(":inbox <action>", "inbox: approve|reject|defer|open"),
		key(":tour dismiss", "dismiss the tour overlay"),
		key(":reconnect", "manually trigger SSE reconnect and sidebar refresh"),
		key(":status / :debug", "show conn/scope/session/turn diagnostic in status bar"),
		key(":help", "open keybinding reference (same as ?)"),
		key(":quit", "quit OtterCamp"),
		"",
		// EX-249: include 'q' which also closes help (same as ? and Esc).
		styleMuted.Render("  Press ?, q, or Esc to close"),
	}
	// EX-255: helpViewLineCount must equal len(lines). Verified by TestHelpViewLineCountMatchesEX255.
	total := len(lines)
	if total <= maxLines {
		return lines
	}

	// EX-209: apply scroll offset with above/below indicators.
	offset := m.helpScrollOffset
	if offset < 0 {
		offset = 0
	}
	maxOffset := total - maxLines
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}

	end := offset + maxLines
	if end > total {
		end = total
	}
	visible := make([]string, 0, maxLines)
	visible = append(visible, lines[offset:end]...)

	// Replace first line with "above" scroll indicator when scrolled down.
	if offset > 0 {
		visible[0] = styleMuted.Render(fmt.Sprintf("  ↑ %d above  (k to scroll up)", offset))
	}
	// Replace last line with "below" scroll indicator when more content follows.
	if end < total {
		below := total - end
		visible[len(visible)-1] = styleMuted.Render(fmt.Sprintf("  +%d more below  (j to scroll down)", below))
	}

	return visible
}

// ── Chat panel ──────────────────────────────────────────────────────────────

func (m Model) renderChatPanel(innerW, innerH int, focused bool) string {
	cw := innerW - 2
	if cw < 4 {
		cw = 4
	}

	// Session header — use human-readable label from sidebar node
	rawSession := strings.TrimSpace(m.activeSession)
	sessionLabel := m.workspace.sessionLabel(rawSession)
	if sessionLabel == "" {
		// Use scope-appropriate fallback if UUID can't be resolved yet
		switch m.activeScope {
		case ScopeTask:
			sessionLabel = "Task Session"
		case ScopeProject:
			sessionLabel = "Project Session"
		default:
			sessionLabel = "General / Frank"
		}
	}
	// When viewing a project, append the project name to the session label
	// so the chat header shows context: "Frank / General › OtterCamp Sales Site".
	if m.activeScope == ScopeProject && m.workspace.selectedProjectID != "" {
		projectName := ""
		if m.workspace.selectedProject != nil && m.workspace.selectedProject.DisplayName != "" {
			projectName = m.workspace.selectedProject.DisplayName
		} else if node := m.workspace.nodes["project-"+m.workspace.selectedProjectID]; node != nil {
			projectName = node.Label
		}
		if projectName != "" {
			sessionLabel = sessionLabel + " › " + projectName
		}
	}
	// EX-136: When viewing a task session, append the task number/title to the
	// chat header so the user sees "Task Session › OC-42: title" rather than
	// just "Task Session". Mirrors the ScopeProject context breadcrumb above.
	if m.activeScope == ScopeTask && m.workspace.selectedTaskID != "" {
		if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil {
			taskContext := task.Title
			if task.TaskNumber > 0 {
				taskContext = fmt.Sprintf("OC-%d: %s", task.TaskNumber, task.Title)
			}
			if taskContext != "" {
				sessionLabel = sessionLabel + " › " + truncate(taskContext, 30)
			}
		}
	}
	isThinking := m.activeTurn && (m.activeTurnSessionID == "" ||
		strings.EqualFold(strings.TrimSpace(m.activeSession), m.activeTurnSessionID))
	headerText := renderChatHeader(sessionLabel, m.activeScope, cw, isThinking)
	headerLines := []string{
		headerText,
		styleDivider.Render(strings.Repeat("─", cw)),
	}

	// Keep bottom chrome visible first, then allocate remaining lines to message viewport.
	targetH := innerH
	if targetH < 1 {
		targetH = 1
	}
	inputLines := strings.Split(m.renderChatInputBox(cw, focused), "\n")
	bottomLines := make([]string, 0, len(inputLines)+8)
	bottomLines = append(bottomLines, "")
	// EX-017: show all queued messages (up to 3) ABOVE the input box so they
	// are never clipped by buildPanelContent's end-trim.
	const maxQueueVisible = 3
	for i, q := range m.queuedMessages {
		if i >= maxQueueVisible {
			more := len(m.queuedMessages) - maxQueueVisible
			bottomLines = append(bottomLines, styleMuted.Render(fmt.Sprintf("  +%d more queued", more)))
			break
		}
		qText := truncate(q.Text, cw-12)
		flags := ""
		if q.Steer {
			flags += " [steer]"
		}
		prefix := fmt.Sprintf("  q%d: ", i+1)
		bottomLines = append(bottomLines, styleMuted.Render(prefix)+
			styleSubtle.Render(qText)+
			styleMuted.Render(flags))
	}
	// EX-088: show queue management hint when messages are pending and input is empty.
	// Placed above the input box so it remains visible even on short panels.
	// EX-422: omit s·steer from the hint when activeTurn=false (e.g. after
	// external cancellation) — steer requires an active turn and will show a
	// "Steer requires an active turn." message if pressed in that state.
	if len(m.queuedMessages) > 0 && strings.TrimSpace(m.chatInput) == "" {
		var queueHint string
		if m.activeTurn {
			queueHint = "  e·edit  ·  s·steer  ·  d·delete queued"
		} else {
			queueHint = "  e·edit  ·  d·delete queued"
		}
		bottomLines = append(bottomLines, styleMuted.Render(queueHint))
	}
	bottomLines = append(bottomLines, inputLines...)
	if m.commandMode {
		if suggestions := m.commandPaletteSuggestions(4); len(suggestions) > 0 {
			// EX-228: hint that Tab fills the top suggestion.
			bottomLines = append(bottomLines, styleMuted.Render("  suggestions  (Tab to fill)"))
			for _, suggestion := range suggestions {
				bottomLines = append(bottomLines, styleSubtle.Render("  "+truncate(suggestion, cw-2)))
			}
		}
	}

	msgAreaH := targetH - len(headerLines) - len(bottomLines)
	if msgAreaH < 1 {
		msgAreaH = 1
	}
	allMsgLines := m.renderChatMessages(cw)
	msgLines, scrollOffset, maxOffset := chatViewportLines(allMsgLines, msgAreaH, m.chatScrollOffset)

	// Show scroll indicator when user has scrolled up (newer messages are below)
	if scrollOffset > 0 {
		scrollHint := fmt.Sprintf("↓ %d more  ·  PgDn scroll  ·  G jump to latest", scrollOffset)
		bottomLines = append([]string{lipgloss.NewStyle().
			Foreground(colMuted).Italic(true).
			Width(cw).Align(lipgloss.Center).
			Render(scrollHint)}, bottomLines...)
		// Recalculate msgAreaH since bottomLines grew by 1
		msgAreaH = targetH - len(headerLines) - len(bottomLines)
		if msgAreaH < 1 {
			msgAreaH = 1
		}
		msgLines, _, _ = chatViewportLines(allMsgLines, msgAreaH, m.chatScrollOffset)
	}

	// Show indicator when older messages are hidden above the current view
	hiddenAbove := maxOffset - scrollOffset
	if hiddenAbove > 0 {
		upHint := fmt.Sprintf("↑ %d older  ·  PgUp scroll  ·  g jump to oldest", hiddenAbove)
		upLine := lipgloss.NewStyle().
			Foreground(colMuted).Italic(true).
			Width(cw).Align(lipgloss.Center).
			Render(upHint)
		msgLines = append([]string{upLine}, msgLines...)
		if len(msgLines) > msgAreaH {
			msgLines = msgLines[:msgAreaH]
		}
	}

	lines := make([]string, 0, len(headerLines)+len(msgLines)+len(bottomLines))
	lines = append(lines, headerLines...)
	lines = append(lines, msgLines...)
	lines = append(lines, bottomLines...)

	overflow := len(lines) - targetH
	if overflow > 0 {
		if overflow >= len(msgLines) {
			msgLines = msgLines[:1]
		} else {
			msgLines = msgLines[overflow:]
		}
		lines = make([]string, 0, len(headerLines)+len(msgLines)+len(bottomLines))
		lines = append(lines, headerLines...)
		lines = append(lines, msgLines...)
		lines = append(lines, bottomLines...)
	}

	content := buildPanelContent(lines, targetH, cw)

	return panelStyle(innerW, innerH, focused).Render(content)
}

func renderChatHeader(sessionLabel string, active ChatScope, width int, thinking bool) string {
	separator := styleMuted.Render("  ·  ")
	indicator := renderScopeIndicator(active, false)
	var thinkingBadge string
	if thinking {
		thinkingBadge = "  " + styleReconnecting.Render("◌")
	}
	sessionWidth := width - lipgloss.Width(separator) - lipgloss.Width(indicator) - lipgloss.Width(thinkingBadge)
	if sessionWidth < 8 {
		indicator = renderScopeIndicator(active, true)
		sessionWidth = width - lipgloss.Width(separator) - lipgloss.Width(indicator) - lipgloss.Width(thinkingBadge)
	}
	if sessionWidth < 1 {
		sessionWidth = 1
	}
	return styleActive.Render(truncate(sessionLabel, sessionWidth)) + separator + indicator + thinkingBadge
}

func renderScopeIndicator(active ChatScope, compact bool) string {
	order := []ChatScope{ScopeTask, ScopeProject, ScopeOrg}
	parts := make([]string, 0, len(order))
	for _, scope := range order {
		label := string(scope)
		if compact {
			label = string(label[0])
		}
		token := "[" + label + "]"
		if scope == active {
			parts = append(parts, styleActive.Render(token))
			continue
		}
		parts = append(parts, styleMuted.Render(token))
	}
	return strings.Join(parts, " ")
}

func (m Model) renderChatMessages(width int) []string {
	if len(m.chatMessages) == 0 {
		center := func(s string) string {
			return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Foreground(colSubtle).Render(s)
		}
		if m.chatHistoryLoading {
			return []string{
				"",
				lipgloss.NewStyle().Width(width).Align(lipgloss.Center).
					Foreground(colMuted).Render("◌  loading messages..."),
			}
		}
		if m.activeTurn {
			spinner := styleReconnecting.Render("◌")
			return []string{
				"",
				lipgloss.NewStyle().Width(width).Align(lipgloss.Center).
					Foreground(colMuted).Render(spinner + " waiting for response..."),
			}
		}
		// EX-096: context-aware empty state — show who you're chatting with
		// so the first message feels intentional rather than anonymous.
		agentName := m.assistantLabel()
		lines := []string{
			"",
			center("no messages yet"),
			center("Enter·send a message to " + agentName),
		}
		// EX-104: for task-scoped sessions also show the task name so users
		// immediately know which task this conversation is scoped to.
		if m.activeScope == ScopeTask && m.workspace.selectedTaskID != "" {
			if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil && task.Title != "" {
				taskLabel := task.Title
				if task.TaskNumber > 0 {
					taskLabel = fmt.Sprintf("OC-%d: %s", task.TaskNumber, task.Title)
				}
				lines = append(lines, "")
				lines = append(lines, center(styleMuted.Render(truncate(taskLabel, width-4))))
			}
		}
		return lines
	}

	var lines []string
	for _, msg := range m.chatMessages {
		// EX-218: skip messages that have no displayable content — empty content
		// and no tool calls — to avoid rendering a floating header with no body.
		if strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 {
			continue
		}

		// Role label
		var roleStr lipgloss.Style
		var roleLabel string
		switch strings.ToLower(msg.Role) {
		case "user":
			roleStr = styleUser
			roleLabel = "You"
		case "assistant":
			roleStr = styleAssistant
			// EX-087: resolve agent name from the task record when in a task-scoped
			// session, so multi-agent deployments show the correct agent name.
			roleLabel = m.assistantLabel()
		case "interjection":
			roleStr = styleInterject
			roleLabel = "Interjection (interjected)"
		case "system":
			roleStr = styleMuted
			roleLabel = "System"
		case "tool_result", "tool":
			// EX-227: show a friendly label for tool result messages instead of the raw role.
			roleStr = styleTool
			roleLabel = "Tool Result"
		default:
			roleStr = styleTool
			roleLabel = msg.Role
		}

		left, right, gap := chatHeaderSegments(roleLabel, msg.Timestamp, width)
		if right == "" {
			lines = append(lines, roleStr.Render(left))
		} else {
			header := roleStr.Render(left) + strings.Repeat(" ", gap) + styleMuted.Render(right)
			lines = append(lines, header)
		}
		lines = append(lines, styleDivider.Render(strings.Repeat("─", width)))

		// Message content
		content := strings.TrimSpace(msg.Content)
		if content != "" {
			role := strings.ToLower(strings.TrimSpace(msg.Role))
			var rendered string
			if role == "assistant" || role == "interjection" {
				rendered = strings.TrimSpace(markdownToPlain(content, width))
			} else {
				rendered = strings.Join(wrapText(content, width), "\n")
			}
			if rendered == "" {
				continue
			}
			for _, line := range strings.Split(rendered, "\n") {
				lines = append(lines, styleText.Render(line))
			}
		}

		// Tool calls
		for i, tc := range msg.ToolCalls {
			var statusStyle lipgloss.Style
			statusLabel := strings.ToLower(strings.TrimSpace(tc.Status))
			switch statusLabel {
			case "success", "completed", "done":
				statusStyle = styleTool
			case "pending", "running", "in_progress":
				statusStyle = styleReconnecting
			default:
				statusStyle = styleDisconnected
			}
			if strings.TrimSpace(statusLabel) == "" {
				statusLabel = "pending"
			}
			callID := toolCallIdentity(tc, i)
			expanded := m.isToolCallExpanded(msg.ID, callID)
			indicator := "▶"
			if expanded {
				indicator = "▼"
			}
				// EX-221: fall back to positional label when tool name is missing.
			toolName := tc.Name
			if strings.TrimSpace(toolName) == "" {
				toolName = fmt.Sprintf("tool[%d]", i+1)
			}
			tcLine := "  " + indicator + " ⚙ " + toolName + " (" + statusStyle.Render(statusLabel) + ")"
			lines = append(lines, styleMuted.Render(tcLine))
			if expanded {
				result := strings.TrimSpace(tc.Result)
				if result == "" {
					lines = append(lines, styleSubtle.Render("    (no result yet)"))
					continue
				}
				const maxToolResultRunes = 400
				runes := []rune(result)
				truncated := false
				if len(runes) > maxToolResultRunes {
					result = string(runes[:maxToolResultRunes])
					truncated = true
				}
				for _, rawLine := range strings.Split(result, "\n") {
					for _, wrapped := range wrapText(rawLine, maxInt(8, width-4)) {
						lines = append(lines, styleText.Render("    "+wrapped))
					}
				}
				if truncated {
					// EX-097/EX-197: show how much was cut so users know full output exists.
					full := len([]rune(strings.TrimSpace(tc.Result)))
					lines = append(lines, styleMuted.Render(fmt.Sprintf("    … (%d of %d chars shown)", maxToolResultRunes, full)))
				}
			}
		}

		if !msg.Finalized && msg.Role == "assistant" {
			lines = append(lines, styleReconnecting.Render("  ▌"))
		}

		lines = append(lines, "")
	}

	// EX-085: show "◌ thinking…" when a turn is active but no in-flight
	// assistant message exists yet (gap between user send and first token).
	if m.activeTurn {
		isTurnForSession := m.activeTurnSessionID == "" ||
			strings.EqualFold(strings.TrimSpace(m.activeSession), m.activeTurnSessionID)
		if isTurnForSession {
			alreadyStreaming := false
			if n := len(m.chatMessages); n > 0 {
				last := m.chatMessages[n-1]
				alreadyStreaming = !last.Finalized && strings.EqualFold(last.Role, "assistant")
			}
			if !alreadyStreaming {
				lines = append(lines, styleReconnecting.Render("  ◌  thinking…"))
				lines = append(lines, "")
			}
		}
	}

	return lines
}

func chatViewportLines(lines []string, height, offset int) (visible []string, clampedOffset int, maxOffset int) {
	if height <= 0 {
		return []string{}, 0, 0
	}
	if len(lines) <= height {
		return lines, 0, 0
	}
	maxOffset = len(lines) - height
	clampedOffset = offset
	if clampedOffset < 0 {
		clampedOffset = 0
	}
	if clampedOffset > maxOffset {
		clampedOffset = maxOffset
	}
	end := len(lines) - clampedOffset
	start := end - height
	if start < 0 {
		start = 0
	}
	if end > len(lines) {
		end = len(lines)
	}
	return lines[start:end], clampedOffset, maxOffset
}

func chatHeaderSegments(roleLabel string, ts time.Time, width int) (left string, right string, gap int) {
	if width <= 0 {
		return "", "", 0
	}
	left = truncate(strings.TrimSpace(roleLabel), width)
	right = chatTimestampLabel(ts)
	if right == "" {
		return left, "", 0
	}
	rightWidth := lipgloss.Width(right)
	if rightWidth+1 >= width {
		return left, "", 0
	}
	maxLeft := width - rightWidth - 1
	left = truncate(left, maxLeft)
	gap = width - lipgloss.Width(left) - rightWidth
	if gap < 1 {
		gap = 1
	}
	return left, right, gap
}

func chatTimestampLabel(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	local := ts.Local()
	now := time.Now()
	if local.Year() == now.Year() && local.YearDay() == now.YearDay() {
		return local.Format("15:04")
	}
	yesterday := now.AddDate(0, 0, -1)
	if local.Year() == yesterday.Year() && local.YearDay() == yesterday.YearDay() {
		return "yesterday " + local.Format("15:04")
	}
	return local.Format("Jan 2 15:04")
}

func (m Model) renderChatInputBox(width int, focused bool) string {
	// ╭─────────────────╮
	// │ your text here  │
	// ╰─────────────────╯
	innerW := width - 4 // 2 for border + 2 for padding
	if innerW < 4 {
		innerW = 4
	}

	var inputText string
	if m.commandMode {
		inputText = m.commandBuffer
	} else {
		inputText = m.chatInput
	}

	displayText := inputText

	// EX-016: show character count when message is getting long
	if !m.commandMode && len([]rune(m.chatInput)) > 100 {
		displayText += styleMuted.Render(fmt.Sprintf(" [%d]", len([]rune(m.chatInput))))
	}

	if focused && !m.commandMode {
		displayText += "▌" // cursor
	}

	if displayText == "" && !focused {
		displayText = styleMuted.Render("type a message...")
	}

	boxBc := colSubtle
	if focused {
		boxBc = colPrimary
	}
	if m.commandMode {
		boxBc = colWarning
	}

	boxStyle := lipgloss.NewStyle().
		Width(innerW).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(boxBc)

	prefix := ""
	if m.commandMode {
		prefix = styleReconnecting.Render(":") + " "
		displayText = strings.TrimPrefix(displayText, ":")
		displayText += "▌" // EX-009: cursor always visible in command mode
	}

	return boxStyle.Render(prefix + displayText)
}

type commandSuggestion struct {
	label string
	match string
}

func (m Model) commandPaletteSuggestions(limit int) []string {
	if limit <= 0 {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(m.commandBuffer, ":")))
	if query == "" {
		return nil
	}

	seen := map[string]struct{}{}
	add := func(items *[]commandSuggestion, label string) {
		key := strings.ToLower(strings.TrimSpace(label))
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		*items = append(*items, commandSuggestion{label: label, match: strings.ToLower(label)})
	}

	candidates := make([]commandSuggestion, 0, 50)
	// EX-216: include all valid commands so the suggestion list is complete.
	// EX-245: added "cmd: help" and "cmd: session" which were missing.
	for _, command := range []string{
		"cmd: frank", "cmd: general", "cmd: dashboard", "cmd: project", "cmd: task",
		"cmd: inbox", "cmd: agents", "cmd: agent", "cmd: activity", "cmd: merges", "cmd: schedules",
		"cmd: focus sidebar", "cmd: focus main", "cmd: focus chat",
		"cmd: scope org", "cmd: scope project", "cmd: scope task",
		"cmd: send", "cmd: cancel-turn",
		"cmd: queue edit", "cmd: queue steer", "cmd: queue delete",
		"cmd: sidebar up", "cmd: sidebar down", "cmd: sidebar home", "cmd: sidebar end",
		"cmd: sidebar expand", "cmd: sidebar collapse", "cmd: sidebar select",
		"cmd: inbox approve", "cmd: inbox reject", "cmd: inbox defer", "cmd: inbox open",
		"cmd: tour dismiss", "cmd: help", "cmd: quit",
		// EX-399: include aliases added in EX-390/391/392/395 so Tab autocomplete works.
		"cmd: chat", "cmd: search", "cmd: filter", "cmd: back", "cmd: refresh",
		"cmd: reload", "cmd: n", "cmd: next", "cmd: version", "cmd: open", "cmd: close",
		"cmd: man", "cmd: history",
		// EX-402: include EX-395 hint-only commands (settings, undo, etc.) and
		// additional commonly-tried commands (clear, sort, ls) so typing their
		// prefix in command mode surfaces a Tab-completable suggestion rather than
		// leaving the user with no autocomplete hint.
		"cmd: settings", "cmd: config", "cmd: undo", "cmd: redo",
		"cmd: copy", "cmd: yank", "cmd: paste",
		"cmd: clear", "cmd: sort", "cmd: ls",
		// EX-406: :reconnect/:connect for manual SSE reconnect when degraded.
		"cmd: reconnect", "cmd: connect",
		// EX-407: :status/:info/:debug for diagnostic connection/session summary.
		"cmd: status", "cmd: info", "cmd: debug",
	} {
		add(&candidates, command)
	}
	for _, id := range m.workspace.visibleSidebarIDs() {
		node := m.workspace.nodes[id]
		if node == nil {
			continue
		}
		switch node.Kind {
		case sidebarKindSession:
			add(&candidates, "session: "+node.Label)
		case sidebarKindProject:
			add(&candidates, "project: "+node.Label)
		}
	}
	for _, taskID := range m.workspace.taskOrder {
		task := m.workspace.tasks[taskID]
		if task == nil {
			continue
		}
		add(&candidates, "task: "+task.Title)
	}

	filtered := make([]commandSuggestion, 0, len(candidates))
	for _, candidate := range candidates {
		if fuzzyMatch(query, candidate.match) {
			filtered = append(filtered, candidate)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		ai := strings.Index(filtered[i].match, query)
		aj := strings.Index(filtered[j].match, query)
		if ai == -1 {
			ai = 1 << 20
		}
		if aj == -1 {
			aj = 1 << 20
		}
		if ai != aj {
			return ai < aj
		}
		if len(filtered[i].label) != len(filtered[j].label) {
			return len(filtered[i].label) < len(filtered[j].label)
		}
		return filtered[i].label < filtered[j].label
	})

	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	out := make([]string, 0, len(filtered))
	for _, candidate := range filtered {
		out = append(out, candidate.label)
	}
	return out
}

func fuzzyMatch(query, candidate string) bool {
	query = strings.TrimSpace(strings.ToLower(query))
	candidate = strings.TrimSpace(strings.ToLower(candidate))
	if query == "" {
		return true
	}
	if strings.Contains(candidate, query) {
		return true
	}

	qr := []rune(query)
	i := 0
	for _, r := range candidate {
		if i < len(qr) && r == qr[i] {
			i++
		}
		if i == len(qr) {
			return true
		}
	}
	return false
}

// ── Status bar ──────────────────────────────────────────────────────────────

func (m Model) renderStatusBar(layout layoutState, focus Panel) string {
	dot := connectionDot(m.connection, m.streamDegraded)

	connText := string(m.connection)
	if m.streamDegraded {
		connText += " (degraded)"
	}
	var connStyle lipgloss.Style
	switch {
	case m.connection == ConnectionConnected && !m.streamDegraded:
		connStyle = styleConnected
	case m.streamDegraded || m.connection == ConnectionReconnecting:
		connStyle = styleReconnecting
	default:
		connStyle = styleDisconnected
	}

	// EX-006: show human-readable session label for the currently active session
	rawSession := strings.TrimSpace(m.activeSession)
	session := m.workspace.sessionLabel(rawSession)
	if session == "" {
		// Scope-aware fallback
		switch m.activeScope {
		case ScopeTask:
			session = "Task Session"
		case ScopeProject:
			session = "Project Session"
		default:
			session = "Frank"
		}
	}
	sizeStr := string(layout.sizeClass)
	// Show view name when main panel is focused (more informative than "main")
	focusStr := panelLabel(focus)
	if focus == MainPanel {
		focusStr = strings.ToLower(string(m.workspace.mainView))
	}

	status := ""
	if m.statusMessage != "" {
		// EX-141: raised from 60 to 80 to accommodate warning messages (⚠ prefix)
		// and the worker-offline message (~67 chars) without truncation. Still safe
		// on terminals as narrow as ~90 columns since the status bar has room after
		// the connection dot, scope label, and size indicator.
		status = styleMuted.Render("  ·  ") + styleSubtle.Render(truncate(m.statusMessage, 80))
	}

	// EX-043: show ◌ in status bar when agent turn is active
	sessionDisplay := session
	if m.activeTurn {
		sessionDisplay += " " + styleReconnecting.Render("◌")
	}
	bar := dot + "  " + connStyle.Render(connText) +
		styleMuted.Render("  ·  ") + styleMuted.Render(sessionDisplay) +
		styleMuted.Render("  ·  ") + styleSubtle.Render(sizeStr+"/"+focusStr) +
		status

	// EX-101: inbox badge — alert users to pending inbox items even when the
	// sidebar is collapsed (narrow screens) and the inbox count isn't visible.
	if m.workspace.inboxCount > 0 && m.workspace.mainView != ViewInbox {
		bar += styleMuted.Render("  ·  ") +
			lipgloss.NewStyle().Foreground(colWarning).Render(fmt.Sprintf("✉ %d", m.workspace.inboxCount))
	}

	if m.firstRun {
		poL := ""
		if m.proofRealtime {
			poL += "  " + styleConnected.Render("✓ realtime")
		}
		if m.proofReplay {
			poL += "  " + styleConnected.Render("✓ replay")
		}
		if poL != "" {
			bar += poL
		}
	}

	if layout.hiddenHints != "" {
		bar += styleMuted.Render("  ·  ") + styleSubtle.Render(layout.hiddenHints)
	}

	return lipgloss.NewStyle().
		Background(colStatusBg).
		Width(layout.contentSize).
		Padding(0, 1).
		Render(bar)
}

// ── Help line ────────────────────────────────────────────────────────────────

func (m Model) renderHelpLine() string {
	help := "Help: " + m.commandFallbackHelp()
	return styleMuted.Render(help)
}

// ── Degraded banner ──────────────────────────────────────────────────────────

func (m Model) degradedModeBanner() string {
	if m.connection == ConnectionConnected && !m.streamDegraded {
		return ""
	}
	icon := styleDisconnected.Render("⚠ ")
	label := lipgloss.NewStyle().Foreground(colError).Bold(true).Render("DEGRADED MODE") + ": "
	// EX-100: context-aware message based on the actual connection state so
	// the banner is actionable rather than showing developer jargon.
	var msg string
	switch {
	case m.connection == ConnectionDisconnected:
		msg = "Connection lost — data may be stale. Reconnecting automatically."
	case m.connection == ConnectionReconnecting:
		msg = "Reconnecting to server…"
	default: // connected but event stream is stale
		msg = "Event stream stale — some data may be delayed. Press r to refresh."
	}
	// EX-020: truncate the banner message to fit the terminal width
	if m.width > 0 {
		overhead := lipgloss.Width(icon) + lipgloss.Width(label) + 2
		available := m.width - overhead
		if available > 10 {
			msg = truncate(msg, available)
		}
	}
	return icon + label + styleDegraded.Render(msg)
}

// ── Utilities ────────────────────────────────────────────────────────────────

// filterActionHint returns "/ · clear filter" when a filter is active and
// "/ · filter" otherwise, so the hint footer is always actionable. EX-229.
func filterActionHint(activeFilter string) string {
	if strings.TrimSpace(activeFilter) != "" {
		return "/·clear filter"
	}
	return "/·filter"
}

// prefixLines prepends prefix to every line in a potentially multi-line string.
// Used to apply gutter indent to the full panel row in XL layouts.
func prefixLines(s, prefix string) string {
	if prefix == "" {
		return s
	}
	parts := strings.Split(s, "\n")
	for i, p := range parts {
		parts[i] = prefix + p
	}
	return strings.Join(parts, "\n")
}

// buildPanelContent joins lines and pads/trims to exactly targetH lines of content.
// It handles embedded newlines in individual items by splitting the joined result
// and trimming to exactly targetH actual lines.
func buildPanelContent(lines []string, targetH, width int) string {
	// Clamp each line's visible width so that lipgloss does not word-wrap
	// inside panelStyle.Render(), which would produce extra rows and push
	// the input box off the bottom of the chat panel.
	if width > 0 {
		for i, line := range lines {
			if lipgloss.Width(line) > width {
				lines[i] = ansitrunc.String(line, uint(width))
			}
		}
	}
	// Trim to fit
	if len(lines) > targetH {
		lines = lines[:targetH]
	}
	// Pad with empty lines
	for len(lines) < targetH {
		lines = append(lines, "")
	}
	result := strings.Join(lines, "\n")
	// Post-process: if any items contained embedded newlines, the joined result
	// may have more than targetH actual lines. Trim the excess.
	actual := strings.Split(result, "\n")
	if len(actual) > targetH {
		actual = actual[:targetH]
		return strings.Join(actual, "\n")
	}
	return result
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	diff := time.Since(t)
	switch {
	case diff < time.Minute:
		return "now"
	case diff < time.Hour:
		return fmt.Sprintf("%dm", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh", int(diff.Hours()))
	default:
		return fmt.Sprintf("%dd", int(diff.Hours()/24))
	}
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-1]) + "…"
}

func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	var result []string
	for _, para := range strings.Split(text, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			result = append(result, "")
			continue
		}
		line := ""
		for _, word := range words {
			if line == "" {
				line = word
			} else if len(line)+1+len(word) <= width {
				line += " " + word
			} else {
				result = append(result, line)
				line = word
			}
		}
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}
