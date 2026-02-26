package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"time"

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

type ChatEnvelopeMsg struct {
	Envelope EventEnvelope
}

type ReplaySyncedMsg struct{}

type coldOpenCompleteMsg struct{}
type tourOverlayExpiredMsg struct{}
type memorySampleMsg struct{}
type chatSendRequestedMsg struct {
	SessionID string
	Content   string
}
type chatSendCompletedMsg struct {
	Err error
}
type chatCancelRequestedMsg struct {
	SessionID string
}
type chatCancelCompletedMsg struct {
	Err error
}

// SessionResolvedMsg carries the resolved UUID of the session when a message is sent.
// Used to filter SSE events by session, preventing cross-session leakage.
type SessionResolvedMsg struct {
	SessionID string
}

const (
	memorySampleInterval   = 5 * time.Second
	keypressLatencyBudget  = 100 * time.Millisecond
	sseRenderLatencyBudget = 250 * time.Millisecond
	chatScrollStepLines    = 8
)

type TUIPerformanceMetrics struct {
	InitialInteractivePaint time.Duration
	KeypressToVisible       time.Duration
	SSEDeltaRenderLatency   time.Duration
	PeakMemoryBytes         uint64
	MemoryBoundBytes        uint64
}

type Model struct {
	state          UIState
	focus          Panel
	commandMode    bool
	commandBuffer  string
	statusMessage  string
	runtimeHints   RuntimeHints
	connection     ConnectionState
	streamDegraded bool
	width          int
	height         int
	quitting       bool
	sidebarVisible bool
	sizeClass      SizeClass
	workspace      workspaceState
	now            func() time.Time
	startedAt      time.Time
	firstRun       bool
	coldOpenActive bool
	tourActive     bool
	proofRealtime  bool
	proofReplay    bool
	perfMetrics    TUIPerformanceMetrics

	chatInput        string
	chatHistory      []string
	chatHistoryIndex int
	chatMessages     []ChatMessage
	chatMessageIndex map[string]int
	chatScrollOffset int
	queuedMessages   []QueuedMessage
	editingQueued    bool
	activeTurn          bool
	activeTurnSessionID string // resolved UUID of the session whose turn is active
	activeScope         ChatScope
	activeSession       string
	localMessageSeq     int
}

func NewModel(state UIState) Model {
	return NewModelWithRuntime(state, RuntimeHints{})
}

func NewModelWithRuntime(state UIState, runtime RuntimeHints) Model {
	normalized := normalizeState(state)
	panel, ok := panelFromView(normalized.LastActiveView)
	if !ok {
		panel = MainPanel
	}
	scope := inferScopeFromSession(normalized.LastActiveChatSession)

	nowFn := runtime.now
	startedAt := nowFn()
	model := Model{
		state:            normalized,
		focus:            panel,
		statusMessage:    initialStatusMessage(normalized, runtime),
		runtimeHints:     runtime,
		connection:       ConnectionDisconnected,
		sidebarVisible:   normalized.SidebarVisible,
		workspace:        newWorkspaceState(),
		now:              nowFn,
		startedAt:        startedAt,
		firstRun:         runtime.FirstRun,
		coldOpenActive:   runtime.FirstRun,
		chatHistoryIndex: -1,
		chatMessageIndex: map[string]int{},
		activeScope:      scope,
		activeSession:    strings.TrimSpace(normalized.LastActiveChatSession),
		perfMetrics: TUIPerformanceMetrics{
			MemoryBoundBytes: runtime.memoryBoundBytes(),
		},
	}
	if runtime.FirstRun {
		model.workspace.setMainView(ViewDashboard)
	}
	model.applyResponsiveLayout()
	return model
}

func (m Model) Init() tea.Cmd {
	commands := make([]tea.Cmd, 0, 2)
	if m.coldOpenActive {
		commands = append(commands, coldOpenTimerCmd(m.runtimeHints.coldOpenDuration()))
	}
	if !m.runtimeHints.DisableMemorySampler {
		commands = append(commands, memorySamplerCmd(memorySampleInterval))
	}
	if len(commands) == 0 {
		return nil
	}
	return tea.Batch(commands...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = normalizeDimensions(typed.Width, typed.Height)
		previousClass := m.sizeClass
		m.applyResponsiveLayout()
		if m.perfMetrics.InitialInteractivePaint == 0 {
			m.perfMetrics.InitialInteractivePaint = m.now().Sub(m.startedAt)
		}
		if previousClass != "" && previousClass != m.sizeClass {
			m.statusMessage = fmt.Sprintf("Layout changed: %s", m.sizeClass)
		}
		return m, nil
	case ConnectionStateMsg:
		m.connection = typed.State
		m.streamDegraded = typed.Degraded
		if typed.State == ConnectionConnected && !m.proofRealtime {
			m.proofRealtime = true
			m.workspace.activity = append(m.workspace.activity, "proof-of-life realtime connected")
		}
		return m, nil
	case ReplaySyncedMsg:
		if !m.proofReplay {
			m.proofReplay = true
			m.workspace.activity = append(m.workspace.activity, "proof-of-life replay synced")
		}
		return m, nil
	case WorkspaceEnvelopeMsg:
		m.workspace.applyRealtimeEnvelope(typed.Envelope)
		m.recordStreamRenderLatency(typed.Envelope)
		m.markReplaySynced()
		return m, nil
	case ChatEnvelopeMsg:
		m.applyChatEnvelope(typed.Envelope)
		m.recordStreamRenderLatency(typed.Envelope)
		m.markReplaySynced()
		return m, nil
	case coldOpenCompleteMsg:
		if m.coldOpenActive {
			m.coldOpenActive = false
			m.tourActive = true
			m.statusMessage = "Operator-ready dashboard loaded. Tour overlay active for 2m (non-blocking)."
			return m, tourOverlayTimerCmd(m.runtimeHints.tourDuration())
		}
		return m, nil
	case tourOverlayExpiredMsg:
		if m.tourActive {
			m.tourActive = false
			m.statusMessage = "Tour overlay dismissed. Normal workspace interaction continues."
		}
		return m, nil
	case memorySampleMsg:
		m.sampleMemory()
		if m.runtimeHints.DisableMemorySampler {
			return m, nil
		}
		return m, memorySamplerCmd(memorySampleInterval)
	case chatSendRequestedMsg:
		if m.runtimeHints.SendChatMessage == nil {
			m.activeTurn = false
			m.statusMessage = "Message send failed: chat send is not configured."
			return m, nil
		}
		return m, sendChatMessageCmd(typed, m.runtimeHints.SendChatMessage)
	case SessionResolvedMsg:
		m.activeTurnSessionID = strings.TrimSpace(typed.SessionID)
		return m, nil
	case chatSendCompletedMsg:
		if typed.Err != nil {
			m.activeTurn = false
			m.activeTurnSessionID = ""
			m.statusMessage = "Message send failed: " + strings.TrimSpace(typed.Err.Error())
		}
		return m, nil
	case chatCancelRequestedMsg:
		if m.runtimeHints.CancelChatTurn == nil {
			m.statusMessage = "Cancel request skipped: chat cancel is not configured."
			return m, nil
		}
		return m, cancelChatTurnCmd(typed, m.runtimeHints.CancelChatTurn)
	case chatCancelCompletedMsg:
		if typed.Err != nil {
			m.statusMessage = "Cancel request failed: " + strings.TrimSpace(typed.Err.Error())
		}
		return m, nil
	case tea.KeyMsg:
		started := m.now()
		updated, cmd := m.updateKey(typed)
		typedModel, ok := updated.(Model)
		if !ok {
			return updated, cmd
		}
		typedModel.perfMetrics.KeypressToVisible = typedModel.now().Sub(started)
		return typedModel, cmd
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
	case tea.KeyCtrlG:
		m.jumpToFrankSession("Ctrl-G")
		return m, nil
	case tea.KeyCtrlP:
		m.enterCommandMode()
		return m, nil
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
	case tea.KeyEnter, tea.KeyEsc, tea.KeyBackspace, tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
		if m.focus == ChatPanel {
			if handled, cmd := m.handleChatControlKey(key); handled {
				return m, cmd
			}
		}
		if key.Type == tea.KeyEnter {
			m.handleEnterKey()
			return m, nil
		}
		if key.Type == tea.KeyEsc {
			m.handleEscapeKey()
			return m, nil
		}
		return m, nil
	case tea.KeySpace:
		if m.focus == ChatPanel {
			m.chatInput += " "
			return m, nil
		}
		return m, nil
	case tea.KeyRunes:
		if len(key.Runes) == 1 {
			r := key.Runes[0]
			if r == ':' {
				m.enterCommandMode()
				return m, nil
			}
			if r == '0' && !m.chatTextInputActive() {
				m.jumpToFrankSession("0")
				return m, nil
			}

			if panel, ok := panelFromShortcut(key.Alt, r); ok {
				m.setFocus(panel)
				m.statusMessage = "Focus: " + panelLabel(m.focus)
				return m, nil
			}

			if r == '[' {
				m.switchScope(cycleScope(m.activeScope, false))
				return m, nil
			}
			if r == ']' {
				m.switchScope(cycleScope(m.activeScope, true))
				return m, nil
			}

			if m.focus == ChatPanel {
				m.handleChatRunes(key)
				return m, nil
			}

			if handled := m.handleWorkspaceRune(r); handled {
				return m, nil
			}
		}
		if m.focus == ChatPanel && len(key.Runes) > 0 {
			m.handleChatRunes(key)
			return m, nil
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
		m.activeSession = m.workspace.activeSessionID
		m.statusMessage = "Sidebar selection applied."
	case MainPanel:
		if m.workspace.mainView == ViewInbox {
			if m.workspace.applyInboxAction("open") {
				m.state.LastActiveChatSession = m.workspace.activeSessionID
				m.activeSession = m.workspace.activeSessionID
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
			m.activeSession = m.workspace.activeSessionID
			m.statusMessage = "Opened inbox item in context."
			return true
		}
	}
	return false
}

func (m *Model) handleChatControlKey(key tea.KeyMsg) (bool, tea.Cmd) {
	switch key.Type {
	case tea.KeyEnter:
		if key.Alt {
			m.chatInput += "\n"
			m.statusMessage = "Inserted newline."
			return true, nil
		}
		return true, m.sendOrQueueInput()
	case tea.KeyPgUp:
		m.scrollChatBy(chatScrollStepLines)
		m.statusMessage = "Chat scrolled up."
		return true, nil
	case tea.KeyPgDown:
		m.scrollChatBy(-chatScrollStepLines)
		m.statusMessage = "Chat scrolled down."
		return true, nil
	case tea.KeyHome:
		m.scrollChatBy(1 << 20)
		m.statusMessage = "Chat scrolled to oldest."
		return true, nil
	case tea.KeyEnd:
		m.chatScrollOffset = 0
		m.statusMessage = "Chat scrolled to latest."
		return true, nil
	case tea.KeyBackspace:
		runes := []rune(m.chatInput)
		if len(runes) > 0 {
			m.chatInput = string(runes[:len(runes)-1])
		}
		return true, nil
	case tea.KeyUp:
		if strings.TrimSpace(m.chatInput) == "" {
			m.recallHistory()
			return true, nil
		}
	case tea.KeyDown:
		if strings.TrimSpace(m.chatInput) == "" && m.chatScrollOffset > 0 {
			m.scrollChatBy(-1)
			return true, nil
		}
	case tea.KeyEsc:
		if m.activeTurn {
			m.activeTurn = false
			m.statusMessage = "Active turn cancelled."
			return true, requestChatCancelCmd(m.ActiveChatSession())
		}
	}
	return false, nil
}

func (m *Model) handleChatRunes(key tea.KeyMsg) {
	if len(key.Runes) == 1 {
		r := key.Runes[0]
		if r == '\n' {
			m.chatInput += "\n"
			m.statusMessage = "Inserted newline."
			return
		}
		if m.activeTurn && len(m.queuedMessages) > 0 && strings.TrimSpace(m.chatInput) == "" {
			switch strings.ToLower(string(r)) {
			case "e":
				m.applyQueueActionEdit()
				return
			case "s":
				m.applyQueueActionSteer()
				return
			case "d":
				m.applyQueueActionDelete()
				return
			}
		}
	}
	m.chatInput += string(key.Runes)
	m.tryAutocompleteMention()
}

func (m *Model) tryAutocompleteMention() {
	trimmedRight := strings.TrimRight(m.chatInput, "\n")
	start := strings.LastIndexAny(trimmedRight, " \t\n")
	tokenStart := 0
	if start >= 0 {
		tokenStart = start + 1
	}
	if tokenStart >= len(trimmedRight) {
		return
	}
	token := trimmedRight[tokenStart:]
	if !strings.HasPrefix(token, "@") || len(token) < 2 {
		return
	}
	completion := autocompleteMention(token)
	if completion == "" || completion == token {
		return
	}
	m.chatInput = trimmedRight[:tokenStart] + completion + " "
	m.statusMessage = "Mention autocomplete applied."
}

func (m Model) updateCommandInput(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyCtrlG:
		m.commandMode = false
		m.commandBuffer = ""
		m.jumpToFrankSession("Ctrl-G")
		return m, nil
	case tea.KeyEsc:
		m.commandMode = false
		m.commandBuffer = ""
		m.statusMessage = "Command cancelled."
		return m, nil
	case tea.KeyEnter:
		m.commandMode = false
		command := strings.TrimSpace(m.commandBuffer)
		m.commandBuffer = ""
		cmd := m.executeCommand(command)
		if m.quitting {
			return m, tea.Quit
		}
		return m, cmd
	case tea.KeyBackspace:
		runes := []rune(m.commandBuffer)
		if len(runes) > 1 {
			m.commandBuffer = string(runes[:len(runes)-1])
		} else {
			m.commandBuffer = ":"
		}
		return m, nil
	case tea.KeySpace:
		m.commandBuffer += " "
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

func (m *Model) executeCommand(raw string) tea.Cmd {
	trimmed := strings.TrimSpace(strings.TrimPrefix(raw, ":"))
	if trimmed == "" {
		m.statusMessage = "No command entered."
		return nil
	}

	fields := strings.Fields(trimmed)
	if strings.EqualFold(fields[0], "inbox") && len(fields) > 1 {
		m.executeInboxCommand(fields[1:])
		return nil
	}

	switch strings.ToLower(fields[0]) {
	case "quit":
		m.quitting = true
		m.statusMessage = "Exiting TUI."
	case "help", "palette":
		m.statusMessage = "Commands: :focus :frank :dashboard :inbox :send :cancel-turn :sidebar :tour dismiss :quit"
	case "focus":
		if len(fields) != 2 {
			m.statusMessage = "Usage: :focus sidebar|main|chat"
			return nil
		}
		panel, ok := panelFromView(fields[1])
		if !ok {
			m.statusMessage = "Unknown panel. Use sidebar, main, or chat."
			return nil
		}
		m.setFocus(panel)
		m.statusMessage = "Focus: " + panelLabel(m.focus)
	case "frank", "general":
		if len(fields) != 1 {
			m.statusMessage = "Usage: :" + strings.ToLower(fields[0])
			return nil
		}
		m.jumpToFrankSession(":" + strings.ToLower(fields[0]))
	case "scope":
		if len(fields) != 2 {
			m.statusMessage = "Usage: :scope org|project|task"
			return nil
		}
		m.switchScope(normalizeScope(fields[1]))
	case "send":
		return m.sendOrQueueInput()
	case "tour":
		if len(fields) != 2 {
			m.statusMessage = "Usage: :tour dismiss"
			return nil
		}
		if strings.EqualFold(fields[1], "dismiss") {
			if !m.tourActive {
				m.statusMessage = "Tour overlay is not active."
				return nil
			}
			m.tourActive = false
			m.statusMessage = "Tour overlay dismissed."
			return nil
		}
		m.statusMessage = "Unknown tour action. Use :tour dismiss."
	case "cancel", "cancel-turn":
		if !m.activeTurn {
			m.statusMessage = "No active turn to cancel."
			return nil
		}
		m.activeTurn = false
		m.statusMessage = "Active turn cancelled."
		return requestChatCancelCmd(m.ActiveChatSession())
	case "queue":
		if len(fields) != 2 {
			m.statusMessage = "Usage: :queue edit|steer|delete"
			return nil
		}
		switch strings.ToLower(fields[1]) {
		case "edit":
			m.applyQueueActionEdit()
		case "steer":
			m.applyQueueActionSteer()
		case "delete", "drop":
			m.applyQueueActionDelete()
		default:
			m.statusMessage = "Unknown queue action. Use edit, steer, or delete."
		}
	case "sidebar":
		m.executeSidebarCommand(fields[1:])
	case "dashboard", "project", "task", "inbox", "activity", "agents", "merges", "schedules":
		view, ok := resolveMainViewCommand(fields[0])
		if !ok {
			m.statusMessage = "Unknown workspace command: " + fields[0]
			return nil
		}
		m.workspace.setMainView(view)
		m.statusMessage = "Main view: " + string(view)
	default:
		m.statusMessage = "Unknown command: " + fields[0]
	}
	return nil
}

func (m Model) View() string {
	return m.viewForShell("board")
}

func (m Model) FocusedPanel() Panel { return m.focus }
func (m Model) Quitting() bool      { return m.quitting }

func (m Model) ConnectionState() ConnectionState { return m.connection }
func (m Model) StreamDegraded() bool             { return m.streamDegraded }

func (m Model) ActiveTurn() bool     { return m.activeTurn }
func (m Model) ChatScope() ChatScope { return m.activeScope }
func (m Model) ActiveChatSession() string {
	if strings.TrimSpace(m.activeSession) != "" {
		return m.activeSession
	}
	return m.workspace.activeSessionID
}

func (m Model) QueueDepth() int { return len(m.queuedMessages) }

func (m Model) QueueSnapshot() []QueuedMessage {
	cloned := make([]QueuedMessage, len(m.queuedMessages))
	copy(cloned, m.queuedMessages)
	return cloned
}

func (m Model) ChatInput() string { return m.chatInput }

func (m Model) ChatMessages() []ChatMessage {
	cloned := make([]ChatMessage, len(m.chatMessages))
	copy(cloned, m.chatMessages)
	return cloned
}

func (m Model) MainView() MainView       { return m.workspace.mainView }
func (m Model) WorkspaceSession() string { return m.workspace.activeSessionID }
func (m Model) StatusMessage() string    { return m.statusMessage }
func (m Model) WorkspaceRender(class SizeClass) string {
	return m.workspace.render(m.workspace.mainView, class)
}
func (m Model) BoardCounts() boardCounts { return m.workspace.boardCounts() }
func (m Model) PerformanceMetrics() TUIPerformanceMetrics {
	return m.perfMetrics
}

func (m Model) QualityGateFailures() []string {
	failures := make([]string, 0, 4)
	if m.perfMetrics.InitialInteractivePaint <= 0 || m.perfMetrics.InitialInteractivePaint > 1200*time.Millisecond {
		failures = append(failures, fmt.Sprintf("initial interactive paint out of budget: %v", m.perfMetrics.InitialInteractivePaint))
	}
	if m.perfMetrics.KeypressToVisible <= 0 || m.perfMetrics.KeypressToVisible > keypressLatencyBudget {
		failures = append(failures, fmt.Sprintf("keypress latency out of budget: %v", m.perfMetrics.KeypressToVisible))
	}
	if m.perfMetrics.SSEDeltaRenderLatency <= 0 || m.perfMetrics.SSEDeltaRenderLatency > sseRenderLatencyBudget {
		failures = append(failures, fmt.Sprintf("sse delta latency out of budget: %v", m.perfMetrics.SSEDeltaRenderLatency))
	}
	if m.perfMetrics.MemoryBoundBytes > 0 && m.perfMetrics.PeakMemoryBytes > m.perfMetrics.MemoryBoundBytes {
		failures = append(failures, fmt.Sprintf("memory ceiling exceeded: peak=%d bound=%d", m.perfMetrics.PeakMemoryBytes, m.perfMetrics.MemoryBoundBytes))
	}
	return failures
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
		if node.Kind == sidebarKindSession && node.SessionID == m.workspace.activeSessionID {
			label = "> " + label
		}
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
	if strings.TrimSpace(m.activeSession) != "" {
		next.LastActiveChatSession = m.activeSession
	}
	return normalizeState(next)
}

func (m Model) focusOrder() []Panel {
	layout := m.CurrentLayout()
	return layout.focusOrder
}

func (m *Model) sendOrQueueInput() tea.Cmd {
	text := strings.TrimSpace(m.chatInput)
	if text == "" {
		m.statusMessage = "Cannot send empty message."
		return nil
	}

	m.chatHistory = append(m.chatHistory, text)
	m.chatHistoryIndex = len(m.chatHistory)
	var cmd tea.Cmd
	if m.activeTurn {
		queued := QueuedMessage{Text: text}
		if m.editingQueued {
			queued.Edited = true
			m.editingQueued = false
		}
		m.queuedMessages = append(m.queuedMessages, queued)
		m.statusMessage = fmt.Sprintf("Queued message (%d pending).", len(m.queuedMessages))
	} else {
		m.appendMessage("local-user", "user", text, true)
		m.activeTurn = true
		m.chatScrollOffset = 0
		m.statusMessage = "Message sent. Waiting for assistant response."
		cmd = requestChatSendCmd(m.ActiveChatSession(), text)
	}
	m.chatInput = ""
	return cmd
}

func (m *Model) recallHistory() {
	if len(m.chatHistory) == 0 {
		return
	}
	if m.chatHistoryIndex < 0 {
		m.chatHistoryIndex = len(m.chatHistory)
	}
	if m.chatHistoryIndex > 0 {
		m.chatHistoryIndex--
	}
	m.chatInput = m.chatHistory[m.chatHistoryIndex]
	m.statusMessage = "Recalled previous message."
}

func (m *Model) applyQueueActionEdit() {
	if len(m.queuedMessages) == 0 {
		return
	}
	queued := m.queuedMessages[0]
	m.queuedMessages = append([]QueuedMessage{}, m.queuedMessages[1:]...)
	m.chatInput = queued.Text
	m.editingQueued = true
	m.statusMessage = "Editing queued message - send to re-queue."
}

func (m *Model) applyQueueActionSteer() {
	if len(m.queuedMessages) == 0 {
		return
	}
	m.queuedMessages[0].Steer = true
	m.statusMessage = "Queued message marked for steer/promote."
}

func (m *Model) applyQueueActionDelete() {
	if len(m.queuedMessages) == 0 {
		return
	}
	m.queuedMessages = append([]QueuedMessage{}, m.queuedMessages[1:]...)
	m.statusMessage = "Queued message deleted."
}

// sessionMatchesActive returns true if the given session_id matches the current
// active turn session. If activeTurnSessionID is unset (e.g. at startup or before
// first send), all session IDs are accepted to allow replay to work.
func (m *Model) sessionMatchesActive(sessionID string) bool {
	if m.activeTurnSessionID == "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(sessionID), m.activeTurnSessionID)
}

func (m *Model) applyChatEnvelope(event EventEnvelope) {
	switch event.EventType {
	case "chat.message.delta", "chat.message.chunk":
		var payload struct {
			MessageID string `json:"message_id"`
			SessionID string `json:"session_id"`
			Role      string `json:"role"`
			Delta     string `json:"delta"`
		}
		if !decodePayload(event.Payload, &payload) {
			return
		}
		if !m.activeTurn {
			return
		}
		if !m.sessionMatchesActive(payload.SessionID) {
			return
		}
		index := m.ensureMessage(payload.MessageID, payload.Role, event.OccurredAt)
		m.chatMessages[index].Content += payload.Delta
		m.chatMessages[index].Finalized = false
		m.activeTurn = true
	case "chat.message.finalized":
		var payload struct {
			MessageID string `json:"message_id"`
			SessionID string `json:"session_id"`
			Role      string `json:"role"`
			Content   string `json:"content"`
		}
		if !decodePayload(event.Payload, &payload) {
			return
		}
		if !m.activeTurn {
			return
		}
		if !m.sessionMatchesActive(payload.SessionID) {
			return
		}
		index := m.ensureMessage(payload.MessageID, payload.Role, event.OccurredAt)
		if strings.TrimSpace(payload.Content) != "" {
			m.chatMessages[index].Content = payload.Content
		}
		m.chatMessages[index].Finalized = true
		m.completeTurnAndPromoteQueue("Promoted queued message after finalize.")
	case "chat.turn.started":
		var payload struct {
			SessionID string `json:"session_id"`
		}
		if decodePayload(event.Payload, &payload) && !m.sessionMatchesActive(payload.SessionID) {
			return
		}
		m.activeTurn = true
	case "chat.turn.completed":
		var payload struct {
			SessionID string `json:"session_id"`
		}
		_ = decodePayload(event.Payload, &payload)
		if !m.sessionMatchesActive(payload.SessionID) {
			return
		}
		if !m.activeTurn && len(m.queuedMessages) == 0 {
			return
		}
		m.completeTurnAndPromoteQueue("Promoted queued message after turn completion.")
	case "chat.turn.cancelled":
		var payload struct {
			SessionID string `json:"session_id"`
		}
		_ = decodePayload(event.Payload, &payload)
		if !m.sessionMatchesActive(payload.SessionID) {
			return
		}
		if !m.activeTurn {
			return
		}
		m.activeTurn = false
		m.activeTurnSessionID = ""
		m.statusMessage = "Active turn cancelled."
	case "chat.tool_call.status":
		var payload struct {
			MessageID string `json:"message_id"`
			Name      string `json:"name"`
			Status    string `json:"status"`
		}
		if !decodePayload(event.Payload, &payload) {
			return
		}
		if !m.activeTurn {
			return
		}
		index := m.ensureMessage(payload.MessageID, "assistant", event.OccurredAt)
		m.upsertToolCall(index, strings.TrimSpace(payload.Name), strings.TrimSpace(payload.Status))
	}
}

func (m *Model) completeTurnAndPromoteQueue(promoteStatus string) {
	m.activeTurn = false
	m.activeTurnSessionID = ""
	m.finalizePendingAssistantMessages()
	if len(m.queuedMessages) == 0 {
		m.statusMessage = ""
		return
	}
	next := m.queuedMessages[0]
	m.queuedMessages = append([]QueuedMessage{}, m.queuedMessages[1:]...)
	m.appendMessage("local-user", "user", next.Text, true)
	m.activeTurn = true
	m.chatScrollOffset = 0
	if strings.TrimSpace(promoteStatus) != "" {
		m.statusMessage = promoteStatus
	}
}

func (m *Model) finalizePendingAssistantMessages() {
	for i := range m.chatMessages {
		if m.chatMessages[i].Role != "assistant" {
			continue
		}
		if m.chatMessages[i].Finalized {
			continue
		}
		m.chatMessages[i].Finalized = true
	}
}

func (m *Model) appendMessage(prefix, role, content string, finalized bool) {
	m.localMessageSeq++
	id := fmt.Sprintf("%s-%d", strings.TrimSpace(prefix), m.localMessageSeq)
	message := ChatMessage{ID: id, Role: normalizeRole(role), Content: content, Finalized: finalized, Timestamp: m.now()}
	m.chatMessageIndex[id] = len(m.chatMessages)
	m.chatMessages = append(m.chatMessages, message)
}

func (m *Model) ensureMessage(messageID, role string, occurredAt time.Time) int {
	id := strings.TrimSpace(messageID)
	if id == "" {
		m.localMessageSeq++
		id = fmt.Sprintf("stream-%d", m.localMessageSeq)
	}
	if index, ok := m.chatMessageIndex[id]; ok {
		if normalized := normalizeRole(role); normalized != "" {
			m.chatMessages[index].Role = normalized
		}
		if m.chatMessages[index].Timestamp.IsZero() {
			if occurredAt.IsZero() {
				m.chatMessages[index].Timestamp = m.now()
			} else {
				m.chatMessages[index].Timestamp = occurredAt
			}
		}
		return index
	}
	if occurredAt.IsZero() {
		occurredAt = m.now()
	}
	message := ChatMessage{ID: id, Role: normalizeRole(role), Finalized: false, Timestamp: occurredAt}
	m.chatMessageIndex[id] = len(m.chatMessages)
	m.chatMessages = append(m.chatMessages, message)
	return len(m.chatMessages) - 1
}

func (m *Model) upsertToolCall(index int, name, status string) {
	if index < 0 || index >= len(m.chatMessages) || name == "" {
		return
	}
	for i := range m.chatMessages[index].ToolCalls {
		if m.chatMessages[index].ToolCalls[i].Name == name {
			m.chatMessages[index].ToolCalls[i].Status = status
			return
		}
	}
	m.chatMessages[index].ToolCalls = append(m.chatMessages[index].ToolCalls, ToolCallStatus{Name: name, Status: status})
}

func decodePayload(raw json.RawMessage, out any) bool {
	if len(raw) == 0 {
		return false
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return false
	}
	return true
}

func (m *Model) switchScope(next ChatScope) {
	m.activeScope = next
	m.activeSession = sessionForScope(next)
	m.chatScrollOffset = 0
	m.statusMessage = fmt.Sprintf("Scope switched to %s.", next)
}

func (m *Model) jumpToFrankSession(trigger string) {
	if err := m.workspace.activateGeneralSession(); err != nil {
		m.statusMessage = "Unable to load Frank session. Press Ctrl-G or :frank to retry."
		return
	}
	m.activeScope = ScopeOrg
	m.activeSession = m.workspace.activeSessionID
	m.state.LastActiveChatSession = m.workspace.activeSessionID
	m.chatScrollOffset = 0
	if strings.TrimSpace(trigger) == "" {
		m.statusMessage = "Switched to Frank session."
		return
	}
	m.statusMessage = fmt.Sprintf("Switched to Frank session (%s).", trigger)
}

func (m Model) chatTextInputActive() bool {
	return m.focus == ChatPanel
}

func inferScopeFromSession(session string) ChatScope {
	trimmed := strings.ToLower(strings.TrimSpace(session))
	switch {
	case strings.HasPrefix(trimmed, "org") || strings.Contains(trimmed, "-org-"):
		return ScopeOrg
	case strings.HasPrefix(trimmed, "task") || strings.Contains(trimmed, "-task-"):
		return ScopeTask
	case strings.HasPrefix(trimmed, "project") || strings.Contains(trimmed, "-project-"):
		return ScopeProject
	default:
		return ScopeOrg
	}
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

func (m *Model) enterCommandMode() {
	m.commandMode = true
	m.commandBuffer = ":"
	m.statusMessage = "Command mode"
}

func (m *Model) executeSidebarCommand(args []string) {
	if len(args) != 1 {
		m.statusMessage = "Usage: :sidebar up|down|home|end|expand|collapse|select"
		return
	}
	m.setFocus(SidebarPanel)
	switch strings.ToLower(args[0]) {
	case "up":
		m.workspace.moveSidebar(-1)
		m.statusMessage = "Sidebar cursor moved up."
	case "down":
		m.workspace.moveSidebar(1)
		m.statusMessage = "Sidebar cursor moved down."
	case "home":
		m.workspace.sidebarHome()
		m.statusMessage = "Sidebar cursor moved home."
	case "end":
		m.workspace.sidebarEnd()
		m.statusMessage = "Sidebar cursor moved end."
	case "expand":
		m.workspace.expandSidebarNode()
		m.statusMessage = "Sidebar node expanded."
	case "collapse":
		m.workspace.collapseSidebarNode()
		m.statusMessage = "Sidebar node collapsed."
	case "select", "open":
		m.workspace.selectSidebarNode()
		m.state.LastActiveChatSession = m.workspace.activeSessionID
		m.activeSession = m.workspace.activeSessionID
		m.statusMessage = "Sidebar selection applied."
	default:
		m.statusMessage = "Unknown sidebar action. Use up, down, home, end, expand, collapse, or select."
	}
}

func (m *Model) executeInboxCommand(args []string) {
	if len(args) != 1 {
		m.statusMessage = "Usage: :inbox up|down|home|end|open|approve|reject|defer"
		return
	}
	m.workspace.setMainView(ViewInbox)
	m.setFocus(MainPanel)
	switch strings.ToLower(args[0]) {
	case "up":
		m.workspace.moveInbox(-1)
		m.statusMessage = "Inbox cursor moved up."
	case "down":
		m.workspace.moveInbox(1)
		m.statusMessage = "Inbox cursor moved down."
	case "home":
		m.workspace.inboxHome()
		m.statusMessage = "Inbox cursor moved home."
	case "end":
		m.workspace.inboxEnd()
		m.statusMessage = "Inbox cursor moved end."
	case "open":
		if !m.workspace.applyInboxAction("open") {
			m.statusMessage = "No inbox item available."
			return
		}
		m.state.LastActiveChatSession = m.workspace.activeSessionID
		m.activeSession = m.workspace.activeSessionID
		m.statusMessage = "Opened inbox item in context."
	case "approve", "reject", "defer":
		action := strings.ToLower(args[0])
		if !m.workspace.applyInboxAction(action) {
			m.statusMessage = "No inbox item available."
			return
		}
		switch action {
		case "approve":
			m.statusMessage = "Inbox item approved."
		case "reject":
			m.statusMessage = "Inbox item rejected."
		default:
			m.statusMessage = "Inbox item deferred."
		}
	default:
		m.statusMessage = "Unknown inbox action. Use up, down, home, end, open, approve, reject, or defer."
	}
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

func initialStatusMessage(state UIState, runtime RuntimeHints) string {
	if runtime.FirstRun {
		return "First run: cold-open, dashboard landing, tour overlay, and proof-of-life checks are active."
	}
	if runtime.ModifierReliabilityUncertain {
		return "tmux mode: use :focus/:frank/:dashboard/:inbox fallbacks if modifiers are unreliable."
	}
	base := "Tab/Shift-Tab cycle focus. 1/2/3 direct focus. :focus/:scope/:quit commands available."
	if strings.TrimSpace(state.LastActiveChatSession) == "" {
		return base + " Frank jump: Ctrl-G, 0 (outside chat input), or :frank."
	}
	return base
}

func (m Model) commandFallbackHelp() string {
	base := ":focus sidebar|main|chat | :frank | :dashboard/:project/:task/:inbox | :send | :cancel-turn | PgUp/PgDn chat scroll | :tour dismiss | :quit"
	if m.runtimeHints.ModifierReliabilityUncertain {
		return "tmux-safe commands: " + base
	}
	return "fallback commands: " + base
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

func (m *Model) markReplaySynced() {
	if m.proofReplay {
		return
	}
	m.proofReplay = true
	m.workspace.activity = append(m.workspace.activity, "proof-of-life replay synced")
}

func (m *Model) scrollChatBy(delta int) {
	if delta == 0 {
		return
	}
	m.chatScrollOffset += delta
	if m.chatScrollOffset < 0 {
		m.chatScrollOffset = 0
	}
}

func (m *Model) recordStreamRenderLatency(event EventEnvelope) {
	if event.EventType != "chat.message.delta" {
		return
	}
	if event.OccurredAt.IsZero() {
		return
	}
	latency := m.now().Sub(event.OccurredAt)
	if latency < 0 {
		latency = 0
	}
	m.perfMetrics.SSEDeltaRenderLatency = latency
}

func (m *Model) sampleMemory() {
	stats := runtime.MemStats{}
	runtime.ReadMemStats(&stats)
	if stats.Alloc > m.perfMetrics.PeakMemoryBytes {
		m.perfMetrics.PeakMemoryBytes = stats.Alloc
	}
}

func requestChatSendCmd(sessionID, content string) tea.Cmd {
	return func() tea.Msg {
		return chatSendRequestedMsg{
			SessionID: strings.TrimSpace(sessionID),
			Content:   content,
		}
	}
}

func sendChatMessageCmd(
	request chatSendRequestedMsg,
	sendFn func(ctx context.Context, sessionID, content string) error,
) tea.Cmd {
	return func() tea.Msg {
		if sendFn == nil {
			return chatSendCompletedMsg{Err: fmt.Errorf("chat send is not configured")}
		}
		err := sendFn(context.Background(), strings.TrimSpace(request.SessionID), request.Content)
		return chatSendCompletedMsg{Err: err}
	}
}

func requestChatCancelCmd(sessionID string) tea.Cmd {
	return func() tea.Msg {
		return chatCancelRequestedMsg{SessionID: strings.TrimSpace(sessionID)}
	}
}

func cancelChatTurnCmd(
	request chatCancelRequestedMsg,
	cancelFn func(ctx context.Context, sessionID string) error,
) tea.Cmd {
	return func() tea.Msg {
		if cancelFn == nil {
			return chatCancelCompletedMsg{Err: fmt.Errorf("chat cancel is not configured")}
		}
		err := cancelFn(context.Background(), strings.TrimSpace(request.SessionID))
		return chatCancelCompletedMsg{Err: err}
	}
}

func coldOpenTimerCmd(duration time.Duration) tea.Cmd {
	return tea.Tick(duration, func(time.Time) tea.Msg {
		return coldOpenCompleteMsg{}
	})
}

func tourOverlayTimerCmd(duration time.Duration) tea.Cmd {
	return tea.Tick(duration, func(time.Time) tea.Msg {
		return tourOverlayExpiredMsg{}
	})
}

func memorySamplerCmd(duration time.Duration) tea.Cmd {
	return tea.Tick(duration, func(time.Time) tea.Msg {
		return memorySampleMsg{}
	})
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
