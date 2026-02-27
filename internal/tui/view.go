package tui

import (
	"fmt"
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

// formatTaskStatus converts raw status strings to human-readable Title Case labels.
func formatTaskStatus(s string) string {
	switch strings.ToLower(s) {
	case "todo":
		return "Todo"
	case "in_progress":
		return "In Progress"
	case "done":
		return "Done"
	case "approved":
		return "Approved"
	case "blocked":
		return "Blocked"
	case "rejected":
		return "Rejected"
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

	// Title — with optional unread total badge (EX-012)
	titleColor := colMuted
	if focused {
		titleColor = colFocus
	}
	title := lipgloss.NewStyle().Foreground(titleColor).Bold(true).Render("SESSIONS")
	unreadTotal := 0
	for _, node := range m.workspace.nodes {
		if node.Kind == sidebarKindSession {
			unreadTotal += node.Unread
		}
	}
	if unreadTotal > 0 {
		title += " " + styleUnread.Render(fmt.Sprintf("(%d)", unreadTotal))
	}
	lines = append(lines, title)
	lines = append(lines, styleDivider.Render(strings.Repeat("─", cw)))

	// Sidebar nodes
	visible := m.workspace.visibleSidebarIDs()
	inboxFooter := ""
	if n := len(m.workspace.inbox); n > 0 {
		inboxFooter = styleMuted.Render(fmt.Sprintf("▼ %d inbox", n))
	}

	// Rows below title+divider are split between nodes, optional "+N more", and optional inbox footer.
	rowsForBody := innerH - 2
	if rowsForBody < 0 {
		rowsForBody = 0
	}
	rowsForNodesAndMore := rowsForBody
	if inboxFooter != "" {
		rowsForNodesAndMore--
	}
	if rowsForNodesAndMore < 0 {
		rowsForNodesAndMore = 0
	}

	maxNodeLines := rowsForNodesAndMore
	if len(visible) > maxNodeLines && maxNodeLines > 0 {
		maxNodeLines--
	}
	if maxNodeLines < 0 {
		maxNodeLines = 0
	}

	for i, id := range visible {
		if i >= maxNodeLines {
			break
		}
		node := m.workspace.nodes[id]
		if node == nil {
			continue
		}
		lines = append(lines, m.renderSidebarNode(node, i == m.workspace.sidebarCursor, cw))
	}
	if remaining := len(visible) - maxNodeLines; remaining > 0 && rowsForNodesAndMore > 0 {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("  +%d more", remaining)))
	}
	if inboxFooter != "" {
		lines = append(lines, inboxFooter)
	}

	// Fill remaining space
	content := buildPanelContent(lines, innerH, cw)

	return panelStyle(innerW, innerH, focused).Render(content)
}

func (m Model) renderSidebarNode(node *sidebarNode, cursor bool, width int) string {
	isActive := node.Kind == sidebarKindSession &&
		node.SessionID == m.workspace.activeSessionID

	var prefix, label string
	switch node.Kind {
	case sidebarKindProject:
		if node.Expanded {
			prefix = "▾ "
		} else {
			prefix = "▸ "
		}
		label = node.Label
	case sidebarKindSession:
		if node.ParentID != "" {
			prefix = "  › "
		} else {
			prefix = "  "
		}
		label = node.Label
	}

	// EX-018: task status icon for task-linked sessions
	var statusBadge string
	if node.Kind == sidebarKindSession && node.TaskID != "" {
		if task := m.workspace.tasks[node.TaskID]; task != nil {
			switch task.Status {
			case "todo":
				statusBadge = " " + styleMuted.Render("○")
			case "in_progress":
				statusBadge = " " + styleReconnecting.Render("◌")
			case "done", "approved":
				statusBadge = " " + styleConnected.Render("●")
			case "blocked", "rejected":
				statusBadge = " " + styleDisconnected.Render("⚠")
			}
		}
	}

	var unreadSuffix string
	if node.Unread > 0 {
		unreadSuffix = " " + styleUnread.Render(fmt.Sprintf("(%d)", node.Unread))
	}

	line := prefix + label

	var rendered string
	switch {
	case cursor && isActive:
		// EX-010: show ✓ check to distinguish active+cursor from cursor-only
		check := " " + styleConnected.Render("✓")
		rendered = styleSelected.Render(truncate(line, width-4)) + check
	case cursor:
		rendered = styleSelected.Render(truncate(line, width-2))
	case isActive:
		rendered = styleActive.Render(truncate(line, width-2))
	default:
		rendered = styleText.Render(truncate(line, width-2))
	}

	return rendered + statusBadge + unreadSuffix
}

// ── Main panel ──────────────────────────────────────────────────────────────

func (m Model) renderMainPanel(innerW, innerH int, focused bool, layout layoutState) string {
	cw := innerW - 2
	if cw < 4 {
		cw = 4
	}

	var lines []string

	// Title — EX-014: human-readable title; EX-015/EX-019: count badges
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

	// Board columns
	lines = append(lines, "")
	lines = append(lines, styleLabel.Render("Task Board"))

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
	todoTasks, inProgTasks, doneTasks := []string{}, []string{}, []string{}
	for _, id := range m.workspace.taskOrder {
		task := m.workspace.tasks[id]
		if task == nil {
			continue
		}
		entry := truncate("  "+task.Title, colW)
		switch task.Status {
		case "todo":
			todoTasks = append(todoTasks, styleText.Render(entry))
		case "in_progress":
			inProgTasks = append(inProgTasks, lipgloss.NewStyle().Foreground(colWarning).Render(entry))
		case "done", "approved":
			doneTasks = append(doneTasks, styleMuted.Render(entry))
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
	// EX-008: show overflow indicator when more than 4 task rows are hidden
	if taskRowCount > 4 {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("  +%d more tasks", taskRowCount-4)))
	}

	// Inbox section
	if len(m.workspace.inbox) > 0 {
		lines = append(lines, "")
		lines = append(lines, divider(width, fmt.Sprintf("Inbox  %d", len(m.workspace.inbox))))
		for i, item := range m.workspace.inbox {
			if i >= 3 {
				lines = append(lines, styleMuted.Render(fmt.Sprintf("  +%d more", len(m.workspace.inbox)-3)))
				break
			}
			bullet := lipgloss.NewStyle().Foreground(colAccent).Render("▸ ")
			lines = append(lines, bullet+styleText.Render(truncate(item.Summary, width-4)))
		}
	}

	// Activity section
	if len(m.workspace.activity) > 0 {
		lines = append(lines, "")
		lines = append(lines, divider(width, "Activity"))
		start := 0
		if len(m.workspace.activity) > 3 {
			start = len(m.workspace.activity) - 3
		}
		for _, entry := range m.workspace.activity[start:] {
			lines = append(lines, styleMuted.Render("  ✓ "+truncate(entry, width-6)))
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
	for _, id := range m.workspace.topLevel {
		node := m.workspace.nodes[id]
		if node == nil || node.Kind != sidebarKindProject {
			continue
		}
		icon := "▸ "
		if node.Expanded {
			icon = "▾ "
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(colFocus).Bold(true).Render(icon+node.Label))
		children := m.workspace.projectChildren(id)
		for _, cid := range children {
			child := m.workspace.nodes[cid]
			if child == nil {
				continue
			}
			lines = append(lines, styleText.Render("    › "+truncate(child.Label, width-8)))
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

	statusColor := colMuted
	switch task.Status {
	case "todo":
		statusColor = colConnected
	case "in_progress":
		statusColor = colWarning
	case "done", "approved":
		statusColor = colMuted
	case "blocked", "rejected":
		statusColor = colError
	}

	titleLine := styleBold.Render(truncate(task.Title, width-4))
	statusLine := lipgloss.NewStyle().Foreground(statusColor).Render("  Status: " + formatTaskStatus(task.Status))
	flowLine := styleMuted.Render(fmt.Sprintf("  Flow step: %d", task.Flow))

	lines = append(lines, titleLine)
	lines = append(lines, statusLine)
	lines = append(lines, flowLine)

	if len(task.History) > 0 {
		lines = append(lines, "")
		lines = append(lines, divider(width, "History"))
		start := 0
		if len(task.History) > 5 {
			start = len(task.History) - 5
		}
		for _, h := range task.History[start:] {
			lines = append(lines, styleMuted.Render("  · "+h))
		}
	}

	lines = append(lines, "")
	lines = append(lines, styleMuted.Render("  Enter·open  Esc·back"))

	return lines
}

func (m Model) renderInboxView(width, maxLines int) []string {
	if len(m.workspace.inbox) == 0 {
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
	for i, item := range m.workspace.inbox {
		isCursor := i == m.workspace.inboxCursor

		var prefix string
		var rowStyle lipgloss.Style
		if isCursor {
			prefix = "▸ "
			rowStyle = lipgloss.NewStyle().Foreground(colFocus).Bold(true)
		} else {
			prefix = "  "
			rowStyle = styleText
		}

		summary := truncate(item.Summary, width-6)
		taskBadge := lipgloss.NewStyle().Foreground(colMuted).Render("  " + item.TaskID)
		lines = append(lines, rowStyle.Render(prefix+summary)+taskBadge)

		if isCursor {
			actions := styleMuted.Render("  a·approve  x·reject  f·defer  o·open  j/k·navigate")
			lines = append(lines, actions)
		}
	}

	return lines
}

func (m Model) renderActivityView(width, maxLines int) []string {
	var lines []string
	lines = append(lines, "")
	start := 0
	if len(m.workspace.activity) > maxLines {
		start = len(m.workspace.activity) - maxLines
	}
	for _, entry := range m.workspace.activity[start:] {
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
	for _, agent := range m.workspace.agents {
		parts := strings.SplitN(agent, "=", 2)
		name, status := agent, ""
		if len(parts) == 2 {
			name, status = parts[0], parts[1]
		}
		var dot string
		switch strings.ToLower(status) {
		case "online":
			dot = styleConnected.Render("● ")
		case "idle":
			dot = styleMuted.Render("○ ")
		default:
			dot = styleDisconnected.Render("○ ")
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
		header("Navigation"),
		key("j / k", "move up/down in lists"),
		key("h / l", "collapse/expand sidebar"),
		key("g / G", "jump to top/bottom"),
		key("Tab/Shift-Tab", "cycle panel focus"),
		key("1 / 2 / 3", "jump to sidebar/main/chat"),
		key("[ / ]", "cycle chat scope"),
		"",
		header("Chat"),
		key("Enter", "send message"),
		key("PgUp / PgDn", "scroll messages"),
		key("Esc", "cancel active agent turn"),
		key("Shift-Enter", "insert newline"),
		"",
		header("Main Panel"),
		key("Enter", "open task / select item"),
		key("Esc", "return to dashboard"),
		key("a/x/f/o", "approve/reject/defer/open"),
		"",
		header("Commands  (press : to open command bar)"),
		key(":frank", "switch to Frank session"),
		key(":dashboard", "show dashboard view"),
		key(":project", "show project tree"),
		key(":task", "show task detail"),
		key(":inbox", "show inbox"),
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
		sessionLabel = "General / Frank"
	}
	headerText := renderChatHeader(sessionLabel, m.activeScope, cw)
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
	msgLines, scrollOffset, scrollMax := chatViewportLines(m.renderChatMessages(cw), msgAreaH, m.chatScrollOffset)

	// Show scroll indicator when not at the bottom
	if scrollMax > 0 && scrollOffset < scrollMax {
		scrollHint := fmt.Sprintf("↓ %d more · PgDn to scroll", scrollMax-scrollOffset)
		bottomLines = append([]string{lipgloss.NewStyle().
			Foreground(colMuted).Italic(true).
			Width(cw).Align(lipgloss.Center).
			Render(scrollHint)}, bottomLines...)
		// Recalculate msgAreaH since bottomLines grew by 1
		msgAreaH = targetH - len(headerLines) - len(bottomLines)
		if msgAreaH < 1 {
			msgAreaH = 1
		}
		msgLines, _, _ = chatViewportLines(m.renderChatMessages(cw), msgAreaH, m.chatScrollOffset)
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

func renderChatHeader(sessionLabel string, active ChatScope, width int) string {
	separator := styleMuted.Render("  ·  ")
	indicator := renderScopeIndicator(active, false)
	sessionWidth := width - lipgloss.Width(separator) - lipgloss.Width(indicator)
	if sessionWidth < 8 {
		indicator = renderScopeIndicator(active, true)
		sessionWidth = width - lipgloss.Width(separator) - lipgloss.Width(indicator)
	}
	if sessionWidth < 1 {
		sessionWidth = 1
	}
	return styleActive.Render(truncate(sessionLabel, sessionWidth)) + separator + indicator
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
			lipgloss.NewStyle().Width(width).Align(lipgloss.Center).
				Foreground(colSubtle).Render("no messages yet"),
			lipgloss.NewStyle().Width(width).Align(lipgloss.Center).
				Foreground(colSubtle).Render("Tab·focus chat  Enter·send"),
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

		// Message content — word-wrapped
		content := strings.TrimSpace(msg.Content)
		if content != "" {
			wrapped := wrapText(content, width)
			for _, wl := range wrapped {
				lines = append(lines, styleText.Render(wl))
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
	return ts.Local().Format("15:04")
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

	// EX-006: show human-readable session label instead of raw session ID
	rawSession := strings.TrimSpace(m.State().LastActiveChatSession)
	session := m.workspace.sessionLabel(rawSession)
	if session == "" {
		session = "Frank"
	}
	sizeStr := string(layout.sizeClass)
	focusStr := panelLabel(focus)

	status := ""
	if m.statusMessage != "" {
		status = styleMuted.Render("  ·  ") + styleSubtle.Render(truncate(m.statusMessage, 40))
	}

	bar := dot + "  " + connStyle.Render(connText) +
		styleMuted.Render("  ·  ") + styleMuted.Render(session) +
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
func buildPanelContent(lines []string, targetH, width int) string {
	// Trim to fit
	if len(lines) > targetH {
		lines = lines[:targetH]
	}
	// Pad with empty lines
	for len(lines) < targetH {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
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
