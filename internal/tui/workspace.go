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
	sidebarKindInbox   sidebarKind = "inbox"
	sidebarKindHeader  sidebarKind = "header"
	sidebarKindTask    sidebarKind = "task"

	generalSidebarNodeID = "session-general"
	generalSessionID     = "session-org-general"
)

type sidebarSectionID string

const (
	sectionChats    sidebarSectionID = "chats"
	sectionProjects sidebarSectionID = "projects"
)

type sidebarNode struct {
	ID         string
	Label      string
	Kind       sidebarKind
	ParentID   string
	Expanded   bool
	Unread     int
	SessionID  string
	TaskID     string
	ProjectID  string
	WorkStatus string
}

type taskRecord struct {
	ID                 string
	Title              string
	Description        string
	AcceptanceCriteria string
	Subtasks           []string
	SessionID          string
	Status             string
	Flow               int
	History            []string
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

// ProjectDetail holds metadata for a project loaded from the API.
type ProjectDetail struct {
	ID           string
	DisplayName  string
	Description  string
	DeliveryMode string
	Tasks        []SidebarTaskItem
}

type workspaceState struct {
	mainView      MainView
	nodes         map[string]*sidebarNode
	topLevel      []string
	sidebarCursor int

	sectionCollapsed  map[sidebarSectionID]bool
	inboxCount        int
	selectedProjectID string
	selectedProject   *ProjectDetail

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
		"inbox": {
			ID:    "inbox",
			Kind:  sidebarKindInbox,
			Label: "INBOX",
		},
		"header-chats": {
			ID:    "header-chats",
			Kind:  sidebarKindHeader,
			Label: "CHATS",
		},
		generalSidebarNodeID: {
			ID:        generalSidebarNodeID,
			Label:     "Frank / General",
			Kind:      sidebarKindSession,
			SessionID: generalSessionID,
		},
		"header-projects": {
			ID:    "header-projects",
			Kind:  sidebarKindHeader,
			Label: "PROJECTS",
		},
	}

	return workspaceState{
		mainView:         ViewDashboard,
		nodes:            nodes,
		topLevel:         []string{"inbox", "header-chats", generalSidebarNodeID, "header-projects"},
		sidebarCursor:    0,
		sectionCollapsed: map[sidebarSectionID]bool{},
		tasks:            map[string]*taskRecord{},
		taskOrder:        []string{},
		taskSessionIDs:   map[string]string{},
		inbox:            []inboxItem{},
		activity:         []string{"workspace booted"},
		agents:           []string{},
		mergeQueue:       []string{},
		schedules:        []string{},
		activeSessionID:  generalSessionID,
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
	sectionHidden := false
	for _, id := range w.topLevel {
		node := w.nodes[id]
		if node == nil {
			continue
		}
		if node.Kind == sidebarKindHeader {
			sectionID := sidebarSectionID(strings.TrimPrefix(id, "header-"))
			sectionHidden = w.sectionCollapsed[sectionID]
			visible = append(visible, id)
			continue
		}
		if node.Kind == sidebarKindInbox {
			visible = append(visible, id)
			continue
		}
		if sectionHidden {
			continue
		}
		visible = append(visible, id)
		if node.Kind == sidebarKindProject && node.Expanded {
			children := w.projectChildren(id)
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

// sessionLabel returns the human-readable label for a session ID by looking
// up the matching sidebar node. Falls back to the raw session ID.
func (w *workspaceState) sessionLabel(sessionID string) string {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return ""
	}
	for _, node := range w.nodes {
		if node.Kind == sidebarKindSession && node.SessionID == trimmed {
			return node.Label
		}
	}
	if taskTitle := w.taskTitleForScopeSession(trimmed); taskTitle != "" {
		return taskTitle
	}
	return trimmed
}

func (w *workspaceState) taskTitleForScopeSession(sessionID string) string {
	if !strings.HasPrefix(sessionID, "session-task-") {
		return ""
	}
	taskID := strings.TrimPrefix(sessionID, "session-task-")
	if taskID == "current" {
		taskID = w.selectedTaskID
	}
	if task, ok := w.tasks[taskID]; ok {
		return strings.TrimSpace(task.Title)
	}
	return ""
}

func (w *workspaceState) taskSessionID(taskID string) string {
	task := w.tasks[taskID]
	if task != nil && strings.TrimSpace(task.SessionID) != "" {
		return strings.TrimSpace(task.SessionID)
	}
	return strings.TrimSpace(w.taskSessionIDs[taskID])
}

func (w *workspaceState) selectedTaskSessionID() string {
	return w.taskSessionID(w.selectedTaskID)
}

func (w *workspaceState) openSelectedTaskSession() (string, bool) {
	sessionID := w.selectedTaskSessionID()
	if sessionID == "" {
		return "", false
	}
	w.activeSessionID = sessionID
	return sessionID, true
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
	if node.Kind == sidebarKindHeader {
		sectionID := sidebarSectionID(strings.TrimPrefix(node.ID, "header-"))
		w.sectionCollapsed[sectionID] = true
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
	switch node.Kind {
	case sidebarKindProject:
		node.Expanded = true
	case sidebarKindHeader:
		sectionID := sidebarSectionID(strings.TrimPrefix(node.ID, "header-"))
		w.sectionCollapsed[sectionID] = false
	}
}

func (w *workspaceState) selectSidebarNode() {
	node := w.currentSidebarNode()
	if node == nil {
		return
	}
	switch node.Kind {
	case sidebarKindInbox:
		w.mainView = ViewInbox
	case sidebarKindHeader:
		sectionID := sidebarSectionID(strings.TrimPrefix(node.ID, "header-"))
		w.sectionCollapsed[sectionID] = !w.sectionCollapsed[sectionID]
	case sidebarKindProject:
		w.mainView = ViewProject
		w.selectedProjectID = node.ProjectID
		node.Expanded = !node.Expanded
	case sidebarKindTask:
		w.mainView = ViewTask
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
		if sessionID := w.taskSessionID(item.TaskID); sessionID != "" {
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

// rebuildSidebar replaces the sidebar nodes/topLevel with real API data.
// It preserves expanded state for existing project nodes and keeps any task
// nodes that were already loaded.
func (w *workspaceState) rebuildSidebar(chats []SidebarChatItem, projects []SidebarProjectItem) {
	// Preserve expanded state for existing projects
	expandedProjects := make(map[string]bool)
	for id, node := range w.nodes {
		if node.Kind == sidebarKindProject {
			expandedProjects[id] = node.Expanded
		}
	}
	// Preserve existing task nodes
	taskNodes := make(map[string]*sidebarNode)
	for id, node := range w.nodes {
		if node.Kind == sidebarKindTask {
			taskNodes[id] = node
		}
	}

	frankNode := w.nodes[generalSidebarNodeID]
	if frankNode == nil {
		frankNode = &sidebarNode{
			ID:        generalSidebarNodeID,
			Label:     "Frank / General",
			Kind:      sidebarKindSession,
			SessionID: generalSessionID,
		}
	}

	newNodes := map[string]*sidebarNode{
		"inbox":              {ID: "inbox", Label: "INBOX", Kind: sidebarKindInbox},
		"header-chats":       {ID: "header-chats", Label: "CHATS", Kind: sidebarKindHeader},
		generalSidebarNodeID: frankNode,
		"header-projects":    {ID: "header-projects", Label: "PROJECTS", Kind: sidebarKindHeader},
	}

	newTopLevel := []string{"inbox", "header-chats", generalSidebarNodeID}

	// Recent chats (up to 4, exclude org-scope which is Frank)
	for i, chat := range chats {
		if i >= 4 {
			break
		}
		id := "chat-" + chat.SessionID
		newNodes[id] = &sidebarNode{
			ID:        id,
			Label:     chat.DisplayName,
			Kind:      sidebarKindSession,
			SessionID: chat.SessionID,
		}
		newTopLevel = append(newTopLevel, id)
	}

	newTopLevel = append(newTopLevel, "header-projects")

	// Projects
	for _, proj := range projects {
		id := "project-" + proj.ID
		newNodes[id] = &sidebarNode{
			ID:        id,
			Label:     proj.DisplayName,
			Kind:      sidebarKindProject,
			ProjectID: proj.ID,
			Expanded:  expandedProjects[id],
		}
		newTopLevel = append(newTopLevel, id)
		// Re-add previously loaded task children for this project
		for taskID, task := range taskNodes {
			if task.ParentID == id {
				newNodes[taskID] = task
			}
		}
	}

	w.nodes = newNodes
	w.topLevel = newTopLevel
}

// setProjectTasks replaces the task children for the given project ID.
// It also marks the project as expanded.
func (w *workspaceState) setProjectTasks(projectID string, tasks []SidebarTaskItem) {
	nodeID := "project-" + projectID
	// Remove existing task nodes for this project
	for id, node := range w.nodes {
		if node.Kind == sidebarKindTask && node.ParentID == nodeID {
			delete(w.nodes, id)
		}
	}
	// Add new task nodes
	for _, task := range tasks {
		id := "task-" + task.ID
		w.nodes[id] = &sidebarNode{
			ID:         id,
			Label:      task.Title,
			Kind:       sidebarKindTask,
			ParentID:   nodeID,
			WorkStatus: task.WorkStatus,
		}
	}
	// Mark the project as expanded
	if proj, ok := w.nodes[nodeID]; ok {
		proj.Expanded = true
	}
}

// indexOfNode returns the visible index of the given node ID, or 0 if not found.
func (w *workspaceState) indexOfNode(nodeID string) int {
	for i, id := range w.visibleSidebarIDs() {
		if id == nodeID {
			return i
		}
	}
	return 0
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
