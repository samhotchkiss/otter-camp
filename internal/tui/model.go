package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
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
type chatHistoryLoadedMsg struct {
	Messages []ChatMessage
}

type SessionResolvedMsg struct {
	SessionID string
}

type sidebarDataLoadedMsg struct {
	InboxCount   int
	OrgSessionID string
	Chats        []SidebarChatItem
	Projects     []SidebarProjectItem
}

type projectTasksLoadedMsg struct {
	ProjectID  string
	Tasks      []SidebarTaskItem
	ExpandNode bool // true when triggered by user interaction (Space/arrow/Enter)
}

type projectDetailLoadedMsg struct {
	Detail ProjectDetail
}

type agentsLoadedMsg struct {
	Agents []string // "name=lifecycle_status" format
}

type taskDetailLoadedMsg struct {
	Detail TaskDetailItem
}

type inboxItemsLoadedMsg struct {
	Items []InboxSummaryItem
}

const (
	memorySampleInterval   = 5 * time.Second
	keypressLatencyBudget  = 100 * time.Millisecond
	sseRenderLatencyBudget = 250 * time.Millisecond
	chatScrollStepLines    = 8
	panelResizeStep        = 0.02
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
	searchMode     bool
	searchPanel    Panel
	searchQuery    string
	sidebarFilter  string
	mainFilter     string
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
	sidebarLoaded  bool
	perfMetrics    TUIPerformanceMetrics

	chatInput            string
	chatHistory          []string
	chatHistoryIndex     int
	chatMessages         []ChatMessage
	chatMessageIndex     map[string]int
	toolCallExpanded     map[string]map[string]bool
	toolCallMessageIndex map[string]int
	chatScrollOffset     int
	chatHistoryLoading   bool // true while a history fetch is in-flight after session switch
	queuedMessages       []QueuedMessage
	editingQueued        bool
	activeTurn           bool
	activeTurnSessionID  string // resolved UUID of the session whose turn is active
	turnsSynced          bool   // true once ReplaySyncedMsg received; gates live turn events
	activeScope          ChatScope
	activeSession        string
	localMessageSeq      int
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
		state:                normalized,
		focus:                panel,
		statusMessage:        initialStatusMessage(normalized, runtime),
		runtimeHints:         runtime,
		connection:           ConnectionDisconnected,
		sidebarVisible:       normalized.SidebarVisible,
		workspace:            newWorkspaceState(),
		now:                  nowFn,
		startedAt:            startedAt,
		firstRun:             runtime.FirstRun,
		coldOpenActive:       runtime.FirstRun,
		chatHistoryIndex:     -1,
		chatMessageIndex:     map[string]int{},
		toolCallExpanded:     map[string]map[string]bool{},
		toolCallMessageIndex: map[string]int{},
		activeScope:          scope,
		activeSession:        strings.TrimSpace(normalized.LastActiveChatSession),
		perfMetrics: TUIPerformanceMetrics{
			MemoryBoundBytes: runtime.memoryBoundBytes(),
		},
	}
	model.workspace.setMainView(MainView(normalizeMainViewState(normalized.LastMainView)))
	if runtime.FirstRun {
		model.workspace.setMainView(ViewDashboard)
	}
	// Task detail view requires a selected task; fall back to dashboard if none is loaded yet.
	if model.workspace.mainView == ViewTask && model.workspace.selectedTaskID == "" {
		model.workspace.setMainView(ViewDashboard)
	}
	// Restore persisted project selection.
	model.workspace.selectedProjectID = normalized.LastSelectedProjectID
	// Project view requires a selected project; fall back to dashboard if none is persisted.
	if model.workspace.mainView == ViewProject && model.workspace.selectedProjectID == "" {
		model.workspace.setMainView(ViewDashboard)
	}
	// If the saved session is a placeholder (session-project-current, session-task-current, etc.)
	// or a raw UUID that can't be resolved, reset to org session on startup.
	if sess := strings.TrimSpace(model.activeSession); sess != "" && sess != generalSessionID {
		isPlaceholder := strings.HasPrefix(sess, "session-")
		isUnresolvableUUID := !isPlaceholder && model.workspace.sessionLabel(sess) == ""
		if isPlaceholder || isUnresolvableUUID {
			model.activeSession = generalSessionID
			model.activeScope = ScopeOrg
		}
	}
	model.applyResponsiveLayout()
	return model
}

func (m Model) Init() tea.Cmd {
	commands := make([]tea.Cmd, 0, 4)
	if m.coldOpenActive {
		commands = append(commands, coldOpenTimerCmd(m.runtimeHints.coldOpenDuration()))
	}
	if !m.runtimeHints.DisableMemorySampler {
		commands = append(commands, memorySamplerCmd(memorySampleInterval))
	}
	// Sidebar data is loaded from ConnectionConnected (on first SSE connect) so
	// we do not fire it here too — doing both causes a burst of API requests
	// that trips the per-IP rate limiter.
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
			// Suppress layout change notification during the startup window (< 3s).
			// tmux and some terminals send multiple SIGWINCH events before settling
			// on the real terminal dimensions; we don't want to show a stale
			// "Layout changed: XL" that persists for the entire session.
			if m.now().Sub(m.startedAt) >= 3*time.Second {
				m.statusMessage = fmt.Sprintf("Layout changed: %s", m.sizeClass)
			}
		}
		return m, nil
	case ConnectionStateMsg:
		m.connection = typed.State
		m.streamDegraded = typed.Degraded
		if typed.State == ConnectionConnected && !m.proofRealtime {
			m.proofRealtime = true
			m.workspace.activity = append(m.workspace.activity, "realtime events connected")
			// Load sidebar data on first successful connection.
			if m.runtimeHints.LoadOrgSession != nil || m.runtimeHints.LoadInboxCount != nil || m.runtimeHints.LoadRecentChats != nil || m.runtimeHints.LoadProjects != nil {
				return m, loadSidebarDataCmd(m.runtimeHints)
			}
		}
		return m, nil
	case ReplaySyncedMsg:
		m.turnsSynced = true
		if !m.proofReplay {
			m.proofReplay = true
			m.workspace.activity = append(m.workspace.activity, "event replay complete")
		}
		var histCmd tea.Cmd
		if m.runtimeHints.LoadChatHistory != nil {
			sessionID := strings.TrimSpace(m.ActiveChatSession())
			// Skip if still using placeholder — sidebar data will trigger load when it arrives.
			if sessionID != generalSessionID && sessionID != "" {
				histCmd = loadChatHistoryCmd(sessionID, m.runtimeHints.LoadChatHistory)
			}
		}
		return m, histCmd
	case chatHistoryLoadedMsg:
		m.chatHistoryLoading = false
		if len(typed.Messages) == 0 {
			return m, nil
		}
		// Merge REST history into the local message list:
		//  - New messages (not in index) are added.
		//  - Existing messages that are not yet finalized are updated in-place
		//    with the finalized REST version (handles SSE reconnect mid-turn where
		//    streaming content was captured but chat.message.finalized was missed).
		var newMsgs []ChatMessage
		for _, msg := range typed.Messages {
			if idx, exists := m.chatMessageIndex[msg.ID]; !exists {
				newMsgs = append(newMsgs, msg)
			} else if !m.chatMessages[idx].Finalized && msg.Finalized {
				m.chatMessages[idx] = msg
			}
		}
		if len(newMsgs) == 0 {
			return m, nil
		}
		combined := append(m.chatMessages, newMsgs...)
		sort.SliceStable(combined, func(i, j int) bool {
			return combined[i].Timestamp.Before(combined[j].Timestamp)
		})
		m.chatMessages = combined
		m.chatMessageIndex = make(map[string]int, len(m.chatMessages))
		for i, msg := range m.chatMessages {
			m.chatMessageIndex[msg.ID] = i
		}
		return m, nil
	case sidebarDataLoadedMsg:
		m.sidebarLoaded = true
		m.workspace.inboxCount = typed.InboxCount
		m.workspace.rebuildSidebar(typed.OrgSessionID, typed.Chats, typed.Projects)
		activityParts := []string{}
		if len(typed.Projects) > 0 {
			activityParts = append(activityParts, fmt.Sprintf("%d project(s)", len(typed.Projects)))
		}
		if len(typed.Chats) > 0 {
			activityParts = append(activityParts, fmt.Sprintf("%d recent chat(s)", len(typed.Chats)))
		}
		if typed.InboxCount > 0 {
			activityParts = append(activityParts, fmt.Sprintf("%d inbox item(s)", typed.InboxCount))
		}
		if len(activityParts) > 0 {
			m.workspace.activity = append(m.workspace.activity, "sidebar loaded: "+strings.Join(activityParts, ", "))
		}
		// Pre-load tasks for all projects so the dashboard task board is populated on startup.
		// Also load agents for the AGENTS view.
		// If a project is the persisted selected project, expand its sidebar node so the user
		// sees their active project's task list on startup without needing to click.
		var cmds []tea.Cmd
		for _, proj := range typed.Projects {
			expand := m.workspace.selectedProjectID == proj.ID
			cmds = append(cmds, loadProjectTasksCmd(proj.ID, m.runtimeHints, expand))
		}
		if cmd := loadAgentsCmd(m.runtimeHints); cmd != nil {
			cmds = append(cmds, cmd)
		}
		// If restoring to a project view with a persisted project ID, eagerly load project detail
		// so the project board is ready without requiring an extra sidebar click.
		if m.workspace.selectedProjectID != "" && m.workspace.selectedProject == nil {
			if cmd := loadProjectDetailCmd(m.workspace.selectedProjectID, m.runtimeHints); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		// If the active session was empty or the placeholder, replace with the real
		// org session UUID and trigger history load unconditionally (sidebar data
		// arrives before or after ReplaySyncedMsg; always load when we first get the UUID).
		if typed.OrgSessionID != "" && (m.activeSession == "" || m.activeSession == generalSessionID) {
			m.activeSession = typed.OrgSessionID
			m.activeScope = ScopeOrg
			m.workspace.activeSessionID = typed.OrgSessionID
			m.state.LastActiveChatSession = typed.OrgSessionID
			if m.runtimeHints.LoadChatHistory != nil {
				cmds = append(cmds, loadChatHistoryCmd(typed.OrgSessionID, m.runtimeHints.LoadChatHistory))
			}
		}
		if len(cmds) == 0 {
			return m, nil
		}
		return m, tea.Batch(cmds...)
	case projectTasksLoadedMsg:
		m.workspace.setProjectTasks(typed.ProjectID, typed.Tasks, typed.ExpandNode)
		return m, nil
	case projectDetailLoadedMsg:
		m.workspace.selectedProject = &typed.Detail
		// Apply any pending cursor request (task opened before project loaded).
		if m.workspace.pendingProjectCursorTaskID != "" {
			m.workspace.syncProjectCursorToTask(m.workspace.pendingProjectCursorTaskID)
		}
		return m, nil
	case agentsLoadedMsg:
		if len(typed.Agents) > 0 {
			m.workspace.agents = typed.Agents
		}
		return m, nil
	case taskDetailLoadedMsg:
		rec := m.workspace.tasks[typed.Detail.ID]
		if rec == nil {
			// Task not yet in the map (e.g. from a CHATS session outside the loaded project)
			// — create a minimal record so ViewTask can render it.
			rec = &taskRecord{
				ID:    typed.Detail.ID,
				Title: typed.Detail.Title,
			}
			if m.workspace.tasks == nil {
				m.workspace.tasks = make(map[string]*taskRecord)
			}
			m.workspace.tasks[typed.Detail.ID] = rec
		}
		rec.Description = typed.Detail.Description
		rec.SessionID = typed.Detail.SessionID
		rec.TaskNumber = typed.Detail.TaskNumber
		rec.AgentName = typed.Detail.AgentName
		rec.FlowNodeName = typed.Detail.FlowNodeName
		rec.RequiresHumanReview = typed.Detail.RequiresHumanReview
		if typed.Detail.Title != "" {
			rec.Title = typed.Detail.Title
		}
		if typed.Detail.WorkStatus != "" {
			rec.Status = typed.Detail.WorkStatus
		}
		// Cache reverse session→label mapping so the chat header resolves
		// the task session UUID to a human-readable label immediately.
		if sid := strings.TrimSpace(typed.Detail.SessionID); sid != "" {
			label := strings.TrimSpace(rec.Title)
			if rec.TaskNumber > 0 {
				label = fmt.Sprintf("OC-%d: %s", rec.TaskNumber, label)
			}
			m.workspace.sessionToTaskLabel[sid] = label
		}
		return m, nil
	case inboxItemsLoadedMsg:
		newInbox := make([]inboxItem, 0, len(typed.Items))
		for _, item := range typed.Items {
			newInbox = append(newInbox, inboxItem{
				ID:      item.ID,
				TaskID:  item.TaskID,
				Summary: item.Summary,
			})
		}
		m.workspace.inbox = newInbox
		if len(newInbox) > 0 {
			m.workspace.inboxCount = len(newInbox)
		}
		return m, nil
	case WorkspaceEnvelopeMsg:
		if cmd := m.applyWorkspaceCommand(typed.Envelope); cmd != nil {
			m.recordStreamRenderLatency(typed.Envelope)
			m.markReplaySynced()
			return m, cmd
		}
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
			// EX-099: restore the failed message to the input box so the user
			// can retry without retyping.  The message is also still in chat
			// history so recallHistory() would find it too.
			if len(m.chatHistory) > 0 {
				m.chatInput = m.chatHistory[len(m.chatHistory)-1]
			}
			m.statusMessage = "Send failed (input restored) — " + strings.TrimSpace(typed.Err.Error())
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
	if m.searchMode {
		return m.updateSearchInput(key)
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
	case tea.KeyEnter, tea.KeyEsc, tea.KeyBackspace, tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
		if m.focus == SidebarPanel {
			if handled, cmd := m.handleSidebarControlKey(key); handled {
				return m, cmd
			}
		}
		if m.focus == ChatPanel {
			if handled, cmd := m.handleChatControlKey(key); handled {
				return m, cmd
			}
		}
		if m.focus == MainPanel && m.workspace.mainView == ViewProject {
			switch key.Type {
			case tea.KeyUp:
				m.workspace.moveProjectTaskCursor(-1)
				return m, nil
			case tea.KeyDown:
				m.workspace.moveProjectTaskCursor(1)
				return m, nil
			}
		}
		if m.focus == MainPanel && m.workspace.mainView == ViewDashboard {
			switch key.Type {
			case tea.KeyUp:
				m.workspace.moveDashboardCursor(-1)
				return m, nil
			case tea.KeyDown:
				m.workspace.moveDashboardCursor(1)
				return m, nil
			}
		}
		if key.Type == tea.KeyEnter {
			cmd := m.handleEnterKey()
			return m, cmd
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
		if m.focus == SidebarPanel {
			node := m.workspace.currentSidebarNode()
			if node != nil {
				switch node.Kind {
				case sidebarKindProject:
					if node.Expanded {
						node.Expanded = false
					} else {
						node.Expanded = true
						return m, loadProjectTasksCmd(node.ProjectID, m.runtimeHints, true)
					}
				case sidebarKindHeader:
					sectionID := sidebarSectionID(strings.TrimPrefix(node.ID, "header-"))
					m.workspace.sectionCollapsed[sectionID] = !m.workspace.sectionCollapsed[sectionID]
				}
			}
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
				return m, m.switchScope(cycleScope(m.activeScope, false))
			}
			if r == ']' {
				return m, m.switchScope(cycleScope(m.activeScope, true))
			}
			if (r == '<' || r == '>') && (m.focus != ChatPanel || strings.TrimSpace(m.chatInput) == "") {
				delta := panelResizeStep
				if r == '<' {
					delta = -panelResizeStep
				}
				if m.resizeFocusedPanel(delta) {
					return m, nil
				}
			}
			if r == '/' && (m.focus == SidebarPanel || m.focus == MainPanel) {
				m.enterSearchMode(m.focus)
				return m, nil
			}
			if m.focus == ChatPanel && strings.TrimSpace(m.chatInput) == "" {
				if r == 'g' {
					m.scrollChatBy(1 << 20)
					m.statusMessage = "Chat scrolled to oldest."
					return m, nil
				}
				if r == 'G' {
					m.chatScrollOffset = 0
					m.statusMessage = "Chat scrolled to latest."
					return m, nil
				}
			}
			if m.focus == ChatPanel {
				m.handleChatRunes(key)
				return m, nil
			}

			// ? opens help only when not typing in the chat input
			if r == '?' {
				if m.workspace.mainView == ViewHelp {
					m.workspace.setMainView(ViewDashboard)
					m.statusMessage = "Returned to dashboard."
				} else {
					m.workspace.setMainView(ViewHelp)
					m.setFocus(MainPanel)
					m.statusMessage = "Keybinding reference. Press ? or Esc to close."
				}
				return m, nil
			}

			if handled, cmd := m.handleWorkspaceRune(r); handled {
				return m, cmd
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

func (m *Model) handleEnterKey() tea.Cmd {
	switch m.focus {
	case SidebarPanel:
		node := m.workspace.currentSidebarNode()
		m.workspace.selectSidebarNode()
		m.state.LastActiveChatSession = m.workspace.activeSessionID
		m.activeSession = m.workspace.activeSessionID
		m.statusMessage = "Sidebar selection applied."
		if node != nil {
			switch node.Kind {
			case sidebarKindSession:
				// Set scope based on the session's scope type
				switch node.SessionScope {
				case "project_task":
					m.activeScope = ScopeTask
					// Navigate to task detail for task-scoped sessions
					if node.TaskID != "" {
						m.workspace.selectedTaskID = node.TaskID
						m.workspace.setMainView(ViewTask)
					}
				case "project":
					m.activeScope = ScopeProject
				default:
					m.activeScope = ScopeOrg
				}
				// Reload chat history and (if task-scoped) load task detail
				var cmds []tea.Cmd
				if m.runtimeHints.LoadChatHistory != nil {
					m.chatMessages = nil
					m.chatHistoryLoading = true
					m.chatMessageIndex = make(map[string]int)
					m.chatScrollOffset = 0
					cmds = append(cmds, loadChatHistoryCmd(m.workspace.activeSessionID, m.runtimeHints.LoadChatHistory))
				}
				if node.TaskID != "" {
					cmds = append(cmds, loadTaskDetailCmd(node.TaskID, m.runtimeHints))
				}
				if len(cmds) > 0 {
					return tea.Batch(cmds...)
				}
			case sidebarKindProject:
				// Update scope indicator + status bar to show project context
				m.activeScope = ScopeProject
				m.statusMessage = node.Label
				// Switch chat panel to Frank/org session when viewing a project
				// so the session title matches the context (not a stale task session)
				if frankNode := m.workspace.nodes[generalSidebarNodeID]; frankNode != nil && frankNode.SessionID != "" {
					if m.activeSession != frankNode.SessionID {
						m.activeSession = frankNode.SessionID
						m.workspace.activeSessionID = frankNode.SessionID
						m.chatMessages = nil
						m.chatMessageIndex = make(map[string]int)
						m.chatScrollOffset = 0
					}
				}
				// Load project detail + tasks
				m.workspace.selectedProject = nil // clear stale detail
				m.workspace.projectTaskCursor = 0 // reset cursor for new project
				cmds := []tea.Cmd{loadProjectTasksCmd(node.ProjectID, m.runtimeHints, true)}
				if m.runtimeHints.LoadProjectDetail != nil {
					cmds = append(cmds, loadProjectDetailCmd(node.ProjectID, m.runtimeHints))
				}
				if m.runtimeHints.LoadChatHistory != nil && m.workspace.activeSessionID != "" {
					cmds = append(cmds, loadChatHistoryCmd(m.workspace.activeSessionID, m.runtimeHints.LoadChatHistory))
				}
				return tea.Batch(cmds...)
			case sidebarKindInbox:
				m.statusMessage = "Inbox"
				return loadInboxItemsCmd(m.runtimeHints)
			case sidebarKindTask:
				// Set the project context so "p" and "Esc·back to project" work correctly
				var projectIDForTask string
				if node.ParentID != "" {
					if projNode := m.workspace.nodes[node.ParentID]; projNode != nil && projNode.Kind == sidebarKindProject {
						m.workspace.selectedProjectID = projNode.ProjectID
						projectIDForTask = projNode.ProjectID
					}
				}
				// Sync the project task cursor so "p" returns to the right row.
				m.workspace.syncProjectCursorToTask(node.TaskID)
				// Load full task detail (description, task number) on demand.
				// Also load project detail if not already loaded, so "p" works immediately.
				cmds := []tea.Cmd{loadTaskDetailCmd(node.TaskID, m.runtimeHints)}
				if projectIDForTask != "" && m.workspace.selectedProject == nil && m.runtimeHints.LoadProjectDetail != nil {
					cmds = append(cmds, loadProjectDetailCmd(projectIDForTask, m.runtimeHints))
				}
				return tea.Batch(cmds...)
			}
		}
	case MainPanel:
		if m.workspace.mainView == ViewInbox {
			if m.workspace.applyInboxAction("open") {
				m.state.LastActiveChatSession = m.workspace.activeSessionID
				m.activeSession = m.workspace.activeSessionID
				m.statusMessage = "Opened inbox item in context."
				return nil
			}
		}
		if m.workspace.mainView == ViewTask {
			if sessionID, ok := m.workspace.openSelectedTaskSession(); ok {
				m.state.LastActiveChatSession = sessionID
				m.activeSession = sessionID
				m.activeScope = ScopeTask
				m.chatScrollOffset = 0
				m.setFocus(ChatPanel)
				taskStatus := ""
				if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil {
					taskStatus = task.Status
				}
				switch taskStatus {
				case "in_progress":
					m.statusMessage = "Resumed task session."
				case "done", "approved":
					m.statusMessage = "Viewing completed task session."
				default:
					m.statusMessage = "Opened task session."
				}
				if m.runtimeHints.LoadChatHistory != nil {
					return loadChatHistoryCmd(sessionID, m.runtimeHints.LoadChatHistory)
				}
				return nil
			}
			m.statusMessage = "No active session for this task."
			return nil
		}
		if m.workspace.mainView == ViewProject {
			// Navigate to the task highlighted by projectTaskCursor
			if proj := m.workspace.selectedProject; proj != nil {
				openTasks := make([]SidebarTaskItem, 0, len(proj.Tasks))
				for _, t := range proj.Tasks {
					if t.WorkStatus != "done" && t.WorkStatus != "approved" && t.WorkStatus != "cancelled" {
						openTasks = append(openTasks, t)
					}
				}
				cursor := m.workspace.projectTaskCursor
				if len(openTasks) > 0 && cursor >= 0 && cursor < len(openTasks) {
					taskID := openTasks[cursor].ID
					m.workspace.selectedTaskID = taskID
					m.workspace.setMainView(ViewTask)
					m.workspace.syncSidebarToTask(taskID)
					m.statusMessage = "Opened task detail."
					return loadTaskDetailCmd(taskID, m.runtimeHints)
				}
			}
			m.workspace.setMainView(ViewTask)
			m.statusMessage = "Opened task detail."
			return nil
		}
		if m.workspace.mainView == ViewDashboard {
			// Auto-select the first open task if none is currently selected
			if m.workspace.selectedTaskID == "" {
				for _, id := range m.workspace.taskOrder {
					task := m.workspace.tasks[id]
					if task != nil && task.Status != "done" && task.Status != "approved" && task.Status != "cancelled" {
						m.workspace.selectedTaskID = id
						break
					}
				}
			}
			m.workspace.setMainView(ViewTask)
			m.statusMessage = "Opened task detail."
			if m.workspace.selectedTaskID != "" {
				// Sync sidebar cursor, project cursor, and project context for
				// consistency with other task-entry paths (enables j/k, p, Esc·back).
				taskNodeID := "task-" + m.workspace.selectedTaskID
				if taskNode := m.workspace.nodes[taskNodeID]; taskNode != nil && taskNode.ParentID != "" {
					if projNode := m.workspace.nodes[taskNode.ParentID]; projNode != nil && projNode.Kind == sidebarKindProject {
						m.workspace.selectedProjectID = projNode.ProjectID
					}
				}
				m.workspace.syncSidebarToTask(m.workspace.selectedTaskID)
				m.workspace.syncProjectCursorToTask(m.workspace.selectedTaskID)
				cmds := []tea.Cmd{loadTaskDetailCmd(m.workspace.selectedTaskID, m.runtimeHints)}
				if m.workspace.selectedProjectID != "" && m.workspace.selectedProject == nil && m.runtimeHints.LoadProjectDetail != nil {
					cmds = append(cmds, loadProjectDetailCmd(m.workspace.selectedProjectID, m.runtimeHints))
				}
				return tea.Batch(cmds...)
			}
			return nil
		}
	}
	return nil
}

func (m *Model) handleEscapeKey() {
	if m.focus == MainPanel {
		// From task detail: go back to project view if we came from one, else dashboard
		if m.workspace.mainView == ViewTask && m.workspace.selectedProjectID != "" {
			m.workspace.setMainView(ViewProject)
			m.statusMessage = "Back to project."
			return
		}
		m.workspace.setMainView(ViewDashboard)
		m.statusMessage = "Returned to dashboard."
	}
}

// stepTaskInProject moves projectTaskCursor by delta (±1) and opens the task
// at the new position. A no-op (returns nil) when there is no project context
// or the project has fewer than 2 tasks.
func (m *Model) stepTaskInProject(delta int) tea.Cmd {
	openTasks := m.workspace.openTasksForProject()
	if len(openTasks) < 2 {
		return nil
	}
	cursor := m.workspace.projectTaskCursor + delta
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(openTasks) {
		cursor = len(openTasks) - 1
	}
	if cursor == m.workspace.projectTaskCursor {
		return nil // already at boundary
	}
	m.workspace.projectTaskCursor = cursor
	taskID := openTasks[cursor].ID
	m.workspace.selectedTaskID = taskID
	m.workspace.syncSidebarToTask(taskID)
	return loadTaskDetailCmd(taskID, m.runtimeHints)
}

func (m *Model) handleWorkspaceRune(r rune) (bool, tea.Cmd) {
	switch r {
	case 'j':
		if m.focus == SidebarPanel {
			m.workspace.moveSidebar(1)
		} else if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			m.workspace.moveInbox(1)
		} else if m.focus == MainPanel && m.workspace.mainView == ViewProject {
			m.workspace.moveProjectTaskCursor(1)
		} else if m.focus == MainPanel && m.workspace.mainView == ViewDashboard {
			m.workspace.moveDashboardCursor(1)
			if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil {
				label := task.Title
				if task.TaskNumber > 0 {
					label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, label)
				}
				m.statusMessage = "▸ " + truncate(label, 40)
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewTask {
			return true, m.stepTaskInProject(1)
		}
		return true, nil
	case 'k':
		if m.focus == SidebarPanel {
			m.workspace.moveSidebar(-1)
		} else if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			m.workspace.moveInbox(-1)
		} else if m.focus == MainPanel && m.workspace.mainView == ViewProject {
			m.workspace.moveProjectTaskCursor(-1)
		} else if m.focus == MainPanel && m.workspace.mainView == ViewDashboard {
			m.workspace.moveDashboardCursor(-1)
			if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil {
				label := task.Title
				if task.TaskNumber > 0 {
					label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, label)
				}
				m.statusMessage = "▸ " + truncate(label, 40)
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewTask {
			return true, m.stepTaskInProject(-1)
		}
		return true, nil
	case 'h':
		if m.focus == SidebarPanel {
			m.workspace.collapseSidebarNode()
			return true, nil
		}
	case 'l':
		if m.focus == SidebarPanel {
			node := m.workspace.currentSidebarNode()
			m.workspace.expandSidebarNode()
			if node != nil && node.Kind == sidebarKindProject {
				return true, loadProjectTasksCmd(node.ProjectID, m.runtimeHints, true)
			}
			return true, nil
		}
	case 's':
		if m.focus == MainPanel || m.focus == SidebarPanel {
			m.toggleSidebar()
			return true, nil
		}
	case 'g':
		if m.focus == SidebarPanel {
			m.workspace.sidebarHome()
		} else if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			m.workspace.inboxHome()
		} else if m.focus == MainPanel && m.workspace.mainView == ViewProject {
			m.workspace.projectTaskCursor = 0
		}
		return true, nil
	case 'G':
		if m.focus == SidebarPanel {
			m.workspace.sidebarEnd()
		} else if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			m.workspace.inboxEnd()
		} else if m.focus == MainPanel && m.workspace.mainView == ViewProject {
			if proj := m.workspace.selectedProject; proj != nil {
				m.workspace.projectTaskCursor = maxInt(0, len(proj.Tasks)-1)
			}
		}
		return true, nil
	case 'q':
		// EX-013: 'q' closes the help screen from any non-chat panel
		if m.workspace.mainView == ViewHelp {
			m.workspace.setMainView(ViewDashboard)
			m.statusMessage = "Returned to dashboard."
			return true, nil
		}
	case 'i':
		if m.focus != ChatPanel {
			m.workspace.setMainView(ViewInbox)
			m.setFocus(MainPanel)
			m.statusMessage = "Inbox"
			return true, loadInboxItemsCmd(m.runtimeHints)
		}
	case 'd':
		if m.focus == MainPanel && m.workspace.mainView == ViewProject {
			// Toggle done tasks visibility in project view
			m.workspace.showDoneTasks = !m.workspace.showDoneTasks
			if m.workspace.showDoneTasks {
				m.statusMessage = "Showing done tasks"
			} else {
				m.statusMessage = "Done tasks hidden"
			}
			return true, nil
		} else if m.focus != ChatPanel {
			m.workspace.setMainView(ViewDashboard)
			m.setFocus(MainPanel)
			m.statusMessage = "Dashboard"
			return true, nil
		}
	case 'p':
		if m.focus != ChatPanel && m.workspace.selectedProjectID != "" {
			m.workspace.setMainView(ViewProject)
			m.setFocus(MainPanel)
			m.statusMessage = "Project view"
			return true, nil
		}
	case 't':
		if m.focus != ChatPanel && m.workspace.selectedTaskID != "" {
			m.workspace.setMainView(ViewTask)
			m.setFocus(MainPanel)
			m.statusMessage = "Task detail"
			return true, nil
		}
	case 'n':
		if m.focus != ChatPanel {
			if nextID := m.workspace.nextUnreadSession(); nextID != "" {
				// Move cursor to the unread session and activate it
				visible := m.workspace.visibleSidebarIDs()
				for i, id := range visible {
					if id == nextID {
						m.workspace.sidebarCursor = i
						break
					}
				}
				m.workspace.selectSidebarNode()
				m.activeSession = m.workspace.activeSessionID
				m.chatScrollOffset = 0
				m.statusMessage = "Jumped to next unread session."
				m.setFocus(SidebarPanel)
				return true, nil
			}
			m.statusMessage = "No unread sessions."
			return true, nil
		}
	case 'r':
		m.workspace.activity = append(m.workspace.activity,
			"sidebar refreshed at "+m.now().Format("15:04:05"))
		m.statusMessage = "Refreshing sidebar data…"
		return true, loadSidebarDataCmd(m.runtimeHints)
	case 'a':
		if m.focus == MainPanel && m.workspace.mainView == ViewInbox && m.workspace.applyInboxAction("approve") {
			m.statusMessage = "Inbox item approved."
			return true, nil
		}
	case 'x':
		if m.focus == MainPanel && m.workspace.mainView == ViewInbox && m.workspace.applyInboxAction("reject") {
			m.statusMessage = "Inbox item rejected."
			return true, nil
		}
	case 'f':
		if m.focus == MainPanel && m.workspace.mainView == ViewInbox && m.workspace.applyInboxAction("defer") {
			m.statusMessage = "Inbox item deferred."
			return true, nil
		}
	case 'o':
		if m.focus == MainPanel && m.workspace.mainView == ViewInbox && m.workspace.applyInboxAction("open") {
			m.state.LastActiveChatSession = m.workspace.activeSessionID
			m.activeSession = m.workspace.activeSessionID
			m.statusMessage = "Opened inbox item in context."
			return true, nil
		}
	}
	return false, nil
}

func (m *Model) handleSidebarControlKey(key tea.KeyMsg) (bool, tea.Cmd) {
	switch key.Type {
	case tea.KeyUp:
		m.workspace.moveSidebar(-1)
		return true, nil
	case tea.KeyDown:
		m.workspace.moveSidebar(1)
		return true, nil
	case tea.KeyLeft:
		m.workspace.collapseSidebarNode()
		return true, nil
	case tea.KeyRight:
		node := m.workspace.currentSidebarNode()
		if node != nil && node.Kind == sidebarKindProject {
			m.workspace.expandSidebarNode()
			return true, loadProjectTasksCmd(node.ProjectID, m.runtimeHints, true)
		}
		m.workspace.expandSidebarNode()
		return true, nil
	}
	return false, nil
}

func (m *Model) handleChatControlKey(key tea.KeyMsg) (bool, tea.Cmd) {
	switch key.Type {
	case tea.KeyEnter:
		if key.Alt {
			m.chatInput += "\n"
			m.statusMessage = "Inserted newline."
			return true, nil
		}
		if strings.TrimSpace(m.chatInput) == "" && m.toggleLatestToolCallExpansion() {
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
		// EX-011: advance through history when in history navigation mode
		if m.chatHistoryIndex >= 0 && m.chatHistoryIndex < len(m.chatHistory) {
			m.forwardHistory()
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

func (m *Model) enterSearchMode(panel Panel) {
	m.searchMode = true
	m.searchPanel = panel
	m.searchQuery = m.filterForPanel(panel)
	m.statusMessage = "Search active. Type to filter; Enter keep, Esc clear."
}

func (m *Model) setFilterForPanel(panel Panel, query string) {
	switch panel {
	case SidebarPanel:
		m.sidebarFilter = query
	case MainPanel:
		m.mainFilter = query
	}
}

func (m Model) filterForPanel(panel Panel) string {
	switch panel {
	case SidebarPanel:
		return m.sidebarFilter
	case MainPanel:
		return m.mainFilter
	default:
		return ""
	}
}

func (m Model) updateSearchInput(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		m.statusMessage = "Exiting TUI."
		return m, tea.Quit
	case tea.KeyEsc:
		m.setFilterForPanel(m.searchPanel, "")
		m.searchMode = false
		m.searchQuery = ""
		m.statusMessage = "Search cleared."
		return m, nil
	case tea.KeyEnter:
		m.setFilterForPanel(m.searchPanel, m.searchQuery)
		m.searchMode = false
		m.statusMessage = "Search applied."
		return m, nil
	case tea.KeyBackspace:
		runes := []rune(m.searchQuery)
		if len(runes) > 0 {
			m.searchQuery = string(runes[:len(runes)-1])
		}
		m.setFilterForPanel(m.searchPanel, m.searchQuery)
		return m, nil
	case tea.KeySpace:
		m.searchQuery += " "
		m.setFilterForPanel(m.searchPanel, m.searchQuery)
		return m, nil
	case tea.KeyRunes:
		if len(key.Runes) > 0 {
			m.searchQuery += string(key.Runes)
			m.setFilterForPanel(m.searchPanel, m.searchQuery)
		}
		return m, nil
	default:
		return m, nil
	}
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
func (m Model) WorkspaceSession() string { return m.activeSession }
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
	next.LastMainView = normalizeMainViewState(string(m.workspace.mainView))
	next.LastSelectedProjectID = m.workspace.selectedProjectID
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

// forwardHistory advances the chat history index toward the most recent entry.
// EX-011: called when Down arrow is pressed while in history navigation mode.
func (m *Model) forwardHistory() {
	if m.chatHistoryIndex < 0 || m.chatHistoryIndex >= len(m.chatHistory) {
		return
	}
	m.chatHistoryIndex++
	if m.chatHistoryIndex >= len(m.chatHistory) {
		m.chatInput = ""
		m.chatHistoryIndex = len(m.chatHistory)
		m.statusMessage = "Cleared chat input."
	} else {
		m.chatInput = m.chatHistory[m.chatHistoryIndex]
		m.statusMessage = "Recalled next message."
	}
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
		// Skip streaming delta events during replay — we only show finalized snapshots
		if !m.turnsSynced {
			return
		}
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
			MessageID  string `json:"message_id"`
			SessionID  string `json:"session_id"`
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
		}
		if !decodePayload(event.Payload, &payload) {
			return
		}
		if strings.EqualFold(strings.TrimSpace(payload.Role), "tool_result") {
			m.attachToolResult(strings.TrimSpace(payload.ToolCallID), payload.Content)
			return
		}
		// During SSE replay, skip finalized events entirely. History is loaded
		// from the REST API via LoadChatHistory (triggered by ReplaySyncedMsg).
		// SSE replay covers all org-level events and would otherwise inject
		// messages from archived/other sessions into the active chat panel.
		if !m.turnsSynced {
			return
		}
		// In live mode: skip user messages entirely — they are already shown
		// optimistically via appendMessage("local-user") when the user sends.
		// Processing them here would (a) create a duplicate entry and (b) call
		// completeTurnAndPromoteQueue prematurely, resetting activeTurn=false
		// before the agent turn even begins — which drops streaming chunks.
		if strings.EqualFold(strings.TrimSpace(payload.Role), "user") {
			return
		}
		if !m.sessionMatchesActive(payload.SessionID) {
			// Non-active assistant message → mark the sidebar session as unread
			if strings.EqualFold(strings.TrimSpace(payload.Role), "assistant") {
				m.workspace.markSessionUnread(payload.SessionID)
			}
			return
		}
		// Always set content — do NOT gate on activeTurn. chat.turn.completed
		// can arrive before chat.message.finalized and clears activeTurn first,
		// which would otherwise leave the message with empty content.
		index := m.ensureMessage(payload.MessageID, payload.Role, event.OccurredAt)
		if strings.TrimSpace(payload.Content) != "" {
			m.chatMessages[index].Content = payload.Content
		}
		m.chatMessages[index].Finalized = true
		m.chatScrollOffset = 0 // snap to bottom so the completed message is visible
		// Only promote the queue if the turn is still marked active; if
		// chat.turn.completed already fired and cleared activeTurn, the queue
		// was already promoted there — calling again would double-promote.
		if m.activeTurn {
			m.completeTurnAndPromoteQueue("Promoted queued message after finalize.")
		}
	case "chat.turn.started":
		// Ignore historical turn lifecycle events during replay — they would
		// set activeTurn=true and cause all replayed delta events to stream in.
		if !m.turnsSynced {
			return
		}
		var payload struct {
			SessionID string `json:"session_id"`
		}
		if decodePayload(event.Payload, &payload) && !m.sessionMatchesActive(payload.SessionID) {
			return
		}
		m.activeTurn = true
		m.chatScrollOffset = 0 // snap to bottom when turn begins
	case "chat.turn.completed":
		if !m.turnsSynced {
			return
		}
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
		if !m.turnsSynced {
			return
		}
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
			MessageID  string `json:"message_id"`
			ToolCallID string `json:"tool_call_id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
		}
		if !decodePayload(event.Payload, &payload) {
			return
		}
		if !m.activeTurn {
			return
		}
		index := m.ensureMessage(payload.MessageID, "assistant", event.OccurredAt)
		m.upsertToolCall(index, strings.TrimSpace(payload.ToolCallID), strings.TrimSpace(payload.Name), strings.TrimSpace(payload.Status))
	}
}

func (m *Model) completeTurnAndPromoteQueue(promoteStatus string) {
	m.activeTurn = false
	m.activeTurnSessionID = ""
	m.chatScrollOffset = 0 // snap to bottom so the completed response is visible
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

func (m *Model) upsertToolCall(index int, toolCallID, name, status string) {
	if index < 0 || index >= len(m.chatMessages) {
		return
	}
	for i := range m.chatMessages[index].ToolCalls {
		call := m.chatMessages[index].ToolCalls[i]
		if (toolCallID != "" && call.ID == toolCallID) || (toolCallID == "" && name != "" && call.Name == name) {
			if toolCallID != "" {
				m.chatMessages[index].ToolCalls[i].ID = toolCallID
				m.toolCallMessageIndex[toolCallID] = index
			}
			if name != "" {
				m.chatMessages[index].ToolCalls[i].Name = name
			}
			m.chatMessages[index].ToolCalls[i].Status = status
			return
		}
	}
	if name == "" {
		name = "tool"
	}
	call := ToolCallStatus{
		ID:     toolCallID,
		Name:   name,
		Status: status,
	}
	m.chatMessages[index].ToolCalls = append(m.chatMessages[index].ToolCalls, call)
	if toolCallID != "" {
		m.toolCallMessageIndex[toolCallID] = index
	}
}

func (m *Model) attachToolResult(toolCallID, content string) {
	result := strings.TrimSpace(content)
	if result == "" {
		return
	}
	if toolCallID != "" {
		if messageIndex, ok := m.toolCallMessageIndex[toolCallID]; ok && messageIndex >= 0 && messageIndex < len(m.chatMessages) {
			for i := range m.chatMessages[messageIndex].ToolCalls {
				if m.chatMessages[messageIndex].ToolCalls[i].ID == toolCallID {
					m.chatMessages[messageIndex].ToolCalls[i].Result = result
					return
				}
			}
		}
		for messageIndex := len(m.chatMessages) - 1; messageIndex >= 0; messageIndex-- {
			for i := range m.chatMessages[messageIndex].ToolCalls {
				if m.chatMessages[messageIndex].ToolCalls[i].ID == toolCallID {
					m.chatMessages[messageIndex].ToolCalls[i].Result = result
					m.toolCallMessageIndex[toolCallID] = messageIndex
					return
				}
			}
		}
	}
	for messageIndex := len(m.chatMessages) - 1; messageIndex >= 0; messageIndex-- {
		calls := m.chatMessages[messageIndex].ToolCalls
		for i := len(calls) - 1; i >= 0; i-- {
			if strings.TrimSpace(m.chatMessages[messageIndex].ToolCalls[i].Result) != "" {
				continue
			}
			m.chatMessages[messageIndex].ToolCalls[i].Result = result
			return
		}
	}
}

func (m *Model) isToolCallExpanded(messageID, callID string) bool {
	if strings.TrimSpace(messageID) == "" || strings.TrimSpace(callID) == "" {
		return false
	}
	calls := m.toolCallExpanded[messageID]
	if calls == nil {
		return false
	}
	return calls[callID]
}

func (m *Model) setToolCallExpanded(messageID, callID string, expanded bool) {
	if strings.TrimSpace(messageID) == "" || strings.TrimSpace(callID) == "" {
		return
	}
	if m.toolCallExpanded[messageID] == nil {
		m.toolCallExpanded[messageID] = map[string]bool{}
	}
	m.toolCallExpanded[messageID][callID] = expanded
}

func (m *Model) toggleLatestToolCallExpansion() bool {
	for msgIndex := len(m.chatMessages) - 1; msgIndex >= 0; msgIndex-- {
		message := &m.chatMessages[msgIndex]
		for callIndex := len(message.ToolCalls) - 1; callIndex >= 0; callIndex-- {
			call := message.ToolCalls[callIndex]
			callID := toolCallIdentity(call, callIndex)
			if callID == "" {
				continue
			}
			nextExpanded := !m.isToolCallExpanded(message.ID, callID)
			m.setToolCallExpanded(message.ID, callID, nextExpanded)
			if nextExpanded {
				m.statusMessage = "Tool call expanded."
			} else {
				m.statusMessage = "Tool call collapsed."
			}
			return true
		}
	}
	return false
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

func (m *Model) switchScope(next ChatScope) tea.Cmd {
	m.activeScope = next
	var sessionID string
	switch next {
	case ScopeOrg:
		sessionID = m.workspace.activeSessionID
		if sessionID == "" {
			sessionID = sessionForScope(next)
		}
	case ScopeTask:
		sessionID = m.workspace.selectedTaskSessionID()
		if sessionID == "" {
			sessionID = sessionForScope(next)
		}
	default:
		sessionID = sessionForScope(next)
	}
	m.activeSession = sessionID
	m.chatScrollOffset = 0
	m.statusMessage = fmt.Sprintf("Scope switched to %s.", next)
	if looksLikeUUID(sessionID) && m.runtimeHints.LoadChatHistory != nil {
		return loadChatHistoryCmd(sessionID, m.runtimeHints.LoadChatHistory)
	}
	return nil
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

func (m *Model) toggleSidebar() {
	if m.sizeClass != SizeS {
		m.statusMessage = "Sidebar toggle is available below 100 columns."
		return
	}
	if m.sidebarVisible || m.focus == SidebarPanel {
		m.sidebarVisible = false
		m.focus = MainPanel
		m.applyResponsiveLayout()
		m.statusMessage = "Sidebar hidden."
		return
	}
	m.sidebarVisible = true
	m.focus = SidebarPanel
	m.applyResponsiveLayout()
	m.statusMessage = "Sidebar shown."
}

func (m *Model) resizeFocusedPanel(delta float64) bool {
	index := -1
	label := ""
	switch m.focus {
	case SidebarPanel:
		index = 0
		label = "Sidebar"
	case ChatPanel:
		index = 2
		label = "Chat"
	default:
		return false
	}

	proportions := m.state.PanelProportions
	if !validPanelProportions(proportions) {
		proportions = DefaultState().PanelProportions
	}

	otherIndex := 2
	if index == 2 {
		otherIndex = 0
	}
	other := proportions[otherIndex]

	minTarget := maxFloat(minPanelProportion, 1-other-maxPanelProportion)
	maxTarget := minFloat(maxPanelProportion, 1-other-minPanelProportion)
	if minTarget > maxTarget {
		return false
	}

	target := proportions[index] + delta
	if target < minTarget {
		target = minTarget
	}
	if target > maxTarget {
		target = maxTarget
	}
	target = clampProportion(target)
	main := clampProportion(1 - other - target)

	updated := proportions
	updated[index] = target
	updated[1] = main
	updated[otherIndex] = other
	m.state.PanelProportions = updated
	m.applyResponsiveLayout()
	m.statusMessage = fmt.Sprintf("%s width %.0f%%", label, target*100)
	return true
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

// assistantLabel returns the display name for assistant-role messages.
// In task-scoped sessions it resolves the assigned agent's name from the
// task record so multi-agent deployments show the correct agent (e.g.
// "Ellie" instead of always "Frank"). Falls back to "Frank".
func (m Model) assistantLabel() string {
	if m.activeScope == ScopeTask && m.workspace.selectedTaskID != "" {
		if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil && task.AgentName != "" {
			return task.AgentName
		}
	}
	return "Frank"
}

func (m Model) commandFallbackHelp() string {
	if m.commandMode {
		return ":frank · :dashboard · :project · :task · :inbox · :focus sidebar|main|chat · :send · :cancel-turn · :quit  ·  Esc cancel"
	}
	switch m.focus {
	case SidebarPanel:
		return "j/k navigate · Space expand · Enter select · n next unread · i inbox · d dashboard · r refresh · / search · ? help"
	case MainPanel:
		switch m.workspace.mainView {
		case ViewInbox:
			return "a approve · x reject · f defer · o open · j/k navigate · n next unread · s toggle sidebar · Esc back · : commands"
		case ViewTask:
			if m.workspace.selectedProjectID != "" {
				taskHelp := "Enter open session · Esc project"
				if len(m.workspace.openTasksForProject()) >= 2 {
					taskHelp += " · j/k next/prev"
				}
				taskHelp += " · p project view · n next unread · : commands · ? help"
				return taskHelp
			}
			return "Enter open session · Esc dashboard · n next unread · : commands · ? help"
		case ViewProject:
			return "j/k navigate tasks · Enter open task · d toggle done · Esc dashboard · n next unread · : commands · ? help"
		case ViewDashboard:
			if len(m.workspace.dashboardActiveTasks()) > 0 {
				return "j/k select task · Enter open · i inbox · n next unread · / filter · : commands · ? help"
			}
			return "i inbox · n next unread · r refresh · : commands · ? help"
		default:
			return "i inbox · d dashboard · n next unread · r refresh · / filter · : commands · ? help"
		}
	case ChatPanel:
		// EX-091: when input is empty and the latest assistant message has tool
		// calls, hint that Enter will expand/collapse them rather than send.
		if strings.TrimSpace(m.chatInput) == "" {
			for i := len(m.chatMessages) - 1; i >= 0; i-- {
				if len(m.chatMessages[i].ToolCalls) > 0 {
					return "Enter expand/collapse tool · PgUp/PgDn scroll · g/G jump · [/] scope · : commands · ? help"
				}
				break
			}
		}
		return "Enter send · Alt-Enter newline · PgUp/PgDn scroll · [/] scope · Esc cancel turn · : commands · ? help"
	}
	if m.runtimeHints.ModifierReliabilityUncertain {
		return "tmux-safe: :focus sidebar|main|chat | :frank | :dashboard/:project/:task/:inbox | :send | :cancel-turn | :quit"
	}
	return ":focus sidebar|main|chat | :frank | :dashboard/:project/:task/:inbox | :send | :cancel-turn | PgUp/PgDn scroll | :quit"
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
	m.workspace.activity = append(m.workspace.activity, "event replay complete")
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

func loadChatHistoryCmd(sessionID string, loadFn func(ctx context.Context, sessionID string) ([]ChatMessage, error)) tea.Cmd {
	return func() tea.Msg {
		if loadFn == nil {
			return chatHistoryLoadedMsg{}
		}
		messages, err := loadFn(context.Background(), strings.TrimSpace(sessionID))
		if err != nil || len(messages) == 0 {
			return chatHistoryLoadedMsg{}
		}
		return chatHistoryLoadedMsg{Messages: messages}
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

// looksLikeUUID returns true when s resembles a UUID (36 chars with dashes at
// positions 8, 13, 18, 23). Placeholder IDs like "session-org-general" return false.
func looksLikeUUID(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 36 {
		return false
	}
	return s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-'
}

func loadSidebarDataCmd(hints RuntimeHints) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		var inboxCount int
		var orgSessionID string
		var chats []SidebarChatItem
		var projects []SidebarProjectItem

		if hints.LoadOrgSession != nil {
			id, _ := hints.LoadOrgSession(ctx)
			orgSessionID = strings.TrimSpace(id)
		}
		if hints.LoadInboxCount != nil {
			n, _ := hints.LoadInboxCount(ctx)
			inboxCount = n
		}
		if hints.LoadRecentChats != nil {
			c, _ := hints.LoadRecentChats(ctx)
			chats = c
		}
		if hints.LoadProjects != nil {
			p, _ := hints.LoadProjects(ctx)
			projects = p
		}

		return sidebarDataLoadedMsg{
			InboxCount:   inboxCount,
			OrgSessionID: orgSessionID,
			Chats:        chats,
			Projects:     projects,
		}
	}
}

func loadProjectTasksCmd(projectID string, hints RuntimeHints, expand bool) tea.Cmd {
	if strings.TrimSpace(projectID) == "" || hints.LoadProjectTasks == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tasks, _ := hints.LoadProjectTasks(ctx, projectID)
		return projectTasksLoadedMsg{ProjectID: projectID, Tasks: tasks, ExpandNode: expand}
	}
}

func loadProjectDetailCmd(projectID string, hints RuntimeHints) tea.Cmd {
	if strings.TrimSpace(projectID) == "" || hints.LoadProjectDetail == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		detail, err := hints.LoadProjectDetail(ctx, projectID)
		if err != nil || detail == nil {
			return projectDetailLoadedMsg{Detail: ProjectDetail{ID: projectID}}
		}
		return projectDetailLoadedMsg{Detail: *detail}
	}
}

func loadAgentsCmd(hints RuntimeHints) tea.Cmd {
	if hints.LoadAgents == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		agents, err := hints.LoadAgents(ctx)
		if err != nil {
			return agentsLoadedMsg{}
		}
		return agentsLoadedMsg{Agents: agents}
	}
}

func loadTaskDetailCmd(taskID string, hints RuntimeHints) tea.Cmd {
	if strings.TrimSpace(taskID) == "" || hints.LoadTaskDetail == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		detail, err := hints.LoadTaskDetail(ctx, taskID)
		if err != nil || detail == nil {
			return taskDetailLoadedMsg{Detail: TaskDetailItem{ID: taskID}}
		}
		return taskDetailLoadedMsg{Detail: *detail}
	}
}

func loadInboxItemsCmd(hints RuntimeHints) tea.Cmd {
	if hints.LoadInboxItems == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		items, err := hints.LoadInboxItems(ctx)
		if err != nil {
			return inboxItemsLoadedMsg{}
		}
		return inboxItemsLoadedMsg{Items: items}
	}
}

// applyWorkspaceCommand handles model-level workspace SSE events that need to
// fire tea.Cmds (e.g. tui.command navigation requests). Returns nil if the
// event is not a model-level command and should fall through to the workspace.
func (m *Model) applyWorkspaceCommand(event EventEnvelope) tea.Cmd {
	if event.EventType == "inbox.item_created" {
		m.workspace.inboxCount++
		return nil
	}
	if event.EventType == "task.status_changed" || event.EventType == "task.completed" {
		var payload struct {
			TaskID    string `json:"task_id"`
			ToStatus  string `json:"to_status"`
			ProjectID string `json:"project_id"`
		}
		if !decodePayload(event.Payload, &payload) || payload.TaskID == "" {
			return nil
		}
		// Update the task record if loaded
		if rec := m.workspace.tasks[payload.TaskID]; rec != nil {
			rec.Status = payload.ToStatus
		}
		// Update the sidebar task node WorkStatus so its icon refreshes
		taskNodeID := "task-" + payload.TaskID
		if node := m.workspace.nodes[taskNodeID]; node != nil {
			node.WorkStatus = payload.ToStatus
		}
		// If the project detail is loaded, update task status and DoneCount
		if m.workspace.selectedProject != nil {
			isNowDone := payload.ToStatus == "done" || payload.ToStatus == "approved" || payload.ToStatus == "cancelled"
			for i := range m.workspace.selectedProject.Tasks {
				t := &m.workspace.selectedProject.Tasks[i]
				if t.ID != payload.TaskID {
					continue
				}
				wasDone := t.WorkStatus == "done" || t.WorkStatus == "approved" || t.WorkStatus == "cancelled"
				t.WorkStatus = payload.ToStatus
				// Adjust done count when a task crosses the done boundary
				if !wasDone && isNowDone {
					m.workspace.selectedProject.DoneCount++
				} else if wasDone && !isNowDone {
					if m.workspace.selectedProject.DoneCount > 0 {
						m.workspace.selectedProject.DoneCount--
					}
				}
				break
			}
		}
		// EX-092: surface task status transitions in the dashboard activity log
		// so the Activity section reflects real-time progress, not just startup.
		if payload.ToStatus != "" {
			label := payload.TaskID
			if rec := m.workspace.tasks[payload.TaskID]; rec != nil {
				if rec.TaskNumber > 0 {
					label = fmt.Sprintf("OC-%d", rec.TaskNumber)
				} else if rec.Title != "" {
					label = truncate(rec.Title, 24)
				}
			}
			statusLabel := formatTaskStatus(payload.ToStatus)
			m.workspace.activity = append(m.workspace.activity,
				label+": "+statusLabel)
		}
		return nil
	}
	if event.EventType == "worker.unresponsive" {
		if !m.turnsSynced {
			return nil
		}
		var payload struct {
			SessionID string `json:"session_id"`
			Message   string `json:"message"`
		}
		if decodePayload(event.Payload, &payload) && m.sessionMatchesActive(payload.SessionID) {
			m.statusMessage = payload.Message
		}
		return nil
	}
	if event.EventType != "tui.command" {
		return nil
	}
	var payload struct {
		Action   string `json:"action"`
		Target   string `json:"target"`
		TargetID string `json:"target_id"`
	}
	if !decodePayload(event.Payload, &payload) || payload.Action != "navigate" {
		return nil
	}
	switch payload.Target {
	case "project":
		// Navigate to project view regardless of whether the sidebar node is loaded yet.
		// The node search is best-effort for cursor positioning; the view/detail load works
		// without it.
		m.workspace.mainView = ViewProject
		m.workspace.selectedProjectID = payload.TargetID
		m.workspace.selectedProject = nil
		m.activeScope = ScopeProject
		m.statusMessage = "Navigated to project."
		for _, node := range m.workspace.nodes {
			if node.Kind == sidebarKindProject && node.ProjectID == payload.TargetID {
				m.workspace.sidebarCursor = m.workspace.indexOfNode(node.ID)
				m.statusMessage = "Navigated to " + node.Label + "."
				break
			}
		}
		cmds := []tea.Cmd{loadProjectTasksCmd(payload.TargetID, m.runtimeHints, true)}
		if m.runtimeHints.LoadProjectDetail != nil {
			cmds = append(cmds, loadProjectDetailCmd(payload.TargetID, m.runtimeHints))
		}
		return tea.Batch(cmds...)
	case "inbox":
		m.workspace.mainView = ViewInbox
		m.statusMessage = "Navigated to inbox."
	case "dashboard":
		m.workspace.mainView = ViewDashboard
		m.statusMessage = "Navigated to dashboard."
	}
	return nil
}
