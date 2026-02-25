package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type Panel int

const (
	SidebarPanel Panel = iota
	MainPanel
	ChatPanel
)

type WorkspaceEnvelopeMsg struct {
	Envelope EventEnvelope
}

type Model struct {
	state          UIState
	focus          Panel
	commandMode    bool
	commandBuffer  string
	statusMessage  string
	connection     ConnectionState
	streamDegraded bool
	width          int
	height         int
	quitting       bool
	sidebarVisible bool
	sizeClass      SizeClass
	workspace      workspaceState
}

func NewModel(state UIState) Model {
	normalized := normalizeState(state)
	panel, ok := panelFromView(normalized.LastActiveView)
	if !ok {
		panel = MainPanel
	}
	model := Model{
		state:          normalized,
		focus:          panel,
		statusMessage:  "Tab/Shift-Tab cycle focus. 1/2/3 direct focus. :focus and :quit commands available.",
		connection:     ConnectionDisconnected,
		sidebarVisible: normalized.SidebarVisible,
		workspace:      newWorkspaceState(),
	}
	model.applyResponsiveLayout()
	return model
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		previousClass := m.sizeClass
		m.applyResponsiveLayout()
		if previousClass != "" && previousClass != m.sizeClass {
			m.statusMessage = fmt.Sprintf("Layout changed: %s", m.sizeClass)
		}
		return m, nil
	case ConnectionStateMsg:
		m.connection = typed.State
		m.streamDegraded = typed.Degraded
		return m, nil
	case WorkspaceEnvelopeMsg:
		m.workspace.applyRealtimeEnvelope(typed.Envelope)
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(typed)
	default:
		return m, nil
	}
}

func (m Model) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.commandMode {
		return m.updateCommandInput(key)
	}

	m.applyResponsiveLayout()
	order := m.focusOrder()

	switch key.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		m.statusMessage = "Exiting TUI."
		return m, tea.Quit
	case tea.KeyTab:
		m.focus = nextPanelInOrder(order, m.focus)
		m.applyResponsiveLayout()
		m.statusMessage = "Focus: " + panelLabel(m.focus)
		return m, nil
	case tea.KeyShiftTab:
		m.focus = previousPanelInOrder(order, m.focus)
		m.applyResponsiveLayout()
		m.statusMessage = "Focus: " + panelLabel(m.focus)
		return m, nil
	case tea.KeyEnter:
		m.handleEnterKey()
		return m, nil
	case tea.KeyEsc:
		m.handleEscapeKey()
		return m, nil
	case tea.KeyRunes:
		if len(key.Runes) == 1 {
			r := key.Runes[0]
			if r == ':' {
				m.commandMode = true
				m.commandBuffer = ":"
				m.statusMessage = "Command mode"
				return m, nil
			}

			if panel, ok := panelFromShortcut(key.Alt, r); ok {
				m.setFocus(panel)
				m.statusMessage = "Focus: " + panelLabel(m.focus)
				return m, nil
			}

			if handled := m.handleWorkspaceRune(r); handled {
				return m, nil
			}
		}
		return m, nil
	default:
		return m, nil
	}
}

func (m *Model) handleEnterKey() {
	switch m.focus {
	case SidebarPanel:
		m.workspace.selectSidebarNode()
		m.state.LastActiveChatSession = m.workspace.activeSessionID
		m.statusMessage = "Sidebar selection applied."
	case MainPanel:
		if m.workspace.mainView == ViewInbox {
			if m.workspace.applyInboxAction("open") {
				m.state.LastActiveChatSession = m.workspace.activeSessionID
				m.statusMessage = "Opened inbox item in context."
				return
			}
		}
		if m.workspace.mainView == ViewProject || m.workspace.mainView == ViewDashboard {
			m.workspace.setMainView(ViewTask)
			m.statusMessage = "Opened task detail."
			return
		}
	}
}

func (m *Model) handleEscapeKey() {
	if m.focus == MainPanel {
		m.workspace.setMainView(ViewDashboard)
		m.statusMessage = "Returned to dashboard."
	}
}

func (m *Model) handleWorkspaceRune(r rune) bool {
	switch r {
	case 'j':
		if m.focus == SidebarPanel {
			m.workspace.moveSidebar(1)
		} else if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			m.workspace.moveInbox(1)
		}
		return true
	case 'k':
		if m.focus == SidebarPanel {
			m.workspace.moveSidebar(-1)
		} else if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			m.workspace.moveInbox(-1)
		}
		return true
	case 'h':
		if m.focus == SidebarPanel {
			m.workspace.collapseSidebarNode()
			return true
		}
	case 'l':
		if m.focus == SidebarPanel {
			m.workspace.expandSidebarNode()
			return true
		}
	case 'g':
		if m.focus == SidebarPanel {
			m.workspace.sidebarHome()
		} else if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			m.workspace.inboxHome()
		}
		return true
	case 'G':
		if m.focus == SidebarPanel {
			m.workspace.sidebarEnd()
		} else if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			m.workspace.inboxEnd()
		}
		return true
	case 'r':
		m.statusMessage = "Workspace refreshed."
		return true
	case 'a':
		if m.focus == MainPanel && m.workspace.mainView == ViewInbox && m.workspace.applyInboxAction("approve") {
			m.statusMessage = "Inbox item approved."
			return true
		}
	case 'x':
		if m.focus == MainPanel && m.workspace.mainView == ViewInbox && m.workspace.applyInboxAction("reject") {
			m.statusMessage = "Inbox item rejected."
			return true
		}
	case 'f':
		if m.focus == MainPanel && m.workspace.mainView == ViewInbox && m.workspace.applyInboxAction("defer") {
			m.statusMessage = "Inbox item deferred."
			return true
		}
	case 'o':
		if m.focus == MainPanel && m.workspace.mainView == ViewInbox && m.workspace.applyInboxAction("open") {
			m.state.LastActiveChatSession = m.workspace.activeSessionID
			m.statusMessage = "Opened inbox item in context."
			return true
		}
	}
	return false
}

func (m Model) updateCommandInput(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyEsc:
		m.commandMode = false
		m.commandBuffer = ""
		m.statusMessage = "Command cancelled."
		return m, nil
	case tea.KeyEnter:
		m.commandMode = false
		command := strings.TrimSpace(m.commandBuffer)
		m.commandBuffer = ""
		m.executeCommand(command)
		if m.quitting {
			return m, tea.Quit
		}
		return m, nil
	case tea.KeyBackspace:
		runes := []rune(m.commandBuffer)
		if len(runes) > 1 {
			m.commandBuffer = string(runes[:len(runes)-1])
		} else {
			m.commandBuffer = ":"
		}
		return m, nil
	case tea.KeyRunes:
		if len(key.Runes) > 0 {
			m.commandBuffer += string(key.Runes)
		}
		return m, nil
	default:
		return m, nil
	}
}

func (m *Model) executeCommand(raw string) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(raw, ":"))
	if trimmed == "" {
		m.statusMessage = "No command entered."
		return
	}

	fields := strings.Fields(trimmed)
	switch strings.ToLower(fields[0]) {
	case "quit":
		m.quitting = true
		m.statusMessage = "Exiting TUI."
	case "focus":
		if len(fields) != 2 {
			m.statusMessage = "Usage: :focus sidebar|main|chat"
			return
		}
		panel, ok := panelFromView(fields[1])
		if !ok {
			m.statusMessage = "Unknown panel. Use sidebar, main, or chat."
			return
		}
		m.setFocus(panel)
		m.statusMessage = "Focus: " + panelLabel(m.focus)
	case "dashboard", "project", "task", "inbox", "activity", "agents", "merges", "schedules":
		view, ok := resolveMainViewCommand(fields[0])
		if !ok {
			m.statusMessage = "Unknown workspace command: " + fields[0]
			return
		}
		m.workspace.setMainView(view)
		m.statusMessage = "Main view: " + string(view)
	default:
		m.statusMessage = "Unknown command: " + fields[0]
	}
}

func (m Model) View() string {
	return m.viewForShell("board")
}

func (m Model) viewForShell(shell string) string {
	layout := computeLayout(m.width, m.height, m.focus, m.sidebarVisible, m.state.PanelProportions)
	focus := normalizeFocus(layout, m.focus)
	if focus != m.focus {
		layout = computeLayout(m.width, m.height, focus, m.sidebarVisible, m.state.PanelProportions)
	}

	header := fmt.Sprintf(
		"%s | %s | %s",
		renderPanelHeader("Sidebar", SidebarPanel == focus, layout.widths[0], layout.visible[0]),
		renderPanelHeader("Main", MainPanel == focus, layout.widths[1], layout.visible[1]),
		renderPanelHeader("Chat", ChatPanel == focus, layout.widths[2], layout.visible[2]),
	)
	shellLine := describeLayoutForShell(layout.sizeClass, shell, layout)
	status := fmt.Sprintf(
		"Status: %s | Realtime=%s%s | Size=%s | Focus=%s | ChatSession=%s",
		m.statusMessage,
		m.connection,
		realtimeDegradedSuffix(m.streamDegraded),
		layout.sizeClass,
		panelLabel(focus),
		valueOrPlaceholder(m.state.LastActiveChatSession),
	)
	if layout.hiddenHints != "" {
		status += " | " + layout.hiddenHints
	}

	prefix := ""
	if layout.gutters > 0 {
		prefix = strings.Repeat(" ", layout.gutters)
	}
	lines := []string{
		prefix + header,
		prefix + shellLine,
		prefix + status,
	}
	if m.commandMode {
		lines = append(lines, prefix+"Command> "+m.commandBuffer)
	}
	return strings.Join(lines, "\n")
}

func (m Model) FocusedPanel() Panel {
	return m.focus
}

func (m Model) Quitting() bool {
	return m.quitting
}

func (m Model) ConnectionState() ConnectionState {
	return m.connection
}

func (m Model) StreamDegraded() bool {
	return m.streamDegraded
}

func (m Model) MainView() MainView {
	return m.workspace.mainView
}

func (m Model) WorkspaceSession() string {
	return m.workspace.activeSessionID
}

func (m Model) WorkspaceRender(class SizeClass) string {
	return m.workspace.render(m.workspace.mainView, class)
}

func (m Model) BoardCounts() boardCounts {
	return m.workspace.boardCounts()
}

func (m Model) ActivityEntries() []string {
	out := make([]string, len(m.workspace.activity))
	copy(out, m.workspace.activity)
	return out
}

func (m Model) SidebarVisibleEntries() []string {
	visibleIDs := m.workspace.visibleSidebarIDs()
	entries := make([]string, 0, len(visibleIDs))
	for _, id := range visibleIDs {
		node := m.workspace.nodes[id]
		if node == nil {
			continue
		}
		label := node.Label
		if node.Unread > 0 {
			label = fmt.Sprintf("%s (%d)", label, node.Unread)
		}
		entries = append(entries, label)
	}
	return entries
}

func (m Model) TaskStatus(taskID string) string {
	task := m.workspace.tasks[taskID]
	if task == nil {
		return ""
	}
	return task.Status
}

func (m Model) TaskFlow(taskID string) int {
	task := m.workspace.tasks[taskID]
	if task == nil {
		return 0
	}
	return task.Flow
}

func (m Model) SizeClass() SizeClass {
	if m.sizeClass == "" {
		return resolveSizeClass(m.width, m.height)
	}
	return m.sizeClass
}

func (m Model) CurrentLayout() layoutState {
	layout := computeLayout(m.width, m.height, m.focus, m.sidebarVisible, m.state.PanelProportions)
	normalized := normalizeFocus(layout, m.focus)
	if normalized != m.focus {
		return computeLayout(m.width, m.height, normalized, m.sidebarVisible, m.state.PanelProportions)
	}
	return layout
}

func (m Model) State() UIState {
	layout := m.CurrentLayout()
	focus := normalizeFocus(layout, m.focus)
	next := m.state
	next.LastActiveView = viewFromPanel(focus)
	next.SidebarVisible = m.sidebarVisible
	if strings.TrimSpace(m.workspace.activeSessionID) != "" {
		next.LastActiveChatSession = m.workspace.activeSessionID
	}
	return normalizeState(next)
}

func (m Model) focusOrder() []Panel {
	layout := m.CurrentLayout()
	return layout.focusOrder
}

func (m *Model) setFocus(panel Panel) {
	m.focus = panel
	if panel == SidebarPanel {
		m.sidebarVisible = true
	}
	if m.sizeClass == SizeS && panel != SidebarPanel {
		m.sidebarVisible = false
	}
	m.applyResponsiveLayout()
}

func (m *Model) applyResponsiveLayout() {
	layout := computeLayout(m.width, m.height, m.focus, m.sidebarVisible, m.state.PanelProportions)
	m.sizeClass = layout.sizeClass
	m.focus = normalizeFocus(layout, m.focus)
	if m.sizeClass == SizeS && m.focus != SidebarPanel {
		m.sidebarVisible = false
	}
}

func panelFromShortcut(alt bool, r rune) (Panel, bool) {
	if alt {
		switch r {
		case '1':
			return SidebarPanel, true
		case '2':
			return MainPanel, true
		case '3':
			return ChatPanel, true
		default:
			return MainPanel, false
		}
	}

	switch r {
	case '1':
		return SidebarPanel, true
	case '2':
		return MainPanel, true
	case '3':
		return ChatPanel, true
	default:
		return MainPanel, false
	}
}

func panelFromView(view string) (Panel, bool) {
	switch strings.ToLower(strings.TrimSpace(view)) {
	case "sidebar":
		return SidebarPanel, true
	case "main":
		return MainPanel, true
	case "chat":
		return ChatPanel, true
	default:
		return MainPanel, false
	}
}

func viewFromPanel(panel Panel) string {
	switch panel {
	case SidebarPanel:
		return "sidebar"
	case ChatPanel:
		return "chat"
	default:
		return "main"
	}
}

func panelLabel(panel Panel) string {
	switch panel {
	case SidebarPanel:
		return "sidebar"
	case ChatPanel:
		return "chat"
	default:
		return "main"
	}
}

func valueOrPlaceholder(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "none"
	}
	return trimmed
}

func realtimeDegradedSuffix(degraded bool) string {
	if degraded {
		return " (degraded)"
	}
	return ""
}
