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

// statusClearMsg is sent after a short timer to auto-clear transient status messages.
// The Generation field prevents clearing a newer message when an old timer fires.
type statusClearMsg struct{ Generation int }
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

// inboxActionCompletedMsg is dispatched when the async API call for an inbox
// action (approve/reject/defer) completes. EX-160.
type inboxActionCompletedMsg struct {
	ItemID string
	Action string
	Err    error
}

// SessionResolvedMsg carries the resolved UUID of the session when a message is sent.
// Used to filter SSE events by session, preventing cross-session leakage.
// SessionID is included so stale history loads (from a session the user has
// since navigated away from) are discarded rather than merged into the new
// session's view (EX-173).
type chatHistoryLoadedMsg struct {
	SessionID string
	Messages  []ChatMessage
	Err       error // EX-198: non-nil when the API call failed
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
	sidebarFilter     string
	mainFilter        string
	helpScrollOffset  int // EX-209: scroll position within the help view
	statusMessage     string
	statusGeneration  int // incremented each time statusMessage is set; used to avoid stale auto-clears
	runtimeHints      RuntimeHints
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
			m.workspace.activity = appendActivity(m.workspace.activity, "realtime events connected")
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
			m.workspace.activity = appendActivity(m.workspace.activity, "event replay complete")
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
		// EX-173: discard history loads that no longer match the active session.
		// Users may switch sessions rapidly (e.g. 'n' multiple times); without
		// this guard the stale load would corrupt the current session's message list.
		if sid := strings.TrimSpace(typed.SessionID); sid != "" && sid != strings.TrimSpace(m.activeSession) {
			return m, nil
		}
		// EX-198: surface load errors so the user knows history is unavailable
		// rather than silently showing an empty chat panel.
		if typed.Err != nil {
			m.statusMessage = "History load failed — " + strings.TrimSpace(typed.Err.Error())
			return m, nil
		}
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
			m.workspace.activity = appendActivity(m.workspace.activity, "sidebar loaded: "+strings.Join(activityParts, ", "))
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
		// EX-152: sync RequiresHumanReview from loaded inbox items to newly-seeded
		// task records. If inbox was loaded before this project's tasks existed in
		// w.tasks, syncTaskHumanReviewFromInbox silently skipped them. Re-applying
		// here ensures the ⚠ badge is correct after project tasks are seeded.
		m.workspace.syncTaskHumanReviewFromInbox()
		return m, nil
	case projectDetailLoadedMsg:
		// EX-174: discard stale project detail loads. If the user navigated to a
		// different project while the previous project's detail was in-flight, the
		// stale result would briefly overwrite the current project's detail.
		if typed.Detail.ID != "" && typed.Detail.ID != m.workspace.selectedProjectID {
			return m, nil
		}
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
		// EX-122: sync RequiresHumanReview from the loaded inbox items so the
		// task detail view shows the ⚠ badge immediately when a new review item
		// arrives — without waiting for a full task detail reload.
		m.workspace.syncTaskHumanReviewFromInbox()
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
		// EX-163: applyChatEnvelope now returns a cmd so queue-promotion sends can propagate.
		if cmd := m.applyChatEnvelope(typed.Envelope); cmd != nil {
			m.recordStreamRenderLatency(typed.Envelope)
			m.markReplaySynced()
			return m, cmd
		}
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
		// EX-189: only apply the resolved session ID while a turn is still active.
		// If the user switched sessions after sending (EX-188 set activeTurnSessionID
		// to the new session's UUID), a stale SessionResolvedMsg from the previous
		// session's in-flight send must not overwrite the new session's filter.
		if m.activeTurn {
			m.activeTurnSessionID = strings.TrimSpace(typed.SessionID)
		}
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
	case inboxActionCompletedMsg:
		// EX-160: server-side error for approve/reject/defer surfaces in the status bar.
		// On success there is nothing to do — local state is already updated optimistically.
		if typed.Err != nil {
			m.statusMessage = fmt.Sprintf("Inbox %s failed: %s", typed.Action, strings.TrimSpace(typed.Err.Error()))
			// EX-183: reload inbox items to restore consistent state. The optimistic
			// local update already removed the item; reloading syncs us back with
			// the server so a failed approval doesn't silently hide an unreviewed item.
			return m, loadInboxItemsCmd(m.runtimeHints)
		}
		return m, nil
	case statusClearMsg:
		// EX-105: only clear if the generation matches (no newer status was set).
		if typed.Generation == m.statusGeneration && m.statusMessage != "" {
			m.statusMessage = ""
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
		// EX-105: auto-clear transient status messages 5 s after the last key press.
		// Generation increments each time so only the most recent timer wins.
		if typedModel.statusMessage != "" {
			typedModel.statusGeneration++
			cmd = tea.Batch(cmd, statusAutoClearCmd(typedModel.statusGeneration))
		}
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
		// EX-165: jumpToFrankSession now returns a cmd to reload chat history.
		return m, m.jumpToFrankSession("Ctrl-G")
	case tea.KeyCtrlP:
		m.enterCommandMode()
		return m, nil
	case tea.KeyCtrlU:
		// EX-300: Ctrl-U (Unix kill-line) clears the chat input when focused on the chat panel.
		// Complements EX-289 (Esc clears input) with a more keyboard-conventional shortcut.
		if m.focus == ChatPanel {
			if strings.TrimSpace(m.chatInput) != "" {
				m.chatInput = ""
				m.chatHistoryIndex = -1
				// EX-424: if the user had pressed 'e' to edit a dequeued message, also
				// clear editingQueued so the next sent message is not incorrectly marked
				// Edited=true. Give a distinct status so the user knows the in-progress
				// edit (and the already-dequeued message) was discarded.
				if m.editingQueued {
					m.editingQueued = false
					m.statusMessage = "Edit cancelled — queued message discarded."
				} else {
					m.statusMessage = "Input cleared."
				}
			} else {
				// EX-325: give honest feedback when there is nothing to clear.
				m.statusMessage = "Nothing to clear."
			}
		} else {
			// EX-344: Ctrl-U outside chat panel — redirect hint.
			m.statusMessage = "Ctrl-U clears chat input. Press 3 or Tab to focus chat."
		}
		return m, nil
	case tea.KeyCtrlW:
		// EX-303: Ctrl-W (Unix kill-word-backward) deletes the last word from the chat input.
		if m.focus == ChatPanel {
			if len(m.chatInput) > 0 {
				trimmed := strings.TrimRight(m.chatInput, " \t\n")
				lastSpace := strings.LastIndexAny(trimmed, " \t\n")
				if lastSpace >= 0 {
					m.chatInput = trimmed[:lastSpace+1]
				} else {
					m.chatInput = ""
				}
				m.chatHistoryIndex = -1
				// EX-425: if Ctrl-W erased the last word of a dequeued message being edited,
				// clear editingQueued so the next message the user types and sends is not
				// incorrectly marked Edited=true. Give a distinct message so the user knows
				// the original queued text (which was dequeued for editing) is now gone.
				if m.editingQueued && strings.TrimSpace(m.chatInput) == "" {
					m.editingQueued = false
					m.statusMessage = "Queued message erased. Type a new message or press Esc to cancel."
				} else {
					m.statusMessage = "Last word deleted."
				}
			} else {
				// EX-326: give honest feedback when there is nothing to delete.
				m.statusMessage = "Nothing to delete."
			}
		} else {
			// EX-345: Ctrl-W outside chat panel — redirect hint.
			m.statusMessage = "Ctrl-W deletes last word from chat input. Press 3 or Tab to focus chat."
		}
		return m, nil
	case tea.KeyCtrlA:
		// EX-364: Ctrl-A (go to line start) — cursor movement not supported in chat input.
		if m.focus == ChatPanel {
			m.statusMessage = "Line-start (Ctrl-A) not supported. Use Ctrl-U to clear, Ctrl-W to delete word."
		} else {
			m.statusMessage = "Ctrl-A moves cursor to line start in chat. Press 3 or Tab to focus chat."
		}
		return m, nil
	case tea.KeyCtrlE:
		// EX-365: Ctrl-E (go to line end) — cursor movement not supported in chat input.
		if m.focus == ChatPanel {
			m.statusMessage = "Line-end (Ctrl-E) not supported. Use Ctrl-U to clear, Ctrl-W to delete word."
		} else {
			m.statusMessage = "Ctrl-E moves cursor to line end in chat. Press 3 or Tab to focus chat."
		}
		return m, nil
	case tea.KeyCtrlK:
		// EX-366: Ctrl-K (kill to end of line) — not supported in chat input.
		if m.focus == ChatPanel {
			m.statusMessage = "Kill-to-end (Ctrl-K) not supported. Use Ctrl-U to clear all, Ctrl-W to delete word."
		} else {
			m.statusMessage = "Ctrl-K kill-to-end is a chat shortcut. Press 3 or Tab to focus chat."
		}
		return m, nil
	case tea.KeyCtrlB, tea.KeyCtrlF:
		// EX-367: Ctrl-B/F (move char backward/forward) — cursor movement not supported.
		direction := "backward"
		if key.Type == tea.KeyCtrlF {
			direction = "forward"
		}
		if m.focus == ChatPanel {
			m.statusMessage = "Cursor movement " + direction + " (Ctrl-B/F) not supported. Use Ctrl-W to delete word, Ctrl-U to clear."
		} else {
			m.statusMessage = "Ctrl-B/F moves cursor in chat. Press 3 or Tab to focus chat."
		}
		return m, nil
	case tea.KeyCtrlD:
		// EX-368: Ctrl-D (delete-char-forward or EOF) — not supported in chat input.
		// When chat is focused and non-empty, hint about delete alternatives.
		// When chat is focused and empty, give quit hint (Ctrl-D EOF behaviour).
		if m.focus == ChatPanel {
			if strings.TrimSpace(m.chatInput) == "" {
				m.statusMessage = "Ctrl-D: press Ctrl-C or type 'q' to quit."
			} else {
				m.statusMessage = "Forward-delete (Ctrl-D) not supported. Use Ctrl-W to delete word, Ctrl-U to clear."
			}
		} else {
			m.statusMessage = "Ctrl-D: press Ctrl-C to quit or 3/Tab to focus chat."
		}
		return m, nil
	case tea.KeyDelete:
		// EX-369: Delete (forward delete) — no cursor concept in chat input.
		if m.focus == ChatPanel {
			m.statusMessage = "Forward-delete not supported. Use Backspace, Ctrl-W to delete word, or Ctrl-U to clear."
		} else {
			m.statusMessage = "Delete key: forward-delete works in chat input. Press 3 or Tab to focus chat."
		}
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
		if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			switch key.Type {
			case tea.KeyHome:
				// EX-276: Home/End in ViewInbox jump to first/last (same as g/G).
				// EX-292: give "Inbox is empty." feedback matching g/G (EX-288).
				m.workspace.inboxHome()
				if len(m.workspace.inbox) > 0 {
					if item := m.workspace.currentInboxItem(); item != nil {
						if item.Summary != "" {
							m.statusMessage = "▸ " + truncate(item.Summary, 40)
						} else {
							// EX-442: item exists but has no summary — still confirm navigation.
							m.statusMessage = "▸ (inbox item)"
						}
					}
				} else {
					m.statusMessage = "Inbox is empty."
				}
				return m, nil
			case tea.KeyEnd:
				// EX-292: give "Inbox is empty." feedback matching g/G (EX-288).
				m.workspace.inboxEnd()
				if len(m.workspace.inbox) > 0 {
					if item := m.workspace.currentInboxItem(); item != nil {
						if item.Summary != "" {
							m.statusMessage = "▸ " + truncate(item.Summary, 40)
						} else {
							// EX-442: item exists but has no summary — still confirm navigation.
							m.statusMessage = "▸ (inbox item)"
						}
					}
				} else {
					m.statusMessage = "Inbox is empty."
				}
				return m, nil
			case tea.KeyUp:
				// EX-272: arrow keys mirror j/k EX-268 navigation with feedback.
				// EX-295: give "Inbox is empty." feedback matching j/k (EX-268).
				prevCursor := m.workspace.inboxCursor
				m.workspace.moveInbox(-1)
				if item := m.workspace.currentInboxItem(); item != nil {
					if m.workspace.inboxCursor == prevCursor {
						m.statusMessage = "At first inbox item."
					} else if item.Summary != "" {
						m.statusMessage = "▸ " + truncate(item.Summary, 40)
					} else {
						// EX-442: moved to item with empty summary — still confirm navigation.
						m.statusMessage = "▸ (inbox item)"
					}
				} else if len(m.workspace.inbox) == 0 {
					m.statusMessage = "Inbox is empty."
				}
				return m, nil
			case tea.KeyDown:
				// EX-295: give "Inbox is empty." feedback matching j/k (EX-268).
				prevCursor := m.workspace.inboxCursor
				m.workspace.moveInbox(1)
				if item := m.workspace.currentInboxItem(); item != nil {
					if m.workspace.inboxCursor == prevCursor {
						m.statusMessage = "At last inbox item."
					} else if item.Summary != "" {
						m.statusMessage = "▸ " + truncate(item.Summary, 40)
					} else {
						// EX-442: moved to item with empty summary — still confirm navigation.
						m.statusMessage = "▸ (inbox item)"
					}
				} else if len(m.workspace.inbox) == 0 {
					m.statusMessage = "Inbox is empty."
				}
				return m, nil
			case tea.KeyPgUp:
				// EX-308: PgUp/PgDn in ViewInbox page through items (8 at a time).
				if len(m.workspace.inbox) == 0 {
					m.statusMessage = "Inbox is empty."
					return m, nil
				}
				prevCursor := m.workspace.inboxCursor
				for i := 0; i < chatScrollStepLines; i++ {
					m.workspace.moveInbox(-1)
				}
				if item := m.workspace.currentInboxItem(); item != nil {
					if m.workspace.inboxCursor == prevCursor {
						m.statusMessage = "At first inbox item."
					} else if item.Summary != "" {
						m.statusMessage = "▸ " + truncate(item.Summary, 40)
					} else {
						// EX-442: moved to item with empty summary — still confirm navigation.
						m.statusMessage = "▸ (inbox item)"
					}
				}
				return m, nil
			case tea.KeyPgDown:
				// EX-308: PgDown in ViewInbox pages forward through items.
				if len(m.workspace.inbox) == 0 {
					m.statusMessage = "Inbox is empty."
					return m, nil
				}
				prevCursor := m.workspace.inboxCursor
				for i := 0; i < chatScrollStepLines; i++ {
					m.workspace.moveInbox(1)
				}
				if item := m.workspace.currentInboxItem(); item != nil {
					if m.workspace.inboxCursor == prevCursor {
						m.statusMessage = "At last inbox item."
					} else if item.Summary != "" {
						m.statusMessage = "▸ " + truncate(item.Summary, 40)
					} else {
						// EX-442: moved to item with empty summary — still confirm navigation.
						m.statusMessage = "▸ (inbox item)"
					}
				}
				return m, nil
			}
		}
		if m.focus == MainPanel && m.workspace.mainView == ViewProject {
			switch key.Type {
			case tea.KeyHome:
				// EX-276: Home/End in ViewProject jump to first/last (same as g/G).
				// EX-293: give "No open tasks." feedback matching g/G (EX-287).
				m.workspace.projectTaskCursor = 0
				if openTasks := m.workspace.openTasksForProject(); len(openTasks) > 0 {
					t := openTasks[0]
					label := t.Title
					if t.TaskNumber > 0 {
						label = fmt.Sprintf("OC-%d: %s", t.TaskNumber, t.Title)
					}
					m.statusMessage = "▸ " + truncate(label, 40)
				} else {
					m.statusMessage = "No open tasks in this project."
				}
				return m, nil
			case tea.KeyEnd:
				// EX-293: give "No open tasks." feedback matching g/G (EX-287).
				if openTasks := m.workspace.openTasksForProject(); len(openTasks) > 0 {
					m.workspace.projectTaskCursor = len(openTasks) - 1
					t := openTasks[len(openTasks)-1]
					label := t.Title
					if t.TaskNumber > 0 {
						label = fmt.Sprintf("OC-%d: %s", t.TaskNumber, t.Title)
					}
					m.statusMessage = "▸ " + truncate(label, 40)
				} else {
					m.statusMessage = "No open tasks in this project."
				}
				return m, nil
			case tea.KeyUp:
				// EX-271: match k/j EX-270 feedback for ↑/↓ arrow keys.
				// EX-296: give "No open tasks." feedback matching k (EX-270).
				prevCursor := m.workspace.projectTaskCursor
				m.workspace.moveProjectTaskCursor(-1)
				if openTasks := m.workspace.openTasksForProject(); len(openTasks) > 0 {
					if m.workspace.projectTaskCursor == prevCursor {
						m.statusMessage = "At first task in project."
					} else if cur := m.workspace.projectTaskCursor; cur >= 0 && cur < len(openTasks) {
						t := openTasks[cur]
						label := t.Title
						if t.TaskNumber > 0 {
							label = fmt.Sprintf("OC-%d: %s", t.TaskNumber, t.Title)
						}
						m.statusMessage = "▸ " + truncate(label, 40)
					}
				} else {
					m.statusMessage = "No open tasks in this project."
				}
				return m, nil
			case tea.KeyDown:
				// EX-296: give "No open tasks." feedback matching j (EX-270).
				prevCursor := m.workspace.projectTaskCursor
				m.workspace.moveProjectTaskCursor(1)
				if openTasks := m.workspace.openTasksForProject(); len(openTasks) > 0 {
					if m.workspace.projectTaskCursor == prevCursor {
						m.statusMessage = "At last task in project."
					} else if cur := m.workspace.projectTaskCursor; cur >= 0 && cur < len(openTasks) {
						t := openTasks[cur]
						label := t.Title
						if t.TaskNumber > 0 {
							label = fmt.Sprintf("OC-%d: %s", t.TaskNumber, t.Title)
						}
						m.statusMessage = "▸ " + truncate(label, 40)
					}
				} else {
					m.statusMessage = "No open tasks in this project."
				}
				return m, nil
			case tea.KeyPgUp:
				// EX-308: PgUp/PgDn in ViewProject page through tasks (8 at a time).
				openTasks := m.workspace.openTasksForProject()
				if len(openTasks) == 0 {
					m.statusMessage = "No open tasks in this project."
					return m, nil
				}
				prevCursor := m.workspace.projectTaskCursor
				for i := 0; i < chatScrollStepLines; i++ {
					m.workspace.moveProjectTaskCursor(-1)
				}
				if m.workspace.projectTaskCursor == prevCursor {
					m.statusMessage = "At first task in project."
				} else if cur := m.workspace.projectTaskCursor; cur >= 0 && cur < len(openTasks) {
					t := openTasks[cur]
					label := t.Title
					if t.TaskNumber > 0 {
						label = fmt.Sprintf("OC-%d: %s", t.TaskNumber, t.Title)
					}
					m.statusMessage = "▸ " + truncate(label, 40)
				}
				return m, nil
			case tea.KeyPgDown:
				// EX-308: PgDown in ViewProject pages forward through tasks.
				openTasks := m.workspace.openTasksForProject()
				if len(openTasks) == 0 {
					m.statusMessage = "No open tasks in this project."
					return m, nil
				}
				prevCursor := m.workspace.projectTaskCursor
				for i := 0; i < chatScrollStepLines; i++ {
					m.workspace.moveProjectTaskCursor(1)
				}
				if m.workspace.projectTaskCursor == prevCursor {
					m.statusMessage = "At last task in project."
				} else if cur := m.workspace.projectTaskCursor; cur >= 0 && cur < len(openTasks) {
					t := openTasks[cur]
					label := t.Title
					if t.TaskNumber > 0 {
						label = fmt.Sprintf("OC-%d: %s", t.TaskNumber, t.Title)
					}
					m.statusMessage = "▸ " + truncate(label, 40)
				}
				return m, nil
			}
		}
		if m.focus == MainPanel && m.workspace.mainView == ViewDashboard {
			switch key.Type {
			case tea.KeyUp:
				// EX-271: match k EX-266 feedback for ↑ arrow key.
				// EX-294: also give feedback when board is empty.
				prevID := m.workspace.selectedTaskID
				m.workspace.moveDashboardCursor(-1)
				if m.workspace.selectedTaskID == prevID && prevID != "" {
					m.statusMessage = "At first task on board."
				} else if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil {
					label := task.Title
					if task.TaskNumber > 0 {
						label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, label)
					}
					m.statusMessage = "▸ " + truncate(label, 40)
				} else if prevID == "" {
					m.statusMessage = "Task board is empty."
				}
				return m, nil
			case tea.KeyDown:
				// EX-271: match j EX-266 feedback for ↓ arrow key.
				// EX-294: also give feedback when board is empty.
				prevID := m.workspace.selectedTaskID
				m.workspace.moveDashboardCursor(1)
				if m.workspace.selectedTaskID == prevID && prevID != "" {
					m.statusMessage = "At last task on board."
				} else if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil {
					label := task.Title
					if task.TaskNumber > 0 {
						label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, label)
					}
					m.statusMessage = "▸ " + truncate(label, 40)
				} else if prevID == "" {
					m.statusMessage = "Task board is empty."
				}
				return m, nil
			case tea.KeyHome:
				// EX-301: Home in ViewDashboard jumps to first task (mirrors 'g' key, EX-135).
				active := m.workspace.dashboardActiveTasks()
				if len(active) > 0 {
					m.workspace.dashboardCursor = 0
					m.workspace.selectedTaskID = active[0]
					if task := m.workspace.tasks[active[0]]; task != nil {
						label := task.Title
						if task.TaskNumber > 0 {
							label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, label)
						}
						m.statusMessage = "▸ " + truncate(label, 40)
					}
				} else {
					m.statusMessage = "Task board is empty."
				}
				return m, nil
			case tea.KeyEnd:
				// EX-301: End in ViewDashboard jumps to last task (mirrors 'G' key, EX-135).
				active := m.workspace.dashboardActiveTasks()
				if len(active) > 0 {
					m.workspace.dashboardCursor = len(active) - 1
					m.workspace.selectedTaskID = active[len(active)-1]
					if task := m.workspace.tasks[active[len(active)-1]]; task != nil {
						label := task.Title
						if task.TaskNumber > 0 {
							label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, label)
						}
						m.statusMessage = "▸ " + truncate(label, 40)
					}
				} else {
					m.statusMessage = "Task board is empty."
				}
				return m, nil
			case tea.KeyPgUp:
				// EX-302: PgUp in ViewDashboard pages backward through tasks.
				active := m.workspace.dashboardActiveTasks()
				if len(active) == 0 {
					m.statusMessage = "Task board is empty."
					return m, nil
				}
				prevID := m.workspace.selectedTaskID
				for i := 0; i < chatScrollStepLines; i++ {
					m.workspace.moveDashboardCursor(-1)
				}
				if m.workspace.selectedTaskID == prevID {
					m.statusMessage = "At first task on board."
				} else if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil {
					label := task.Title
					if task.TaskNumber > 0 {
						label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, label)
					}
					m.statusMessage = "▸ " + truncate(label, 40)
				}
				return m, nil
			case tea.KeyPgDown:
				// EX-302: PgDn in ViewDashboard pages forward through tasks.
				active := m.workspace.dashboardActiveTasks()
				if len(active) == 0 {
					m.statusMessage = "Task board is empty."
					return m, nil
				}
				prevID := m.workspace.selectedTaskID
				for i := 0; i < chatScrollStepLines; i++ {
					m.workspace.moveDashboardCursor(1)
				}
				if m.workspace.selectedTaskID == prevID {
					m.statusMessage = "At last task on board."
				} else if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil {
					label := task.Title
					if task.TaskNumber > 0 {
						label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, label)
					}
					m.statusMessage = "▸ " + truncate(label, 40)
				}
				return m, nil
			}
		}
		if m.focus == MainPanel && m.workspace.mainView == ViewTask {
			// EX-306/307: PgUp/PgDn/Home/End in ViewTask navigate through project tasks,
			// mirroring j/k (stepTaskInProject ±1) with larger jumps.
			// EX-312: ↑/↓ arrow keys also navigate tasks (mirror k/j from handleWorkspaceRune).
			// stepTaskInProject handles clamping and boundary messages automatically.
			switch key.Type {
			case tea.KeyUp:
				return m, m.stepTaskInProject(-1)
			case tea.KeyDown:
				return m, m.stepTaskInProject(1)
			case tea.KeyPgUp:
				return m, m.stepTaskInProject(-chatScrollStepLines)
			case tea.KeyPgDown:
				return m, m.stepTaskInProject(chatScrollStepLines)
			case tea.KeyHome:
				return m, m.stepTaskInProject(-99999)
			case tea.KeyEnd:
				return m, m.stepTaskInProject(99999)
			}
		}
		if m.focus == MainPanel && m.workspace.mainView == ViewHelp {
			// EX-274: PgUp/PgDn scroll the help view by a page — currently j/k
			// scroll one line at a time but PgUp/PgDn were silent no-ops.
			// EX-312: ↑/↓ arrow keys also scroll one line (mirror k/j from handleWorkspaceRune).
			helpMaxOffset := func() int {
				extra := 2
				if m.degradedModeBanner() != "" {
					extra++
				}
				if m.coldOpenActive {
					extra++
				}
				if m.tourActive {
					extra++
				}
				_, termH := normalizeDimensions(m.width, m.height)
				maxLines := termH - extra - 2 - 4
				if maxLines < 1 {
					maxLines = 1
				}
				mo := helpViewLineCount - maxLines
				if mo < 0 {
					mo = 0
				}
				return mo
			}
			switch key.Type {
			case tea.KeyUp:
				// EX-312: ↑ scrolls up one line (mirrors 'k').
				// EX-441: give scroll feedback consistent with PgUp and j/k (EX-440).
				if m.helpScrollOffset > 0 {
					m.helpScrollOffset--
					m.statusMessage = "Help scrolled up."
				} else {
					m.statusMessage = "Already at top of help."
				}
				return m, nil
			case tea.KeyDown:
				// EX-312: ↓ scrolls down one line (mirrors 'j').
				// EX-441: give scroll feedback consistent with PgDn and j/k (EX-440).
				maxOff := helpMaxOffset()
				if m.helpScrollOffset >= maxOff {
					m.statusMessage = "Already at bottom of help."
				} else {
					m.helpScrollOffset++
					m.statusMessage = "Help scrolled down."
				}
				return m, nil
			case tea.KeyPgUp:
				// PgUp = scroll UP through the document (like pressing k×8).
				if m.helpScrollOffset == 0 {
					m.statusMessage = "Already at top of help."
				} else {
					m.helpScrollOffset -= chatScrollStepLines
					if m.helpScrollOffset < 0 {
						m.helpScrollOffset = 0
					}
					m.statusMessage = "Help scrolled up."
				}
				return m, nil
			case tea.KeyPgDown:
				// PgDn = scroll DOWN through the document (like pressing j×8).
				maxOff := helpMaxOffset()
				if m.helpScrollOffset >= maxOff {
					m.statusMessage = "Already at bottom of help."
				} else {
					m.helpScrollOffset += chatScrollStepLines
					if m.helpScrollOffset > maxOff {
						m.helpScrollOffset = maxOff
					}
					m.statusMessage = "Help scrolled down."
				}
				return m, nil
			case tea.KeyHome:
				// EX-304: Home in ViewHelp jumps to top (mirrors 'g' key, EX-209).
				if m.helpScrollOffset == 0 {
					m.statusMessage = "Already at top of help."
				} else {
					m.helpScrollOffset = 0
					m.statusMessage = "Help jumped to top."
				}
				return m, nil
			case tea.KeyEnd:
				// EX-304: End in ViewHelp jumps to bottom (mirrors 'G' key, EX-209).
				maxOff := helpMaxOffset()
				if m.helpScrollOffset >= maxOff {
					m.statusMessage = "Already at bottom of help."
				} else {
					m.helpScrollOffset = maxOff
					m.statusMessage = "Help jumped to bottom."
				}
				return m, nil
			}
		}
		if key.Type == tea.KeyEnter {
			cmd := m.handleEnterKey()
			return m, cmd
		}
		if key.Type == tea.KeyEsc {
			// EX-180: handleEscapeKey now returns a tea.Cmd so it can load
			// project data when navigating back from task to project view.
			return m, m.handleEscapeKey()
		}
		// EX-312: ↑/↓ in non-navigable main views — give the same hint as j/k (EX-283).
		if m.focus == MainPanel && (key.Type == tea.KeyUp || key.Type == tea.KeyDown) {
			switch m.workspace.mainView {
			case ViewAgents, ViewMerges, ViewSchedules, ViewActivity:
				m.statusMessage = "j/k navigation not available here. Use r to refresh."
				return m, nil
			}
		}
		// EX-336: PgUp/PgDown/Home/End in static views mirror the ↑/↓ hint (EX-312).
		if m.focus == MainPanel && (key.Type == tea.KeyPgUp || key.Type == tea.KeyPgDown || key.Type == tea.KeyHome || key.Type == tea.KeyEnd) {
			switch m.workspace.mainView {
			case ViewAgents, ViewMerges, ViewSchedules, ViewActivity:
				m.statusMessage = "Navigation not available here. Use r to refresh."
				return m, nil
			}
		}
		// EX-347: ← in MainPanel mirrors Esc (go back / navigate up the view hierarchy).
		if m.focus == MainPanel && key.Type == tea.KeyLeft {
			return m, m.handleEscapeKey()
		}
		// EX-348: → in MainPanel mirrors Enter (open selected item / navigate down).
		if m.focus == MainPanel && key.Type == tea.KeyRight {
			cmd := m.handleEnterKey()
			return m, cmd
		}
		// EX-353: Backspace in MainPanel mirrors Esc (common "go back" gesture in TUIs).
		// This makes navigation consistent with file managers and web browsers.
		if m.focus == MainPanel && key.Type == tea.KeyBackspace {
			return m, m.handleEscapeKey()
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
					// EX-285: give feedback when toggling — matches h/l (EX-282).
					if node.Expanded {
						node.Expanded = false
						m.statusMessage = "Collapsed " + truncate(node.Label, 30) + "."
					} else {
						node.Expanded = true
						m.statusMessage = "Expanded " + truncate(node.Label, 30) + " — loading tasks…"
						return m, loadProjectTasksCmd(node.ProjectID, m.runtimeHints, true)
					}
				case sidebarKindHeader:
					sectionID := sidebarSectionID(strings.TrimPrefix(node.ID, "header-"))
					wasCollapsed := m.workspace.sectionCollapsed[sectionID]
					m.workspace.sectionCollapsed[sectionID] = !wasCollapsed
					// EX-285: give feedback — matches Enter on header (EX-261).
					if wasCollapsed {
						m.statusMessage = node.Label + " section expanded."
					} else {
						m.statusMessage = node.Label + " section collapsed."
					}
				default:
					// EX-324: Space on non-expandable nodes (task, session, inbox) — suggest Enter.
					m.statusMessage = "Use Enter to open this item."
				}
			} else {
				// EX-408: sidebar is empty — give honest feedback rather than silent no-op.
				m.statusMessage = "No items in sidebar."
			}
			return m, nil
		}
		// EX-346: Space in MainPanel — give a contextual hint (Space is only documented for sidebar).
		if m.focus == MainPanel {
			switch m.workspace.mainView {
			case ViewAgents, ViewMerges, ViewSchedules, ViewActivity:
				m.statusMessage = "Space not available here. Use r to refresh."
			// EX-408: Space in navigable views — redirect to Enter/o which opens the selected item.
			case ViewDashboard:
				if len(m.workspace.taskOrder) == 0 {
					m.statusMessage = "No tasks yet. Space is not bound here."
				} else {
					m.statusMessage = "Space is not bound. Use Enter to open task detail, j/k to navigate."
				}
			case ViewInbox:
				if len(m.workspace.inbox) == 0 {
					m.statusMessage = "Inbox is empty. Space is not bound here."
				} else {
					m.statusMessage = "Space is not bound. Use Enter/o to open, a·approve x·reject f·defer."
				}
			case ViewProject:
				if openTasks := m.workspace.openTasksForProject(); len(openTasks) == 0 {
					m.statusMessage = "No open tasks. Space is not bound here."
				} else {
					m.statusMessage = "Space is not bound. Use Enter to open task detail, j/k to navigate."
				}
			case ViewTask:
				m.statusMessage = "Space is not bound in task view. Use Enter to open session, Esc to go back."
			case ViewHelp:
				// Space in help view pages down (natural scroll gesture).
				helpMaxOff := func() int {
					extra := 2
					if m.degradedModeBanner() != "" {
						extra++
					}
					if m.coldOpenActive {
						extra++
					}
					if m.tourActive {
						extra++
					}
					_, termH := normalizeDimensions(m.width, m.height)
					maxLines := termH - extra - 2 - 4
					if maxLines < 1 {
						maxLines = 1
					}
					mo := helpViewLineCount - maxLines
					if mo < 0 {
						mo = 0
					}
					return mo
				}()
				if m.helpScrollOffset >= helpMaxOff {
					m.statusMessage = "Already at bottom of help."
				} else {
					m.helpScrollOffset += chatScrollStepLines
					if m.helpScrollOffset > helpMaxOff {
						m.helpScrollOffset = helpMaxOff
					}
					m.statusMessage = "Help scrolled down."
				}
			}
		}
		return m, nil
	case tea.KeyRunes:
		if len(key.Runes) == 1 {
			r := key.Runes[0]
			// EX-350: ':' enters command mode only when not mid-composition in chat.
			// When chat is focused with non-empty input, ':' should type (colon is
			// common in English messages). When input is empty (or focus is elsewhere),
			// ':' opens the command palette as documented. This mirrors the '?'→help
			// guard (EX-349) and the '0'→Frank guard (!chatTextInputActive).
			if r == ':' && !(m.focus == ChatPanel && strings.TrimSpace(m.chatInput) != "") {
				m.enterCommandMode()
				return m, nil
			}
			if r == '0' && !m.chatTextInputActive() {
				// EX-165: use the returned cmd to reload history.
				return m, m.jumpToFrankSession("0")
			}

			// EX-352: panel shortcuts 1/2/3 should not steal keystrokes mid-composition.
			// When chat is focused and the user is typing, numbers should type into
			// the input (mirrors '0' guard and the '<'/'>' guard pattern).
			if panel, ok := panelFromShortcut(key.Alt, r); ok {
				if !(m.focus == ChatPanel && strings.TrimSpace(m.chatInput) != "") {
					m.setFocus(panel)
					m.statusMessage = "Focus: " + panelLabel(m.focus)
					return m, nil
				}
			}

			// EX-351: '[' / ']' scope cycling should not intercept typing in chat.
			// Allow typing '[' and ']' when mid-composition, mirrors '<'/'>' guard.
			if r == '[' && !(m.focus == ChatPanel && strings.TrimSpace(m.chatInput) != "") {
				return m, m.switchScope(cycleScope(m.activeScope, false))
			}
			if r == ']' && !(m.focus == ChatPanel && strings.TrimSpace(m.chatInput) != "") {
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
				// EX-204: main panel width is set by the other two panels; give hint.
				if m.focus == MainPanel {
					m.statusMessage = "Focus sidebar or chat panel to resize with < >"
					return m, nil
				}
				// EX-401: resizeFocusedPanel returned false for a non-MainPanel focus.
				// This can happen when panel proportions are at extreme values (e.g.
				// the other panel fills nearly the whole screen). Give honest feedback
				// rather than silently falling through to an unrelated handler.
				m.statusMessage = "Cannot resize further — panel at minimum or maximum width."
				return m, nil
			}
			if r == '/' && (m.focus == SidebarPanel || m.focus == MainPanel) {
				// EX-214: the help view does not filter on mainFilter, so entering
				// search mode while ViewHelp is active would silently set a filter
				// that persists when the user navigates to another view.
				if m.focus == MainPanel && m.workspace.mainView == ViewHelp {
					m.statusMessage = "Search not available in help view. Press j/k to scroll."
					return m, nil
				}
				m.enterSearchMode(m.focus)
				return m, nil
			}
			if m.focus == ChatPanel && strings.TrimSpace(m.chatInput) == "" {
				if r == 'g' {
					// EX-318: if there are no messages, don't pretend to scroll.
					if len(m.chatMessages) == 0 {
						m.statusMessage = "No messages yet."
						return m, nil
					}
					// EX-430: symmetric with EX-427 (already-at-latest for 'G') —
					// when chatScrollOffset is already at the sentinel (1<<20), the
					// user is at the oldest message; say so instead of "Chat scrolled
					// to oldest." which implies movement when there was none.
					if m.chatScrollOffset >= 1<<20 {
						m.statusMessage = "Already at oldest message."
						return m, nil
					}
					m.chatScrollOffset = 1 << 20
					m.statusMessage = "Chat scrolled to oldest."
					return m, nil
				}
				if r == 'G' {
					if len(m.chatMessages) == 0 {
						m.statusMessage = "No messages yet."
						return m, nil
					}
					// EX-427: EX-264 added "Already at latest message." for KeyEnd but
					// the rune 'G' (keyboard equivalent of End) was not updated to match.
					// When chatScrollOffset is already 0, 'G' said "Chat scrolled to latest."
					// even though nothing changed — the same misleading pattern that
					// EX-264 fixed for KeyEnd and PgDown.
					if m.chatScrollOffset == 0 {
						m.statusMessage = "Already at latest message."
						return m, nil
					}
					m.chatScrollOffset = 0
					m.statusMessage = "Chat scrolled to latest."
					return m, nil
				}
				if r == '?' {
					// EX-349: '?' with empty chat input opens/closes help (mirrors global ? handler).
					// When input is non-empty, '?' should type into the input as normal.
					if m.workspace.mainView == ViewHelp {
						m.workspace.setMainView(ViewDashboard)
						m.statusMessage = "Returned to dashboard."
					} else {
						m.workspace.setMainView(ViewHelp)
						m.setFocus(MainPanel)
						m.helpScrollOffset = 0
						m.statusMessage = "Keybinding reference. Press ? or Esc to close."
					}
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
					m.helpScrollOffset = 0 // EX-209: reset scroll on open
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
	case tea.KeyCtrlS:
		// EX-378: Ctrl-S (save) — no explicit save needed; changes sync automatically.
		if m.focus == ChatPanel {
			m.statusMessage = "Ctrl-S: no save needed — chat messages sync automatically."
		} else {
			m.statusMessage = "Ctrl-S: no save needed — all changes are persisted automatically."
		}
		return m, nil
	case tea.KeyCtrlN:
		// EX-376: Ctrl-N (emacs/readline "next line") — no handler. Give a redirect hint.
		// Ctrl-P is captured for command mode, so Ctrl-N can't be symmetric ↓ here.
		if m.focus == ChatPanel {
			m.statusMessage = "Ctrl-N: use ↓ arrow or PgDn to scroll down."
		} else {
			m.statusMessage = "Ctrl-N: use ↓/j to navigate down, or 3/Tab to focus chat."
		}
		return m, nil
	case tea.KeyCtrlR:
		// EX-376: Ctrl-R (readline reverse-search) — not supported. Point to /.
		if m.focus == ChatPanel {
			m.statusMessage = "Ctrl-R: reverse search not supported. Use ↑ to browse message history."
		} else {
			m.statusMessage = "Ctrl-R: reverse search not supported. Use / to filter sidebar or main view."
		}
		return m, nil
	case tea.KeyCtrlY:
		// EX-376: Ctrl-Y (emacs yank/paste) — not supported. Terminal paste works directly.
		if m.focus == ChatPanel {
			m.statusMessage = "Ctrl-Y: use terminal paste (right-click or Ctrl-Shift-V) to paste text."
		} else {
			m.statusMessage = "Ctrl-Y: paste works in chat input (3 or Tab to focus chat)."
		}
		return m, nil
	case tea.KeyCtrlL:
		// EX-383: Ctrl-L (readline: clear screen) — screen is managed by the TUI, not the shell.
		// Pressing it in a terminal TUI does nothing useful; data refreshes via r.
		if m.focus == ChatPanel {
			m.statusMessage = "Ctrl-L: screen redraw is automatic. Use r to refresh data."
		} else {
			m.statusMessage = "Ctrl-L: screen redraw is automatic. Use r to refresh, Ctrl-C to quit."
		}
		return m, nil
	case tea.KeyCtrlT:
		// EX-383: Ctrl-T (readline: transpose chars) — no cursor concept in chat input.
		if m.focus == ChatPanel {
			m.statusMessage = "Ctrl-T: transpose not supported. Use Backspace + retype to fix."
		} else {
			m.statusMessage = "Ctrl-T: transpose not supported. Type in chat (3 or Tab) to edit."
		}
		return m, nil
	case tea.KeyCtrlV:
		// EX-383: Ctrl-V (Windows paste / readline: verbatim insert) — not supported.
		// Terminal paste (Ctrl-Shift-V or right-click) works directly in the chat input.
		if m.focus == ChatPanel {
			m.statusMessage = "Ctrl-V: use terminal paste (right-click or Ctrl-Shift-V) to paste."
		} else {
			m.statusMessage = "Ctrl-V: paste works in chat input. Press 3 or Tab to focus chat."
		}
		return m, nil
	case tea.KeyCtrlX:
		// EX-383: Ctrl-X (Emacs prefix key / cut in some editors) — not bound here.
		if m.focus == ChatPanel {
			m.statusMessage = "Ctrl-X: not bound. Use Ctrl-W to delete word, Ctrl-U to clear."
		} else {
			m.statusMessage = "Ctrl-X: not bound. Use : for commands or Ctrl-C to quit."
		}
		return m, nil
	case tea.KeyCtrlZ:
		// EX-383: Ctrl-Z (Unix: suspend to background) — not recommended inside a TUI.
		// Suspending the process leaves the terminal in a broken state; Ctrl-C is safer.
		m.statusMessage = "Ctrl-Z: suspend not recommended in TUI. Use Ctrl-C to quit or Esc to cancel."
		return m, nil
	case tea.KeyCtrlO:
		// EX-384: Ctrl-O (Emacs: open-line / shell: execute-and-remain) — not bound.
		// Point to the actual navigation shortcuts.
		if m.focus == ChatPanel {
			m.statusMessage = "Ctrl-O: not bound. Use ↑/↓ to browse history, Ctrl-G to jump to chat."
		} else {
			m.statusMessage = "Ctrl-O: not bound. Use ↑/↓ or j/k to navigate, ? for key reference."
		}
		return m, nil
	case tea.KeyF1:
		// EX-384: F1 — universally "help". Open the help view.
		m.workspace.setMainView(ViewHelp)
		m.setFocus(MainPanel)
		m.helpScrollOffset = 0
		m.statusMessage = "Keybinding reference. Press ? or Esc to close."
		return m, nil
	case tea.KeyF5:
		// EX-384: F5 — universally "refresh". Mirror the 'r' key.
		if m.focus == MainPanel && m.workspace.mainView == ViewHelp {
			m.statusMessage = "r·refresh not available in help view. Press j/k to scroll or Esc to close."
			return m, nil
		}
		m.statusMessage = "Refreshing…"
		m.workspace.activity = appendActivity(m.workspace.activity,
			"sidebar refreshed at "+m.now().Format("15:04:05"))
		return m, loadSidebarDataCmd(m.runtimeHints)
	case tea.KeyF2, tea.KeyF3, tea.KeyF4, tea.KeyF6, tea.KeyF7, tea.KeyF8, tea.KeyF9, tea.KeyF10, tea.KeyF11, tea.KeyF12:
		// EX-384: F2-F4 and F6-F12 — not bound. Point to the key reference.
		m.statusMessage = "F-key not bound. Press ? for key reference, : for commands, r to refresh."
		return m, nil
	case tea.KeyCtrlUp, tea.KeyCtrlDown:
		// EX-386: Ctrl+↑/↓ (jump by paragraph/block) — no block-level cursor here.
		// In chat, point to Ctrl-W (word delete) or PgUp/PgDn; outside chat, redirect to j/k.
		if m.focus == ChatPanel {
			m.statusMessage = "Ctrl+↑/↓ not supported. Use PgUp/PgDn to scroll messages."
		} else {
			m.statusMessage = "Ctrl+↑/↓ not supported. Use j/k or ↑/↓ to navigate."
		}
		return m, nil
	case tea.KeyCtrlLeft, tea.KeyCtrlRight:
		// EX-386: Ctrl+←/→ (jump by word) — no cursor in chat input.
		// Point to Ctrl-W (kill last word) as the nearest available analogue.
		if m.focus == ChatPanel {
			m.statusMessage = "Word-jump (Ctrl+←/→) not supported. Use Ctrl-W to delete word, Ctrl-U to clear."
		} else {
			m.statusMessage = "Ctrl+←/→ not supported. Use ←/→ arrows or j/k to navigate."
		}
		return m, nil
	case tea.KeyCtrlHome:
		// EX-386: Ctrl+Home — jump to document start. Mirror Home behaviour.
		if handled, cmd := m.handleSidebarControlKey(tea.KeyMsg{Type: tea.KeyHome}); m.focus == SidebarPanel && handled {
			return m, cmd
		}
		if m.focus == ChatPanel {
			// EX-428: mirror the EX-317 empty-messages guard from KeyHome.
			// Ctrl+Home previously said "Chat scrolled to oldest." even when there
			// were no messages to scroll through — same misleading pattern as KeyHome
			// before EX-317.
			if len(m.chatMessages) == 0 {
				m.statusMessage = "No messages yet."
			} else if m.chatScrollOffset >= 1<<20 {
				// EX-430: mirror the EX-430 already-at-oldest guard from KeyHome.
				m.statusMessage = "Already at oldest message."
			} else {
				m.chatScrollOffset = 1 << 20
				m.statusMessage = "Chat scrolled to oldest."
			}
		} else {
			// For main panel, delegate to Home key handling via the normal dispatch path.
			return m.updateKey(tea.KeyMsg{Type: tea.KeyHome})
		}
		return m, nil
	case tea.KeyCtrlEnd:
		// EX-386: Ctrl+End — jump to document end. Mirror End behaviour.
		if handled, cmd := m.handleSidebarControlKey(tea.KeyMsg{Type: tea.KeyEnd}); m.focus == SidebarPanel && handled {
			return m, cmd
		}
		if m.focus == ChatPanel {
			// EX-428: mirror the EX-319 / EX-264 / EX-427 guards from KeyEnd and 'G'.
			// Ctrl+End previously said "Chat scrolled to latest." even when there were
			// no messages (EX-319 pattern) or when already at offset 0 (EX-264 pattern).
			if len(m.chatMessages) == 0 {
				m.statusMessage = "No messages yet."
			} else if m.chatScrollOffset == 0 {
				m.statusMessage = "Already at latest message."
			} else {
				m.chatScrollOffset = 0
				m.statusMessage = "Chat scrolled to latest."
			}
		} else {
			return m.updateKey(tea.KeyMsg{Type: tea.KeyEnd})
		}
		return m, nil
	case tea.KeyCtrlPgUp:
		// EX-386: Ctrl+PgUp — mirror PgUp behaviour.
		return m.updateKey(tea.KeyMsg{Type: tea.KeyPgUp})
	case tea.KeyCtrlPgDown:
		// EX-386: Ctrl+PgDown — mirror PgDown behaviour.
		return m.updateKey(tea.KeyMsg{Type: tea.KeyPgDown})
	case tea.KeyShiftUp, tea.KeyShiftDown:
		// EX-386: Shift+↑/↓ (text selection) — no selection model in this TUI.
		if m.focus == ChatPanel {
			m.statusMessage = "Text selection not supported. Use ↑/↓ to scroll messages."
		} else {
			m.statusMessage = "Text selection not supported. Use j/k or ↑/↓ to navigate."
		}
		return m, nil
	case tea.KeyShiftLeft, tea.KeyShiftRight:
		// EX-386: Shift+←/→ (extend selection) — no selection model.
		if m.focus == ChatPanel {
			m.statusMessage = "Text selection not supported. Use Ctrl-W to delete word."
		} else {
			m.statusMessage = "Text selection not supported. Use ←/→ to navigate."
		}
		return m, nil
	case tea.KeyShiftHome, tea.KeyShiftEnd:
		// EX-386: Shift+Home/End (select to line boundary) — no selection model.
		if m.focus == ChatPanel {
			m.statusMessage = "Text selection not supported. Use Ctrl-U to clear, Ctrl-W to delete word."
		} else {
			m.statusMessage = "Text selection not supported. Use Home/End to jump to boundary."
		}
		return m, nil
	case tea.KeyInsert:
		// EX-385: Insert key — no insert/overwrite mode in this TUI.
		// Users may press it expecting vim-style insert mode or terminal paste.
		if m.focus == ChatPanel {
			m.statusMessage = "Insert: no insert/overwrite mode. Type normally; use terminal paste to paste."
		} else {
			m.statusMessage = "Insert key not bound. Type in chat (3 or Tab). Use terminal paste to paste text."
		}
		return m, nil
	case tea.KeyCtrlQ:
		// EX-385: Ctrl-Q (XON resume / Emacs secondary prefix) — not bound here.
		// If Ctrl-S/Ctrl-Q flow control is disabled, Ctrl-Q reaches the app.
		if m.focus == ChatPanel {
			m.statusMessage = "Ctrl-Q: not bound. Use Ctrl-W to delete word, Ctrl-U to clear."
		} else {
			m.statusMessage = "Ctrl-Q: not bound. Use : for commands or Ctrl-C to quit."
		}
		return m, nil
	case tea.KeyCtrlAt:
		// EX-398: Ctrl-@ (null / Ctrl+Space on some terminals) — not bound.
		if m.focus == ChatPanel {
			m.statusMessage = "Ctrl-@: not bound. Use Ctrl-C to quit or Tab to cycle panels."
		} else {
			m.statusMessage = "Ctrl-@: not bound. Use : for commands or Ctrl-C to quit."
		}
		return m, nil
	case tea.KeyCtrlBackslash:
		// EX-398: Ctrl-\ (SIGQUIT on some terminals) — give a safe redirect.
		m.statusMessage = "Ctrl-\\: not bound safely. Use Ctrl-C to quit or Esc to cancel."
		return m, nil
	case tea.KeyCtrlCloseBracket:
		// EX-398: Ctrl-] (telnet escape / vim: jump to tag) — not bound.
		if m.focus == ChatPanel {
			m.statusMessage = "Ctrl-]: not bound. Use Esc to cancel, Tab to cycle panels."
		} else {
			m.statusMessage = "Ctrl-]: not bound. Use Enter to open, Esc to go back."
		}
		return m, nil
	case tea.KeyCtrlCaret:
		// EX-398: Ctrl-^ (vim: alternate file / readline) — not bound.
		m.statusMessage = "Ctrl-^: not bound. Use 0 for Frank/org session, n for next unread."
		return m, nil
	case tea.KeyCtrlUnderscore:
		// EX-398: Ctrl-_ (undo in readline/nano) — no undo in TUI.
		if m.focus == ChatPanel {
			m.statusMessage = "Ctrl-_: undo not supported. Ask the agent via chat to revert changes."
		} else {
			m.statusMessage = "Ctrl-_: undo not supported. Use : for commands or ? for key reference."
		}
		return m, nil
	default:
		// EX-404: catch-all for any key type not explicitly listed above
		// (e.g. tea.KeyNull, future bubbletea additions, terminal-specific sequences).
		// Give an honest "not bound" hint so the user knows the key was received
		// but has no action assigned, rather than silently discarding it.
		if m.focus == ChatPanel {
			m.statusMessage = "Key not bound. Type your message or press ? for key reference."
		} else {
			m.statusMessage = "Key not bound. Press ? for key reference or : for commands."
		}
		return m, nil
	}
}

func (m *Model) handleEnterKey() tea.Cmd {
	switch m.focus {
	case SidebarPanel:
		node := m.workspace.currentSidebarNode()
		// EX-261: give honest feedback when sidebar is empty instead of the
		// misleading "Sidebar selection applied." (nothing was actually selected).
		if node == nil {
			m.statusMessage = "No items in sidebar."
			return nil
		}
		// EX-261: section header toggle is handled inside selectSidebarNode();
		// give a contextual message rather than the generic "Sidebar selection applied."
		if node.Kind == sidebarKindHeader {
			sectionID := sidebarSectionID(strings.TrimPrefix(node.ID, "header-"))
			currentlyCollapsed := m.workspace.sectionCollapsed[sectionID]
			m.workspace.selectSidebarNode() // toggles the section
			if currentlyCollapsed {
				m.statusMessage = node.Label + " section expanded."
			} else {
				m.statusMessage = node.Label + " section collapsed."
			}
			return nil
		}
		m.workspace.selectSidebarNode()
		m.state.LastActiveChatSession = m.workspace.activeSessionID
		// EX-186: clear stale turn state before switching sessions.
		m.clearTurnIfSwitchingSession(m.workspace.activeSessionID)
		m.activeSession = m.workspace.activeSessionID
		m.statusMessage = "Sidebar selection applied."
		if node != nil {
			switch node.Kind {
			case sidebarKindSession:
				// EX-299: override generic "Sidebar selection applied." with session name.
				m.statusMessage = "Switched to " + truncate(node.Label, 36) + "."
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
						// EX-186: clear stale turn state before switching sessions.
						m.clearTurnIfSwitchingSession(frankNode.SessionID)
						m.activeSession = frankNode.SessionID
						m.workspace.activeSessionID = frankNode.SessionID
						m.chatMessages = nil
						// EX-182: mark as loading so the chat panel shows the loading
						// indicator while history loads rather than blank content.
						m.chatHistoryLoading = true
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
				// EX-298: override generic "Sidebar selection applied." with task name.
				m.statusMessage = "▸ " + truncate(node.Label, 40)
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
				// EX-186: clear stale turn state before switching sessions.
				m.clearTurnIfSwitchingSession(m.workspace.activeSessionID)
				m.activeSession = m.workspace.activeSessionID
				// EX-172: inbox items are task-scoped; set scope so assistantLabel()
				// and chat header indicators are accurate.
				m.activeScope = ScopeTask
				m.statusMessage = "Opened inbox item in context."
				// EX-175: load task detail so task view shows number/description.
				var cmds []tea.Cmd
				if taskID := m.workspace.selectedTaskID; taskID != "" {
					cmds = append(cmds, loadTaskDetailCmd(taskID, m.runtimeHints))
				}
				// EX-169: reload chat history for the task session being opened.
				sessionID := m.workspace.activeSessionID
				if looksLikeUUID(sessionID) && m.runtimeHints.LoadChatHistory != nil {
					m.chatMessages = nil
					m.chatHistoryLoading = true
					m.chatMessageIndex = make(map[string]int)
					m.chatScrollOffset = 0
					cmds = append(cmds, loadChatHistoryCmd(sessionID, m.runtimeHints.LoadChatHistory))
				}
				if len(cmds) > 0 {
					return tea.Batch(cmds...)
				}
				return nil
			}
			// EX-192: inbox open failed (empty inbox or no item selected) — give feedback.
			m.statusMessage = "No inbox items to open."
			return nil
		}
		if m.workspace.mainView == ViewTask {
			if sessionID, ok := m.workspace.openSelectedTaskSession(); ok {
				m.state.LastActiveChatSession = sessionID
				// EX-186: clear stale turn state before switching sessions.
				m.clearTurnIfSwitchingSession(sessionID)
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
					// EX-182: clear stale messages so the loading indicator
					// appears while the task session's history loads.
					m.chatMessages = nil
					m.chatHistoryLoading = true
					m.chatMessageIndex = make(map[string]int)
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
					// EX-178: task-scoped view; set scope so assistantLabel() and
					// chat header scope indicators are accurate.
					m.activeScope = ScopeTask
					m.statusMessage = "Opened task detail."
					return loadTaskDetailCmd(taskID, m.runtimeHints)
				}
				// EX-191: no open tasks — give feedback instead of transitioning to a blank task view.
				if len(proj.Tasks) > 0 {
					m.statusMessage = "All tasks complete. Press 'd' to show done tasks."
				} else {
					m.statusMessage = "No tasks in this project."
				}
				return nil
			}
			// No project loaded yet — transition to task view anyway (data may still be loading).
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
			// EX-203: if there are still no tasks after the auto-select attempt,
			// give feedback instead of navigating to a blank task detail view.
			if m.workspace.selectedTaskID == "" {
				if len(m.workspace.taskOrder) > 0 {
					m.statusMessage = "All tasks are complete. No open tasks to open."
				} else {
					m.statusMessage = "No tasks yet. Create a task to get started."
				}
				return nil
			}
			m.workspace.setMainView(ViewTask)
			// EX-178: task-scoped view; set scope so assistantLabel() and
			// chat header scope indicators are accurate.
			m.activeScope = ScopeTask
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
		// EX-284: Enter in static views (Agents, Merges, Schedules, Activity)
		// has no selection model — give a hint rather than silently doing nothing.
		// EX-313: ViewHelp has different semantics — give a help-specific hint.
		if m.workspace.mainView == ViewHelp {
			m.statusMessage = "Press ?, q, or Esc to close help."
		} else {
			m.statusMessage = "Enter not available in this view. Use r to refresh or Esc to go back."
		}
	}
	return nil
}

func (m *Model) handleEscapeKey() tea.Cmd {
	// EX-205: Esc closes ViewHelp from any focus context, not just MainPanel.
	// The help screen renders in the main panel but focus may be elsewhere.
	if m.workspace.mainView == ViewHelp {
		m.workspace.setMainView(ViewDashboard)
		m.statusMessage = "Returned to dashboard."
		return nil
	}
	// EX-305: Esc in sidebar moves focus to main panel (natural dismissal gesture,
	// mirrors how Esc in chat would be handled by handleChatControlKey).
	if m.focus == SidebarPanel {
		m.setFocus(MainPanel)
		m.statusMessage = "Focus: " + panelLabel(m.focus)
		return nil
	}
	if m.focus == MainPanel {
		// From task detail: go back to project view if we came from one, else dashboard
		if m.workspace.mainView == ViewTask && m.workspace.selectedProjectID != "" {
			m.workspace.setMainView(ViewProject)
			m.statusMessage = "Back to project."
			// EX-180: load project data if it hasn't been fetched yet (e.g. user
			// opened task from dashboard before the initial project detail arrived).
			if m.workspace.selectedProject == nil {
				var cmds []tea.Cmd
				if m.runtimeHints.LoadProjectDetail != nil {
					cmds = append(cmds, loadProjectDetailCmd(m.workspace.selectedProjectID, m.runtimeHints))
				}
				cmds = append(cmds, loadProjectTasksCmd(m.workspace.selectedProjectID, m.runtimeHints, false))
				return tea.Batch(cmds...)
			}
			return nil
		}
		// EX-265: when already on the dashboard, "Returned to dashboard." is
		// misleading — you never went anywhere. Give honest feedback instead.
		if m.workspace.mainView == ViewDashboard {
			m.statusMessage = "Already on dashboard."
			return nil
		}
		m.workspace.setMainView(ViewDashboard)
		m.statusMessage = "Returned to dashboard."
	}
	return nil
}

// stepTaskInProject moves projectTaskCursor by delta (±1) and opens the task
// at the new position. A no-op (returns nil) when there is no project context
// or the project has fewer than 2 tasks.
func (m *Model) stepTaskInProject(delta int) tea.Cmd {
	openTasks := m.workspace.openTasksForProject()
	if len(openTasks) < 2 {
		// EX-202: give feedback when there's nothing to navigate through.
		// EX-286: also give feedback when there are 0 open tasks (no project context
		// or all tasks done) instead of silently doing nothing.
		switch len(openTasks) {
		case 0:
			if m.workspace.selectedProjectID == "" {
				m.statusMessage = "No project context. Open a task from a project to use j/k navigation."
			} else {
				m.statusMessage = "No open tasks in this project."
			}
		case 1:
			m.statusMessage = "Only one task in this project."
		}
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
		// EX-202: give directional feedback at list boundaries.
		if delta < 0 {
			m.statusMessage = "At first task."
		} else {
			m.statusMessage = "At last task."
		}
		return nil
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
			// EX-281: boundary + label feedback for sidebar navigation (mirrors j/k
			// feedback already added for ViewInbox/ViewProject/ViewDashboard).
			// EX-415: empty sidebar guard — mirrors EX-413/414 for j key.
			if len(m.workspace.visibleSidebarIDs()) == 0 {
				m.statusMessage = "No items in sidebar."
				return true, nil
			}
			prevCursor := m.workspace.sidebarCursor
			m.workspace.moveSidebar(1)
			if m.workspace.sidebarCursor == prevCursor {
				m.statusMessage = "At last item in sidebar."
			} else if node := m.workspace.currentSidebarNode(); node != nil {
				m.statusMessage = "▸ " + truncate(node.Label, 40)
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			// EX-268: show item summary on navigation (mirrors EX-266 dashboard pattern).
			// EX-295: give "Inbox is empty." feedback matching g/G/Home/End (EX-288/292).
			prevCursor := m.workspace.inboxCursor
			m.workspace.moveInbox(1)
			if item := m.workspace.currentInboxItem(); item != nil {
				if m.workspace.inboxCursor == prevCursor {
					m.statusMessage = "At last inbox item."
				} else if item.Summary != "" {
					m.statusMessage = "▸ " + truncate(item.Summary, 40)
				} else {
					// EX-442: moved to item with empty summary — still confirm navigation.
					m.statusMessage = "▸ (inbox item)"
				}
			} else if len(m.workspace.inbox) == 0 {
				m.statusMessage = "Inbox is empty."
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewProject {
			// EX-270: boundary feedback for project task list (mirrors EX-266 dashboard pattern).
			// EX-296: give "No open tasks." feedback matching g/G/Home/End (EX-287/293).
			prevCursor := m.workspace.projectTaskCursor
			m.workspace.moveProjectTaskCursor(1)
			if openTasks := m.workspace.openTasksForProject(); len(openTasks) > 0 {
				if m.workspace.projectTaskCursor == prevCursor {
					m.statusMessage = "At last task in project."
				} else if cur := m.workspace.projectTaskCursor; cur >= 0 && cur < len(openTasks) {
					t := openTasks[cur]
					label := t.Title
					if t.TaskNumber > 0 {
						label = fmt.Sprintf("OC-%d: %s", t.TaskNumber, t.Title)
					}
					m.statusMessage = "▸ " + truncate(label, 40)
				}
			} else {
				m.statusMessage = "No open tasks in this project."
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewDashboard {
			prevID := m.workspace.selectedTaskID
			m.workspace.moveDashboardCursor(1)
			// EX-266: when the cursor didn't advance (already at last task),
			// give directional feedback like EX-202 does for the project view.
			// EX-294: when board is empty (prevID and selectedTaskID both ""), say so.
			if m.workspace.selectedTaskID == prevID && prevID != "" {
				m.statusMessage = "At last task on board."
			} else if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil {
				label := task.Title
				if task.TaskNumber > 0 {
					label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, label)
				}
				m.statusMessage = "▸ " + truncate(label, 40)
			} else if prevID == "" {
				// EX-294: board was empty before the move — give honest feedback.
				m.statusMessage = "Task board is empty."
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewTask {
			return true, m.stepTaskInProject(1)
		} else if m.focus == MainPanel && m.workspace.mainView == ViewHelp {
			// EX-255: detect "already at bottom" symmetrically with EX-250 (k at top).
			// Replicate the maxLines calculation from View() + renderMainViewContent:
			//   maxLines = (termH - extraLines - 2) - 4
			extra := 2 // status bar + help bar
			if m.degradedModeBanner() != "" {
				extra++
			}
			if m.coldOpenActive {
				extra++
			}
			if m.tourActive {
				extra++
			}
			_, termH := normalizeDimensions(m.width, m.height)
			maxLines := termH - extra - 2 - 4
			if maxLines < 1 {
				maxLines = 1
			}
			maxOffset := helpViewLineCount - maxLines
			if maxOffset < 0 {
				maxOffset = 0
			}
			if m.helpScrollOffset >= maxOffset {
				m.statusMessage = "Already at bottom of help."
			} else {
				m.helpScrollOffset++
				// EX-440: give feedback consistent with PgDn ("Help scrolled down.") and
				// g/G ("Help jumped to top/bottom."). j/k at boundary give feedback; the
				// mid-scroll case was silent, unlike every other j/k navigable view.
				m.statusMessage = "Help scrolled down."
			}
		} else if m.focus == MainPanel {
			// EX-283: j/k in static views (Agents, Merges, Schedules, Activity)
			// have no cursor to move — give a hint instead of silently doing nothing.
			m.statusMessage = "j/k navigation not available here. Use r to refresh."
		}
		return true, nil
	case 'k':
		if m.focus == SidebarPanel {
			// EX-281: boundary + label feedback (mirrors j case above).
			// EX-415: empty sidebar guard — mirrors EX-413/414 for k key.
			if len(m.workspace.visibleSidebarIDs()) == 0 {
				m.statusMessage = "No items in sidebar."
				return true, nil
			}
			prevCursor := m.workspace.sidebarCursor
			m.workspace.moveSidebar(-1)
			if m.workspace.sidebarCursor == prevCursor {
				m.statusMessage = "At first item in sidebar."
			} else if node := m.workspace.currentSidebarNode(); node != nil {
				m.statusMessage = "▸ " + truncate(node.Label, 40)
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			// EX-268: show item summary on navigation (mirrors EX-266 dashboard pattern).
			// EX-295: give "Inbox is empty." feedback matching g/G/Home/End (EX-288/292).
			prevCursor := m.workspace.inboxCursor
			m.workspace.moveInbox(-1)
			if item := m.workspace.currentInboxItem(); item != nil {
				if m.workspace.inboxCursor == prevCursor {
					m.statusMessage = "At first inbox item."
				} else if item.Summary != "" {
					m.statusMessage = "▸ " + truncate(item.Summary, 40)
				} else {
					// EX-442: moved to item with empty summary — still confirm navigation.
					m.statusMessage = "▸ (inbox item)"
				}
			} else if len(m.workspace.inbox) == 0 {
				m.statusMessage = "Inbox is empty."
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewProject {
			// EX-270: boundary feedback (mirrors EX-266 dashboard pattern).
			// EX-296: give "No open tasks." feedback matching g/G/Home/End (EX-287/293).
			prevCursor := m.workspace.projectTaskCursor
			m.workspace.moveProjectTaskCursor(-1)
			if openTasks := m.workspace.openTasksForProject(); len(openTasks) > 0 {
				if m.workspace.projectTaskCursor == prevCursor {
					m.statusMessage = "At first task in project."
				} else if cur := m.workspace.projectTaskCursor; cur >= 0 && cur < len(openTasks) {
					t := openTasks[cur]
					label := t.Title
					if t.TaskNumber > 0 {
						label = fmt.Sprintf("OC-%d: %s", t.TaskNumber, t.Title)
					}
					m.statusMessage = "▸ " + truncate(label, 40)
				}
			} else {
				m.statusMessage = "No open tasks in this project."
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewDashboard {
			prevID := m.workspace.selectedTaskID
			m.workspace.moveDashboardCursor(-1)
			// EX-266: when the cursor didn't retreat (already at first task),
			// give directional feedback like EX-202 does for the project view.
			// EX-294: when board is empty (prevID and selectedTaskID both ""), say so.
			if m.workspace.selectedTaskID == prevID && prevID != "" {
				m.statusMessage = "At first task on board."
			} else if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil {
				label := task.Title
				if task.TaskNumber > 0 {
					label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, label)
				}
				m.statusMessage = "▸ " + truncate(label, 40)
			} else if prevID == "" {
				// EX-294: board was empty before the move — give honest feedback.
				m.statusMessage = "Task board is empty."
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewTask {
			return true, m.stepTaskInProject(-1)
		} else if m.focus == MainPanel && m.workspace.mainView == ViewHelp {
			if m.helpScrollOffset > 0 {
				m.helpScrollOffset--
				// EX-440: give feedback consistent with PgUp ("Help scrolled up.") and
				// g/G ("Help jumped to top/bottom."). Mirrors the j fix for the same view.
				m.statusMessage = "Help scrolled up."
			} else {
				// EX-250: give feedback instead of silent no-op when already at the top.
				m.statusMessage = "Already at top of help."
			}
		} else if m.focus == MainPanel {
			// EX-283: k in static views has no cursor — give a hint.
			m.statusMessage = "j/k navigation not available here. Use r to refresh."
		}
		return true, nil
	case 'h':
		if m.focus == SidebarPanel {
			// EX-416: empty sidebar guard — mirrors EX-413/414/415 for h key.
			if len(m.workspace.visibleSidebarIDs()) == 0 {
				m.statusMessage = "No items in sidebar."
				return true, nil
			}
			node := m.workspace.currentSidebarNode()
			// EX-282: capture state before collapseSidebarNode mutates the node.
			var nodeLabel string
			var nodeExpanded bool
			var nodeKind sidebarKind
			var nodeParentID string
			if node != nil {
				nodeLabel = node.Label
				nodeExpanded = node.Expanded
				nodeKind = node.Kind
				nodeParentID = node.ParentID
			}
			m.workspace.collapseSidebarNode()
			if node != nil {
				switch nodeKind {
				case sidebarKindProject:
					if nodeExpanded {
						m.statusMessage = "Collapsed " + truncate(nodeLabel, 30) + "."
					} else if nodeParentID != "" {
						m.statusMessage = "Moved to parent."
					} else {
						// EX-323: project already collapsed at top level â give honest feedback.
						m.statusMessage = "Already collapsed."
					}
				case sidebarKindHeader:
					m.statusMessage = "Collapsed " + truncate(nodeLabel, 30) + "."
				default:
					if nodeParentID != "" {
						m.statusMessage = "Moved to parent."
					} else {
						// EX-323: no parent to move to â give hint.
						m.statusMessage = "At top level â use â/â to navigate."
					}
				}
			}
			return true, nil
		}
		// EX-217: give feedback instead of silent no-op when sidebar not focused.
		m.statusMessage = "h/l collapse/expand sidebar sections — press 1 to focus sidebar"
		return true, nil
	case 'l':
		if m.focus == SidebarPanel {
			// EX-416: empty sidebar guard — mirrors EX-413/414/415 for l key.
			if len(m.workspace.visibleSidebarIDs()) == 0 {
				m.statusMessage = "No items in sidebar."
				return true, nil
			}
			node := m.workspace.currentSidebarNode()
			m.workspace.expandSidebarNode()
			// EX-282: give feedback about what was expanded.
			if node != nil {
				if node.Kind == sidebarKindProject {
					m.statusMessage = "Expanded " + truncate(node.Label, 30) + " — loading tasks…"
					return true, loadProjectTasksCmd(node.ProjectID, m.runtimeHints, true)
				}
				if node.Kind == sidebarKindHeader {
					m.statusMessage = "Expanded " + truncate(node.Label, 30) + "."
					return true, nil
				}
				// EX-321: non-expandable nodes (task, session, inbox) — suggest Enter.
				m.statusMessage = "Use Enter to open this item."
			}
			return true, nil
		}
		// EX-217: give feedback instead of silent no-op when sidebar not focused.
		m.statusMessage = "h/l collapse/expand sidebar sections — press 1 to focus sidebar"
		return true, nil
	case 's':
		if m.focus == MainPanel || m.focus == SidebarPanel {
			m.toggleSidebar()
			return true, nil
		}
	case 'g':
		if m.focus == SidebarPanel {
			// EX-297: give label/boundary feedback matching Home (EX-290).
			// EX-414: empty sidebar — give honest "No items" instead of "At first item."
			if len(m.workspace.visibleSidebarIDs()) == 0 {
				m.statusMessage = "No items in sidebar."
				return true, nil
			}
			prevCursor := m.workspace.sidebarCursor
			m.workspace.sidebarHome()
			if m.workspace.sidebarCursor == prevCursor {
				m.statusMessage = "At first item in sidebar."
			} else if node := m.workspace.currentSidebarNode(); node != nil {
				m.statusMessage = "▸ " + truncate(node.Label, 40)
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			m.workspace.inboxHome()
			// EX-273: mirror ViewDashboard g feedback — show item summary.
			// EX-288: mirror EX-190 — give feedback when inbox is empty.
			if len(m.workspace.inbox) > 0 {
				if item := m.workspace.currentInboxItem(); item != nil {
					if item.Summary != "" {
						m.statusMessage = "▸ " + truncate(item.Summary, 40)
					} else {
						// EX-442: item exists but has no summary — still confirm navigation.
						m.statusMessage = "▸ (inbox item)"
					}
				}
			} else {
				m.statusMessage = "Inbox is empty."
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewProject {
			m.workspace.projectTaskCursor = 0
			// EX-273: show first task title like ViewDashboard does.
			if openTasks := m.workspace.openTasksForProject(); len(openTasks) > 0 {
				t := openTasks[0]
				label := t.Title
				if t.TaskNumber > 0 {
					label = fmt.Sprintf("OC-%d: %s", t.TaskNumber, t.Title)
				}
				m.statusMessage = "▸ " + truncate(label, 40)
			} else {
				// EX-287: match EX-190 pattern — give feedback when no open tasks.
				m.statusMessage = "No open tasks in this project."
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewDashboard {
			// EX-135: jump to first task on dashboard board (g = vim home)
			active := m.workspace.dashboardActiveTasks()
			if len(active) > 0 {
				m.workspace.dashboardCursor = 0
				m.workspace.selectedTaskID = active[0]
				if task := m.workspace.tasks[active[0]]; task != nil {
					label := task.Title
					if task.TaskNumber > 0 {
						label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, label)
					}
					m.statusMessage = "▸ " + truncate(label, 40)
				}
			} else {
				// EX-190: no active tasks — give feedback instead of silent no-op.
				m.statusMessage = "No active tasks on dashboard."
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewHelp {
			// EX-209/322: g jumps to top of help view; give feedback.
			if m.helpScrollOffset == 0 {
				m.statusMessage = "Already at top of help."
			} else {
				m.helpScrollOffset = 0
				m.statusMessage = "Help jumped to top."
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewTask {
			// EX-337: g in task view jumps to first task (mirrors Home/PgUp x inf).
			return true, m.stepTaskInProject(-99999)
		} else if m.focus == MainPanel {
			// EX-339: g in static views (Agents/Merges/Schedules/Activity) mirrors j/k hint (EX-283).
			switch m.workspace.mainView {
			case ViewAgents, ViewMerges, ViewSchedules, ViewActivity:
				m.statusMessage = "Navigation not available here. Use r to refresh."
			}
		}
		return true, nil
	case 'G':
		if m.focus == SidebarPanel {
			// EX-297: give label/boundary feedback matching End (EX-290).
			// EX-414: empty sidebar — give honest "No items" instead of "At last item."
			if len(m.workspace.visibleSidebarIDs()) == 0 {
				m.statusMessage = "No items in sidebar."
				return true, nil
			}
			prevCursor := m.workspace.sidebarCursor
			m.workspace.sidebarEnd()
			if m.workspace.sidebarCursor == prevCursor {
				m.statusMessage = "At last item in sidebar."
			} else if node := m.workspace.currentSidebarNode(); node != nil {
				m.statusMessage = "▸ " + truncate(node.Label, 40)
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			m.workspace.inboxEnd()
			// EX-273: mirror ViewDashboard G feedback — show item summary.
			// Guard against empty inbox: currentInboxItem clamps cursor, which
			// would mutate it away from -1 and break existing semantics.
			// EX-288: also give feedback when inbox is empty.
			if len(m.workspace.inbox) > 0 {
				if item := m.workspace.currentInboxItem(); item != nil {
					if item.Summary != "" {
						m.statusMessage = "▸ " + truncate(item.Summary, 40)
					} else {
						// EX-442: item exists but has no summary — still confirm navigation.
						m.statusMessage = "▸ (inbox item)"
					}
				}
			} else {
				m.statusMessage = "Inbox is empty."
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewProject {
			// EX-273: use openTasksForProject so cursor lands within bounds and
			// we can show the title — matches the g case feedback pattern.
			if openTasks := m.workspace.openTasksForProject(); len(openTasks) > 0 {
				m.workspace.projectTaskCursor = len(openTasks) - 1
				t := openTasks[len(openTasks)-1]
				label := t.Title
				if t.TaskNumber > 0 {
					label = fmt.Sprintf("OC-%d: %s", t.TaskNumber, t.Title)
				}
				m.statusMessage = "▸ " + truncate(label, 40)
			} else {
				// EX-287: match EX-190 pattern — give feedback when no open tasks.
				m.statusMessage = "No open tasks in this project."
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewDashboard {
			// EX-135: jump to last task on dashboard board (G = vim end)
			active := m.workspace.dashboardActiveTasks()
			if len(active) > 0 {
				m.workspace.dashboardCursor = len(active) - 1
				m.workspace.selectedTaskID = active[len(active)-1]
				if task := m.workspace.tasks[active[len(active)-1]]; task != nil {
					label := task.Title
					if task.TaskNumber > 0 {
						label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, label)
					}
					m.statusMessage = "▸ " + truncate(label, 40)
				}
			} else {
				// EX-190: no active tasks — give feedback instead of silent no-op.
				m.statusMessage = "No active tasks on dashboard."
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewHelp {
			// EX-209/322: G jumps to bottom of help view; give feedback.
			// EX-429: previously used hardcoded 9999 threshold so pressing 'G' after
			// reaching the bottom via KeyEnd or j×N still said "Help jumped to bottom."
			// (e.g. after KeyEnd set offset=50, 50>=9999 is false). Compute the actual
			// maxOffset using the same formula as KeyEnd/PgDn so the boundary is
			// correctly detected regardless of how the user got to the bottom.
			// The jump itself still uses a large sentinel (1<<20) so that
			// renderHelpView can clamp it correctly for any maxLines passed at
			// render time — the test at TestHelpViewScrollsWithJK relies on this.
			extra := 2
			if m.degradedModeBanner() != "" {
				extra++
			}
			if m.coldOpenActive {
				extra++
			}
			if m.tourActive {
				extra++
			}
			_, termH := normalizeDimensions(m.width, m.height)
			maxLines := termH - extra - 2 - 4
			if maxLines < 1 {
				maxLines = 1
			}
			maxOff := helpViewLineCount - maxLines
			if maxOff < 0 {
				maxOff = 0
			}
			if m.helpScrollOffset >= maxOff {
				m.statusMessage = "Already at bottom of help."
			} else {
				m.helpScrollOffset = 1 << 20
				m.statusMessage = "Help jumped to bottom."
			}
		} else if m.focus == MainPanel && m.workspace.mainView == ViewTask {
			// EX-338: G in task view jumps to last task (mirrors End/PgDn x inf).
			return true, m.stepTaskInProject(99999)
		} else if m.focus == MainPanel {
			// EX-339: G in static views mirrors j/k hint (EX-283).
			switch m.workspace.mainView {
			case ViewAgents, ViewMerges, ViewSchedules, ViewActivity:
				m.statusMessage = "Navigation not available here. Use r to refresh."
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
		// EX-335: 'q' in non-help views is a vim-familiar key that users may try
		// expecting it to quit or close something — give a helpful redirect.
		m.statusMessage = "q closes the help view. Use :quit or Ctrl-C to exit."
		return true, nil
	case 'i':
		if m.focus != ChatPanel {
			// EX-436: if already on inbox, the view hasn't changed — say "Inbox refreshed."
			// so the user knows the refresh happened rather than receiving the same "Inbox"
			// message as when navigating to inbox fresh. Mirrors EX-332/333/334 pattern for
			// d/p/t but here a reload always fires (inbox may be stale), so we don't no-op.
			alreadyOnInbox := m.focus == MainPanel && m.workspace.mainView == ViewInbox
			m.workspace.setMainView(ViewInbox)
			m.setFocus(MainPanel)
			if alreadyOnInbox {
				m.statusMessage = "Inbox refreshed."
			} else {
				m.statusMessage = "Inbox"
			}
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
			// EX-332: if already on dashboard, say so (mirrors EX-265 for Esc).
			if m.focus == MainPanel && m.workspace.mainView == ViewDashboard {
				m.statusMessage = "Already on dashboard."
				return true, nil
			}
			m.workspace.setMainView(ViewDashboard)
			m.setFocus(MainPanel)
			m.statusMessage = "Dashboard"
			return true, nil
		}
	case 'p':
		if m.focus != ChatPanel && m.workspace.selectedProjectID == "" {
			// EX-199: no project selected — give feedback instead of silent no-op.
			m.statusMessage = "No project selected. Select a project from the sidebar."
			return true, nil
		}
		if m.focus != ChatPanel && m.workspace.selectedProjectID != "" {
			// EX-333: if already on project view for this project, say so.
			if m.focus == MainPanel && m.workspace.mainView == ViewProject {
				m.statusMessage = "Already in project view."
				return true, nil
			}
			m.workspace.setMainView(ViewProject)
			m.setFocus(MainPanel)
			m.statusMessage = "Project view"
			// EX-176: if project detail hasn't loaded yet (e.g. user pressed 'p'
			// while the detail load from selecting the project was still in-flight),
			// trigger a fresh load so the task list renders immediately.
			if m.workspace.selectedProject == nil {
				var cmds []tea.Cmd
				if m.runtimeHints.LoadProjectDetail != nil {
					cmds = append(cmds, loadProjectDetailCmd(m.workspace.selectedProjectID, m.runtimeHints))
				}
				cmds = append(cmds, loadProjectTasksCmd(m.workspace.selectedProjectID, m.runtimeHints, false))
				return true, tea.Batch(cmds...)
			}
			return true, nil
		}
	case 't':
		if m.focus != ChatPanel && m.workspace.selectedTaskID == "" {
			// EX-199: no task selected — give feedback instead of silent no-op.
			m.statusMessage = "No task selected. Select a task from the dashboard or project view."
			return true, nil
		}
		if m.focus != ChatPanel && m.workspace.selectedTaskID != "" {
			// EX-334: if already on task detail view for this task, say so.
			if m.focus == MainPanel && m.workspace.mainView == ViewTask {
				m.statusMessage = "Already viewing task detail."
				return true, nil
			}
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
				sessionID := m.workspace.activeSessionID
				// EX-186: clear stale turn state before switching sessions.
				m.clearTurnIfSwitchingSession(sessionID)
				m.activeSession = sessionID
				m.chatScrollOffset = 0
				m.statusMessage = "Jumped to next unread session."
				m.setFocus(SidebarPanel)
				// EX-166: reload chat history so the chat panel shows the unread session's
				// messages, not the previous session's (selectSidebarNode only sets the ID).
				if looksLikeUUID(sessionID) && m.runtimeHints.LoadChatHistory != nil {
					m.chatMessages = nil
					m.chatHistoryLoading = true
					m.chatMessageIndex = make(map[string]int)
					return true, loadChatHistoryCmd(sessionID, m.runtimeHints.LoadChatHistory)
				}
				return true, nil
			}
			m.statusMessage = "No unread sessions."
			return true, nil
		}
	case 'r':
		// EX-247: r has no meaning in the help view — give a hint instead of
		// triggering a sidebar refresh that would confuse the user.
		if m.focus == MainPanel && m.workspace.mainView == ViewHelp {
			m.statusMessage = "r·refresh not available in help view. Press j/k to scroll or Esc to close."
			return true, nil
		}
		// EX-133: when focused on task or project detail, refresh that view's
		// data in addition to the sidebar so manual refresh is context-aware.
		if m.focus == MainPanel && m.workspace.mainView == ViewTask && m.workspace.selectedTaskID != "" {
			m.statusMessage = "Refreshing task detail…"
			return true, loadTaskDetailCmd(m.workspace.selectedTaskID, m.runtimeHints)
		}
		if m.focus == MainPanel && m.workspace.mainView == ViewProject && m.workspace.selectedProjectID != "" {
			m.statusMessage = "Refreshing project detail…"
			// EX-184: also reload project tasks so newly-created tasks and status
			// changes that arrived while offline appear after a manual refresh.
			return true, tea.Batch(
				loadProjectDetailCmd(m.workspace.selectedProjectID, m.runtimeHints),
				loadProjectTasksCmd(m.workspace.selectedProjectID, m.runtimeHints, false),
			)
		}
		// EX-162: refresh the content the user is actually viewing, not just sidebar metadata.
		if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			m.statusMessage = "Refreshing inbox…"
			return true, loadInboxItemsCmd(m.runtimeHints)
		}
		if m.focus == MainPanel && m.workspace.mainView == ViewAgents {
			m.statusMessage = "Refreshing agents…"
			return true, loadAgentsCmd(m.runtimeHints)
		}
		// EX-206: use a more descriptive status message that matches the current view.
		switch {
		case m.focus == MainPanel && m.workspace.mainView == ViewDashboard:
			m.statusMessage = "Refreshing task board…"
		case m.focus == MainPanel && m.workspace.mainView == ViewActivity:
			m.statusMessage = "Refreshing activity…"
		case m.focus == MainPanel && m.workspace.mainView == ViewMerges:
			m.statusMessage = "Refreshing merges…"
		case m.focus == MainPanel && m.workspace.mainView == ViewSchedules:
			m.statusMessage = "Refreshing schedules…"
		default:
			m.statusMessage = "Refreshing sidebar data…"
		}
		m.workspace.activity = appendActivity(m.workspace.activity,
			"sidebar refreshed at "+m.now().Format("15:04:05"))
		return true, loadSidebarDataCmd(m.runtimeHints)
	case 'a':
		// EX-160: capture item ID before applyInboxAction removes it, then issue API call.
		if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			item := m.workspace.currentInboxItem()
			if item != nil && m.workspace.applyInboxAction("approve") {
				m.statusMessage = "Inbox item approved."
				return true, actOnInboxItemCmd(item.ID, "approve", m.runtimeHints.ActOnInboxItem)
			}
			// EX-196: no item to approve — give feedback instead of silent no-op.
			m.statusMessage = "No inbox item to approve."
			return true, nil
		}
		// EX-148: allow approve from task view when RequiresHumanReview is set.
		if m.focus == MainPanel && m.workspace.mainView == ViewTask {
			task := m.workspace.tasks[m.workspace.selectedTaskID]
			if task != nil && task.RequiresHumanReview {
				itemID := m.workspace.inboxItemIDForTask(task.ID)
				if m.workspace.applyInboxActionForTask(task.ID, "approve") {
					m.statusMessage = "Task approved."
					return true, actOnInboxItemCmd(itemID, "approve", m.runtimeHints.ActOnInboxItem)
				}
				// EX-437: review badge shown but no inbox item found — inbox may be stale.
				m.statusMessage = "Review item not found. Press i to refresh Inbox."
				return true, nil
			} else if task != nil {
				// EX-196: task doesn't require review — let user know.
				m.statusMessage = "This task doesn't require review."
				return true, nil
			} else {
				// EX-331: no task loaded yet in task view — give honest feedback.
				m.statusMessage = "No task loaded. Use Enter on a task first."
				return true, nil
			}
		}
		// EX-340/EX-381: a in non-inbox/task views or sidebar — give redirect hint.
		if m.focus == MainPanel || m.focus == SidebarPanel {
			m.statusMessage = "a·approve works in Inbox or Task view (when ⚠ shown). Press i for Inbox."
			return true, nil
		}
	case 'x':
		// EX-160: capture item ID before applyInboxAction removes it.
		if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			item := m.workspace.currentInboxItem()
			if item != nil && m.workspace.applyInboxAction("reject") {
				m.statusMessage = "Inbox item rejected."
				return true, actOnInboxItemCmd(item.ID, "reject", m.runtimeHints.ActOnInboxItem)
			}
			// EX-196: no item to reject — give feedback.
			m.statusMessage = "No inbox item to reject."
			return true, nil
		}
		// EX-148: allow reject from task view.
		if m.focus == MainPanel && m.workspace.mainView == ViewTask {
			task := m.workspace.tasks[m.workspace.selectedTaskID]
			if task != nil && task.RequiresHumanReview {
				itemID := m.workspace.inboxItemIDForTask(task.ID)
				if m.workspace.applyInboxActionForTask(task.ID, "reject") {
					m.statusMessage = "Task rejected."
					return true, actOnInboxItemCmd(itemID, "reject", m.runtimeHints.ActOnInboxItem)
				}
				// EX-437: review badge shown but no inbox item found — inbox may be stale.
				m.statusMessage = "Review item not found. Press i to refresh Inbox."
				return true, nil
			} else if task != nil {
				// EX-196: task doesn't require review.
				m.statusMessage = "This task doesn't require review."
				return true, nil
			} else {
				// EX-331: no task loaded yet.
				m.statusMessage = "No task loaded. Use Enter on a task first."
				return true, nil
			}
		}
		// EX-341/EX-381: x in non-inbox/task views or sidebar — give redirect hint.
		if m.focus == MainPanel || m.focus == SidebarPanel {
			m.statusMessage = "x·reject works in Inbox or Task view (when ⚠ shown). Press i for Inbox."
			return true, nil
		}
	case 'f':
		// EX-160: capture item ID before applyInboxAction removes it.
		if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			item := m.workspace.currentInboxItem()
			if item != nil && m.workspace.applyInboxAction("defer") {
				m.statusMessage = "Inbox item deferred."
				return true, actOnInboxItemCmd(item.ID, "defer", m.runtimeHints.ActOnInboxItem)
			}
			// EX-196: no item to defer — give feedback.
			m.statusMessage = "No inbox item to defer."
			return true, nil
		}
		// EX-148: allow defer from task view.
		if m.focus == MainPanel && m.workspace.mainView == ViewTask {
			task := m.workspace.tasks[m.workspace.selectedTaskID]
			if task != nil && task.RequiresHumanReview {
				itemID := m.workspace.inboxItemIDForTask(task.ID)
				if m.workspace.applyInboxActionForTask(task.ID, "defer") {
					m.statusMessage = "Task deferred."
					return true, actOnInboxItemCmd(itemID, "defer", m.runtimeHints.ActOnInboxItem)
				}
				// EX-437: review badge shown but no inbox item found — inbox may be stale.
				m.statusMessage = "Review item not found. Press i to refresh Inbox."
				return true, nil
			} else if task != nil {
				// EX-196: task doesn't require review.
				m.statusMessage = "This task doesn't require review."
				return true, nil
			} else {
				// EX-331: no task loaded yet.
				m.statusMessage = "No task loaded. Use Enter on a task first."
				return true, nil
			}
		}
		// EX-342/EX-381: f in non-inbox/task views or sidebar — give redirect hint.
		if m.focus == MainPanel || m.focus == SidebarPanel {
			m.statusMessage = "f·defer works in Inbox or Task view (when ⚠ shown). Press i for Inbox."
			return true, nil
		}
	case 'o':
		if m.focus == MainPanel && m.workspace.mainView == ViewInbox {
			if !m.workspace.applyInboxAction("open") {
				// EX-192 (also applies to 'o'): no item to open — give feedback.
				m.statusMessage = "No inbox items to open."
				return true, nil
			}
			m.state.LastActiveChatSession = m.workspace.activeSessionID
			// EX-186: clear stale turn state before switching sessions.
			m.clearTurnIfSwitchingSession(m.workspace.activeSessionID)
			m.activeSession = m.workspace.activeSessionID
			// EX-172: inbox items are task-scoped; set scope so assistantLabel()
			// resolves the correct agent name and [task]/[project]/[org] indicators
			// are accurate in the chat header.
			m.activeScope = ScopeTask
			m.statusMessage = "Opened inbox item in context."
			// EX-175: load task detail so the task view shows task number and
			// description even when the task was never previously loaded via the
			// sidebar (e.g. user navigated straight to inbox on first launch).
			var cmds []tea.Cmd
			if taskID := m.workspace.selectedTaskID; taskID != "" {
				cmds = append(cmds, loadTaskDetailCmd(taskID, m.runtimeHints))
			}
			// EX-169: reload chat history for the task session being opened.
			sessionID := m.workspace.activeSessionID
			if looksLikeUUID(sessionID) && m.runtimeHints.LoadChatHistory != nil {
				m.chatMessages = nil
				m.chatHistoryLoading = true
				m.chatMessageIndex = make(map[string]int)
				m.chatScrollOffset = 0
				cmds = append(cmds, loadChatHistoryCmd(sessionID, m.runtimeHints.LoadChatHistory))
			}
			if len(cmds) > 0 {
				return true, tea.Batch(cmds...)
			}
			return true, nil
		}
		// EX-237: 'o' in task view should open the task session (same as Enter).
		// The task view hints show "o·open task session" when RequiresHumanReview,
		// so 'o' must actually work here, not silently fall through.
		if m.focus == MainPanel && m.workspace.mainView == ViewTask {
			return true, m.handleEnterKey()
		}
		// EX-343/EX-381: o in non-inbox/task views or sidebar — give redirect hint.
		if m.focus == MainPanel || m.focus == SidebarPanel {
			m.statusMessage = "o·open works in Inbox (opens item) or Task view (opens session). Press i for Inbox."
			return true, nil
		}
	case 'c':
		// EX-373: 'c' is not a documented shortcut, but users familiar with vim/tmux
		// may press it expecting to cancel the current agent turn. Redirect to the
		// actual cancel shortcut (Esc in chat panel) or :cancel-turn command.
		if m.focus != ChatPanel {
			if m.activeTurn {
				m.statusMessage = "c is not bound. Press Esc in chat (3 or Tab) or type :cancel-turn to cancel the turn."
			} else {
				m.statusMessage = "c is not bound here. Press ? for help or : for commands."
			}
			return true, nil
		}
	case 'e':
		// EX-377: 'e' (edit) — not bound, but commonly tried in task-management TUIs.
		// Redirect to the chat panel where users can message the agent about edits.
		if m.focus != ChatPanel {
			m.statusMessage = "e is not bound. Chat with the agent (3 or Tab) to request edits."
			return true, nil
		}
	case 'u':
		// EX-377: 'u' (undo) — vim muscle memory. No undo in this TUI; redirect to chat.
		if m.focus != ChatPanel {
			m.statusMessage = "u is not bound. Ask the agent to revert changes via chat (3 or Tab)."
			return true, nil
		}
	case 'v':
		// EX-377: 'v' (view/visual) — not bound. Redirect to Enter/→ for opening items.
		if m.focus != ChatPanel {
			m.statusMessage = "v is not bound. Use Enter or → to open the selected item."
			return true, nil
		}
	case 'm':
		// EX-377: 'm' (mark/menu/move) — not bound. Redirect to inbox actions.
		if m.focus != ChatPanel {
			m.statusMessage = "m is not bound. Press a·approve, x·reject, or f·defer in Inbox view."
			return true, nil
		}
	case 'w':
		// EX-378: 'w' (vim: next word) — not bound. Redirect to j/k for navigation.
		if m.focus != ChatPanel {
			m.statusMessage = "w is not bound. Use j/k or ↑/↓ to navigate, or ? for key reference."
			return true, nil
		}
	case 'b':
		// EX-378: 'b' (vim: previous word) — not bound. Redirect to j/k for navigation.
		if m.focus != ChatPanel {
			m.statusMessage = "b is not bound. Use j/k or ↑/↓ to navigate, or ? for key reference."
			return true, nil
		}
	case 'y':
		// EX-378: 'y' (vim: yank/copy) — not bound. Redirect to terminal clipboard.
		if m.focus != ChatPanel {
			m.statusMessage = "y is not bound. Use your terminal to copy text."
			return true, nil
		}
	case 'z':
		// EX-378: 'z' (vim: recenter/fold) — not bound.
		if m.focus != ChatPanel {
			m.statusMessage = "z is not bound. Use j/k or ↑/↓ to scroll, or ? for key reference."
			return true, nil
		}
	case 'N':
		// EX-380: capital 'N' — not bound. Users may mean lowercase 'n' (next unread).
		if m.focus != ChatPanel {
			m.statusMessage = "N is not bound. Press n (lowercase) to jump to next unread session."
			return true, nil
		}
	case 'I':
		// EX-380: capital 'I' — not bound. Users may mean lowercase 'i' (inbox).
		if m.focus != ChatPanel {
			m.statusMessage = "I is not bound. Press i (lowercase) to open the inbox."
			return true, nil
		}
	case 'D':
		// EX-380: capital 'D' — not bound. Users may mean lowercase 'd' (dashboard / done toggle).
		if m.focus != ChatPanel {
			m.statusMessage = "D is not bound. Press d (lowercase) for dashboard or to show/hide done tasks."
			return true, nil
		}
	case 'R':
		// EX-380: capital 'R' — not bound. Users may mean lowercase 'r' (refresh).
		if m.focus != ChatPanel {
			m.statusMessage = "R is not bound. Press r (lowercase) to refresh."
			return true, nil
		}
	case 'P':
		// EX-380: capital 'P' — not bound. Users may mean lowercase 'p' (project view).
		if m.focus != ChatPanel {
			m.statusMessage = "P is not bound. Press p (lowercase) to open the project view."
			return true, nil
		}
	case 'T':
		// EX-380: capital 'T' — not bound. Users may mean lowercase 't' (task view).
		if m.focus != ChatPanel {
			m.statusMessage = "T is not bound. Press t (lowercase) to open task detail."
			return true, nil
		}
	case '4', '5', '6', '7', '8', '9':
		// EX-372: Users may guess that 4-9 switch panels (1=sidebar, 2=main, 3=chat).
		// Give a hint pointing to the actual panel shortcuts instead of silently doing nothing.
		if m.focus != ChatPanel {
			m.statusMessage = "Panels: 1 sidebar · 2 main · 3 chat. Press ? for full key reference."
			return true, nil
		}
	case '.':
		// EX-388: '.' (vim: repeat last action) — not supported. Point to : for commands.
		// Vim users may press '.' expecting to repeat the last approval/rejection.
		if m.focus != ChatPanel {
			m.statusMessage = ". (dot-repeat) not supported. Use : to repeat commands or ? for key reference."
			return true, nil
		}
	case '\\':
		// EX-388: '\' (vim leader key) — not bound. Point to : and ? shortcuts.
		if m.focus != ChatPanel {
			m.statusMessage = "\\ not bound. Use : for commands or ? for key reference."
			return true, nil
		}
	case 'A':
		// EX-382: capital A — not bound. Users may mean lowercase 'a' (approve).
		if m.focus != ChatPanel {
			m.statusMessage = "A is not bound. Press a (lowercase) to approve in Inbox or Task view."
			return true, nil
		}
	case 'B':
		// EX-382: capital B — not bound. Users may mean lowercase 'b' (vim: prev word).
		if m.focus != ChatPanel {
			m.statusMessage = "B is not bound. Use j/k or ↑/↓ to navigate, or ? for key reference."
			return true, nil
		}
	case 'C':
		// EX-382: capital C — not bound. Users may mean lowercase 'c' (cancel-turn hint).
		if m.focus != ChatPanel {
			m.statusMessage = "C is not bound. Press c (lowercase) or use :cancel-turn for active-turn hints."
			return true, nil
		}
	case 'E':
		// EX-382: capital E — not bound. Users may mean lowercase 'e' (edit hint).
		if m.focus != ChatPanel {
			m.statusMessage = "E is not bound. Press e (lowercase) or chat (3 or Tab) to request edits."
			return true, nil
		}
	case 'F':
		// EX-382: capital F — not bound. Users may mean lowercase 'f' (defer in inbox).
		if m.focus != ChatPanel {
			m.statusMessage = "F is not bound. Press f (lowercase) to defer in Inbox view. Press i for Inbox."
			return true, nil
		}
	case 'H':
		// EX-382: capital H — not bound. Users may mean lowercase 'h' (collapse sidebar).
		if m.focus != ChatPanel {
			m.statusMessage = "H is not bound. Press h (lowercase) to collapse sidebar sections (1 to focus sidebar)."
			return true, nil
		}
	case 'J':
		// EX-382: capital J — not bound. Users may mean lowercase 'j' (navigate down).
		if m.focus != ChatPanel {
			m.statusMessage = "J is not bound. Press j (lowercase) to navigate down."
			return true, nil
		}
	case 'K':
		// EX-382: capital K — not bound. Users may mean lowercase 'k' (navigate up).
		if m.focus != ChatPanel {
			m.statusMessage = "K is not bound. Press k (lowercase) to navigate up."
			return true, nil
		}
	case 'L':
		// EX-382: capital L — not bound. Users may mean lowercase 'l' (expand sidebar).
		if m.focus != ChatPanel {
			m.statusMessage = "L is not bound. Press l (lowercase) to expand sidebar sections (1 to focus sidebar)."
			return true, nil
		}
	case 'M':
		// EX-382: capital M — not bound. Users may mean lowercase 'm' (mark/menu hint).
		if m.focus != ChatPanel {
			m.statusMessage = "M is not bound. Press a·approve, x·reject, or f·defer in Inbox view."
			return true, nil
		}
	case 'O':
		// EX-382: capital O — not bound. Users may mean lowercase 'o' (open inbox/session).
		if m.focus != ChatPanel {
			m.statusMessage = "O is not bound. Press o (lowercase) to open an inbox item or task session."
			return true, nil
		}
	case 'Q':
		// EX-382: capital Q — not bound. Users may mean lowercase 'q' (close help / quit hint).
		if m.focus != ChatPanel {
			m.statusMessage = "Q is not bound. Press q (lowercase) to close help, or use :quit to exit."
			return true, nil
		}
	case 'S':
		// EX-382: capital S — not bound. Users may mean lowercase 's' (sidebar toggle).
		if m.focus != ChatPanel {
			m.statusMessage = "S is not bound. Press s (lowercase) to toggle the sidebar."
			return true, nil
		}
	case 'U':
		// EX-382: capital U — not bound. Users may mean Ctrl-U (clear chat) or lowercase 'u' (undo hint).
		if m.focus != ChatPanel {
			m.statusMessage = "U is not bound. Use Ctrl-U to clear chat input (3 or Tab to focus chat)."
			return true, nil
		}
	case 'V':
		// EX-382: capital V — not bound. Users may mean lowercase 'v' (view/visual hint).
		if m.focus != ChatPanel {
			m.statusMessage = "V is not bound. Use Enter or → to open the selected item."
			return true, nil
		}
	case 'W':
		// EX-382: capital W — not bound. Users may mean lowercase 'w' (vim: next WORD).
		if m.focus != ChatPanel {
			m.statusMessage = "W is not bound. Use j/k or ↑/↓ to navigate, or ? for key reference."
			return true, nil
		}
	case 'X':
		// EX-382: capital X — not bound. Users may mean lowercase 'x' (reject in inbox).
		if m.focus != ChatPanel {
			m.statusMessage = "X is not bound. Press x (lowercase) to reject in Inbox or Task view."
			return true, nil
		}
	case 'Y':
		// EX-382: capital Y — not bound. Users may mean lowercase 'y' (yank hint).
		if m.focus != ChatPanel {
			m.statusMessage = "Y is not bound. Use your terminal to copy text."
			return true, nil
		}
	case 'Z':
		// EX-382: capital Z — not bound. Users may try shift-ZZ (vim quit) or ZQ.
		if m.focus != ChatPanel {
			m.statusMessage = "Z is not bound. Use :quit or Ctrl-C to exit."
			return true, nil
		}
	case ';':
		// EX-393: ';' (vim: repeat last find-character) — not bound.
		// Users with vim muscle memory may press ';' after navigation.
		if m.focus != ChatPanel {
			m.statusMessage = "; is not bound. Use j/k or ↑/↓ to navigate, Enter to open."
			return true, nil
		}
	case '\'':
		// EX-393: ''' (vim: jump to mark) — marks not supported.
		if m.focus != ChatPanel {
			m.statusMessage = "' (mark jump) not supported. Use g/G for top/bottom, n for unread."
			return true, nil
		}
	case '`':
		// EX-393: '`' (vim: jump to mark exactly) — marks not supported.
		if m.focus != ChatPanel {
			m.statusMessage = "` (mark jump) not supported. Use g/G for top/bottom, n for unread."
			return true, nil
		}
	case '"':
		// EX-393: '"' (vim: register access) — registers not supported.
		if m.focus != ChatPanel {
			m.statusMessage = "\" (register) not supported. Use : for commands or ? for key reference."
			return true, nil
		}
	case '-':
		// EX-393: '-' (vim: move cursor up one line to first non-blank) — not bound.
		if m.focus != ChatPanel {
			m.statusMessage = "- is not bound. Use k/↑ to navigate up, or j/↓ to navigate down."
			return true, nil
		}
	case '+':
		// EX-393: '+' (vim: next line, first non-blank) — not bound.
		if m.focus != ChatPanel {
			m.statusMessage = "+ is not bound. Use j/↓ to navigate down."
			return true, nil
		}
	case '!':
		// EX-393: '!' (vim: filter through external command) — not supported.
		if m.focus != ChatPanel {
			m.statusMessage = "! not bound. Use : for commands or chat (3/Tab) to run commands via agent."
			return true, nil
		}
	case '@':
		// EX-393: '@' (vim: execute macro) — macros not supported.
		if m.focus != ChatPanel {
			m.statusMessage = "@ (macro) not supported. Use : for commands or ? for key reference."
			return true, nil
		}
	case '^':
		// EX-393: '^' (vim: first non-blank of line) — not applicable.
		if m.focus != ChatPanel {
			m.statusMessage = "^ is not bound. Use g/G for top/bottom navigation."
			return true, nil
		}
	case '~':
		// EX-393: '~' (vim: toggle case) — not supported.
		if m.focus != ChatPanel {
			m.statusMessage = "~ not bound. Use : for commands or ? for key reference."
			return true, nil
		}
	case '(':
		// EX-396: '(' (vim: sentence backward) — not applicable.
		if m.focus != ChatPanel {
			m.statusMessage = "( is not bound. Use k/↑ to navigate up, g for top."
			return true, nil
		}
	case ')':
		// EX-396: ')' (vim: sentence forward) — not applicable.
		if m.focus != ChatPanel {
			m.statusMessage = ") is not bound. Use j/↓ to navigate down, G for bottom."
			return true, nil
		}
	case '{':
		// EX-396: '{' (vim: paragraph backward) — not applicable.
		if m.focus != ChatPanel {
			m.statusMessage = "{ is not bound. Use k/↑ or PgUp to navigate up."
			return true, nil
		}
	case '}':
		// EX-396: '}' (vim: paragraph forward) — not applicable.
		if m.focus != ChatPanel {
			m.statusMessage = "} is not bound. Use j/↓ or PgDn to navigate down."
			return true, nil
		}
	case '*':
		// EX-396: '*' (vim: search forward for word under cursor) — no search.
		if m.focus != ChatPanel {
			m.statusMessage = "* (search) not supported. Use / to filter the sidebar or main view."
			return true, nil
		}
	case '#':
		// EX-396: '#' (vim: search backward for word under cursor) — no search.
		if m.focus != ChatPanel {
			m.statusMessage = "# (search) not supported. Use / to filter the sidebar or main view."
			return true, nil
		}
	case '$':
		// EX-396: '$' (vim: end of line) — not applicable.
		if m.focus != ChatPanel {
			m.statusMessage = "$ is not bound. Use G to jump to last item or End to go to bottom."
			return true, nil
		}
	case '%':
		// EX-396: '%' (vim: match brackets) — not applicable.
		if m.focus != ChatPanel {
			m.statusMessage = "% is not bound. Use : for commands or ? for key reference."
			return true, nil
		}
	case '&':
		// EX-396: '&' (vim: repeat last substitution) — not applicable.
		if m.focus != ChatPanel {
			m.statusMessage = "& is not bound. Use : for commands or ? for key reference."
			return true, nil
		}
	case '_':
		// EX-396: '_' (vim: first non-blank of current/next line) — not applicable.
		if m.focus != ChatPanel {
			m.statusMessage = "_ is not bound. Use g for top of list or Enter to open selected item."
			return true, nil
		}
	case '=':
		// EX-396: '=' (vim: auto-indent) — not applicable.
		if m.focus != ChatPanel {
			m.statusMessage = "= is not bound. Use : for commands or ? for key reference."
			return true, nil
		}
	case ',':
		// EX-397: ',' (vim: repeat find-char backward; also used in many CLIs) — not bound.
		if m.focus != ChatPanel {
			m.statusMessage = ", is not bound. Use j/k or ↑/↓ to navigate."
			return true, nil
		}
	case '|':
		// EX-397: '|' (vim: goto column; shell pipe) — not applicable here.
		if m.focus != ChatPanel {
			m.statusMessage = "| is not bound. Use : for commands or ? for key reference."
			return true, nil
		}
	default:
		// EX-405: catch-all for runes not explicitly handled above (e.g. unicode
		// characters, emoji, accented letters) pressed in a non-chat panel.
		// All printable ASCII characters have explicit cases; this branch covers
		// non-ASCII input that shouldn't silently disappear.
		if m.focus != ChatPanel {
			m.statusMessage = "Key not bound. Press ? for key reference or : for commands."
			return true, nil
		}
	}
	return false, nil
}

func (m *Model) handleSidebarControlKey(key tea.KeyMsg) (bool, tea.Cmd) {
	switch key.Type {
	case tea.KeyUp:
		// EX-281: boundary + label feedback for arrow keys (mirrors j/k feedback).
		// EX-415: empty sidebar guard — give honest "No items" feedback.
		if len(m.workspace.visibleSidebarIDs()) == 0 {
			m.statusMessage = "No items in sidebar."
			return true, nil
		}
		prevCursor := m.workspace.sidebarCursor
		m.workspace.moveSidebar(-1)
		if m.workspace.sidebarCursor == prevCursor {
			m.statusMessage = "At first item in sidebar."
		} else if node := m.workspace.currentSidebarNode(); node != nil {
			m.statusMessage = "▸ " + truncate(node.Label, 40)
		}
		return true, nil
	case tea.KeyDown:
		// EX-415: symmetric empty-sidebar guard.
		if len(m.workspace.visibleSidebarIDs()) == 0 {
			m.statusMessage = "No items in sidebar."
			return true, nil
		}
		prevCursor := m.workspace.sidebarCursor
		m.workspace.moveSidebar(1)
		if m.workspace.sidebarCursor == prevCursor {
			m.statusMessage = "At last item in sidebar."
		} else if node := m.workspace.currentSidebarNode(); node != nil {
			m.statusMessage = "▸ " + truncate(node.Label, 40)
		}
		return true, nil
	case tea.KeyLeft:
		// EX-282: ← mirrors h — give same collapse feedback.
		// EX-416: empty sidebar guard — mirrors EX-413/414/415 for ← key.
		if len(m.workspace.visibleSidebarIDs()) == 0 {
			m.statusMessage = "No items in sidebar."
			return true, nil
		}
		// Capture state before collapseSidebarNode mutates the node pointer.
		node := m.workspace.currentSidebarNode()
		var nodeLabel string
		var nodeExpanded bool
		var nodeKind sidebarKind
		var nodeParentID string
		if node != nil {
			nodeLabel = node.Label
			nodeExpanded = node.Expanded
			nodeKind = node.Kind
			nodeParentID = node.ParentID
		}
		m.workspace.collapseSidebarNode()
		if node != nil {
			switch nodeKind {
			case sidebarKindProject:
				if nodeExpanded {
					m.statusMessage = "Collapsed " + truncate(nodeLabel, 30) + "."
				} else if nodeParentID != "" {
					m.statusMessage = "Moved to parent."
				} else {
					// EX-323: project already collapsed at top level â give honest feedback.
					m.statusMessage = "Already collapsed."
				}
			case sidebarKindHeader:
				m.statusMessage = "Collapsed " + truncate(nodeLabel, 30) + "."
			default:
				if nodeParentID != "" {
					m.statusMessage = "Moved to parent."
				} else {
					// EX-323: no parent to move to â give hint.
					m.statusMessage = "At top level â use â/â to navigate."
				}
			}
		}
		return true, nil
	case tea.KeyRight:
		// EX-282: → mirrors l — give same expand feedback.
		// EX-416: empty sidebar guard — mirrors EX-413/414/415 for → key.
		if len(m.workspace.visibleSidebarIDs()) == 0 {
			m.statusMessage = "No items in sidebar."
			return true, nil
		}
		node := m.workspace.currentSidebarNode()
		if node != nil {
			if node.Kind == sidebarKindProject {
				m.workspace.expandSidebarNode()
				m.statusMessage = "Expanded " + truncate(node.Label, 30) + " — loading tasks…"
				return true, loadProjectTasksCmd(node.ProjectID, m.runtimeHints, true)
			}
			if node.Kind == sidebarKindHeader {
				m.workspace.expandSidebarNode()
				m.statusMessage = "Expanded " + truncate(node.Label, 30) + "."
				return true, nil
			}
			// EX-321: non-expandable nodes (task, session, inbox) — suggest Enter.
			m.statusMessage = "Use Enter to open this item."
			return true, nil
		}
		return true, nil
	case tea.KeyHome:
		// EX-277: Home/End in sidebar jump to first/last item (same as g/G).
		// EX-290: give label/boundary feedback matching j/k and ↑/↓ (EX-281).
		// EX-413: if sidebar is empty, say so instead of the misleading
		// "At first item in sidebar." (there are no items to be at).
		if len(m.workspace.visibleSidebarIDs()) == 0 {
			m.statusMessage = "No items in sidebar."
			return true, nil
		}
		prevCursor := m.workspace.sidebarCursor
		m.workspace.sidebarHome()
		if m.workspace.sidebarCursor == prevCursor {
			m.statusMessage = "At first item in sidebar."
		} else if node := m.workspace.currentSidebarNode(); node != nil {
			m.statusMessage = "▸ " + truncate(node.Label, 40)
		}
		return true, nil
	case tea.KeyEnd:
		// EX-290: give label/boundary feedback matching j/k and ↑/↓ (EX-281).
		// EX-413: symmetric empty-sidebar guard.
		if len(m.workspace.visibleSidebarIDs()) == 0 {
			m.statusMessage = "No items in sidebar."
			return true, nil
		}
		prevCursor := m.workspace.sidebarCursor
		m.workspace.sidebarEnd()
		if m.workspace.sidebarCursor == prevCursor {
			m.statusMessage = "At last item in sidebar."
		} else if node := m.workspace.currentSidebarNode(); node != nil {
			m.statusMessage = "▸ " + truncate(node.Label, 40)
		}
		return true, nil
	case tea.KeyPgUp:
		// EX-278: PgUp/PgDn in sidebar scroll by a page (8 items at a time).
		// EX-291: give boundary feedback when the page scroll hits the first item.
		// EX-413: symmetric empty-sidebar guard.
		if len(m.workspace.visibleSidebarIDs()) == 0 {
			m.statusMessage = "No items in sidebar."
			return true, nil
		}
		prevCursor := m.workspace.sidebarCursor
		for i := 0; i < chatScrollStepLines; i++ {
			m.workspace.moveSidebar(-1)
		}
		if m.workspace.sidebarCursor == prevCursor {
			m.statusMessage = "At first item in sidebar."
		} else if node := m.workspace.currentSidebarNode(); node != nil {
			m.statusMessage = "▸ " + truncate(node.Label, 40)
		}
		return true, nil
	case tea.KeyPgDown:
		// EX-291: give boundary feedback when the page scroll hits the last item.
		// EX-413: symmetric empty-sidebar guard.
		if len(m.workspace.visibleSidebarIDs()) == 0 {
			m.statusMessage = "No items in sidebar."
			return true, nil
		}
		prevCursor := m.workspace.sidebarCursor
		for i := 0; i < chatScrollStepLines; i++ {
			m.workspace.moveSidebar(1)
		}
		if m.workspace.sidebarCursor == prevCursor {
			m.statusMessage = "At last item in sidebar."
		} else if node := m.workspace.currentSidebarNode(); node != nil {
			m.statusMessage = "▸ " + truncate(node.Label, 40)
		}
		return true, nil
	case tea.KeyBackspace:
		// EX-358: Backspace in sidebar mirrors ← (collapse/navigate to parent),
		// the natural "go back" gesture in tree views (mirrors EX-353 for MainPanel).
		return m.handleSidebarControlKey(tea.KeyMsg{Type: tea.KeyLeft})
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
		// EX-317: if there are no messages yet, don't pretend we scrolled.
		if len(m.chatMessages) == 0 {
			m.statusMessage = "No messages yet."
			return true, nil
		}
		// EX-431: symmetric with EX-430 ('g'/Home sentinel guard) — when
		// chatScrollOffset is already at the sentinel (1<<20), PgUp would
		// increment it further without changing the view; say "Already at
		// oldest message." instead of the misleading "Chat scrolled up."
		if m.chatScrollOffset >= 1<<20 {
			m.statusMessage = "Already at oldest message."
			return true, nil
		}
		m.scrollChatBy(chatScrollStepLines)
		m.statusMessage = "Chat scrolled up."
		return true, nil
	case tea.KeyPgDown:
		// EX-319: if there are no messages yet, say so first.
		if len(m.chatMessages) == 0 {
			m.statusMessage = "No messages yet."
			return true, nil
		}
		// EX-264: when already at the newest message (offset==0), scrolling
		// down is a no-op — say so instead of showing "Chat scrolled down."
		if m.chatScrollOffset == 0 {
			m.statusMessage = "Already at latest message."
		} else {
			m.scrollChatBy(-chatScrollStepLines)
			m.statusMessage = "Chat scrolled down."
		}
		return true, nil
	case tea.KeyHome:
		// EX-317: if there are no messages yet, don't pretend we scrolled.
		if len(m.chatMessages) == 0 {
			m.statusMessage = "No messages yet."
			return true, nil
		}
		// EX-430: symmetric with EX-264/EX-427/EX-428 — "Already at oldest message."
		// when the user is already scrolled as far up as possible (sentinel 1<<20).
		if m.chatScrollOffset >= 1<<20 {
			m.statusMessage = "Already at oldest message."
			return true, nil
		}
		m.chatScrollOffset = 1 << 20
		m.statusMessage = "Chat scrolled to oldest."
		return true, nil
	case tea.KeyEnd:
		// EX-319: if there are no messages yet, say so first.
		if len(m.chatMessages) == 0 {
			m.statusMessage = "No messages yet."
			return true, nil
		}
		// EX-264: symmetric — End when already at newest is a no-op.
		if m.chatScrollOffset == 0 {
			m.statusMessage = "Already at latest message."
		} else {
			m.chatScrollOffset = 0
			m.statusMessage = "Chat scrolled to latest."
		}
		return true, nil
	case tea.KeyBackspace:
		runes := []rune(m.chatInput)
		if len(runes) > 0 {
			m.chatInput = string(runes[:len(runes)-1])
			// EX-279: editing (backspace) also exits history navigation mode.
			if m.chatHistoryIndex >= 0 {
				m.chatHistoryIndex = -1
			}
			// EX-425: if the user has backspaced through the entire dequeued message,
			// clear editingQueued so the next sent message is not incorrectly marked
			// Edited=true. No separate status message here — backspace is a character-
			// level edit gesture (unlike Esc/Ctrl-U which are explicit cancel gestures).
			if m.editingQueued && strings.TrimSpace(m.chatInput) == "" {
				m.editingQueued = false
			}
		} else {
			// EX-439: Backspace on empty input is a silent no-op — inconsistent with
			// Ctrl-W on empty (EX-326: "Nothing to delete.") and search/command Backspace
			// on empty (EX-432). Give the same honest feedback.
			m.statusMessage = "Nothing to delete."
		}
		return true, nil
	case tea.KeyUp:
		if strings.TrimSpace(m.chatInput) == "" || m.chatHistoryIndex >= 0 {
			m.recallHistory()
			return true, nil
		}
		// EX-354: ↑ with non-empty input (not in history mode) is a silent no-op.
		// Give a hint: history navigation only works when the input box is clear.
		m.statusMessage = "Clear input first (Esc) to browse message history."
		return true, nil
	case tea.KeyDown:
		if strings.TrimSpace(m.chatInput) == "" && m.chatScrollOffset > 0 {
			m.scrollChatBy(-1)
			// EX-438: give feedback consistent with PgDown ("Chat scrolled down.") and
			// End ("Chat scrolled to latest.") — the ↓ single-line scroll was a silent
			// no-op even though all other chat scroll directions set a status message.
			if m.chatScrollOffset == 0 {
				m.statusMessage = "Chat scrolled to latest."
			} else {
				m.statusMessage = "Chat scrolled down."
			}
			return true, nil
		}
		// EX-011: advance through history when in history navigation mode
		if m.chatHistoryIndex >= 0 && m.chatHistoryIndex < len(m.chatHistory) {
			m.forwardHistory()
			return true, nil
		}
		// EX-280: ↓ when past end of history (chatHistoryIndex >= len) or
		// already at the bottom (scrollOffset==0, not scrolling) gives feedback
		// instead of silently doing nothing — mirrors PgDown's "Already at latest."
		// Check history-end before the empty-input guard because forwardHistory
		// sets chatInput="" when going past the end, so the order matters.
		if m.chatHistoryIndex >= len(m.chatHistory) && len(m.chatHistory) > 0 {
			m.statusMessage = "Already at newest message."
			return true, nil
		}
		if strings.TrimSpace(m.chatInput) == "" && m.chatScrollOffset == 0 {
			// EX-320: distinguish "no messages" from "at newest message".
			if len(m.chatMessages) == 0 {
				m.statusMessage = "No messages yet."
			} else {
				m.statusMessage = "Already at latest message."
			}
			return true, nil
		}
		// EX-355: ↓ with non-empty input and not in history mode is a silent no-op.
		// Give a hint: scroll/history navigation only works when input is clear.
		if strings.TrimSpace(m.chatInput) != "" {
			m.statusMessage = "Press Esc to clear input, then ↓ to scroll messages."
			return true, nil
		}
	case tea.KeyEsc:
		if m.activeTurn {
			m.activeTurn = false
			// EX-185: clear activeTurnSessionID so the next turn's
			// chat.turn.started can update it (the guard is "== """).
			m.activeTurnSessionID = ""
			m.statusMessage = "Active turn cancelled."
			return true, requestChatCancelCmd(m.ActiveChatSession())
		}
		// EX-289: Esc with no active turn but non-empty input — clear the draft
		// so the user can dismiss a partially-typed message without sending it.
		// EX-424: if the user had pressed 'e' to edit a dequeued message, also clear
		// editingQueued so the next sent message is not incorrectly marked Edited=true.
		if strings.TrimSpace(m.chatInput) != "" {
			m.chatInput = ""
			m.chatHistoryIndex = -1
			if m.editingQueued {
				m.editingQueued = false
				m.statusMessage = "Edit cancelled — queued message discarded."
			} else {
				m.statusMessage = "Input cleared."
			}
			return true, nil
		}
		// EX-309: Esc with no active turn and empty input — move focus to main panel
		// (natural dismissal gesture, mirrors EX-305 for sidebar).
		// Exception: if ViewHelp is active, let handleEscapeKey close it (EX-205).
		if m.workspace.mainView != ViewHelp {
			m.setFocus(MainPanel)
			m.statusMessage = "Focus: " + panelLabel(m.focus)
			return true, nil
		}
	case tea.KeyLeft, tea.KeyRight:
		// EX-356/357: ← / → in the chat panel normally move the text cursor,
		// but this input does not track a cursor position. Give a hint so the
		// user understands why the key appears to do nothing and learns the
		// available text-editing shortcuts (Ctrl-W, Ctrl-U).
		m.statusMessage = "Text cursor movement not supported. Use Ctrl-W to delete word, Ctrl-U to clear."
		return true, nil
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
		// EX-420: queue actions (e/s/d) should work when activeTurn is true AND when
		// activeTurn is just been set to false by an external chat.turn.cancelled while
		// messages are still queued. Previously the guard required activeTurn=true which
		// meant 'd' after external cancellation navigated to dashboard instead of
		// deleting the queued message (the status message said "Press d to discard" but
		// 'd' didn't actually work).
		if len(m.queuedMessages) > 0 && strings.TrimSpace(m.chatInput) == "" {
			switch strings.ToLower(string(r)) {
			case "e":
				m.applyQueueActionEdit()
				return
			case "s":
				if m.activeTurn {
					// Steer only makes sense when a turn is in progress.
					m.applyQueueActionSteer()
					return
				}
				// EX-421: steer pressed in the queue-action context (queued messages,
				// empty input) but no active turn — the turn was externally cancelled.
				// Give explicit feedback instead of typing 's' into the chat input,
				// which would be silent and contradicts the d/e feedback pattern.
				m.statusMessage = "Steer requires an active turn. Use d·delete or e·edit queued messages."
				return
			case "d":
				m.applyQueueActionDelete()
				return
			}
		}
	}
	// EX-279: typing while in history navigation mode should exit history mode
	// so that the next ↑ starts fresh from the end of the history list, not
	// from the middle where the user happened to be browsing.
	if m.chatHistoryIndex >= 0 {
		m.chatHistoryIndex = -1
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
	// EX-257: align with filter terminology from EX-254; clarify Enter/Esc actions.
	// EX-316: also mention ↑/↓ (EX-313) and Ctrl-U/Ctrl-W (EX-310/EX-314).
	m.statusMessage = "Filter mode: type · ↑/↓ nav · Ctrl-U clear · Ctrl-W del-word · Enter apply · Esc cancel"
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
	case tea.KeyCtrlG:
		// EX-315: Ctrl-G in filter mode exits search and jumps to Frank (mirrors global Ctrl-G).
		m.searchMode = false
		m.searchQuery = ""
		m.setFilterForPanel(m.searchPanel, "")
		return m, m.jumpToFrankSession("Ctrl-G")
	case tea.KeyCtrlP:
		// EX-315: Ctrl-P in filter mode exits search and opens command mode (mirrors global Ctrl-P).
		m.searchMode = false
		m.searchQuery = ""
		m.setFilterForPanel(m.searchPanel, "")
		m.enterCommandMode()
		return m, nil
	case tea.KeyEsc:
		// EX-254: include the query in the message so the user sees what was cleared.
		// EX-433: when the filter query is already empty (no filter to clear), saying
		// "Filter cleared." implies something was cleared when nothing was. Use
		// "Filter mode exited." to accurately describe what happened.
		prev := strings.TrimSpace(m.searchQuery)
		m.setFilterForPanel(m.searchPanel, "")
		m.searchMode = false
		m.searchQuery = ""
		if prev != "" {
			m.statusMessage = fmt.Sprintf("Filter %q cleared.", prev)
		} else {
			m.statusMessage = "Filter mode exited."
		}
		return m, nil
	case tea.KeyTab:
		// EX-389: Tab in search mode commits the current filter and exits search (mirrors Enter).
		// This is the natural "confirm and move on" gesture — users expect Tab to complete/confirm.
		m.setFilterForPanel(m.searchPanel, m.searchQuery)
		m.searchMode = false
		q := strings.TrimSpace(m.searchQuery)
		if q != "" {
			m.statusMessage = fmt.Sprintf("Filter %q applied.", q)
		} else {
			// EX-434: symmetric with EX-433 (Esc) — an empty query means no filter was
			// applied; "Filter cleared." implies a filter existed. Say "Filter mode exited."
			m.statusMessage = "Filter mode exited."
		}
		return m, nil
	case tea.KeyEnter:
		m.setFilterForPanel(m.searchPanel, m.searchQuery)
		m.searchMode = false
		// EX-254: include the query in the confirmation so the user sees what filter is active.
		// EX-434: symmetric with EX-433 (Esc) — empty query → "Filter mode exited." not "Filter cleared."
		q := strings.TrimSpace(m.searchQuery)
		if q != "" {
			m.statusMessage = fmt.Sprintf("Filter %q applied.", q)
		} else {
			m.statusMessage = "Filter mode exited."
		}
		return m, nil
	case tea.KeyBackspace:
		runes := []rune(m.searchQuery)
		if len(runes) > 0 {
			m.searchQuery = string(runes[:len(runes)-1])
		} else {
			// EX-432: symmetric with EX-330 (Ctrl-W) and EX-329 (Ctrl-U) — when the
			// filter query is already empty, Backspace cannot delete further; give
			// honest feedback rather than silently doing nothing.
			m.statusMessage = "Nothing to delete. Press Esc to exit search mode."
		}
		m.setFilterForPanel(m.searchPanel, m.searchQuery)
		return m, nil
	case tea.KeyCtrlU:
		// EX-310: Ctrl-U in filter mode clears the query without exiting filter mode.
		if strings.TrimSpace(m.searchQuery) != "" {
			m.searchQuery = ""
			m.setFilterForPanel(m.searchPanel, "")
			m.statusMessage = "Filter cleared. Continue typing or Esc to exit."
		} else {
			// EX-329: nothing to clear — give honest feedback.
			m.statusMessage = "Filter is empty. Press Esc to exit search mode."
		}
		return m, nil
	case tea.KeyCtrlW:
		// EX-314: Ctrl-W in filter mode deletes the last word (mirrors EX-303 for chat).
		if len(m.searchQuery) > 0 {
			trimmed := strings.TrimRight(m.searchQuery, " \t")
			lastSpace := strings.LastIndexAny(trimmed, " \t")
			if lastSpace >= 0 {
				m.searchQuery = trimmed[:lastSpace+1]
			} else {
				m.searchQuery = ""
			}
			m.setFilterForPanel(m.searchPanel, m.searchQuery)
			m.statusMessage = "Last word deleted."
		} else {
			// EX-330: nothing to delete — give honest feedback.
			m.statusMessage = "Nothing to delete. Press Esc to exit search mode."
		}
		return m, nil
	case tea.KeyCtrlA:
		// EX-374: Ctrl-A (line-start) — cursor movement not supported in filter field.
		m.statusMessage = "Line-start (Ctrl-A) not supported. Use Ctrl-U to clear, Ctrl-W to delete word."
		return m, nil
	case tea.KeyCtrlE:
		// EX-374: Ctrl-E (line-end) — cursor movement not supported in filter field.
		m.statusMessage = "Line-end (Ctrl-E) not supported. Type to append to query, Ctrl-W to delete word."
		return m, nil
	case tea.KeyCtrlB, tea.KeyCtrlF:
		// EX-379: Ctrl-B/F (char backward/forward) — cursor movement not supported in filter.
		m.statusMessage = "Cursor movement (Ctrl-B/F) not supported. Use Ctrl-W to delete word, Ctrl-U to clear."
		return m, nil
	case tea.KeyCtrlD:
		// EX-379: Ctrl-D (delete char forward) — no cursor concept in filter field.
		m.statusMessage = "Forward-delete (Ctrl-D) not supported. Use Backspace or Ctrl-W."
		return m, nil
	case tea.KeyCtrlK:
		// EX-379: Ctrl-K (kill to end of line) — no cursor concept; use Ctrl-U to clear all.
		m.statusMessage = "Kill-to-end (Ctrl-K) not supported. Use Ctrl-U to clear, Ctrl-W to delete word."
		return m, nil
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown:
		// EX-313: ↑/↓ in filter mode commits the filter and then navigates the list,
		// so the user can type a query and immediately scroll results without pressing Enter first.
		// EX-360: PgUp/PgDn mirror this behaviour — commit filter + page through results.
		m.setFilterForPanel(m.searchPanel, m.searchQuery)
		m.searchMode = false
		q := strings.TrimSpace(m.searchQuery)
		if q != "" {
			m.statusMessage = fmt.Sprintf("Filter %q applied.", q)
		}
		// Delegate to normal key handling to perform the navigation.
		result, cmd := m.updateKey(key)
		return result, cmd
	case tea.KeyLeft, tea.KeyRight:
		// EX-370: ← / → in filter mode — cursor movement not supported in query field.
		m.statusMessage = "Text cursor movement not supported. Use Ctrl-W to delete word, Ctrl-U to clear."
		return m, nil
	case tea.KeyHome:
		// EX-370: Home in filter mode — give a hint matching the ← hint.
		m.statusMessage = "Line-start (Home) not supported. Use Ctrl-U to clear the filter."
		return m, nil
	case tea.KeyEnd:
		// EX-370: End in filter mode — give a hint matching the → hint.
		m.statusMessage = "Line-end (End) not supported. Type to extend the filter."
		return m, nil
	case tea.KeyDelete:
		// EX-370: Delete (forward delete) in filter mode — no cursor concept.
		m.statusMessage = "Forward-delete not supported in filter mode. Use Backspace or Ctrl-W."
		return m, nil
	case tea.KeyCtrlY:
		// EX-387: Ctrl-Y (emacs yank/paste) in search mode — use terminal paste instead.
		m.statusMessage = "Ctrl-Y: use terminal paste (right-click or Ctrl-Shift-V) to paste text."
		return m, nil
	case tea.KeyCtrlZ:
		// EX-387: Ctrl-Z (suspend) in search mode — not safe inside a TUI.
		m.statusMessage = "Ctrl-Z: suspend not recommended. Press Esc to exit search mode."
		return m, nil
	case tea.KeyCtrlL:
		// EX-394: Ctrl-L (redraw) in search mode — screen is auto-managed.
		m.statusMessage = "Ctrl-L: screen redraw is automatic. Press Esc to exit filter mode."
		return m, nil
	case tea.KeyCtrlN:
		// EX-394: Ctrl-N (next) in search mode — use ↓ to navigate.
		m.statusMessage = "Ctrl-N: use ↓ (commits filter + navigates) to go to next result."
		return m, nil
	case tea.KeyCtrlO:
		// EX-394: Ctrl-O in search mode — not bound.
		m.statusMessage = "Ctrl-O: not bound. Press Enter to apply filter, Esc to cancel."
		return m, nil
	case tea.KeyCtrlQ:
		// EX-394: Ctrl-Q in search mode — not bound.
		m.statusMessage = "Ctrl-Q: not bound. Press Esc to cancel filter mode."
		return m, nil
	case tea.KeyCtrlR:
		// EX-394: Ctrl-R (reverse search) in search mode — not supported.
		m.statusMessage = "Ctrl-R: reverse search not supported. Type to filter, ↑/↓ to navigate."
		return m, nil
	case tea.KeyCtrlS:
		// EX-394: Ctrl-S (save) in search mode — no save needed.
		m.statusMessage = "Ctrl-S: no save needed. Press Enter to apply filter."
		return m, nil
	case tea.KeyCtrlT:
		// EX-394: Ctrl-T (transpose) in search mode — not supported.
		m.statusMessage = "Ctrl-T: not bound in filter mode. Type to search, Esc to cancel."
		return m, nil
	case tea.KeyCtrlV:
		// EX-394: Ctrl-V in search mode — use terminal paste.
		m.statusMessage = "Ctrl-V: use terminal paste (right-click or Ctrl-Shift-V) to paste text."
		return m, nil
	case tea.KeyCtrlX:
		// EX-394: Ctrl-X in search mode — not bound.
		m.statusMessage = "Ctrl-X: not bound. Use Ctrl-U to clear filter, Esc to cancel."
		return m, nil
	case tea.KeyF1:
		// EX-394: F1 in search mode — Esc first, then ? for help.
		m.statusMessage = "Press Esc to exit filter, then ? for help."
		return m, nil
	case tea.KeyF2, tea.KeyF3, tea.KeyF4, tea.KeyF5, tea.KeyF6, tea.KeyF7, tea.KeyF8, tea.KeyF9, tea.KeyF10, tea.KeyF11, tea.KeyF12:
		// EX-394: F2-F12 in search mode — not bound.
		m.statusMessage = "F-key not bound in filter mode. Type to search, Enter to apply, Esc to cancel."
		return m, nil
	case tea.KeyInsert:
		// EX-394: Insert in search mode — no insert/overwrite mode.
		m.statusMessage = "Insert: no overwrite mode. Type normally to add characters."
		return m, nil
	case tea.KeyCtrlUp, tea.KeyCtrlDown, tea.KeyCtrlLeft, tea.KeyCtrlRight:
		// EX-394: Ctrl+arrows in search mode — commit filter, then navigate.
		// EX-435: align empty-query message with EX-433/434 ("Filter mode exited.").
		m.setFilterForPanel(m.searchPanel, m.searchQuery)
		m.searchMode = false
		q := strings.TrimSpace(m.searchQuery)
		if q != "" {
			m.statusMessage = fmt.Sprintf("Filter %q applied.", q)
		} else {
			m.statusMessage = "Filter mode exited."
		}
		result, cmd := m.updateKey(key)
		return result, cmd
	case tea.KeyCtrlHome, tea.KeyCtrlEnd, tea.KeyCtrlPgUp, tea.KeyCtrlPgDown:
		// EX-394: Ctrl+Home/End/PgUp/PgDown in search mode — commit filter, then navigate.
		// EX-435: align empty-query message with EX-433/434 ("Filter mode exited.").
		m.setFilterForPanel(m.searchPanel, m.searchQuery)
		m.searchMode = false
		q := strings.TrimSpace(m.searchQuery)
		if q != "" {
			m.statusMessage = fmt.Sprintf("Filter %q applied.", q)
		} else {
			m.statusMessage = "Filter mode exited."
		}
		result, cmd := m.updateKey(key)
		return result, cmd
	case tea.KeyShiftUp, tea.KeyShiftDown, tea.KeyShiftLeft, tea.KeyShiftRight, tea.KeyShiftHome, tea.KeyShiftEnd:
		// EX-394: Shift+navigation in search mode — text selection not supported.
		m.statusMessage = "Text selection not supported in filter mode. Use Ctrl-U to clear."
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
		// EX-403: any key type not explicitly handled in search mode (e.g.
		// rare Ctrl sequences) — give a redirect hint.
		m.statusMessage = "Key not bound in filter mode. Type to search, Enter to apply, Esc to cancel."
		return m, nil
	}
}

func (m Model) updateCommandInput(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyCtrlC:
		// EX-363: Ctrl-C in command mode should quit the TUI (mirrors global Ctrl-C),
		// not silently do nothing while leaving the user stuck in command mode.
		m.quitting = true
		m.commandMode = false
		m.statusMessage = "Exiting TUI."
		return m, tea.Quit
	case tea.KeyCtrlG:
		m.commandMode = false
		m.commandBuffer = ""
		// EX-165: use the returned cmd to reload history.
		return m, m.jumpToFrankSession("Ctrl-G")
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
	case tea.KeyTab:
		// EX-228: Tab fills the top command palette suggestion into the buffer.
		// EX-275: when there are no matching suggestions, give feedback instead
		// of silently doing nothing — the user pressed Tab expecting a completion.
		if suggestions := m.commandPaletteSuggestions(1); len(suggestions) > 0 {
			m.commandBuffer = ":" + suggestions[0]
		} else {
			m.statusMessage = "No matching commands."
		}
		return m, nil
	case tea.KeyBackspace:
		runes := []rune(m.commandBuffer)
		if len(runes) > 1 {
			m.commandBuffer = string(runes[:len(runes)-1])
		} else {
			// EX-432: symmetric with EX-328 (Ctrl-W) and EX-327 (Ctrl-U) — when the
			// command buffer is already at the ":" prompt, Backspace cannot delete
			// further; give honest feedback rather than silently doing nothing.
			m.commandBuffer = ":"
			m.statusMessage = "Nothing to delete."
		}
		return m, nil
	case tea.KeyCtrlU:
		// EX-311: Ctrl-U in command mode clears the buffer back to just ":" (the prompt).
		if m.commandBuffer != ":" {
			m.commandBuffer = ":"
			m.statusMessage = "Command cleared."
		} else {
			// EX-327: nothing to clear — give honest feedback.
			m.statusMessage = "Nothing to clear."
		}
		return m, nil
	case tea.KeyCtrlW:
		// EX-314: Ctrl-W in command mode deletes the last word (mirrors EX-303 for chat).
		// Always keep the ":" prompt prefix.
		suffix := strings.TrimPrefix(m.commandBuffer, ":")
		if len(suffix) > 0 {
			trimmed := strings.TrimRight(suffix, " \t")
			lastSpace := strings.LastIndexAny(trimmed, " \t")
			if lastSpace >= 0 {
				m.commandBuffer = ":" + trimmed[:lastSpace+1]
			} else {
				m.commandBuffer = ":"
			}
			m.statusMessage = "Last word deleted."
		} else {
			// EX-328: nothing to delete — give honest feedback.
			m.statusMessage = "Nothing to delete."
		}
		return m, nil
	case tea.KeyCtrlA:
		// EX-375: Ctrl-A (line-start) — cursor movement not supported in command buffer.
		m.statusMessage = "Line-start (Ctrl-A) not supported. Use Ctrl-U to clear, Ctrl-W to delete word."
		return m, nil
	case tea.KeyCtrlE:
		// EX-375: Ctrl-E (line-end) — cursor movement not supported in command buffer.
		m.statusMessage = "Line-end (Ctrl-E) not supported. Type to append, Ctrl-W to delete word."
		return m, nil
	case tea.KeyCtrlB, tea.KeyCtrlF:
		// EX-379: Ctrl-B/F (char backward/forward) — cursor movement not supported in command buffer.
		m.statusMessage = "Cursor movement (Ctrl-B/F) not supported. Use Ctrl-W to delete word, Ctrl-U to clear."
		return m, nil
	case tea.KeyCtrlD:
		// EX-379: Ctrl-D (delete char forward) — no cursor in command buffer.
		if m.commandBuffer == ":" {
			m.statusMessage = "Ctrl-D: nothing to delete. Press Esc to cancel command mode."
		} else {
			m.statusMessage = "Forward-delete (Ctrl-D) not supported. Use Backspace or Ctrl-W."
		}
		return m, nil
	case tea.KeyCtrlK:
		// EX-379: Ctrl-K (kill to end of line) — no cursor; use Ctrl-U to clear all.
		m.statusMessage = "Kill-to-end (Ctrl-K) not supported. Use Ctrl-U to clear, Ctrl-W to delete word."
		return m, nil
	case tea.KeySpace:
		m.commandBuffer += " "
		return m, nil
	case tea.KeyLeft, tea.KeyRight:
		// EX-361: ← / → in command mode — text cursor movement is not supported
		// in the command buffer. Inform the user rather than silently doing nothing.
		m.statusMessage = "Text cursor movement not supported. Use Ctrl-W to delete word, Ctrl-U to clear."
		return m, nil
	case tea.KeyUp:
		// EX-362: ↑ in command mode — command history recall not yet supported.
		// Give a helpful hint pointing to the available alternatives.
		m.statusMessage = "Command history not supported. Use Tab to autocomplete."
		return m, nil
	case tea.KeyDown:
		// EX-371: ↓ in command mode — symmetric with ↑ (EX-362), no history.
		m.statusMessage = "Command history not supported. Use Tab to autocomplete."
		return m, nil
	case tea.KeyHome, tea.KeyEnd:
		// EX-371: Home/End in command mode — cursor movement not supported.
		m.statusMessage = "Cursor movement not supported. Use Ctrl-W to delete word, Ctrl-U to clear."
		return m, nil
	case tea.KeyDelete:
		// EX-371: Delete (forward delete) in command mode — no cursor concept.
		m.statusMessage = "Forward-delete not supported. Use Backspace or Ctrl-W to delete word."
		return m, nil
	case tea.KeyCtrlY:
		// EX-387: Ctrl-Y (emacs yank/paste) in command mode — use terminal paste instead.
		m.statusMessage = "Ctrl-Y: use terminal paste (right-click or Ctrl-Shift-V) to paste."
		return m, nil
	case tea.KeyCtrlZ:
		// EX-387: Ctrl-Z in command mode — not safe. Exit command mode gracefully instead.
		m.statusMessage = "Ctrl-Z: suspend not recommended. Press Esc to cancel command mode."
		return m, nil
	case tea.KeyCtrlL:
		// EX-394: Ctrl-L (redraw) in command mode — screen is auto-managed.
		m.statusMessage = "Ctrl-L: screen redraw is automatic. Press Esc to cancel command mode."
		return m, nil
	case tea.KeyCtrlN:
		// EX-394: Ctrl-N in command mode — use ↓ for command history (not supported yet).
		m.statusMessage = "Ctrl-N: command history not supported. Use Tab to autocomplete."
		return m, nil
	case tea.KeyCtrlO:
		// EX-394: Ctrl-O in command mode — not bound.
		m.statusMessage = "Ctrl-O: not bound. Type a command or press Esc to cancel."
		return m, nil
	case tea.KeyCtrlQ:
		// EX-394: Ctrl-Q in command mode — not bound.
		m.statusMessage = "Ctrl-Q: not bound. Press Esc to cancel command mode."
		return m, nil
	case tea.KeyCtrlR:
		// EX-394: Ctrl-R (reverse search) in command mode — not supported.
		m.statusMessage = "Ctrl-R: reverse search not supported. Use Tab to autocomplete commands."
		return m, nil
	case tea.KeyCtrlS:
		// EX-394: Ctrl-S (save) in command mode — no save needed.
		m.statusMessage = "Ctrl-S: no save needed. Use :w or Esc to exit command mode."
		return m, nil
	case tea.KeyCtrlT:
		// EX-394: Ctrl-T (transpose) in command mode — not supported.
		m.statusMessage = "Ctrl-T: not bound in command mode. Type a command or press Esc."
		return m, nil
	case tea.KeyCtrlV:
		// EX-394: Ctrl-V in command mode — use terminal paste.
		m.statusMessage = "Ctrl-V: use terminal paste (right-click or Ctrl-Shift-V) to paste."
		return m, nil
	case tea.KeyCtrlX:
		// EX-394: Ctrl-X in command mode — not bound.
		m.statusMessage = "Ctrl-X: not bound. Use Ctrl-U to clear command, Esc to cancel."
		return m, nil
	case tea.KeyF1:
		// EX-394: F1 in command mode — Esc first, then ? for help.
		m.statusMessage = "Press Esc to exit command mode, then ? for help."
		return m, nil
	case tea.KeyF2, tea.KeyF3, tea.KeyF4, tea.KeyF5, tea.KeyF6, tea.KeyF7, tea.KeyF8, tea.KeyF9, tea.KeyF10, tea.KeyF11, tea.KeyF12:
		// EX-394: F2-F12 in command mode — not bound.
		m.statusMessage = "F-key not bound in command mode. Type a command or press Esc to cancel."
		return m, nil
	case tea.KeyInsert:
		// EX-394: Insert in command mode — no overwrite mode.
		m.statusMessage = "Insert: no overwrite mode in command mode. Type normally."
		return m, nil
	case tea.KeyCtrlUp, tea.KeyCtrlDown, tea.KeyCtrlLeft, tea.KeyCtrlRight:
		// EX-394: Ctrl+arrows in command mode — cursor movement not supported.
		m.statusMessage = "Ctrl+arrows not supported. Use Ctrl-W to delete word, Ctrl-U to clear."
		return m, nil
	case tea.KeyCtrlHome, tea.KeyCtrlEnd:
		// EX-394: Ctrl+Home/End in command mode — cursor movement not supported.
		m.statusMessage = "Cursor movement not supported. Use Ctrl-U to clear, Esc to cancel."
		return m, nil
	case tea.KeyCtrlPgUp, tea.KeyCtrlPgDown:
		// EX-394: Ctrl+PgUp/PgDn in command mode — not bound.
		m.statusMessage = "Ctrl+PgUp/PgDn not bound in command mode. Press Esc to cancel."
		return m, nil
	case tea.KeyShiftUp, tea.KeyShiftDown, tea.KeyShiftLeft, tea.KeyShiftRight, tea.KeyShiftHome, tea.KeyShiftEnd:
		// EX-394: Shift+navigation in command mode — text selection not supported.
		m.statusMessage = "Text selection not supported. Use Ctrl-W to delete word, Ctrl-U to clear."
		return m, nil
	case tea.KeyPgUp, tea.KeyPgDown:
		// EX-394: PgUp/PgDn in command mode — not meaningful for command buffer navigation.
		m.statusMessage = "PgUp/PgDn not supported in command mode. Press Esc to exit and navigate."
		return m, nil
	case tea.KeyRunes:
		if len(key.Runes) > 0 {
			m.commandBuffer += string(key.Runes)
		}
		return m, nil
	default:
		// EX-403: any key type not explicitly handled in command mode (e.g.
		// rare Ctrl sequences like Ctrl-@, Ctrl-\, Ctrl-]) — give a redirect
		// hint rather than silently dropping the keystroke.
		m.statusMessage = "Key not bound in command mode. Type a command, Tab to autocomplete, or Esc to cancel."
		return m, nil
	}
}

func (m *Model) executeCommand(raw string) tea.Cmd {
	trimmed := strings.TrimSpace(strings.TrimPrefix(raw, ":"))
	if trimmed == "" {
		m.statusMessage = "No command entered."
		return nil
	}

	// EX-228: handle "project: <name>", "session: <name>", "task: <title>" patterns
	// that the command palette produces when it autocompletes sidebar items.
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "project: ") {
		name := strings.TrimSpace(trimmed[len("project: "):])
		return m.jumpToProjectByName(name)
	}
	if strings.HasPrefix(lower, "session: ") {
		name := strings.TrimSpace(trimmed[len("session: "):])
		return m.jumpToSessionByName(name)
	}
	if strings.HasPrefix(lower, "task: ") {
		title := strings.TrimSpace(trimmed[len("task: "):])
		return m.jumpToTaskByTitle(title)
	}
	// EX-228: "cmd: frank" etc. produced by commandPaletteSuggestions — strip the "cmd: " prefix.
	if strings.HasPrefix(lower, "cmd: ") {
		trimmed = strings.TrimSpace(trimmed[len("cmd: "):])
		lower = strings.ToLower(trimmed)
	}
	// EX-269: guard against empty command after "cmd: " prefix stripping (e.g. ":cmd: ").
	if trimmed == "" {
		m.statusMessage = "No command entered."
		return nil
	}

	fields := strings.Fields(trimmed)
	if len(fields) > 1 && strings.EqualFold(fields[0], "inbox") {
		// EX-160: executeInboxCommand now returns a cmd for server-side API calls.
		return m.executeInboxCommand(fields[1:])
	}

	switch strings.ToLower(fields[0]) {
	case "quit", "q", "q!", "wq", "wqa", "qa", "qa!", "x", "exit":
		// EX-390: common vim/shell quit aliases: :q :q! :wq :wqa :qa :qa! :x :exit all quit.
		m.quitting = true
		m.statusMessage = "Exiting TUI."
	case "w", "wa", "write":
		// EX-390: :w (vim write) — no save needed; all changes persist automatically.
		m.statusMessage = "No save needed — changes are persisted automatically."
	case "e", "edit":
		// EX-391: :e/:edit (vim: open file for editing) — request edits via chat instead.
		m.statusMessage = ":edit not supported. Use chat (3 or Tab) to ask the agent to make edits."
	case "n", "next":
		// EX-391: :n/:next (vim: next file/buffer) — jump to next unread session.
		if nextID := m.workspace.nextUnreadSession(); nextID != "" {
			visible := m.workspace.visibleSidebarIDs()
			for i, id := range visible {
				if id == nextID {
					m.workspace.sidebarCursor = i
					break
				}
			}
			m.workspace.selectSidebarNode()
			sessionID := m.workspace.activeSessionID
			m.clearTurnIfSwitchingSession(sessionID)
			m.activeSession = sessionID
			m.chatScrollOffset = 0
			m.statusMessage = "Jumped to next unread session."
			m.setFocus(SidebarPanel)
			if looksLikeUUID(sessionID) && m.runtimeHints.LoadChatHistory != nil {
				m.chatMessages = nil
				m.chatHistoryLoading = true
				m.chatMessageIndex = make(map[string]int)
				return loadChatHistoryCmd(sessionID, m.runtimeHints.LoadChatHistory)
			}
		} else {
			m.statusMessage = "No unread sessions."
		}
	case "reconnect", "connect":
		// EX-406: :reconnect/:connect — manually trigger a sidebar data refresh,
		// which causes the SSE stream to reconnect if it has dropped. Useful when
		// the degraded-mode banner is shown and the user wants to retry immediately
		// rather than waiting for the auto-reconnect interval.
		m.statusMessage = "Reconnecting…"
		m.workspace.activity = appendActivity(m.workspace.activity,
			"manual reconnect at "+m.now().Format("15:04:05"))
		return loadSidebarDataCmd(m.runtimeHints)
	case "status", "info", "debug":
		// EX-407: :status/:info/:debug — show a diagnostic summary of the current
		// connection, session, scope, and turn state. Useful for troubleshooting.
		connLabel := "connected"
		switch m.connection {
		case ConnectionDisconnected:
			connLabel = "disconnected"
		case ConnectionReconnecting:
			connLabel = "reconnecting"
		}
		if m.streamDegraded {
			connLabel += " (degraded)"
		}
		scopeLabel := "org"
		switch m.activeScope {
		case ScopeProject:
			scopeLabel = "project"
		case ScopeTask:
			scopeLabel = "task"
		}
		turnLabel := "idle"
		if m.activeTurn {
			turnLabel = fmt.Sprintf("active (queued: %d)", len(m.queuedMessages))
		}
		sessionLabel := truncate(m.workspace.sessionLabel(m.ActiveChatSession()), 30)
		if sessionLabel == "" {
			sessionLabel = truncate(m.ActiveChatSession(), 30)
		}
		m.statusMessage = fmt.Sprintf("conn:%s  scope:%s  session:%s  turn:%s",
			connLabel, scopeLabel, sessionLabel, turnLabel)
	case "clear", "cls":
		// EX-391: :clear/:cls (shell clear screen) — screen is managed by the TUI.
		m.statusMessage = "Screen is managed automatically. Use r to refresh data."
	case "ls", "list":
		// EX-391: :ls (vim list buffers / shell list) — point to sidebar.
		m.statusMessage = "Use the sidebar (1 to focus) to browse sessions and projects."
	case "reload", "refresh":
		// EX-391: :reload/:refresh — mirror the 'r' key behaviour.
		if m.workspace.mainView == ViewHelp {
			m.statusMessage = "r·refresh not available in help view. Press Esc to close."
			return nil
		}
		m.statusMessage = "Refreshing…"
		m.workspace.activity = appendActivity(m.workspace.activity,
			"sidebar refreshed at "+m.now().Format("15:04:05"))
		return loadSidebarDataCmd(m.runtimeHints)
	case "sp", "split", "vs", "vsplit", "new", "tabnew":
		// EX-391: split/window commands — not supported in this single-pane TUI.
		m.statusMessage = "Split windows not supported. Use 1/2/3 or Tab to cycle panels."
	case "search", "find", "filter":
		// EX-392: :search/:find/:filter — enter search/filter mode on the focused panel.
		if m.focus == ChatPanel {
			m.statusMessage = "Search not available in chat. Use / in sidebar or main panel (1 or 2 to focus)."
			return nil
		}
		if m.focus == MainPanel && m.workspace.mainView == ViewHelp {
			m.statusMessage = "Search not available in help view. Press j/k to scroll."
			return nil
		}
		m.enterSearchMode(m.focus)
		m.statusMessage = "Filter mode — type to filter, Enter to apply, Esc to cancel."
	case "back":
		// EX-392: :back — navigate back (same as Esc in main panel).
		return m.handleEscapeKey()
	case "sort":
		// EX-392: :sort — no sort support yet.
		m.statusMessage = ":sort not supported. Use j/k to navigate, r to refresh."
	case "history":
		// EX-392: :history — redirect to chat history navigation.
		m.statusMessage = "Use ↑/↓ in chat panel (3 or Tab) to browse message history."
	case "help", "palette":
		// EX-239: open the help screen directly (same as ?) rather than showing a
		// truncated status message. The old statusMessage approach was cut off at 80
		// chars (~5 commands visible) which defeated the purpose of a quick reference.
		m.workspace.setMainView(ViewHelp)
		m.setFocus(MainPanel)
		m.helpScrollOffset = 0
		m.statusMessage = "Keybinding reference. Press ? or Esc to close."
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
		// EX-165: jumpToFrankSession now returns a cmd to reload history.
		return m.jumpToFrankSession(":" + strings.ToLower(fields[0]))
	case "scope":
		if len(fields) != 2 {
			m.statusMessage = "Usage: :scope org|project|task"
			return nil
		}
		// EX-244: validate scope argument before switching so an unrecognised value
		// (e.g. `:scope workspace`) does not silently map to project scope.
		arg := strings.ToLower(strings.TrimSpace(fields[1]))
		switch arg {
		case string(ScopeOrg), string(ScopeProject), string(ScopeTask):
			// valid
		default:
			m.statusMessage = fmt.Sprintf("Unknown scope %q. Use: org, project, or task.", fields[1])
			return nil
		}
		// EX-164: switchScope returns a history-reload cmd; don't discard it.
		return m.switchScope(normalizeScope(fields[1]))
	case "send":
		// EX-238: if the user typed `:send <message>`, use that text as the input.
		// Otherwise fall back to whatever is in m.chatInput.
		if len(fields) > 1 {
			m.chatInput = strings.TrimSpace(strings.Join(fields[1:], " "))
		}
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
		// EX-242: give explicit feedback when the queue is empty instead of
		// silently no-oping — the user typed the command expecting something to happen.
		if len(m.queuedMessages) == 0 {
			m.statusMessage = "No messages queued."
			return nil
		}
		switch strings.ToLower(fields[1]) {
		case "edit":
			m.applyQueueActionEdit()
		case "steer":
			// EX-423: steer only makes sense when a turn is in progress (mirrors
			// EX-421 fix in handleChatRunes). Warn the user instead of silently
			// marking the message with a flag that will have no effect.
			if !m.activeTurn {
				m.statusMessage = "Steer requires an active turn. Use :queue edit or :queue delete."
				return nil
			}
			m.applyQueueActionSteer()
		case "delete", "drop":
			m.applyQueueActionDelete()
		default:
			m.statusMessage = "Unknown queue action. Use edit, steer, or delete."
		}
	case "sidebar":
		// EX-168: executeSidebarCommand now returns a cmd (e.g. history reload on select).
		return m.executeSidebarCommand(fields[1:])
	case "session":
		// EX-240: `:session <name>` should jump to a session by name (same as the
		// autocomplete `session: <name>` format). Without a name, show usage.
		if len(fields) > 1 {
			return m.jumpToSessionByName(strings.Join(fields[1:], " "))
		}
		m.statusMessage = "Usage: :session <name>  (Tab to autocomplete)"
	case "project":
		// EX-240: `:project <name>` with a name should jump to that project.
		// Without a name, just switch to the project view (existing behaviour).
		if len(fields) > 1 {
			return m.jumpToProjectByName(strings.Join(fields[1:], " "))
		}
		m.workspace.setMainView(ViewProject)
		m.setFocus(MainPanel)
		// EX-260: match keyboard shortcut 'p' → "Project view" for consistency.
		m.statusMessage = "Project view"
	case "task":
		// EX-240: `:task <title>` with a title should jump to that task.
		// Without a title, just switch to the task view (existing behaviour).
		if len(fields) > 1 {
			return m.jumpToTaskByTitle(strings.Join(fields[1:], " "))
		}
		m.workspace.setMainView(ViewTask)
		m.setFocus(MainPanel)
		// EX-260: match keyboard shortcut 't' → "Task detail" for consistency.
		m.statusMessage = "Task detail"
	case "dashboard", "inbox", "activity", "agents", "merges", "schedules":
		view, ok := resolveMainViewCommand(fields[0])
		if !ok {
			m.statusMessage = "Unknown workspace command: " + fields[0]
			return nil
		}
		m.workspace.setMainView(view)
		// EX-260: use Title-cased view name to match keyboard shortcut messages
		// ('i' → "Inbox", 'd' → "Dashboard") rather than "Main view: inbox".
		m.statusMessage = viewNavLabel(view)
		// EX-170: load data for views that need a fresh fetch (consistent with
		// keyboard shortcut behaviour: 'i' loads inbox, ViewAgents loads agents).
		switch view {
		case ViewInbox:
			return loadInboxItemsCmd(m.runtimeHints)
		case ViewAgents:
			return loadAgentsCmd(m.runtimeHints)
		}
	case "agent":
		// EX-395: :agent (singular) → same as :agents (plural).
		m.workspace.setMainView(ViewAgents)
		m.setFocus(MainPanel)
		m.statusMessage = viewNavLabel(ViewAgents)
		return loadAgentsCmd(m.runtimeHints)
	case "chat":
		// EX-395: :chat — focus the chat panel (same as pressing 3 or Tab).
		m.setFocus(ChatPanel)
		m.statusMessage = "Chat panel focused. Type your message and press Enter to send."
	case "settings", "config", "preferences", "prefs":
		// EX-395: :settings/:config — no settings UI yet; redirect to CLAUDE.md or env.
		m.statusMessage = ":settings not available in TUI. Edit .env to configure model profiles."
	case "version", "ver":
		// EX-395: :version — display build info hint.
		m.statusMessage = "Run `ottercamp version` in a terminal to see version info."
	case "undo", "redo":
		// EX-395: :undo/:redo — no undo/redo in this TUI; redirect to chat.
		m.statusMessage = "Undo/redo not supported. Ask the agent via chat (3 or Tab) to revert changes."
	case "copy", "yank":
		// EX-395: :copy/:yank — use terminal clipboard.
		m.statusMessage = "Copy not supported. Use your terminal to select and copy text."
	case "paste":
		// EX-395: :paste — use terminal paste.
		m.statusMessage = "Paste not supported as a command. Use terminal paste (right-click or Ctrl-Shift-V)."
	case "open":
		// EX-395: :open — open the selected item (same as Enter or 'o').
		return m.handleEnterKey()
	case "close":
		// EX-395: :close — no tabs/windows to close; navigate back or dismiss help.
		if m.workspace.mainView == ViewHelp {
			m.workspace.setMainView(ViewDashboard)
			m.statusMessage = "Returned to dashboard."
		} else {
			m.statusMessage = ":close not applicable. Use Esc to go back or :quit to exit."
		}
	case "man", "manual":
		// EX-395: :man/:manual — redirect to built-in help.
		m.workspace.setMainView(ViewHelp)
		m.setFocus(MainPanel)
		m.helpScrollOffset = 0
		m.statusMessage = "Keybinding reference. Press ? or Esc to close."
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
	// EX-400: if the session ID is still a placeholder (e.g. "session-org-general"),
	// the real UUID hasn't arrived from the server yet. Sending now would produce
	// "Message sent." → API error → "Send failed (input restored)." which is
	// misleading. Block early with an honest message so the user waits or refreshes.
	if !m.activeTurn {
		sessionID := m.ActiveChatSession()
		if !looksLikeUUID(sessionID) {
			m.statusMessage = "Session loading — please wait a moment, then try again (r to refresh)."
			return nil
		}
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
		// EX-243: give feedback instead of silently doing nothing so the user
		// understands why ↑ didn't restore a previous message.
		m.statusMessage = "No message history."
		return
	}
	if m.chatHistoryIndex < 0 {
		m.chatHistoryIndex = len(m.chatHistory)
	}
	if m.chatHistoryIndex > 0 {
		m.chatHistoryIndex--
		m.chatInput = m.chatHistory[m.chatHistoryIndex]
		m.statusMessage = "Recalled previous message."
	} else {
		// EX-253: already at the oldest message — say so instead of silently
		// setting the input to the same value again with the same status message.
		m.chatInput = m.chatHistory[0]
		m.statusMessage = "Already at oldest message."
	}
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
		// EX-256: "Cleared chat input." was confusing — the user pressed ↓ past the
		// newest history entry; they're back to composing a new message, not "clearing".
		m.statusMessage = "Back to new message."
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

// applyChatEnvelope processes a chat SSE event and returns a tea.Cmd when one
// is needed (e.g. to send a promoted queued message to the server — EX-163).
func (m *Model) applyChatEnvelope(event EventEnvelope) tea.Cmd {
	switch event.EventType {
	case "chat.message.delta", "chat.message.chunk":
		// Skip streaming delta events during replay — we only show finalized snapshots
		if !m.turnsSynced {
			return nil
		}
		var payload struct {
			MessageID string `json:"message_id"`
			SessionID string `json:"session_id"`
			Role      string `json:"role"`
			Delta     string `json:"delta"`
		}
		if !decodePayload(event.Payload, &payload) {
			return nil
		}
		if !m.activeTurn {
			return nil
		}
		if !m.sessionMatchesActive(payload.SessionID) {
			return nil
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
			return nil
		}
		if strings.EqualFold(strings.TrimSpace(payload.Role), "tool_result") {
			m.attachToolResult(strings.TrimSpace(payload.ToolCallID), payload.Content)
			return nil
		}
		// During SSE replay, skip finalized events entirely. History is loaded
		// from the REST API via LoadChatHistory (triggered by ReplaySyncedMsg).
		// SSE replay covers all org-level events and would otherwise inject
		// messages from archived/other sessions into the active chat panel.
		if !m.turnsSynced {
			return nil
		}
		// In live mode: skip user messages entirely — they are already shown
		// optimistically via appendMessage("local-user") when the user sends.
		// Processing them here would (a) create a duplicate entry and (b) call
		// completeTurnAndPromoteQueue prematurely, resetting activeTurn=false
		// before the agent turn even begins — which drops streaming chunks.
		if strings.EqualFold(strings.TrimSpace(payload.Role), "user") {
			return nil
		}
		if !m.sessionMatchesActive(payload.SessionID) {
			// Non-active assistant message → mark the sidebar session as unread
			if strings.EqualFold(strings.TrimSpace(payload.Role), "assistant") {
				m.workspace.markSessionUnread(payload.SessionID)
			}
			return nil
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
			// EX-163: return the send cmd so the promoted message reaches the server.
			return m.completeTurnAndPromoteQueue("Promoted queued message after finalize.")
		}
	case "chat.turn.started":
		// Ignore historical turn lifecycle events during replay — they would
		// set activeTurn=true and cause all replayed delta events to stream in.
		if !m.turnsSynced {
			return nil
		}
		var payload struct {
			SessionID string `json:"session_id"`
		}
		if decodePayload(event.Payload, &payload) && !m.sessionMatchesActive(payload.SessionID) {
			return nil
		}
		m.activeTurn = true
		// EX-127: capture activeTurnSessionID from the event payload immediately
		// so cross-session SSE events are filtered correctly from turn start,
		// without waiting for the async SessionResolvedMsg to arrive.
		if looksLikeUUID(payload.SessionID) && m.activeTurnSessionID == "" {
			m.activeTurnSessionID = payload.SessionID
		}
		m.chatScrollOffset = 0 // snap to bottom when turn begins
	case "chat.turn.completed":
		if !m.turnsSynced {
			return nil
		}
		var payload struct {
			SessionID string `json:"session_id"`
		}
		_ = decodePayload(event.Payload, &payload)
		if !m.sessionMatchesActive(payload.SessionID) {
			return nil
		}
		if !m.activeTurn && len(m.queuedMessages) == 0 {
			return nil
		}
		// EX-163: return the send cmd so the promoted message reaches the server.
		return m.completeTurnAndPromoteQueue("Promoted queued message after turn completion.")
	case "chat.turn.cancelled":
		if !m.turnsSynced {
			return nil
		}
		var payload struct {
			SessionID string `json:"session_id"`
		}
		_ = decodePayload(event.Payload, &payload)
		if !m.sessionMatchesActive(payload.SessionID) {
			return nil
		}
		if !m.activeTurn {
			return nil
		}
		m.activeTurn = false
		m.activeTurnSessionID = ""
		// EX-412: if there are queued messages when the turn is externally
		// cancelled, they won't auto-promote (only completeTurnAndPromoteQueue
		// does that). Surface a hint so the user knows they have queued messages
		// to act on rather than seeing a generic "Active turn cancelled." with no
		// mention of the orphaned queue.
		// EX-420: use 'd·delete / e·edit' — both work when activeTurn is false
		// (EX-420 extended the queue-action guard to cover this state).
		if n := len(m.queuedMessages); n > 0 {
			m.statusMessage = fmt.Sprintf("Active turn cancelled — %d queued message(s) remain. Press d·delete or e·edit.", n)
		} else {
			m.statusMessage = "Active turn cancelled."
		}
	case "chat.tool_call.status":
		var payload struct {
			MessageID  string `json:"message_id"`
			ToolCallID string `json:"tool_call_id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
		}
		if !decodePayload(event.Payload, &payload) {
			return nil
		}
		if !m.activeTurn {
			return nil
		}
		index := m.ensureMessage(payload.MessageID, "assistant", event.OccurredAt)
		m.upsertToolCall(index, strings.TrimSpace(payload.ToolCallID), strings.TrimSpace(payload.Name), strings.TrimSpace(payload.Status))
	case "chat.message.redacted":
		// EX-140: replace redacted message content with a placeholder so the
		// user knows the message was removed rather than seeing stale content.
		var payload struct {
			SessionID string `json:"session_id"`
			MessageID string `json:"message_id"`
		}
		if !decodePayload(event.Payload, &payload) {
			return nil
		}
		if !m.sessionMatchesActive(payload.SessionID) {
			return nil
		}
		msgID := strings.TrimSpace(payload.MessageID)
		if idx, ok := m.chatMessageIndex[msgID]; ok {
			m.chatMessages[idx].Content = "[Redacted]"
			m.chatMessages[idx].Finalized = true
		}
	case "worker.unresponsive":
		// EX-410: surface a warning when the backend detects no worker response.
		// The warning clears naturally: completeTurnAndPromoteQueue and
		// chat.turn.cancelled both overwrite statusMessage on resolution.
		if !m.turnsSynced {
			return nil
		}
		var payload struct {
			SessionID string `json:"session_id"`
			Message   string `json:"message"`
		}
		if !decodePayload(event.Payload, &payload) {
			return nil
		}
		if !m.sessionMatchesActive(payload.SessionID) {
			return nil
		}
		if strings.TrimSpace(payload.Message) != "" {
			m.statusMessage = payload.Message
		} else {
			m.statusMessage = "Worker appears offline — check that `ottercamp worker` is running."
		}
	}
	return nil
}

// completeTurnAndPromoteQueue clears the active turn and, if there are
// queued messages, promotes the first one to the active position.
// Returns a tea.Cmd to send the promoted message to the server (EX-163).
func (m *Model) completeTurnAndPromoteQueue(promoteStatus string) tea.Cmd {
	m.activeTurn = false
	m.activeTurnSessionID = ""
	m.chatScrollOffset = 0 // snap to bottom so the completed response is visible
	m.finalizePendingAssistantMessages()
	if len(m.queuedMessages) == 0 {
		// EX-426: after a successful turn with no queued messages, the status bar
		// was silently cleared to "" — leaving users watching the status bar with
		// no confirmation that the turn finished. Show a brief confirmation instead
		// so the transition from "Message sent. Waiting for assistant response."
		// is acknowledged rather than silently disappearing.
		m.statusMessage = "Response received."
		return nil
	}
	next := m.queuedMessages[0]
	m.queuedMessages = append([]QueuedMessage{}, m.queuedMessages[1:]...)
	m.appendMessage("local-user", "user", next.Text, true)
	m.activeTurn = true
	m.chatScrollOffset = 0
	if strings.TrimSpace(promoteStatus) != "" {
		m.statusMessage = promoteStatus
	}
	// EX-163: the queued message was stored locally but never sent to the server.
	// Now that the previous turn is done, dispatch the send to the backend.
	return requestChatSendCmd(m.ActiveChatSession(), next.Text)
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

// activityMaxEntries is the maximum number of entries kept in the activity log.
// Older entries are dropped from the front to keep memory usage bounded.
const activityMaxEntries = 200

// appendActivity appends entry to the activity log, trimming the front of the
// slice when it exceeds activityMaxEntries so the slice does not grow unbounded.
func appendActivity(entries []string, entry string) []string {
	entries = append(entries, entry)
	if len(entries) > activityMaxEntries {
		entries = entries[len(entries)-activityMaxEntries:]
	}
	return entries
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
		// EX-207: if no task session is available, let the user know so they
		// understand why the chat panel shows a placeholder instead of messages.
		if sessionID == "" {
			m.statusMessage = "Scope: task (no task selected — select a task first)"
			sessionID = sessionForScope(next)
		}
	case ScopeProject:
		// EX-417: project scope uses the org/Frank session (same as ScopeOrg),
		// because project-scoped chats route through the general org session.
		// Previously the default case called sessionForScope(ScopeProject) which
		// returns the non-UUID placeholder "session-project-current", causing
		// sendOrQueueInput to show "Session loading — please wait..." instead of
		// a helpful "no project selected" hint.
		sessionID = m.workspace.activeSessionID
		if sessionID == "" {
			sessionID = sessionForScope(next)
		}
		if m.workspace.selectedProjectID == "" {
			m.statusMessage = "Scope: project (no project selected — select a project from the sidebar first)"
		}
	default:
		sessionID = sessionForScope(next)
	}
	// EX-186: clear stale turn state before switching sessions.
	m.clearTurnIfSwitchingSession(sessionID)
	m.activeSession = sessionID
	m.chatScrollOffset = 0
	if m.statusMessage == "" {
		m.statusMessage = fmt.Sprintf("Scope switched to %s.", next)
	}
	if looksLikeUUID(sessionID) && m.runtimeHints.LoadChatHistory != nil {
		// EX-181: clear stale messages before loading so the chat panel shows
		// the loading indicator instead of the previous session's content.
		m.chatMessages = nil
		m.chatHistoryLoading = true
		m.chatMessageIndex = make(map[string]int)
		return loadChatHistoryCmd(sessionID, m.runtimeHints.LoadChatHistory)
	}
	return nil
}

// jumpToFrankSession switches the active session to the org-level (Frank) session
// and returns a cmd to reload its chat history (EX-165).
func (m *Model) jumpToFrankSession(trigger string) tea.Cmd {
	if err := m.workspace.activateGeneralSession(); err != nil {
		m.statusMessage = "Unable to load Frank session. Press Ctrl-G or :frank to retry."
		return nil
	}
	sessionID := m.workspace.activeSessionID
	m.activeScope = ScopeOrg
	// EX-186: clear stale turn state before switching sessions.
	m.clearTurnIfSwitchingSession(sessionID)
	m.activeSession = sessionID
	m.state.LastActiveChatSession = sessionID
	m.chatScrollOffset = 0
	if strings.TrimSpace(trigger) == "" {
		m.statusMessage = "Switched to Frank session."
	} else {
		m.statusMessage = fmt.Sprintf("Switched to Frank session (%s).", trigger)
	}
	// EX-165: reload history so chat shows Frank's messages, not the previous session's.
	if looksLikeUUID(sessionID) && m.runtimeHints.LoadChatHistory != nil {
		m.chatMessages = nil
		m.chatHistoryLoading = true
		m.chatMessageIndex = make(map[string]int)
		return loadChatHistoryCmd(sessionID, m.runtimeHints.LoadChatHistory)
	}
	return nil
}

// jumpToProjectByName finds a project node by display name (case-insensitive
// prefix or substring match) and navigates to the project view. EX-228.
func (m *Model) jumpToProjectByName(name string) tea.Cmd {
	nameLower := strings.ToLower(strings.TrimSpace(name))
	for _, id := range m.workspace.topLevel {
		node := m.workspace.nodes[id]
		if node == nil || node.Kind != sidebarKindProject {
			continue
		}
		if strings.Contains(strings.ToLower(node.Label), nameLower) {
			m.workspace.selectedProjectID = node.ProjectID
			m.workspace.setMainView(ViewProject)
			m.statusMessage = "Project: " + node.Label
			return loadProjectTasksCmd(node.ProjectID, m.runtimeHints, false)
		}
	}
	m.statusMessage = fmt.Sprintf("Project %q not found.", name)
	return nil
}

// jumpToSessionByName finds a session node by label and switches to it. EX-228.
func (m *Model) jumpToSessionByName(name string) tea.Cmd {
	nameLower := strings.ToLower(strings.TrimSpace(name))
	for _, id := range m.workspace.visibleSidebarIDs() {
		node := m.workspace.nodes[id]
		if node == nil || node.Kind != sidebarKindSession {
			continue
		}
		if strings.Contains(strings.ToLower(node.Label), nameLower) {
			sessionID := node.SessionID
			m.clearTurnIfSwitchingSession(sessionID)
			m.activeSession = sessionID
			m.state.LastActiveChatSession = sessionID
			m.chatScrollOffset = 0
			m.workspace.activeSessionID = sessionID
			m.statusMessage = "Session: " + node.Label
			if looksLikeUUID(sessionID) && m.runtimeHints.LoadChatHistory != nil {
				m.chatMessages = nil
				m.chatHistoryLoading = true
				m.chatMessageIndex = make(map[string]int)
				return loadChatHistoryCmd(sessionID, m.runtimeHints.LoadChatHistory)
			}
			return nil
		}
	}
	m.statusMessage = fmt.Sprintf("Session %q not found.", name)
	return nil
}

// jumpToTaskByTitle finds a task by title substring and navigates to its detail view. EX-228.
func (m *Model) jumpToTaskByTitle(title string) tea.Cmd {
	titleLower := strings.ToLower(strings.TrimSpace(title))
	for _, taskID := range m.workspace.taskOrder {
		task := m.workspace.tasks[taskID]
		if task == nil {
			continue
		}
		if strings.Contains(strings.ToLower(task.Title), titleLower) {
			m.workspace.selectedTaskID = taskID
			m.workspace.setMainView(ViewTask)
			m.activeScope = ScopeTask
			label := task.Title
			if task.TaskNumber > 0 {
				label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, task.Title)
			}
			m.statusMessage = "Task: " + truncate(label, 40)
			return nil
		}
	}
	m.statusMessage = fmt.Sprintf("Task %q not found.", title)
	return nil
}

func (m Model) chatTextInputActive() bool {
	return m.focus == ChatPanel
}

// clearTurnIfSwitchingSession must be called just before changing m.activeSession.
// If the incoming session differs from the current one it:
//   - clears activeTurn (EX-186) so the spinner doesn't persist in the new session,
//   - discards queuedMessages + editingQueued (EX-187) so stale turn-completed events
//     for the old session cannot send those messages to the new session, and
//   - sets activeTurnSessionID to the new session UUID when available (EX-188), so
//     events from the old session (or unrelated supervisor recovery runs) are still
//     filtered by sessionMatchesActive — avoiding a re-introduction of EX-144.
func (m *Model) clearTurnIfSwitchingSession(newSessionID string) {
	if strings.EqualFold(strings.TrimSpace(m.activeSession), strings.TrimSpace(newSessionID)) {
		return // same session — keep all state
	}
	if m.activeTurn || len(m.queuedMessages) > 0 || m.activeTurnSessionID != "" {
		// EX-201: inform the user when queued messages are discarded so they
		// know their pending work was lost (and can re-enter it if needed).
		if len(m.queuedMessages) > 0 {
			n := len(m.queuedMessages)
			if n == 1 {
				m.statusMessage = "Queued message discarded (switched session)."
			} else {
				m.statusMessage = fmt.Sprintf("%d queued messages discarded (switched session).", n)
			}
		}
		m.activeTurn = false
		m.queuedMessages = nil
		m.editingQueued = false
		// EX-188: point the filter at the new session so events from the old session
		// and unrelated supervisor runs don't bleed through. When the new session ID
		// is a UUID, strict filtering applies immediately. When it's a placeholder
		// (not yet resolved), fall back to "" (accept-all) — it will be tightened on
		// the first chat.turn.started event.
		if looksLikeUUID(newSessionID) {
			m.activeTurnSessionID = strings.TrimSpace(newSessionID)
		} else {
			m.activeTurnSessionID = ""
		}
	}
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
		// EX-359: clarify why 's' had no effect instead of saying what WOULD work.
		// At wider widths the sidebar is always visible — toggling it isn't available.
		m.statusMessage = "Sidebar is always visible above 100 columns. Resize narrower to enable toggling."
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

	raw := proportions[index] + delta
	target := raw
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

	// EX-263: when the resize hits a boundary, say so instead of silently showing
	// the same percentage the user was already at.
	switch {
	case raw < minTarget:
		m.statusMessage = fmt.Sprintf("%s at minimum width (%.0f%%)", label, target*100)
	case raw > maxTarget:
		m.statusMessage = fmt.Sprintf("%s at maximum width (%.0f%%)", label, target*100)
	default:
		m.statusMessage = fmt.Sprintf("%s width %.0f%%", label, target*100)
	}
	return true
}

func (m *Model) enterCommandMode() {
	m.commandMode = true
	m.commandBuffer = ":"
	m.statusMessage = "Command mode"
}

func (m *Model) executeSidebarCommand(args []string) tea.Cmd {
	if len(args) != 1 {
		m.statusMessage = "Usage: :sidebar up|down|home|end|expand|collapse|select"
		return nil
	}
	m.setFocus(SidebarPanel)
	// EX-418: navigation and selection commands in an empty sidebar should say
	// "No items in sidebar." rather than a misleading "moved up/down/expanded" etc.
	if len(m.workspace.visibleSidebarIDs()) == 0 {
		m.statusMessage = "No items in sidebar."
		return nil
	}
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
		// Capture node before selectSidebarNode modifies state.
		node := m.workspace.currentSidebarNode()
		m.workspace.selectSidebarNode()
		m.state.LastActiveChatSession = m.workspace.activeSessionID
		// EX-186: clear stale turn state before switching sessions.
		m.clearTurnIfSwitchingSession(m.workspace.activeSessionID)
		m.activeSession = m.workspace.activeSessionID
		m.statusMessage = "Sidebar selection applied."
		// EX-177: dispatch data loads appropriate for the selected node kind,
		// mirroring the handleEnterKey path so :sidebar select and Enter are equivalent.
		var cmds []tea.Cmd
		if node != nil {
			switch node.Kind {
			case sidebarKindProject:
				cmds = append(cmds, loadProjectTasksCmd(node.ProjectID, m.runtimeHints, true))
				if m.runtimeHints.LoadProjectDetail != nil {
					cmds = append(cmds, loadProjectDetailCmd(node.ProjectID, m.runtimeHints))
				}
			case sidebarKindTask:
				if node.TaskID != "" {
					cmds = append(cmds, loadTaskDetailCmd(node.TaskID, m.runtimeHints))
				}
			case sidebarKindInbox:
				cmds = append(cmds, loadInboxItemsCmd(m.runtimeHints))
			}
		}
		// EX-168: reload history so the chat panel shows the newly-selected session.
		sessionID := m.workspace.activeSessionID
		if looksLikeUUID(sessionID) && m.runtimeHints.LoadChatHistory != nil {
			m.chatMessages = nil
			m.chatHistoryLoading = true
			m.chatMessageIndex = make(map[string]int)
			m.chatScrollOffset = 0
			cmds = append(cmds, loadChatHistoryCmd(sessionID, m.runtimeHints.LoadChatHistory))
		}
		if len(cmds) > 0 {
			return tea.Batch(cmds...)
		}
	default:
		m.statusMessage = "Unknown sidebar action. Use up, down, home, end, expand, collapse, or select."
	}
	return nil
}

// executeInboxCommand handles :inbox <action> commands. Returns a tea.Cmd
// for approve/reject/defer so the action is also sent to the server (EX-160).
func (m *Model) executeInboxCommand(args []string) tea.Cmd {
	if len(args) != 1 {
		m.statusMessage = "Usage: :inbox up|down|home|end|open|approve|reject|defer"
		return nil
	}
	m.workspace.setMainView(ViewInbox)
	m.setFocus(MainPanel)
	switch strings.ToLower(args[0]) {
	case "up":
		// EX-419: empty inbox guard — moving cursor in an empty inbox is a no-op;
		// say so rather than "Inbox cursor moved up."
		if len(m.workspace.inbox) == 0 {
			m.statusMessage = "No inbox items."
			return nil
		}
		m.workspace.moveInbox(-1)
		m.statusMessage = "Inbox cursor moved up."
	case "down":
		// EX-419: symmetric empty inbox guard for down.
		if len(m.workspace.inbox) == 0 {
			m.statusMessage = "No inbox items."
			return nil
		}
		m.workspace.moveInbox(1)
		m.statusMessage = "Inbox cursor moved down."
	case "home":
		// EX-419: symmetric empty inbox guard for home.
		if len(m.workspace.inbox) == 0 {
			m.statusMessage = "No inbox items."
			return nil
		}
		m.workspace.inboxHome()
		m.statusMessage = "Inbox cursor moved home."
	case "end":
		// EX-419: symmetric empty inbox guard for end.
		if len(m.workspace.inbox) == 0 {
			m.statusMessage = "No inbox items."
			return nil
		}
		m.workspace.inboxEnd()
		m.statusMessage = "Inbox cursor moved end."
	case "open":
		if !m.workspace.applyInboxAction("open") {
			m.statusMessage = "No inbox item available."
			return nil
		}
		m.state.LastActiveChatSession = m.workspace.activeSessionID
		// EX-186: clear stale turn state before switching sessions.
		m.clearTurnIfSwitchingSession(m.workspace.activeSessionID)
		m.activeSession = m.workspace.activeSessionID
		// EX-172: inbox items are task-scoped; set scope so assistantLabel()
		// and chat header indicators are accurate.
		m.activeScope = ScopeTask
		m.statusMessage = "Opened inbox item in context."
		// EX-175: load task detail so task view shows number/description.
		var cmds []tea.Cmd
		if taskID := m.workspace.selectedTaskID; taskID != "" {
			cmds = append(cmds, loadTaskDetailCmd(taskID, m.runtimeHints))
		}
		// EX-171: reload chat history for the task session being opened.
		sessionID := m.workspace.activeSessionID
		if looksLikeUUID(sessionID) && m.runtimeHints.LoadChatHistory != nil {
			m.chatMessages = nil
			m.chatHistoryLoading = true
			m.chatMessageIndex = make(map[string]int)
			m.chatScrollOffset = 0
			cmds = append(cmds, loadChatHistoryCmd(sessionID, m.runtimeHints.LoadChatHistory))
		}
		if len(cmds) > 0 {
			return tea.Batch(cmds...)
		}
	case "approve", "reject", "defer":
		action := strings.ToLower(args[0])
		// EX-160: capture item ID before applyInboxAction removes it from the list.
		item := m.workspace.currentInboxItem()
		if !m.workspace.applyInboxAction(action) {
			m.statusMessage = "No inbox item available."
			return nil
		}
		switch action {
		case "approve":
			m.statusMessage = "Inbox item approved."
		case "reject":
			m.statusMessage = "Inbox item rejected."
		default:
			m.statusMessage = "Inbox item deferred."
		}
		if item != nil {
			return actOnInboxItemCmd(item.ID, action, m.runtimeHints.ActOnInboxItem)
		}
	default:
		m.statusMessage = "Unknown inbox action. Use up, down, home, end, open, approve, reject, or defer."
	}
	return nil
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
		// EX-155/EX-228/EX-246: include full command set + Tab hint. Note: ? is NOT
		// listed because in command mode keypresses go into the command buffer, not to
		// the ? shortcut. Use :help or Esc+? to reach the keybinding reference.
		return "Tab autocomplete  ·  :frank · :dashboard · :project · :task · :inbox · :agents · :activity · :merges · :schedules · :scope · :focus · :send · :cancel-turn · :help · :quit  ·  Esc cancel"
	}
	switch m.focus {
	case SidebarPanel:
		// EX-251: reflect active filter state so "/ clear filter" appears when a
		// filter is narrowing the sidebar list (consistent with view footer hints).
		sidebarSearchHint := "/ filter"
		if strings.TrimSpace(m.sidebarFilter) != "" {
			sidebarSearchHint = "/ clear filter"
		}
		return "j/k navigate · Space expand · Enter select · n next unread · i inbox · d dashboard · r refresh · " + sidebarSearchHint + " · ? help"
	case MainPanel:
		switch m.workspace.mainView {
		case ViewInbox:
			return "a approve · x reject · f defer · o open · j/k navigate · n next unread · s toggle sidebar · Esc back · : commands"
		case ViewTask:
			// EX-149: surface approve/reject/defer hints when RequiresHumanReview is set.
			var reviewHints string
			if task := m.workspace.tasks[m.workspace.selectedTaskID]; task != nil && task.RequiresHumanReview {
				reviewHints = " · a approve · x reject · f defer"
			}
			if m.workspace.selectedProjectID != "" {
				taskHelp := "Enter open session · Esc project"
				if len(m.workspace.openTasksForProject()) >= 2 {
					taskHelp += " · j/k next/prev"
				}
				taskHelp += reviewHints + " · p project view · r refresh · n next unread · : commands · ? help"
				return taskHelp
			}
			return "Enter open session · Esc dashboard" + reviewHints + " · r refresh · n next unread · : commands · ? help"
		case ViewProject:
			// EX-121: reflect current showDoneTasks state so the hint is actionable.
			doneHint := "d show done"
			if m.workspace.showDoneTasks {
				doneHint = "d hide done"
			}
			return "j/k navigate tasks · Enter open task · " + doneHint + " · r refresh · Esc dashboard · n next unread · : commands · ? help"
		case ViewHelp:
			// EX-241: help view hint shows scroll/close keys; r·refresh and / filter
			// are irrelevant here so replace the default with a focused hint set.
			// EX-249: also surface 'q' which closes help (same as ? and Esc).
			return "j/k scroll · g/G top/bottom · ? / q / Esc close help"
		case ViewDashboard:
			if len(m.workspace.dashboardActiveTasks()) > 0 {
				// EX-116: include "t·task" hint when a task is selected so users know
				// how to jump back to the task detail view from the dashboard.
				taskHint := "j/k select task · Enter open"
				if m.workspace.selectedTaskID != "" {
					taskHint += " · t task detail"
				}
				return taskHint + " · g/G first/last · i inbox · n next unread · / filter · : commands · ? help"
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

// viewNavLabel returns a human-readable status message label for main view navigation.
// Matches the Title-Case format used by keyboard shortcut messages (e.g. 'i' → "Inbox").
// EX-260: used by executeCommand so :inbox, :dashboard etc. produce the same format
// as their keyboard shortcut equivalents.
func viewNavLabel(view MainView) string {
	switch view {
	case ViewDashboard:
		return "Dashboard"
	case ViewInbox:
		return "Inbox"
	case ViewActivity:
		return "Activity"
	case ViewAgents:
		return "Agents"
	case ViewMerges:
		return "Merge Queue"
	case ViewSchedules:
		return "Schedules"
	case ViewProject:
		return "Project view"
	case ViewTask:
		return "Task detail"
	case ViewHelp:
		return "Help"
	default:
		return string(view)
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

func (m *Model) markReplaySynced() {
	if m.proofReplay {
		return
	}
	m.proofReplay = true
	m.workspace.activity = appendActivity(m.workspace.activity, "event replay complete")
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

// actOnInboxItemCmd fires an async API call for an inbox action (approve/reject/defer/dismiss).
// EX-160: previously inbox key bindings only updated local state; now they also reach the server.
// On completion a statusMessage update is triggered via inboxActionCompletedMsg.
func actOnInboxItemCmd(itemID, action string, actFn func(ctx context.Context, itemID, action string) error) tea.Cmd {
	if strings.TrimSpace(itemID) == "" || actFn == nil {
		return nil
	}
	return func() tea.Msg {
		err := actFn(context.Background(), strings.TrimSpace(itemID), action)
		return inboxActionCompletedMsg{ItemID: itemID, Action: action, Err: err}
	}
}

func loadChatHistoryCmd(sessionID string, loadFn func(ctx context.Context, sessionID string) ([]ChatMessage, error)) tea.Cmd {
	sid := strings.TrimSpace(sessionID)
	return func() tea.Msg {
		if loadFn == nil {
			return chatHistoryLoadedMsg{SessionID: sid}
		}
		messages, err := loadFn(context.Background(), sid)
		if err != nil {
			// EX-198: propagate error so the TUI can surface it to the user.
			return chatHistoryLoadedMsg{SessionID: sid, Err: err}
		}
		return chatHistoryLoadedMsg{SessionID: sid, Messages: messages}
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

// statusAutoClearCmd schedules a statusClearMsg after 5 s.
// If a newer status message is set before the timer fires, the generation
// mismatch prevents the stale clear from taking effect.
func statusAutoClearCmd(gen int) tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg {
		return statusClearMsg{Generation: gen}
	})
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// safeIndex returns s[i] if i is in bounds, otherwise returns "".
func safeIndex(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
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

// taskLabel returns a short human-readable label for a task record, falling
// back to the raw taskID when no record is cached. Used by SSE handlers that
// append to the activity log.
func taskLabel(rec *taskRecord, taskID string) string {
	if rec != nil {
		if rec.TaskNumber > 0 {
			return fmt.Sprintf("OC-%d", rec.TaskNumber)
		}
		if rec.Title != "" {
			return truncate(rec.Title, 24)
		}
	}
	return taskID
}

// applyWorkspaceCommand handles model-level workspace SSE events that need to
// fire tea.Cmds (e.g. tui.command navigation requests). Returns nil if the
// event is not a model-level command and should fall through to the workspace.
func (m *Model) applyWorkspaceCommand(event EventEnvelope) tea.Cmd {
	if event.EventType == "inbox.item_created" {
		m.workspace.inboxCount++
		// EX-120: reload inbox items so the new item appears immediately when
		// the user opens the inbox. Without this, the badge increments but the
		// list is stale until the user manually refreshes.
		return loadInboxItemsCmd(m.runtimeHints)
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
		// EX-131: use taskLabel helper to DRY up OC-N / title / ID fallback.
		if payload.ToStatus != "" {
			label := taskLabel(m.workspace.tasks[payload.TaskID], payload.TaskID)
			statusLabel := formatTaskStatus(payload.ToStatus)
			m.workspace.activity = appendActivity(m.workspace.activity,
				label+": "+statusLabel)
		}
		// EX-138: when the task crosses the done boundary in the currently viewed
		// project, reload the project detail so DoneTasks is populated correctly.
		// Without this, toggling 'd' after a real-time completion shows an empty
		// Done section because DoneTasks is only set by a full project detail load.
		isNowDone := payload.ToStatus == "done" || payload.ToStatus == "approved" || payload.ToStatus == "cancelled"
		if isNowDone && m.workspace.selectedProject != nil &&
			m.workspace.selectedProjectID != "" &&
			strings.EqualFold(m.workspace.selectedProjectID, payload.ProjectID) {
			return loadProjectDetailCmd(m.workspace.selectedProjectID, m.runtimeHints)
		}
		return nil
	}
	// EX-128: handle task.created so the project view and activity log update
	// immediately when a new task is added — without requiring a manual refresh.
	if event.EventType == "task.created" {
		var payload struct {
			TaskID     string `json:"task_id"`
			ProjectID  string `json:"project_id"`
			TaskNumber int    `json:"task_number"`
		}
		if !decodePayload(event.Payload, &payload) || payload.TaskID == "" {
			return nil
		}
		label := payload.TaskID
		if payload.TaskNumber > 0 {
			label = fmt.Sprintf("OC-%d", payload.TaskNumber)
		}
		m.workspace.activity = appendActivity(m.workspace.activity, label+": created")
		// Reload project detail when the new task belongs to the currently
		// viewed project so the task list reflects the addition immediately.
		if m.workspace.selectedProjectID != "" && payload.ProjectID != "" &&
			strings.EqualFold(m.workspace.selectedProjectID, payload.ProjectID) {
			return loadProjectDetailCmd(m.workspace.selectedProjectID, m.runtimeHints)
		}
		return nil
	}
	// EX-125: handle the server's flow.advanced event so the task detail view
	// reflects the latest flow step without requiring a manual refresh.
	// The server payload contains task_id but not a numeric step; we reload
	// the full task detail to pick up the new FlowNodeName and Flow fields.
	if event.EventType == "flow.advanced" {
		var payload struct {
			TaskID string `json:"task_id"`
		}
		if !decodePayload(event.Payload, &payload) || payload.TaskID == "" {
			return nil
		}
		label := taskLabel(m.workspace.tasks[payload.TaskID], payload.TaskID)
		m.workspace.activity = appendActivity(m.workspace.activity, label+": flow advanced")
		return loadTaskDetailCmd(payload.TaskID, m.runtimeHints)
	}
	// EX-129: budget.anomaly_detected — surface a status-bar warning so the user
	// knows the org is burning tokens at an unusual rate without leaving the TUI.
	// EX-153: also persist in activity log so the warning survives the 5s auto-clear.
	if event.EventType == "budget.anomaly_detected" {
		var payload struct {
			Period               string  `json:"period"`
			CurrentTokens        int64   `json:"current_tokens"`
			RollingAverageTokens int64   `json:"rolling_average_tokens"`
			RollingAverageRatio  float64 `json:"rolling_average_ratio"`
		}
		if decodePayload(event.Payload, &payload) {
			multiplier := int(payload.RollingAverageRatio)
			if multiplier < 2 {
				multiplier = 2
			}
			msg := fmt.Sprintf(
				"⚠ Budget anomaly: %s usage is ~%dx above average (%d tokens vs avg %d).",
				payload.Period, multiplier, payload.CurrentTokens, payload.RollingAverageTokens,
			)
			m.statusMessage = msg
			// EX-153: persist so the anomaly isn't lost after the 5s auto-clear.
			m.workspace.activity = appendActivity(m.workspace.activity,
				fmt.Sprintf("budget anomaly: %s usage ~%dx above avg", payload.Period, multiplier))
		}
		return nil
	}
	// EX-129: task.merged — record in the activity log so the user can see
	// when a branch lands without navigating to the merges view.
	if event.EventType == "task.merged" {
		var payload struct {
			BranchName string `json:"branch_name"`
			ProjectID  string `json:"project_id"`
		}
		if decodePayload(event.Payload, &payload) && payload.BranchName != "" {
			m.workspace.activity = appendActivity(m.workspace.activity, payload.BranchName+": merged")
		}
		return nil
	}
	// EX-129: flow.started — record in the activity log and reload the task
	// detail so the flow step indicator updates immediately.
	if event.EventType == "flow.started" {
		var payload struct {
			TaskID string `json:"task_id"`
		}
		if !decodePayload(event.Payload, &payload) || payload.TaskID == "" {
			return nil
		}
		label := taskLabel(m.workspace.tasks[payload.TaskID], payload.TaskID)
		m.workspace.activity = appendActivity(m.workspace.activity, label+": flow started")
		return loadTaskDetailCmd(payload.TaskID, m.runtimeHints)
	}
	// EX-129: flow.rejected — record in the activity log and reload the task
	// detail so the rejected badge and flow step indicator are up-to-date.
	if event.EventType == "flow.rejected" {
		var payload struct {
			TaskID string `json:"task_id"`
		}
		if !decodePayload(event.Payload, &payload) || payload.TaskID == "" {
			return nil
		}
		label := taskLabel(m.workspace.tasks[payload.TaskID], payload.TaskID)
		m.workspace.activity = appendActivity(m.workspace.activity, label+": flow rejected")
		return loadTaskDetailCmd(payload.TaskID, m.runtimeHints)
	}
	// EX-130: project.deployed — record in the activity log with a short commit
	// SHA so the user can see deployments completing without opening the delivery view.
	if event.EventType == "project.deployed" {
		var payload struct {
			CommitSHA string `json:"commit_sha"`
		}
		if decodePayload(event.Payload, &payload) {
			sha := payload.CommitSHA
			if len(sha) > 8 {
				sha = sha[:8]
			}
			suffix := "deployed"
			if sha != "" {
				suffix = "deployed " + sha
			}
			m.workspace.activity = appendActivity(m.workspace.activity, suffix)
		}
		return nil
	}
	// EX-130: project.rollback_initiated — show in activity log so the user sees
	// the rollback was triggered without having to poll the delivery view.
	if event.EventType == "project.rollback_initiated" {
		var payload struct {
			TargetCommitSHA string `json:"target_commit_sha"`
		}
		if decodePayload(event.Payload, &payload) {
			sha := payload.TargetCommitSHA
			if len(sha) > 8 {
				sha = sha[:8]
			}
			entry := "rollback initiated"
			if sha != "" {
				entry = "rollback to " + sha
			}
			m.workspace.activity = appendActivity(m.workspace.activity, entry)
		}
		return nil
	}
	// EX-130: deploy.approval_requested — set status bar so the user knows a
	// deploy is waiting for their approval before it can proceed.
	// EX-145: also add to activity log so the action item persists past the 5s auto-clear.
	if event.EventType == "deploy.approval_requested" {
		var payload struct {
			CommitSHA string `json:"commit_sha"`
		}
		if decodePayload(event.Payload, &payload) {
			sha := payload.CommitSHA
			if len(sha) > 8 {
				sha = sha[:8]
			}
			msg := "Deploy pending approval."
			if sha != "" {
				msg = "Deploy pending approval: " + sha
			}
			m.statusMessage = msg
			// EX-145: persist in activity log so this action item isn't missed.
			m.workspace.activity = appendActivity(m.workspace.activity, msg)
		}
		return nil
	}
	// EX-130: tool.capability_denied — show a brief status bar warning so the
	// user knows an agent was blocked by a policy without having to check logs.
	// EX-153: also persist in activity log so the policy denial survives the 5s auto-clear.
	if event.EventType == "tool.capability_denied" {
		var payload struct {
			ToolName   string `json:"tool_name"`
			Capability string `json:"capability"`
		}
		if decodePayload(event.Payload, &payload) && payload.ToolName != "" {
			msg := "Policy denied tool: " + payload.ToolName
			if payload.Capability != "" {
				msg += " (" + payload.Capability + ")"
			}
			m.statusMessage = msg
			// EX-153: persist in activity log.
			m.workspace.activity = appendActivity(m.workspace.activity,
				"policy denied: "+truncate(payload.ToolName, 40))
		}
		return nil
	}
	// EX-131: memory.extracted — record in the activity log so the user can see
	// when the background memory extractor has processed a conversation turn.
	// EX-137: agent.pm_removed — record in activity log so the user can see
	// when a PM agent is removed from a project.
	if event.EventType == "agent.pm_removed" {
		var payload struct {
			AgentID   string `json:"agent_id"`
			ProjectID string `json:"project_id"`
		}
		if decodePayload(event.Payload, &payload) {
			entry := "PM agent removed from project"
			// Try to resolve agent name from cached agents list
			for _, agentStr := range m.workspace.agents {
				parts := strings.SplitN(agentStr, "=", 2)
				if len(parts) == 2 && strings.Contains(parts[0], payload.AgentID) {
					entry = "PM removed: " + truncate(parts[0], 30)
					break
				}
			}
			m.workspace.activity = appendActivity(m.workspace.activity, entry)
		}
		return nil
	}
	if event.EventType == "memory.extracted" {
		var payload struct {
			Count int `json:"count"`
		}
		if decodePayload(event.Payload, &payload) && payload.Count > 0 {
			entry := fmt.Sprintf("memory: %d item extracted", payload.Count)
			if payload.Count != 1 {
				entry = fmt.Sprintf("memory: %d items extracted", payload.Count)
			}
			m.workspace.activity = appendActivity(m.workspace.activity, entry)
		}
		return nil
	}
	// EX-131: mcp.catalog.changed — show a brief status bar message so the user
	// knows the available MCP tools have changed without needing to open settings.
	if event.EventType == "mcp.catalog.changed" {
		var payload struct {
			AddedCount   int `json:"added_count"`
			UpdatedCount int `json:"updated_count"`
			RemovedCount int `json:"removed_count"`
		}
		if decodePayload(event.Payload, &payload) {
			var statusMsg, activityEntry string
			switch {
			case payload.AddedCount > 0 && payload.RemovedCount > 0:
				statusMsg = fmt.Sprintf("MCP catalog updated: +%d added, -%d removed.", payload.AddedCount, payload.RemovedCount)
				activityEntry = fmt.Sprintf("mcp catalog: +%d added, -%d removed", payload.AddedCount, payload.RemovedCount)
			case payload.AddedCount > 0:
				statusMsg = fmt.Sprintf("MCP catalog updated: +%d tools added.", payload.AddedCount)
				activityEntry = fmt.Sprintf("mcp catalog: +%d tools added", payload.AddedCount)
			case payload.RemovedCount > 0:
				statusMsg = fmt.Sprintf("MCP catalog updated: -%d tools removed.", payload.RemovedCount)
				activityEntry = fmt.Sprintf("mcp catalog: -%d tools removed", payload.RemovedCount)
			default:
				statusMsg = "MCP catalog refreshed."
				activityEntry = "mcp catalog refreshed"
			}
			m.statusMessage = statusMsg
			// EX-153: persist in activity log so the change is traceable after auto-clear.
			m.workspace.activity = appendActivity(m.workspace.activity, activityEntry)
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
			// EX-159: also persist in the activity log so the warning is visible
			// after the user navigates away and back (status bar auto-clears in 5s).
			m.workspace.activity = appendActivity(m.workspace.activity, "worker unresponsive: check `ottercamp worker`")
		}
		return nil
	}
	// EX-141: agent lifecycle events — reload agents view and add activity entry
	// so the AGENTS panel reflects promotions and expirations in real-time.
	if event.EventType == "agent.expired" {
		var payload struct {
			AgentID string `json:"agent_id"`
			Reason  string `json:"reason"`
		}
		if decodePayload(event.Payload, &payload) {
			entry := "agent expired"
			if payload.Reason != "" {
				entry = "agent expired: " + truncate(payload.Reason, 40)
			}
			m.workspace.activity = appendActivity(m.workspace.activity, entry)
		}
		return loadAgentsCmd(m.runtimeHints)
	}
	if event.EventType == "agent.promoted" {
		m.workspace.activity = appendActivity(m.workspace.activity, "agent promoted to staff")
		return loadAgentsCmd(m.runtimeHints)
	}
	// EX-141: supervisor escalation — surface immediately so the user knows an
	// agent is stuck and a supervisor recovery has been triggered.
	// EX-153: also persist in activity log so the escalation survives the 5s auto-clear.
	if event.EventType == "supervisor.escalation_created" {
		var payload struct {
			RunID string `json:"run_id"`
		}
		if decodePayload(event.Payload, &payload) {
			runLabel := truncate(payload.RunID, 8)
			m.statusMessage = "⚠ Supervisor escalation: run " + runLabel + " appears stuck. Recovery initiated."
			// EX-153: persist in activity log.
			m.workspace.activity = appendActivity(m.workspace.activity,
				"supervisor escalation: run "+runLabel)
		}
		return nil
	}
	// EX-140: project lifecycle — reload sidebar on structural changes (new,
	// deleted, archived) and reload detail on updates to the current project.
	if event.EventType == "project.created" {
		var payload struct {
			Slug string `json:"slug"`
		}
		if decodePayload(event.Payload, &payload) && payload.Slug != "" {
			m.workspace.activity = appendActivity(m.workspace.activity, "project created: "+payload.Slug)
		}
		return loadSidebarDataCmd(m.runtimeHints)
	}
	if event.EventType == "project.updated" {
		var payload struct {
			ProjectID string `json:"project_id"`
		}
		if decodePayload(event.Payload, &payload) &&
			m.workspace.selectedProjectID != "" &&
			strings.EqualFold(m.workspace.selectedProjectID, payload.ProjectID) {
			return loadProjectDetailCmd(m.workspace.selectedProjectID, m.runtimeHints)
		}
		return nil
	}
	if event.EventType == "project.archived" {
		var payload struct {
			Slug string `json:"slug"`
		}
		if decodePayload(event.Payload, &payload) && payload.Slug != "" {
			m.workspace.activity = appendActivity(m.workspace.activity, "project archived: "+payload.Slug)
		}
		return loadSidebarDataCmd(m.runtimeHints)
	}
	if event.EventType == "project.deleted" {
		var payload struct {
			Slug string `json:"slug"`
		}
		if decodePayload(event.Payload, &payload) && payload.Slug != "" {
			m.workspace.activity = appendActivity(m.workspace.activity, "project deleted: "+payload.Slug)
		}
		return loadSidebarDataCmd(m.runtimeHints)
	}
	// EX-140: chat session lifecycle — reload sidebar when sessions are created
	// (new session node appears) or closed (session archived from sidebar).
	// EX-146: also add an activity entry so session creation is visible in Activity view.
	if event.EventType == "chat.session.created" {
		var payload struct {
			Scope string `json:"scope_type"`
		}
		if decodePayload(event.Payload, &payload) && payload.Scope != "" {
			m.workspace.activity = appendActivity(m.workspace.activity, "session created: "+payload.Scope)
		} else {
			m.workspace.activity = appendActivity(m.workspace.activity, "session created")
		}
		return loadSidebarDataCmd(m.runtimeHints)
	}
	if event.EventType == "chat.session.closed" {
		var payload struct {
			SessionID string `json:"session_id"`
		}
		if decodePayload(event.Payload, &payload) && m.sessionMatchesActive(payload.SessionID) {
			m.workspace.activity = appendActivity(m.workspace.activity, "active session closed")
			// EX-167: also set status bar so users notice the session ended,
			// not just the activity log which they might not be watching.
			// EX-411: if a turn was in-progress when the session closed, clear
			// the turn state so the user isn't stuck in "waiting for response"
			// with a dead session — the response will never arrive.
			if m.activeTurn {
				m.activeTurn = false
				m.activeTurnSessionID = ""
				m.queuedMessages = nil
				m.statusMessage = "Session closed while turn active — select another session to continue."
			} else {
				m.statusMessage = "Active session closed — select another session to continue."
			}
		}
		return loadSidebarDataCmd(m.runtimeHints)
	}
	// EX-140/EX-143: run failure events — surface failures in the status bar AND
	// add to the activity log so failures remain visible after the 5s auto-clear.
	if event.EventType == "run.failed" {
		var payload struct {
			RunID        string `json:"run_id"`
			FailureClass string `json:"failure_class"`
			Reason       string `json:"reason"`
		}
		if decodePayload(event.Payload, &payload) {
			reason := payload.Reason
			if reason == "" {
				reason = payload.FailureClass
			}
			if reason == "" {
				reason = "unknown reason"
			}
			m.statusMessage = "⚠ Run failed: " + truncate(reason, 60)
			// EX-143: also persist in activity log so the failure survives the 5s auto-clear.
			m.workspace.activity = appendActivity(m.workspace.activity, "run failed: "+truncate(reason, 50))
		}
		return nil
	}
	if event.EventType == "run.dead_lettered" {
		var payload struct {
			RunID        string `json:"run_id"`
			FailureClass string `json:"failure_class"`
			LastError    string `json:"last_error"`
			AttemptCount int    `json:"attempt_count"`
		}
		if decodePayload(event.Payload, &payload) {
			msg := fmt.Sprintf("⚠ Run dead-lettered after %d attempt(s): %s",
				payload.AttemptCount, truncate(payload.LastError, 50))
			m.statusMessage = msg
			// EX-143: also persist in activity log.
			entry := fmt.Sprintf("run dead-lettered (%d attempts): %s",
				payload.AttemptCount, truncate(payload.LastError, 40))
			m.workspace.activity = appendActivity(m.workspace.activity, entry)
		}
		return nil
	}
	// EX-142: task.review_rejected — append reason to activity so users can see
	// why a task was sent back without navigating to the task event log.
	// EX-144: also reload task detail so RequiresHumanReview badge updates
	// if the server clears the flag when the task is sent back for rework.
	if event.EventType == "task.review_rejected" {
		var payload struct {
			TaskID string `json:"task_id"`
			Reason string `json:"reason"`
		}
		if decodePayload(event.Payload, &payload) {
			label := taskLabel(m.workspace.tasks[payload.TaskID], payload.TaskID)
			entry := label + ": review rejected"
			if payload.Reason != "" {
				entry += " — " + truncate(payload.Reason, 40)
			}
			m.workspace.activity = appendActivity(m.workspace.activity, entry)
			// EX-144: reload task detail so the ⚠ badge reflects the updated state.
			if payload.TaskID != "" {
				return loadTaskDetailCmd(payload.TaskID, m.runtimeHints)
			}
		}
		return nil
	}
	// EX-142: chat.session.mode_changed — note mode transitions in the activity
	// log so users know when a session switches between sync and async.
	if event.EventType == "chat.session.mode_changed" {
		var payload struct {
			OldMode string `json:"old_mode"`
			NewMode string `json:"new_mode"`
		}
		if decodePayload(event.Payload, &payload) && payload.NewMode != "" {
			entry := "session mode: " + payload.OldMode + " → " + payload.NewMode
			m.workspace.activity = appendActivity(m.workspace.activity, entry)
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
	case "task":
		// EX-157: navigate to a specific task by UUID. Mirrors the :task command but
		// also sets selectedTaskID and syncs sidebar/project cursors so the task is
		// immediately visible and in context.
		m.workspace.selectedTaskID = payload.TargetID
		m.workspace.setMainView(ViewTask)
		m.activeScope = ScopeTask
		m.statusMessage = "Navigated to task."
		if task := m.workspace.tasks[payload.TargetID]; task != nil {
			label := task.Title
			if task.TaskNumber > 0 {
				label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, label)
			}
			m.statusMessage = "Navigated to " + truncate(label, 40) + "."
		}
		m.workspace.syncSidebarToTask(payload.TargetID)
		m.workspace.syncProjectCursorToTask(payload.TargetID)
		return loadTaskDetailCmd(payload.TargetID, m.runtimeHints)
	case "inbox":
		m.workspace.mainView = ViewInbox
		m.statusMessage = "Navigated to inbox."
		// EX-179: load fresh inbox data so the list is populated immediately,
		// consistent with the 'i' key and ':inbox' command behaviours.
		return loadInboxItemsCmd(m.runtimeHints)
	case "dashboard":
		m.workspace.mainView = ViewDashboard
		m.statusMessage = "Navigated to dashboard."
	}
	return nil
}
