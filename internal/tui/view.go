package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
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
		banner := lipgloss.NewStyle().Foreground(colFocus).Bold(true).Render("  OTTERCAMP") +
			styleMuted.Render(" // FIRST RUN") +
			"  " + styleMuted.Render("Booting operator console...")
		sections = append(sections, prefix+banner)
	}

	if banner := m.degradedModeBanner(); banner != "" {
		sections = append(sections, prefix+banner)
	}

	sections = append(sections, prefixLines(panelRow, prefix))
	sections = append(sections, prefix+m.renderStatusBar(layout, focus))
	sections = append(sections, prefix+m.renderHelpLine())

	if m.tourActive {
		tour := styleMuted.Render("Tour: ") +
			styleSubtle.Render("1/sidebar  2/main  3/chat") +
			styleMuted.Render("  ·  ") +
			styleSubtle.Render(":frank  :inbox  :tour dismiss")
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

	displayLines := 0
	lastRendered := 0
	for i, id := range visible {
		if displayLines >= maxNodeLines {
			break
		}
		node := m.workspace.nodes[id]
		if node == nil {
			lastRendered = i + 1
			continue
		}
		// Add section divider above each header (except the very first node)
		if node.Kind == sidebarKindHeader && len(lines) > 0 {
			if displayLines >= maxNodeLines {
				break
			}
			lines = append(lines, styleMuted.Render(strings.Repeat("─", maxInt(1, cw-2))))
			displayLines++
			if displayLines >= maxNodeLines {
				break
			}
		}
		lines = append(lines, m.renderSidebarNode(node, i == m.workspace.sidebarCursor, cw, iconOnly))
		displayLines++
		lastRendered = i + 1
	}
	if remaining := len(visible) - lastRendered; remaining > 0 && rowsForBody > 0 {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("  +%d more", remaining)))
	}

	if searchLine != "" {
		lines = append(lines, "")
		lines = append(lines, searchLine)
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
	case sidebarKindSession:
		if node.ParentID != "" {
			prefix = "    › "
		} else if node.SessionScope == "project_task" && node.WorkStatus != "" {
			// Show task work status icon for task-scoped chat sessions
			switch node.WorkStatus {
			case "done", "approved":
				prefix = "  ● "
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
		// Always keep structural anchors
		if node.Kind == sidebarKindHeader || node.Kind == sidebarKindInbox {
			include[id] = struct{}{}
			continue
		}
		if matchesFilter(node.Label, query) {
			include[id] = struct{}{}
			if node.ParentID != "" {
				include[node.ParentID] = struct{}{}
			}
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
	}
	return styleMuted.Render(truncate("Search "+prompt, width))
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
		lines = append(lines, "")
		lines = append(lines, searchLine)
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

	// Board columns
	lines = append(lines, "")
	boardTitle := "Task Board"
	if m.workspace.selectedProjectID != "" {
		if projNode := m.workspace.nodes["project-"+m.workspace.selectedProjectID]; projNode != nil {
			boardTitle = projNode.Label + " — Task Board"
		}
	}
	lines = append(lines, styleLabel.Render(boardTitle))

	// Build three columns
	colW := (width - 6) / 3
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
	if width > 80 && counts.Blocked > 0 {
		visibleHdrs = append(visibleHdrs, blockedHdr)
		visibleSeps = append(visibleSeps, blockedSep)
	}
	// Render header and separator as two separate lines (no embedded newlines)
	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, visibleHdrs...))
	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, visibleSeps...))

	// Task rows under each column
	cursorID := m.workspace.selectedTaskID
	todoTasks, inProgTasks, doneTasks := []string{}, []string{}, []string{}
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
		switch task.Status {
		case "draft", "todo":
			if isCursor {
				entry := truncate("► "+taskLabel, colW)
				todoTasks = append(todoTasks, styleBold.Foreground(colFocus).Render(entry))
			} else {
				entry := truncate("○ "+taskLabel, colW)
				todoTasks = append(todoTasks, styleText.Render(entry))
			}
		case "done", "approved", "cancelled":
			entry := truncate("✓ "+taskLabel, colW)
			doneTasks = append(doneTasks, styleMuted.Render(entry))
		case "blocked", "rejected", "deferred":
			// omit from column view; reflected in blocked count only
		default: // in_progress and unknown active statuses
			if isCursor {
				entry := truncate("► "+taskLabel, colW)
				inProgTasks = append(inProgTasks, styleBold.Foreground(colFocus).Render(entry))
			} else {
				entry := truncate("◌ "+taskLabel, colW)
				inProgTasks = append(inProgTasks, lipgloss.NewStyle().Foreground(colWarning).Render(entry))
			}
		}
	}
	taskRowCount := maxInt(len(todoTasks), maxInt(len(inProgTasks), len(doneTasks)))
	for i := 0; i < taskRowCount && i < 4; i++ {
		row := [3]string{"", "", ""}
		if i < len(todoTasks) {
			row[0] = todoTasks[i]
		}
		if i < len(inProgTasks) {
			row[1] = inProgTasks[i]
		}
		if i < len(doneTasks) {
			row[2] = doneTasks[i]
		}
		r0 := lipgloss.NewStyle().Width(colW).Render(row[0])
		r1 := lipgloss.NewStyle().Width(colW).Render(row[1])
		r2 := lipgloss.NewStyle().Width(colW).Render(row[2])
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, r0, r1, r2))
	}
	// EX-008: show per-column overflow indicator when more than 4 tasks in any column
	if taskRowCount > 4 {
		todoOver, inProgOver, doneOver := "", "", ""
		if len(todoTasks) > 4 {
			todoOver = styleMuted.Render(fmt.Sprintf("  +%d more", len(todoTasks)-4))
		}
		if len(inProgTasks) > 4 {
			inProgOver = styleMuted.Render(fmt.Sprintf("  +%d more", len(inProgTasks)-4))
		}
		if len(doneTasks) > 4 {
			doneOver = styleMuted.Render(fmt.Sprintf("  +%d more", len(doneTasks)-4))
		}
		r0 := lipgloss.NewStyle().Width(colW).Render(todoOver)
		r1 := lipgloss.NewStyle().Width(colW).Render(inProgOver)
		r2 := lipgloss.NewStyle().Width(colW).Render(doneOver)
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, r0, r1, r2))
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
				lines = append(lines, styleMuted.Render(fmt.Sprintf("  +%d more", len(filteredInbox)-3)))
				break
			}
			bullet := lipgloss.NewStyle().Foreground(colAccent).Render("▸ ")
			taskBadge := ""
			if task := m.workspace.tasks[item.TaskID]; task != nil && task.TaskNumber > 0 {
				taskBadge = styleMuted.Render(fmt.Sprintf("  OC-%d", task.TaskNumber))
			}
			lines = append(lines, bullet+styleText.Render(truncate(item.Summary, width-12))+taskBadge)
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
			lines = append(lines, styleMuted.Render("  ✓ "+truncate(entry, width-6)))
		}
	}

	// Navigation hint — show selected task name when cursor could be off-screen
	activeTasks := m.workspace.dashboardActiveTasks()
	if len(activeTasks) > 0 {
		lines = append(lines, "")
		if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil {
			var nameLabel string
			if task.TaskNumber > 0 {
				nameLabel = fmt.Sprintf("OC-%d: %s", task.TaskNumber, truncate(task.Title, 34))
			} else {
				nameLabel = truncate(task.Title, 42)
			}
			lines = append(lines, styleBold.Foreground(colFocus).Render("  "+nameLabel)+"  "+styleMuted.Render("Enter·open  ·  j/k·navigate"))
		} else {
			lines = append(lines, styleMuted.Render("  j/k·select task  ·  Enter·open"))
		}
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
		// Still loading
		lines = append(lines, styleMuted.Render("  Loading…"))
		return lines
	}

	// Open tasks from selectedProject.Tasks (loaded from API)
	query := normalizedFilterQuery(m.mainFilter)
	var openTasks []SidebarTaskItem
	if len(proj.Tasks) > 0 {
		for _, t := range proj.Tasks {
			if t.WorkStatus == "done" || t.WorkStatus == "approved" || t.WorkStatus == "cancelled" {
				continue
			}
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
		// Fall back to sidebar task nodes if API detail not yet available
		children := m.workspace.projectChildren(projectNodeID)
		for _, cid := range children {
			child := m.workspace.nodes[cid]
			if child == nil || child.Kind != sidebarKindTask {
				continue
			}
			if child.WorkStatus == "done" || child.WorkStatus == "approved" {
				continue
			}
			taskLabel := child.Label
			if !matchesFilter(taskLabel, query) && !matchesFilter(child.WorkStatus, query) {
				continue
			}
			openTasks = append(openTasks, SidebarTaskItem{
				ID:         child.ID,
				Title:      child.Label,
				WorkStatus: child.WorkStatus,
			})
		}
	}

	if len(openTasks) == 0 {
		lines = append(lines, styleLabel.Render("OPEN TASKS (0)"))
		if proj.DoneCount > 0 {
			lines = append(lines, lipgloss.NewStyle().Foreground(colConnected).Render(fmt.Sprintf("  ✓  All %d tasks complete", proj.DoneCount)))
		} else {
			lines = append(lines, styleMuted.Render("  No open tasks."))
		}
	} else {
		taskHeader := fmt.Sprintf("OPEN TASKS (%d)", len(openTasks))
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
			truncTitle := truncate(taskTitle, maxTitleW)
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
			if len(lines) < maxLines {
				lines = append(lines, "")
			}
			if len(lines) < maxLines {
				doneHeader := fmt.Sprintf("DONE (%d)", len(proj.DoneTasks))
				lines = append(lines, styleLabel.Render(doneHeader))
			}
			for _, t := range proj.DoneTasks {
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
		// Navigation hint row.
		hintParts := "Enter·open  ·  j/k·navigate  ·  Esc·back"
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
		return []string{styleMuted.Render("  No task selected")}
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
		lines = append(lines, styleMuted.Render("  Agent:  "+task.AgentName))
	}
	if task.Flow > 0 || task.FlowNodeName != "" {
		flowLine := "  Flow:   "
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
	hintParts = append(hintParts, "Esc·back")
	if m.workspace.selectedProjectID != "" {
		hintParts = append(hintParts, "p·project view")
		openTasks := m.workspace.openTasksForProject()
		if len(openTasks) >= 2 {
			hintParts = append(hintParts, "j/k·next/prev task")
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
		return []string{
			"",
			lipgloss.JoinHorizontal(lipgloss.Center,
				lipgloss.NewStyle().Width(width).Align(lipgloss.Center).
					Foreground(colMuted).Render("✓ Inbox clear"),
			),
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
			actions := styleMuted.Render("  a·approve  ·  x·reject  ·  f·defer  ·  o·open  ·  j/k·navigate")
			lines = append(lines, actions)
		}
	}

	return lines
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
	start := 0
	if len(filteredActivity) > maxLines {
		start = len(filteredActivity) - maxLines
	}
	for _, entry := range filteredActivity[start:] {
		dot := lipgloss.NewStyle().Foreground(colConnected).Render("✓ ")
		lines = append(lines, dot+styleText.Render(truncate(entry, width-4)))
	}
	if len(lines) == 1 {
		lines = append(lines, styleMuted.Render("  No activity yet"))
	}
	return lines
}

func (m Model) renderAgentsView(width, maxLines int) []string {
	var lines []string
	lines = append(lines, "")
	if len(m.workspace.agents) == 0 {
		lines = append(lines, lipgloss.NewStyle().Width(width).Align(lipgloss.Center).
			Foreground(colSubtle).Render("no agents loaded"))
		return lines
	}
	for _, agent := range m.workspace.agents {
		parts := strings.SplitN(agent, "=", 2)
		name, status := agent, ""
		if len(parts) == 2 {
			name, status = parts[0], parts[1]
		}
		var dot string
		switch strings.ToLower(status) {
		case "active", "online":
			dot = styleConnected.Render("● ")
		case "paused", "idle", "draft":
			dot = styleReconnecting.Render("◌ ")
		default: // retired, cancelled, unknown
			dot = styleMuted.Render("○ ")
		}
		agentLine := dot + styleBold.Render(name)
		if status != "" {
			agentLine += styleMuted.Render("  " + status)
		}
		lines = append(lines, agentLine)
	}
	return lines
}

func (m Model) renderMergesView(width, maxLines int) []string {
	var lines []string
	lines = append(lines, "")
	for _, pr := range m.workspace.mergeQueue {
		lines = append(lines, lipgloss.NewStyle().Foreground(colAccent).Render("⎇ ")+styleText.Render(pr))
	}
	if len(lines) == 1 {
		lines = append(lines, styleMuted.Render("  No pending merges"))
	}
	return lines
}

func (m Model) renderSchedulesView(width, maxLines int) []string {
	var lines []string
	lines = append(lines, "")
	for _, s := range m.workspace.schedules {
		lines = append(lines, lipgloss.NewStyle().Foreground(colPrimary).Render("⏰ ")+styleText.Render(s))
	}
	if len(lines) == 1 {
		lines = append(lines, styleMuted.Render("  No schedules"))
	}
	return lines
}

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
		key("h/l  ←/→", "collapse/expand section or project"),
		key("Space", "toggle expand project / section"),
		key("Enter", "select: open session or project"),
		key("/ or Esc", "search filter / clear"),
		key("s", "toggle sidebar (below 100 cols)"),
		key("< / >", "resize sidebar width"),
		"",
		header("Main Panel  (press 2 to focus)"),
		key("j/k  ↑/↓", "navigate tasks in project view / next·prev task in task detail"),
		key("g / G", "jump to top/bottom of view"),
		key("Enter", "open task detail or inbox item"),
		key("Esc", "back to project (from task) / dashboard"),
		key("d", "toggle done tasks section (project view only)"),
		key("a / x / f / o", "inbox: approve/reject/defer/open"),
		"",
		header("Chat  (press 3 to focus)"),
		key("Enter", "send message"),
		key("PgUp / PgDn", "scroll messages"),
		key("↑ / ↓", "scroll one line"),
		key("g / G", "scroll to oldest / latest message"),
		key("Esc", "cancel active agent turn"),
		key("Alt-Enter", "insert newline"),
		key("[ / ]", "cycle chat scope"),
		"",
		header("Global"),
		key("Tab / Shift-Tab", "cycle panel focus"),
		key("1 / 2 / 3", "jump to sidebar/main/chat"),
		key("i", "jump to Inbox"),
		key("d", "jump to Dashboard (or toggle done in project view)"),
		key("p", "return to selected project (if any)"),
		key("t", "return to selected task (if any)"),
		key("n", "jump to next unread session"),
		key("r", "refresh sidebar data"),
		key("?", "toggle this help screen"),
		key(":command", "open command palette"),
		"",
		header("Commands  (press : to open)"),
		key(":frank", "switch to Frank session"),
		key(":dashboard / :inbox", "navigate to view"),
		key(":project / :task", "navigate to view"),
		key(":focus <panel>", "focus sidebar|main|chat"),
		key(":send <message>", "send message to Frank"),
		key(":cancel-turn", "cancel agent turn"),
		key(":quit", "quit OtterCamp"),
		"",
		styleMuted.Render("  Press ? or Esc to close"),
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return lines
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
	bottomLines := make([]string, 0, len(inputLines)+2)
	bottomLines = append(bottomLines, "")
	bottomLines = append(bottomLines, inputLines...)
	if m.commandMode {
		if suggestions := m.commandPaletteSuggestions(4); len(suggestions) > 0 {
			bottomLines = append(bottomLines, styleMuted.Render("  suggestions"))
			for _, suggestion := range suggestions {
				bottomLines = append(bottomLines, styleSubtle.Render("  "+truncate(suggestion, cw-2)))
			}
		}
	}
	// EX-017: show all queued messages (up to 3), with overflow indicator
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

	msgAreaH := targetH - len(headerLines) - len(bottomLines)
	if msgAreaH < 1 {
		msgAreaH = 1
	}
	allMsgLines := m.renderChatMessages(cw)
	msgLines, scrollOffset, maxOffset := chatViewportLines(allMsgLines, msgAreaH, m.chatScrollOffset)

	// Show scroll indicator when user has scrolled up (newer messages are below)
	if scrollOffset > 0 {
		scrollHint := fmt.Sprintf("↓ %d more · PgDn to scroll", scrollOffset)
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
		upHint := fmt.Sprintf("↑ %d older messages · PgUp to scroll", hiddenAbove)
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
		return []string{
			"",
			center("no messages yet"),
			center("Tab·focus chat  Enter·send"),
		}
	}

	var lines []string
	for _, msg := range m.chatMessages {
		// Role label
		var roleStr lipgloss.Style
		var roleLabel string
		switch strings.ToLower(msg.Role) {
		case "user":
			roleStr = styleUser
			roleLabel = "You"
		case "assistant":
			roleStr = styleAssistant
			roleLabel = "Frank"
		case "interjection":
			roleStr = styleInterject
			roleLabel = "Interjection (interjected)"
		case "system":
			roleStr = styleMuted
			roleLabel = "System"
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
			tcLine := "  " + indicator + " ⚙ " + tc.Name + " (" + statusStyle.Render(statusLabel) + ")"
			lines = append(lines, styleMuted.Render(tcLine))
			if expanded {
				result := strings.TrimSpace(tc.Result)
				if result == "" {
					lines = append(lines, styleSubtle.Render("    (no result yet)"))
					continue
				}
				const maxToolResultRunes = 280
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
					lines = append(lines, styleMuted.Render("    [show more]"))
				}
			}
		}

		if !msg.Finalized && msg.Role == "assistant" {
			lines = append(lines, styleReconnecting.Render("  ▌"))
		}

		lines = append(lines, "")
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

	candidates := make([]commandSuggestion, 0, 32)
	for _, command := range []string{
		"cmd: frank", "cmd: dashboard", "cmd: project", "cmd: task", "cmd: inbox",
		"cmd: focus sidebar", "cmd: focus main", "cmd: focus chat",
		"cmd: send", "cmd: cancel-turn", "cmd: quit",
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
		status = styleMuted.Render("  ·  ") + styleSubtle.Render(truncate(m.statusMessage, 40))
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
	msg := "upstream dependency unavailable or stale. Recovery: verify connectivity and allow replay resync."
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
