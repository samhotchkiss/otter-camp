package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type MainView string

const (
	ViewDashboard MainView = "dashboard"
	ViewProject   MainView = "project"
	ViewTask      MainView = "task"
	ViewInbox     MainView = "inbox"
	ViewActivity  MainView = "activity"
	ViewAgents    MainView = "agents"
	ViewMerges    MainView = "merges"
	ViewSchedules MainView = "schedules"
	ViewHelp      MainView = "help"
)

var commandToView = map[string]MainView{
	"dashboard": ViewDashboard,
	"project":   ViewProject,
	"task":      ViewTask,
	"inbox":     ViewInbox,
	"activity":  ViewActivity,
	"agents":    ViewAgents,
	"merges":    ViewMerges,
	"schedules": ViewSchedules,
}

type sidebarKind string

const (
	sidebarKindSession sidebarKind = "session"
	sidebarKindProject sidebarKind = "project"

	generalSidebarNodeID = "session-general"
	generalSessionID     = "session-org-general"
)

type sidebarNode struct {
	ID        string
	Label     string
	Kind      sidebarKind
	ParentID  string
	Expanded  bool
	Unread    int
	SessionID string
	TaskID    string
}

type taskRecord struct {
	ID      string
	Title   string
	Status  string
	Flow    int
	History []string
}

type inboxItem struct {
	ID      string
	TaskID  string
	Summary string
}

type boardCounts struct {
	Todo       int
	InProgress int
	Done       int
	Blocked    int
}

type workspaceState struct {
	mainView      MainView
	nodes         map[string]*sidebarNode
	topLevel      []string
	sidebarCursor int

	tasks          map[string]*taskRecord
	taskOrder      []string
	taskSessionIDs map[string]string
	selectedTaskID string

	inbox       []inboxItem
	inboxCursor int
	activity    []string
	agents      []string
	mergeQueue  []string
	schedules   []string

	activeSessionID string
}

func newWorkspaceState() workspaceState {
	nodes := map[string]*sidebarNode{
		generalSidebarNodeID: {
			ID:        generalSidebarNodeID,
			Label:     "General / Frank",
			Kind:      sidebarKindSession,
			SessionID: generalSessionID,
		},
		"project-alpha": {
			ID:       "project-alpha",
			Label:    "Project Alpha",
			Kind:     sidebarKindProject,
			Expanded: true,
		},
		"session-alpha-task-1": {
			ID:        "session-alpha-task-1",
			Label:     "Task 1 / Launch docs",
			Kind:      sidebarKindSession,
			ParentID:  "project-alpha",
			SessionID: "session-task-1",
			TaskID:    "task-1",
		},
		"session-alpha-task-2": {
			ID:        "session-alpha-task-2",
			Label:     "Task 2 / CI hardening",
			Kind:      sidebarKindSession,
			ParentID:  "project-alpha",
			SessionID: "session-task-2",
			TaskID:    "task-2",
		},
	}

	tasks := map[string]*taskRecord{
		"task-1": {ID: "task-1", Title: "Launch docs", Status: "todo", Flow: 1, History: []string{"created"}},
		"task-2": {ID: "task-2", Title: "CI hardening", Status: "in_progress", Flow: 2, History: []string{"created"}},
	}

	return workspaceState{
		mainView:      ViewDashboard,
		nodes:         nodes,
		topLevel:      []string{generalSidebarNodeID, "project-alpha"},
		sidebarCursor: 0,
		tasks:         tasks,
		taskOrder:     []string{"task-1", "task-2"},
		taskSessionIDs: map[string]string{
			"task-1": "session-task-1",
			"task-2": "session-task-2",
		},
		selectedTaskID: "task-1",
		inbox: []inboxItem{
			{ID: "inbox-1", TaskID: "task-1", Summary: "Approve launch checklist"},
			{ID: "inbox-2", TaskID: "task-2", Summary: "Review flaky test quarantine"},
		},
		activity: []string{"workspace booted"},
		agents:   []string{"Frank=online", "Lori=idle", "Ellie=online"},
		mergeQueue: []string{
			"PR#1496 task-104",
			"PR#1500 task-106",
		},
		schedules:       []string{"daily standup 09:00", "nightly regression 01:00"},
		activeSessionID: generalSessionID,
	}
}

func resolveMainViewCommand(command string) (MainView, bool) {
	view, ok := commandToView[strings.ToLower(strings.TrimSpace(command))]
	return view, ok
}

func (w *workspaceState) setMainView(view MainView) {
	w.mainView = view
}

func (w *workspaceState) visibleSidebarIDs() []string {
	visible := make([]string, 0, len(w.nodes))
	for _, id := range w.topLevel {
		node := w.nodes[id]
		if node == nil {
			continue
		}
		visible = append(visible, node.ID)
		if node.Kind == sidebarKindProject && node.Expanded {
			children := w.projectChildren(node.ID)
			visible = append(visible, children...)
		}
	}
	return visible
}

func (w *workspaceState) projectChildren(projectID string) []string {
	children := make([]string, 0, 4)
	for id, node := range w.nodes {
		if node.ParentID == projectID {
			children = append(children, id)
		}
	}
	sort.Strings(children)
	return children
}

func (w *workspaceState) currentSidebarID() string {
	visible := w.visibleSidebarIDs()
	if len(visible) == 0 {
		return ""
	}
	if w.sidebarCursor < 0 {
		w.sidebarCursor = 0
	}
	if w.sidebarCursor >= len(visible) {
		w.sidebarCursor = len(visible) - 1
	}
	return visible[w.sidebarCursor]
}

func (w *workspaceState) currentSidebarNode() *sidebarNode {
	return w.nodes[w.currentSidebarID()]
}

func (w *workspaceState) moveSidebar(delta int) {
	visible := w.visibleSidebarIDs()
	if len(visible) == 0 {
		w.sidebarCursor = 0
		return
	}
	w.sidebarCursor += delta
	if w.sidebarCursor < 0 {
		w.sidebarCursor = 0
	}
	if w.sidebarCursor >= len(visible) {
		w.sidebarCursor = len(visible) - 1
	}
}

func (w *workspaceState) sidebarHome() {
	w.sidebarCursor = 0
}

func (w *workspaceState) sidebarEnd() {
	visible := w.visibleSidebarIDs()
	if len(visible) == 0 {
		w.sidebarCursor = 0
		return
	}
	w.sidebarCursor = len(visible) - 1
}

func (w *workspaceState) collapseSidebarNode() {
	node := w.currentSidebarNode()
	if node == nil {
		return
	}
	if node.Kind == sidebarKindProject && node.Expanded {
		node.Expanded = false
		return
	}
	if node.ParentID != "" {
		visible := w.visibleSidebarIDs()
		for i, id := range visible {
			if id == node.ParentID {
				w.sidebarCursor = i
				return
			}
		}
	}
}

func (w *workspaceState) expandSidebarNode() {
	node := w.currentSidebarNode()
	if node == nil {
		return
	}
	if node.Kind == sidebarKindProject {
		node.Expanded = true
	}
}

func (w *workspaceState) selectSidebarNode() {
	node := w.currentSidebarNode()
	if node == nil {
		return
	}
	switch node.Kind {
	case sidebarKindProject:
		w.mainView = ViewProject
	case sidebarKindSession:
		if node.TaskID != "" {
			w.mainView = ViewTask
			w.selectedTaskID = node.TaskID
		}
		w.activeSessionID = node.SessionID
		node.Unread = 0
		w.propagateUnread()
	}
}

func (w *workspaceState) pinGeneralSessionTop() {
	if len(w.topLevel) == 0 {
		w.topLevel = []string{generalSidebarNodeID}
		return
	}

	next := make([]string, 0, len(w.topLevel)+1)
	next = append(next, generalSidebarNodeID)
	for _, id := range w.topLevel {
		if id == generalSidebarNodeID {
			continue
		}
		next = append(next, id)
	}
	w.topLevel = next
}

func (w *workspaceState) activateGeneralSession() error {
	node := w.nodes[generalSidebarNodeID]
	if node == nil || node.Kind != sidebarKindSession || strings.TrimSpace(node.SessionID) == "" {
		return fmt.Errorf("general session unavailable")
	}

	w.pinGeneralSessionTop()
	w.activeSessionID = node.SessionID
	node.Unread = 0
	w.propagateUnread()

	for i, id := range w.visibleSidebarIDs() {
		if id == generalSidebarNodeID {
			w.sidebarCursor = i
			break
		}
	}
	return nil
}

func (w *workspaceState) markSessionUnread(sessionID string) {
	target := strings.TrimSpace(sessionID)
	if target == "" {
		return
	}
	for _, node := range w.nodes {
		if node.Kind == sidebarKindSession && node.SessionID == target {
			node.Unread++
		}
	}
	w.propagateUnread()
}

func (w *workspaceState) propagateUnread() {
	for _, node := range w.nodes {
		if node.Kind == sidebarKindProject {
			node.Unread = 0
		}
	}
	for _, node := range w.nodes {
		if node.Kind != sidebarKindSession || node.ParentID == "" {
			continue
		}
		parent := w.nodes[node.ParentID]
		if parent != nil {
			parent.Unread += node.Unread
		}
	}
}

func (w *workspaceState) boardCounts() boardCounts {
	counts := boardCounts{}
	for _, id := range w.taskOrder {
		task := w.tasks[id]
		if task == nil {
			continue
		}
		switch task.Status {
		case "todo":
			counts.Todo++
		case "done", "approved":
			counts.Done++
		case "blocked", "rejected", "deferred":
			counts.Blocked++
		default:
			counts.InProgress++
		}
	}
	return counts
}

func (w *workspaceState) currentInboxItem() *inboxItem {
	if len(w.inbox) == 0 {
		w.inboxCursor = 0
		return nil
	}
	if w.inboxCursor < 0 {
		w.inboxCursor = 0
	}
	if w.inboxCursor >= len(w.inbox) {
		w.inboxCursor = len(w.inbox) - 1
	}
	return &w.inbox[w.inboxCursor]
}

func (w *workspaceState) moveInbox(delta int) {
	if len(w.inbox) == 0 {
		w.inboxCursor = 0
		return
	}
	w.inboxCursor += delta
	if w.inboxCursor < 0 {
		w.inboxCursor = 0
	}
	if w.inboxCursor >= len(w.inbox) {
		w.inboxCursor = len(w.inbox) - 1
	}
}

func (w *workspaceState) inboxHome() {
	w.inboxCursor = 0
}

func (w *workspaceState) inboxEnd() {
	if len(w.inbox) == 0 {
		w.inboxCursor = 0
		return
	}
	w.inboxCursor = len(w.inbox) - 1
}

func (w *workspaceState) applyInboxAction(action string) bool {
	item := w.currentInboxItem()
	if item == nil {
		return false
	}

	switch action {
	case "approve":
		w.setTaskStatus(item.TaskID, "approved")
		w.activity = append(w.activity, fmt.Sprintf("inbox approve %s", item.TaskID))
		w.removeInboxItem(item.ID)
		return true
	case "reject":
		w.setTaskStatus(item.TaskID, "rejected")
		w.activity = append(w.activity, fmt.Sprintf("inbox reject %s", item.TaskID))
		w.removeInboxItem(item.ID)
		return true
	case "defer":
		w.setTaskStatus(item.TaskID, "deferred")
		w.activity = append(w.activity, fmt.Sprintf("inbox defer %s", item.TaskID))
		w.removeInboxItem(item.ID)
		return true
	case "open":
		w.selectedTaskID = item.TaskID
		w.mainView = ViewTask
		if sessionID := w.taskSessionIDs[item.TaskID]; sessionID != "" {
			w.activeSessionID = sessionID
		}
		w.activity = append(w.activity, fmt.Sprintf("open in context %s", item.TaskID))
		return true
	default:
		return false
	}
}

func (w *workspaceState) removeInboxItem(id string) {
	filtered := make([]inboxItem, 0, len(w.inbox))
	for _, item := range w.inbox {
		if item.ID == id {
			continue
		}
		filtered = append(filtered, item)
	}
	w.inbox = filtered
	if w.inboxCursor >= len(w.inbox) && len(w.inbox) > 0 {
		w.inboxCursor = len(w.inbox) - 1
	}
	if len(w.inbox) == 0 {
		w.inboxCursor = 0
	}
}

func (w *workspaceState) setTaskStatus(taskID, status string) {
	task := w.tasks[taskID]
	if task == nil {
		return
	}
	task.Status = status
	task.History = append(task.History, "status="+status)
}

func (w *workspaceState) advanceTaskFlow(taskID string, step int) {
	task := w.tasks[taskID]
	if task == nil {
		return
	}
	task.Flow = step
	task.History = append(task.History, fmt.Sprintf("flow=%d", step))
}

func (w *workspaceState) applyRealtimeEnvelope(event EventEnvelope) {
	switch event.EventType {
	case "task.status.changed":
		var payload struct {
			TaskID    string `json:"task_id"`
			Status    string `json:"status"`
			SessionID string `json:"session_id"`
		}
		if !workspaceDecode(event.Payload, &payload) {
			return
		}
		w.setTaskStatus(strings.TrimSpace(payload.TaskID), strings.TrimSpace(payload.Status))
		w.activity = append(w.activity, fmt.Sprintf("realtime status %s=%s", payload.TaskID, payload.Status))
		w.markSessionUnread(payload.SessionID)
	case "task.flow.advanced":
		var payload struct {
			TaskID    string `json:"task_id"`
			FlowStep  int    `json:"flow_step"`
			SessionID string `json:"session_id"`
		}
		if !workspaceDecode(event.Payload, &payload) {
			return
		}
		w.advanceTaskFlow(strings.TrimSpace(payload.TaskID), payload.FlowStep)
		w.activity = append(w.activity, fmt.Sprintf("realtime flow %s=%d", payload.TaskID, payload.FlowStep))
		w.markSessionUnread(payload.SessionID)
	}
}

func workspaceDecode(raw json.RawMessage, out any) bool {
	if len(raw) == 0 {
		return false
	}
	return json.Unmarshal(raw, out) == nil
}

func (w *workspaceState) render(view MainView, class SizeClass) string {
	counts := w.boardCounts()
	switch view {
	case ViewDashboard:
		return fmt.Sprintf("view=dashboard size=%s tasks(todo=%d,in_progress=%d,done=%d,blocked=%d) inbox=%d", class, counts.Todo, counts.InProgress, counts.Done, counts.Blocked, len(w.inbox))
	case ViewProject:
		return fmt.Sprintf("view=project size=%s projects=%d selected_session=%s", class, w.projectCount(), valueOrPlaceholder(w.activeSessionID))
	case ViewTask:
		task := w.tasks[w.selectedTaskID]
		if task == nil {
			return fmt.Sprintf("view=task size=%s task=none", class)
		}
		return fmt.Sprintf("view=task size=%s id=%s status=%s flow=%d history=%d", class, task.ID, task.Status, task.Flow, len(task.History))
	case ViewInbox:
		item := w.currentInboxItem()
		if item == nil {
			return fmt.Sprintf("view=inbox size=%s empty", class)
		}
		return fmt.Sprintf("view=inbox size=%s current=%s total=%d", class, item.TaskID, len(w.inbox))
	case ViewActivity:
		last := "none"
		if len(w.activity) > 0 {
			last = w.activity[len(w.activity)-1]
		}
		return fmt.Sprintf("view=activity size=%s entries=%d last=%s", class, len(w.activity), last)
	case ViewAgents:
		return fmt.Sprintf("view=agents size=%s count=%d", class, len(w.agents))
	case ViewMerges:
		return fmt.Sprintf("view=merges size=%s count=%d", class, len(w.mergeQueue))
	case ViewSchedules:
		return fmt.Sprintf("view=schedules size=%s count=%d", class, len(w.schedules))
	default:
		return fmt.Sprintf("view=%s size=%s", view, class)
	}
}

func (w *workspaceState) projectCount() int {
	count := 0
	for _, id := range w.topLevel {
		node := w.nodes[id]
		if node != nil && node.Kind == sidebarKindProject {
			count++
		}
	}
	return count
}
