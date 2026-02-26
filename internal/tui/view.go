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

	sections = append(sections, prefix+panelRow)
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

	// Title
	titleText := "SESSIONS"
	titleColor := colMuted
	if focused {
		titleColor = colFocus
	}
	title := lipgloss.NewStyle().Foreground(titleColor).Bold(true).Render(titleText)
	lines = append(lines, title)
	lines = append(lines, styleDivider.Render(strings.Repeat("─", cw)))

	// Sidebar nodes
	visible := m.workspace.visibleSidebarIDs()
	maxNodes := innerH - 4 // title + divider + 2 margin
	if maxNodes < 1 {
		maxNodes = 1
	}

	for i, id := range visible {
		if i >= maxNodes {
			more := len(visible) - maxNodes
			lines = append(lines, styleMuted.Render(fmt.Sprintf("  +%d more", more)))
			break
		}
		node := m.workspace.nodes[id]
		if node == nil {
			continue
		}
		lines = append(lines, m.renderSidebarNode(node, i == m.workspace.sidebarCursor, cw))
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

	var unreadSuffix string
	if node.Unread > 0 {
		unreadSuffix = " " + styleUnread.Render(fmt.Sprintf("(%d)", node.Unread))
	}

	line := prefix + label

	var rendered string
	switch {
	case cursor && isActive:
		rendered = styleSelected.Render(truncate(line, width-2)) + unreadSuffix
	case cursor:
		rendered = styleSelected.Render(truncate(line, width-2)) + unreadSuffix
	case isActive:
		rendered = styleActive.Render(truncate(line, width-2)) + unreadSuffix
	default:
		rendered = styleText.Render(truncate(line, width-2)) + unreadSuffix
	}

	return rendered
}

// ── Main panel ──────────────────────────────────────────────────────────────

func (m Model) renderMainPanel(innerW, innerH int, focused bool, layout layoutState) string {
	cw := innerW - 2
	if cw < 4 {
		cw = 4
	}

	var lines []string

	// Title
	titleText := strings.ToUpper(string(m.workspace.mainView))
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

	todoHdr := buildColumn("TODO", counts.Todo, colW, colConnected)
	inProgHdr := buildColumn("IN PROGRESS", counts.InProgress, colW, colWarning)
	doneHdr := buildColumn("DONE", counts.Done, colW, colMuted)
	blockedHdr := buildColumn("BLOCKED", counts.Blocked, colW, colError)

	visibleCols := []string{todoHdr, inProgHdr, doneHdr}
	if width > 80 && counts.Blocked > 0 {
		visibleCols = append(visibleCols, blockedHdr)
	}
	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, visibleCols...))

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

func buildColumn(name string, count, width int, color lipgloss.Color) string {
	label := lipgloss.NewStyle().Foreground(color).Bold(true).
		Render(name)
	countStr := lipgloss.NewStyle().Foreground(color).
		Render(fmt.Sprintf(" (%d)", count))
	header := label + countStr
	line := styleDivider.Render(strings.Repeat("─", width-1))
	return lipgloss.NewStyle().Width(width).Render(header+"\n"+line) + " "
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
	statusLine := lipgloss.NewStyle().Foreground(statusColor).Render("  Status: " + task.Status)
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

	// Session header
	sessionLabel := strings.TrimSpace(m.activeSession)
	if sessionLabel == "" {
		sessionLabel = "General / Frank"
	}
	scopeLabel := string(m.activeScope)
	headerText := styleActive.Render(sessionLabel) +
		styleMuted.Render("  ·  ") +
		styleSubtle.Render(scopeLabel)
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
	if len(m.queuedMessages) > 0 {
		q := m.queuedMessages[0]
		qText := truncate(q.Text, cw-8)
		flags := ""
		if q.Steer {
			flags += " [steer]"
		}
		bottomLines = append(bottomLines, styleMuted.Render("  queued: ")+
			styleSubtle.Render(qText)+
			styleMuted.Render(flags))
	}

	msgAreaH := targetH - len(headerLines) - len(bottomLines)
	if msgAreaH < 1 {
		msgAreaH = 1
	}
	msgLines, _, _ := chatViewportLines(m.renderChatMessages(cw), msgAreaH, m.chatScrollOffset)

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
		for _, tc := range msg.ToolCalls {
			var statusStyle lipgloss.Style
			switch tc.Status {
			case "success":
				statusStyle = styleTool
			case "pending":
				statusStyle = styleReconnecting
			default:
				statusStyle = styleDisconnected
			}
			tcLine := "  ⚙ " + tc.Name + "  " + statusStyle.Render(tc.Status)
			lines = append(lines, styleMuted.Render(tcLine))
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

	session := valueOrPlaceholder(m.State().LastActiveChatSession)
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
	return icon + label + styleDegraded.Render(msg)
}

// ── Utilities ────────────────────────────────────────────────────────────────

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
