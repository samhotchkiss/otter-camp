package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
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
	ViewSettings  MainView = "settings"
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
	ID           string
	Label        string
	Kind         sidebarKind
	ParentID     string
	Expanded     bool
	Unread       int
	SessionID    string
	SessionScope string // "organization", "project", "project_task" for session nodes
	TaskID       string
	TaskNumber   int
	ProjectID    string
	ProjectSlug  string
	WorkStatus   string
	UpdatedAt    time.Time // for session nodes: last activity time
}

type taskRecord struct {
	ID                       string
	ProjectID                string
	TaskNumber               int
	Title                    string
	Description              string
	AcceptanceCriteria       string
	Subtasks                 []string
	SessionID                string // preferred execution session (active when present, otherwise recent)
	DiscussionSessionID      string
	ActiveExecutionSessionID        string
	RecentExecutionSessionID        string
	Status                   string
	Priority                 int
	Flow                     int
	FlowNodeName             string // human-readable current flow step name
	AgentName                string // display_name of assigned agent
	RequiresHumanReview      bool
	History                  []string
	Events                   []TaskEvent
	BranchName               string
	FlowCurrentNodeID        string
	FlowNodes                []TaskFlowNode
	FlowEdges                []TaskFlowEdge
	SelectedFlowNodeID       string
	PlanningPlaybook         string
	PlanningWorkType         string
	PlanningProjectStage     string
	PlanningEvidenceMaturity string
	PlanningRiskLevel        string
	PlanningArtifacts        []TaskPlanningArtifact
	PlanningFollowOns        []string
	PlanningProcessStatus    string
	PlanningChecklist        []string
	PlanningMissing          []string
	PlanningOverrideReason   string
	BlockedReason            string
	RecoveryHint             string
	FlowSteps                []FlowStep
	SubtaskItems             []SubtaskItem
	Dependencies             []TaskDependency
}

type inboxItem struct {
	ID      string
	TaskID  string
	Summary string
}

type boardCounts struct {
	Queued     int
	InProgress int
	Done       int
	Blocked    int
	Review     int
}

// ProjectAgent represents an agent assigned to a project.
type ProjectAgent struct {
	DisplayName string
	Role        string // "project_manager", "worker", "reviewer", "observer"
}

type ProjectFileEntry struct {
	Path  string
	IsDir bool
	Depth int
}

// ProjectDetail holds metadata for a project loaded from the API.
type ProjectDetail struct {
	ID           string
	Slug         string
	DisplayName  string
	Description  string
	DeliveryMode string
	IsPaused     bool
	PauseReason  string
	RepoURL      string // connected GitHub repository URL
	RepoPath     string
	Files        []ProjectFileEntry
	Tasks        []SidebarTaskItem // open tasks only
	DoneTasks    []SidebarTaskItem // completed/approved/cancelled tasks
	DoneCount    int               // count of completed tasks
	Agents       []ProjectAgent    // agents assigned to this project
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

	tasks                      map[string]*taskRecord
	projectTaskLists           map[string][]SidebarTaskItem
	taskOrder                  []string
	taskSessionIDs             map[string]string
	sessionToTaskLabel         map[string]string // session UUID → human-readable task label
	selectedTaskID             string
	projectTaskCursor          int    // cursor within the project view open-task list
	pendingProjectCursorTaskID string // set when task is opened before project detail loads
	showDoneTasks              bool   // whether to show done tasks in project view
	showProjectFiles           bool   // whether to show the project file section
	showTaskHistory            bool   // whether to show task history/audit trail
	dashboardCursor            int    // cursor within the dashboard task board (index into taskOrder excluding done/cancelled)

	inbox             []inboxItem
	inboxCursor       int
	activity          []string
	agents            []string
	mergeQueue        []string
	schedules         []string
	operatorDashboard *OperatorDashboardData
	operatorTargets   map[int]operatorDashboardTarget

	activeSessionID string
	settings        *SettingsData
}

type operatorDashboardTarget struct {
	Shortcut  int
	Title     string
	ProjectID string
	TaskID    string
	RunID     string
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
		mainView:           ViewDashboard,
		nodes:              nodes,
		topLevel:           []string{"inbox", "header-chats", generalSidebarNodeID, "header-projects"},
		sidebarCursor:      0,
		sectionCollapsed:   map[sidebarSectionID]bool{},
		tasks:              map[string]*taskRecord{},
		projectTaskLists:   map[string][]SidebarTaskItem{},
		taskOrder:          []string{},
		taskSessionIDs:     map[string]string{},
		sessionToTaskLabel: map[string]string{},
		selectedTaskID:     "",
		showTaskHistory:    true,
		inbox:              []inboxItem{},
		activity:           []string{"workspace initialized"},
		agents:             []string{},
		mergeQueue:         []string{},
		schedules:          []string{},
		operatorTargets:    map[int]operatorDashboardTarget{},
		activeSessionID:    generalSessionID,
	}
}

func (w *workspaceState) setOperatorDashboard(data *OperatorDashboardData) {
	if data == nil {
		w.operatorDashboard = nil
		w.operatorTargets = map[int]operatorDashboardTarget{}
		return
	}

	cloned := *data
	w.operatorTargets = map[int]operatorDashboardTarget{}
	cloned.Active = operatorDashboardSectionWithShortcuts(data.Active, &w.operatorTargets)
	cloned.Stale = operatorDashboardSectionWithShortcuts(data.Stale, &w.operatorTargets)
	cloned.Blocked = operatorDashboardSectionWithShortcuts(data.Blocked, &w.operatorTargets)
	cloned.RecentFailures = operatorDashboardSectionWithShortcuts(data.RecentFailures, &w.operatorTargets)
	cloned.RecentActivity = operatorDashboardSectionWithShortcuts(data.RecentActivity, &w.operatorTargets)
	w.operatorDashboard = &cloned
}

func operatorDashboardSectionWithShortcuts(section OperatorDashboardSection, targets *map[int]operatorDashboardTarget) OperatorDashboardSection {
	cloned := OperatorDashboardSection{
		Count:      section.Count,
		TotalCount: section.TotalCount,
		Items:      make([]OperatorDashboardItem, 0, len(section.Items)),
	}
	if *targets == nil {
		*targets = map[int]operatorDashboardTarget{}
	}
	nextShortcut := len(*targets) + 4
	for _, item := range section.Items {
		clonedItem := item
		if nextShortcut <= 9 {
			target := operatorDashboardTarget{
				Shortcut: nextShortcut,
				Title:    strings.TrimSpace(clonedItem.Title),
			}
			if clonedItem.Project != nil {
				target.ProjectID = strings.TrimSpace(clonedItem.Project.ID)
			}
			if clonedItem.Task != nil {
				target.TaskID = strings.TrimSpace(clonedItem.Task.ID)
			}
			if clonedItem.Run != nil {
				target.RunID = strings.TrimSpace(clonedItem.Run.ID)
			}
			if target.ProjectID != "" || target.TaskID != "" || target.RunID != "" {
				clonedItem.Shortcut = nextShortcut
				(*targets)[nextShortcut] = target
				nextShortcut++
			}
		}
		cloned.Items = append(cloned.Items, clonedItem)
	}
	return cloned
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
	type child struct {
		id         string
		taskNumber int
	}
	var kids []child
	for id, node := range w.nodes {
		if node.ParentID == projectID {
			kids = append(kids, child{id: id, taskNumber: node.TaskNumber})
		}
	}
	// Sort by task number descending (highest/newest first); fall back to ID for ties.
	sort.Slice(kids, func(i, j int) bool {
		if kids[i].taskNumber != kids[j].taskNumber {
			return kids[i].taskNumber > kids[j].taskNumber
		}
		return kids[i].id < kids[j].id
	})
	children := make([]string, len(kids))
	for i, k := range kids {
		children[i] = k.id
	}
	return children
}

func cloneSidebarTaskItems(tasks []SidebarTaskItem) []SidebarTaskItem {
	cloned := make([]SidebarTaskItem, len(tasks))
	copy(cloned, tasks)
	return cloned
}

func activeSidebarTaskItems(tasks []SidebarTaskItem) []SidebarTaskItem {
	active := make([]SidebarTaskItem, 0, len(tasks))
	for _, task := range tasks {
		if isCompletedTaskStatus(task.WorkStatus) {
			continue
		}
		active = append(active, task)
	}
	return active
}

func isCompletedTaskStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "done", "approved", "cancelled":
		return true
	default:
		return false
	}
}

func sortSidebarTaskItems(tasks []SidebarTaskItem) {
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].TaskNumber != tasks[j].TaskNumber {
			return tasks[i].TaskNumber > tasks[j].TaskNumber
		}
		leftTitle := strings.TrimSpace(tasks[i].Title)
		rightTitle := strings.TrimSpace(tasks[j].Title)
		if leftTitle != rightTitle {
			return leftTitle < rightTitle
		}
		return tasks[i].ID < tasks[j].ID
	})
}

func mergeSidebarTaskItem(current, next SidebarTaskItem) SidebarTaskItem {
	merged := current
	if strings.TrimSpace(next.ID) != "" {
		merged.ID = strings.TrimSpace(next.ID)
	}
	if strings.TrimSpace(next.Title) != "" {
		merged.Title = strings.TrimSpace(next.Title)
	}
	if next.TaskNumber > 0 {
		merged.TaskNumber = next.TaskNumber
	}
	if next.Priority > 0 {
		merged.Priority = next.Priority
	}
	if strings.TrimSpace(next.WorkStatus) != "" {
		merged.WorkStatus = strings.TrimSpace(next.WorkStatus)
	}
	return merged
}

func taskRecordSidebarItem(record *taskRecord) (SidebarTaskItem, bool) {
	if record == nil {
		return SidebarTaskItem{}, false
	}
	if isCompletedTaskStatus(record.Status) {
		return SidebarTaskItem{}, false
	}
	title := strings.TrimSpace(record.Title)
	if title == "" {
		switch {
		case record.TaskNumber > 0:
			title = fmt.Sprintf("Task %d", record.TaskNumber)
		default:
			title = "Task"
		}
	}
	return SidebarTaskItem{
		ID:         strings.TrimSpace(record.ID),
		Title:      title,
		WorkStatus: strings.TrimSpace(record.Status),
		TaskNumber: record.TaskNumber,
		Priority:   record.Priority,
	}, strings.TrimSpace(record.ID) != ""
}

func (w *workspaceState) projectTaskSnapshot(projectID string) ([]SidebarTaskItem, bool) {
	tasks, ok := w.projectTaskLists[strings.TrimSpace(projectID)]
	if !ok {
		return nil, false
	}
	return cloneSidebarTaskItems(tasks), true
}

func (w *workspaceState) ensureSelectedProjectShell() {
	projectID := strings.TrimSpace(w.selectedProjectID)
	if projectID == "" {
		return
	}
	if w.selectedProject == nil || !strings.EqualFold(strings.TrimSpace(w.selectedProject.ID), projectID) {
		w.selectedProject = &ProjectDetail{ID: projectID}
	}
	if node := w.nodes["project-"+projectID]; node != nil {
		if cleanProjectDisplayLabel(w.selectedProject.DisplayName) == "" && !isSyntheticProjectIDLabel(node.Label) {
			w.selectedProject.DisplayName = strings.TrimSpace(node.Label)
		}
		if strings.TrimSpace(w.selectedProject.Slug) == "" {
			w.selectedProject.Slug = strings.TrimSpace(node.ProjectSlug)
		}
	}
	if liveTasks, ok := w.projectTaskSnapshot(projectID); ok {
		w.selectedProject.Tasks = liveTasks
	}
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
// up the matching sidebar node. Returns "" when label cannot be resolved
// (caller should fall back to the org session label).
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
	// Don't expose raw UUIDs — return empty so caller shows a fallback label.
	return ""
}

func (w *workspaceState) taskTitleForScopeSession(sessionID string) string {
	// Fast lookup from cached reverse map (populated when task detail is loaded)
	if label, ok := w.sessionToTaskLabel[sessionID]; ok && label != "" {
		return label
	}
	// Handle placeholder IDs like "session-task-{taskID}"
	if strings.HasPrefix(sessionID, "session-task-") {
		taskID := strings.TrimPrefix(sessionID, "session-task-")
		if taskID == "current" {
			taskID = w.selectedTaskID
		}
		if task, ok := w.tasks[taskID]; ok {
			return strings.TrimSpace(task.Title)
		}
	}
	// For real UUIDs: scan tasks map for a matching SessionID
	for _, task := range w.tasks {
		if task == nil {
			continue
		}
		if strings.TrimSpace(task.SessionID) == sessionID ||
			strings.TrimSpace(task.DiscussionSessionID) == sessionID ||
			strings.TrimSpace(task.ActiveExecutionSessionID) == sessionID ||
			strings.TrimSpace(task.RecentExecutionSessionID) == sessionID {
			title := strings.TrimSpace(task.Title)
			if task.TaskNumber > 0 {
				return fmt.Sprintf("OC-%d: %s", task.TaskNumber, title)
			}
			return title
		}
	}
	return ""
}

func (w *workspaceState) taskSessionID(taskID string) string {
	task := w.tasks[taskID]
	if task != nil {
		switch {
		case strings.TrimSpace(task.ActiveExecutionSessionID) != "":
			return strings.TrimSpace(task.ActiveExecutionSessionID)
		case strings.TrimSpace(task.RecentExecutionSessionID) != "":
			return strings.TrimSpace(task.RecentExecutionSessionID)
		case strings.TrimSpace(task.SessionID) != "":
			return strings.TrimSpace(task.SessionID)
		case strings.TrimSpace(task.DiscussionSessionID) != "":
			return strings.TrimSpace(task.DiscussionSessionID)
		}
	}
	return strings.TrimSpace(w.taskSessionIDs[taskID])
}

func (w *workspaceState) taskDiscussionSessionID(taskID string) string {
	task := w.tasks[taskID]
	if task != nil && strings.TrimSpace(task.DiscussionSessionID) != "" {
		return strings.TrimSpace(task.DiscussionSessionID)
	}
	return strings.TrimSpace(w.taskSessionIDs[taskID])
}

func (w *workspaceState) rememberTaskDiscussionSession(taskID, sessionID string) {
	taskID = strings.TrimSpace(taskID)
	sessionID = strings.TrimSpace(sessionID)
	if taskID == "" || sessionID == "" {
		return
	}
	if w.taskSessionIDs == nil {
		w.taskSessionIDs = make(map[string]string)
	}
	w.taskSessionIDs[taskID] = sessionID
}

func (w *workspaceState) selectedTaskSessionID() string {
	return w.taskSessionID(w.selectedTaskID)
}

// projectSessionID returns the session ID for a project-scoped chat session,
// found by scanning sidebar nodes for a session with SessionScope="project"
// and ProjectID matching. Returns "" if no match found.
func (w *workspaceState) projectSessionID(projectID string) string {
	if projectID == "" {
		return ""
	}
	for _, node := range w.nodes {
		if node.Kind == sidebarKindSession && node.SessionScope == "project" && node.ProjectID == projectID {
			return node.SessionID
		}
	}
	return ""
}

func (w *workspaceState) openSelectedTaskSession() (string, bool) {
	sessionID := w.selectedTaskSessionID()
	if sessionID == "" {
		return "", false
	}
	// Do NOT overwrite activeSessionID — that holds the org/general session.
	// m.activeSession (Model field) is updated by the caller.
	return sessionID, true
}

// syncSidebarToTask moves the sidebar cursor to the visible node for the given
// task ID (e.g. "task-<uuid>"). Expands the parent project section if needed.
func (w *workspaceState) syncSidebarToTask(taskID string) {
	if taskID == "" {
		return
	}
	nodeID := "task-" + taskID
	taskNode := w.nodes[nodeID]
	if taskNode == nil {
		return
	}
	// If the parent project is collapsed, expand it first.
	if taskNode.ParentID != "" {
		if parent := w.nodes[taskNode.ParentID]; parent != nil && !parent.Expanded {
			parent.Expanded = true
		}
	}
	visible := w.visibleSidebarIDs()
	for i, id := range visible {
		if id == nodeID {
			w.sidebarCursor = i
			return
		}
	}
}

// syncProjectCursorToTask sets projectTaskCursor to the position of taskID in
// the current selectedProject's open task list. If selectedProject is nil (not
// yet loaded), the ID is stored in pendingProjectCursorTaskID and applied once
// the project detail arrives.
func (w *workspaceState) syncProjectCursorToTask(taskID string) {
	if taskID == "" {
		return
	}
	w.pendingProjectCursorTaskID = taskID // always store so it can be applied on load
	if w.selectedProject == nil {
		return
	}
	idx := 0
	for _, t := range w.selectedProject.Tasks {
		if t.WorkStatus == "done" || t.WorkStatus == "approved" || t.WorkStatus == "cancelled" {
			continue
		}
		if t.ID == taskID {
			w.projectTaskCursor = idx
			w.pendingProjectCursorTaskID = "" // consumed
			return
		}
		idx++
	}
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
		if node.TaskID != "" {
			w.selectedTaskID = node.TaskID
		}
	case sidebarKindSession:
		if node.TaskID != "" {
			w.mainView = ViewTask
			w.selectedTaskID = node.TaskID
			w.rememberTaskDiscussionSession(node.TaskID, node.SessionID)
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

// totalUnreadSessions returns the sum of unread counts across all session nodes.
// Used to show an unread badge on the CHATS section header.
func (w *workspaceState) totalUnreadSessions() int {
	total := 0
	for _, node := range w.nodes {
		if node.Kind == sidebarKindSession {
			total += node.Unread
		}
	}
	return total
}

// chatSessionCount returns the number of non-Frank session nodes (recent chats).
// It counts only session nodes in topLevel (not children of projects).
func (w *workspaceState) chatSessionCount() int {
	count := 0
	for _, id := range w.topLevel {
		if id == generalSidebarNodeID {
			continue
		}
		node := w.nodes[id]
		if node != nil && node.Kind == sidebarKindSession {
			count++
		}
	}
	return count
}

// nextUnreadSession returns the sidebar node ID of the next session node with
// Unread > 0, cycling through visible nodes starting after the current cursor.
// Returns "" if there are no unread sessions.
func (w *workspaceState) nextUnreadSession() string {
	visible := w.visibleSidebarIDs()
	n := len(visible)
	if n == 0 {
		return ""
	}
	start := (w.sidebarCursor + 1) % n
	for i := 0; i < n; i++ {
		id := visible[(start+i)%n]
		node := w.nodes[id]
		if node != nil && node.Kind == sidebarKindSession && node.Unread > 0 {
			return id
		}
	}
	return ""
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
		if task.RequiresHumanReview {
			counts.Review++
			continue
		}
		switch task.Status {
		case "draft", "todo":
			counts.Queued++
		case "done", "approved", "cancelled":
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

// openTasksForProject returns the best available live open-task view for the
// selected project. When no source has loaded yet, it returns nil.
func (w *workspaceState) openTasksForProject() []SidebarTaskItem {
	openTasks, _ := w.projectOpenTasks(w.selectedProjectID)
	return openTasks
}

func (w *workspaceState) projectOpenTasks(projectID string) ([]SidebarTaskItem, bool) {
	trimmedProjectID := strings.TrimSpace(projectID)
	if trimmedProjectID == "" {
		return nil, false
	}
	if snapshot, ok := w.projectTaskSnapshot(trimmedProjectID); ok {
		return activeSidebarTaskItems(snapshot), true
	}

	candidates := make(map[string]SidebarTaskItem)
	order := make([]string, 0)
	addCandidate := func(item SidebarTaskItem) {
		item.ID = strings.TrimSpace(item.ID)
		item.Title = strings.TrimSpace(item.Title)
		item.WorkStatus = strings.TrimSpace(item.WorkStatus)
		if item.ID == "" || isCompletedTaskStatus(item.WorkStatus) {
			return
		}
		if existing, ok := candidates[item.ID]; ok {
			candidates[item.ID] = mergeSidebarTaskItem(existing, item)
			return
		}
		candidates[item.ID] = item
		order = append(order, item.ID)
	}

	if w.selectedProject != nil && strings.EqualFold(strings.TrimSpace(w.selectedProject.ID), trimmedProjectID) {
		for _, task := range w.selectedProject.Tasks {
			addCandidate(task)
		}
	}
	for _, childID := range w.projectChildren("project-" + trimmedProjectID) {
		child := w.nodes[childID]
		if child == nil || child.Kind != sidebarKindTask {
			continue
		}
		addCandidate(SidebarTaskItem{
			ID:         strings.TrimSpace(child.TaskID),
			Title:      strings.TrimSpace(child.Label),
			WorkStatus: strings.TrimSpace(child.WorkStatus),
			TaskNumber: child.TaskNumber,
		})
	}
	recordItems := make([]SidebarTaskItem, 0, len(w.tasks))
	for _, record := range w.tasks {
		if record == nil || !strings.EqualFold(strings.TrimSpace(record.ProjectID), trimmedProjectID) {
			continue
		}
		if item, ok := taskRecordSidebarItem(record); ok {
			recordItems = append(recordItems, item)
		}
	}
	sortSidebarTaskItems(recordItems)
	for _, item := range recordItems {
		addCandidate(item)
	}
	if len(order) == 0 {
		return nil, false
	}

	result := make([]SidebarTaskItem, 0, len(order))
	for _, taskID := range order {
		result = append(result, candidates[taskID])
	}
	return result, true
}

// navigableTasksForProject returns all tasks the cursor can reach:
// open tasks first, then done tasks (when showDoneTasks is true or
// when all open tasks are complete — matching the auto-show logic in
// renderProjectView).
func (w *workspaceState) navigableTasksForProject() []SidebarTaskItem {
	if w.selectedProject == nil {
		return nil
	}
	openTasks, ready := w.projectOpenTasks(w.selectedProjectID)
	result := append([]SidebarTaskItem(nil), openTasks...)
	// EX-491: auto-show done tasks when all open tasks are complete,
	// matching the autoShowDone logic in renderProjectView.
	autoShowDone := ready && len(result) == 0 && w.selectedProject.DoneCount > 0
	if w.showDoneTasks || autoShowDone {
		result = append(result, w.selectedProject.DoneTasks...)
	}
	return result
}

func (w *workspaceState) moveProjectTaskCursor(delta int) {
	nav := w.navigableTasksForProject()
	if len(nav) == 0 {
		w.projectTaskCursor = 0
		return
	}
	w.projectTaskCursor += delta
	if w.projectTaskCursor < 0 {
		w.projectTaskCursor = 0
	}
	if w.projectTaskCursor >= len(nav) {
		w.projectTaskCursor = len(nav) - 1
	}
}

// dashboardActiveTasks returns the ordered list of task IDs that are visible on the dashboard board
// (excludes done/approved/cancelled tasks).
func (w *workspaceState) dashboardActiveTasks() []string {
	// Group tasks by column so j/k navigation moves within a column before
	// crossing to the next. Order: queued → in_progress → blocked → review → done.
	var queued, inProg, blocked, review, done []string
	for _, id := range w.taskOrder {
		t := w.tasks[id]
		if t == nil {
			continue
		}
		if t.RequiresHumanReview {
			review = append(review, id)
			continue
		}
		switch t.Status {
		case "draft", "todo":
			queued = append(queued, id)
		case "done", "approved", "cancelled":
			done = append(done, id)
		case "blocked", "rejected", "deferred":
			blocked = append(blocked, id)
		default:
			inProg = append(inProg, id)
		}
	}
	out := make([]string, 0, len(w.taskOrder))
	out = append(out, queued...)
	out = append(out, inProg...)
	out = append(out, blocked...)
	out = append(out, review...)
	out = append(out, done...)
	return out
}

func (w *workspaceState) moveDashboardCursor(delta int) {
	active := w.dashboardActiveTasks()
	if len(active) == 0 {
		w.dashboardCursor = 0
		return
	}
	// When nothing is selected yet, initialise cursor so the first j/k press
	// lands on the first or last item rather than skipping the first entry.
	if w.selectedTaskID == "" {
		if delta > 0 {
			w.dashboardCursor = -1
		} else {
			w.dashboardCursor = len(active)
		}
	}
	w.dashboardCursor += delta
	if w.dashboardCursor < 0 {
		w.dashboardCursor = 0
	}
	if w.dashboardCursor >= len(active) {
		w.dashboardCursor = len(active) - 1
	}
	w.selectedTaskID = active[w.dashboardCursor]
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
	w.inboxCursor = len(w.inbox) - 1
}

func (w *workspaceState) applyInboxAction(action string) bool {
	item := w.currentInboxItem()
	if item == nil {
		return false
	}

	taskLabel := item.TaskID
	if t := w.tasks[item.TaskID]; t != nil && t.TaskNumber > 0 {
		taskLabel = fmt.Sprintf("OC-%d", t.TaskNumber)
	} else if len(item.Summary) > 0 {
		s := item.Summary
		if len(s) > 30 {
			s = s[:27] + "..."
		}
		taskLabel = s
	}

	switch action {
	case "approve":
		w.setTaskStatus(item.TaskID, "approved")
		w.activity = appendActivity(w.activity, fmt.Sprintf("approved: %s", taskLabel))
		w.removeInboxItem(item.ID)
		return true
	case "reject":
		w.setTaskStatus(item.TaskID, "rejected")
		w.activity = appendActivity(w.activity, fmt.Sprintf("rejected: %s", taskLabel))
		w.removeInboxItem(item.ID)
		return true
	case "defer":
		w.setTaskStatus(item.TaskID, "deferred")
		w.activity = appendActivity(w.activity, fmt.Sprintf("deferred: %s", taskLabel))
		w.removeInboxItem(item.ID)
		return true
	case "open":
		w.selectedTaskID = item.TaskID
		w.mainView = ViewTask
		if sessionID := w.taskSessionID(item.TaskID); sessionID != "" {
			w.activeSessionID = sessionID
		}
		w.activity = appendActivity(w.activity, fmt.Sprintf("opened: %s", taskLabel))
		return true
	default:
		return false
	}
}

// applyInboxActionForTask finds the first inbox item for taskID and applies
// action to it. Used by ViewTask keybindings (a/x/f) so that the
// approve/reject/defer hints shown in the task detail view actually work.
// EX-148: task view showed approval hints but they only worked in ViewInbox.
// inboxItemIDForTask returns the inbox item ID for the first item that matches
// taskID. Used by EX-160 key handlers to capture the item ID before
// applyInboxActionForTask removes the item from the list.
func (w *workspaceState) inboxItemIDForTask(taskID string) string {
	for _, item := range w.inbox {
		if item.TaskID == taskID {
			return item.ID
		}
	}
	return ""
}

func (w *workspaceState) applyInboxActionForTask(taskID, action string) bool {
	if taskID == "" {
		return false
	}
	for _, item := range w.inbox {
		if item.TaskID != taskID {
			continue
		}
		// Found a matching inbox item — set the inbox cursor to it and delegate.
		for i, it := range w.inbox {
			if it.ID == item.ID {
				w.inboxCursor = i
				break
			}
		}
		return w.applyInboxAction(action)
	}
	return false
}

func (w *workspaceState) removeInboxItem(id string) {
	// Capture the task ID of the removed item before filtering.
	removedTaskID := ""
	for _, item := range w.inbox {
		if item.ID == id {
			removedTaskID = item.TaskID
			break
		}
	}
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
	// EX-123: clear RequiresHumanReview on the task when no inbox items remain
	// for it, so the ⚠ badge in the task detail view disappears immediately
	// after the user acts on the last review request.
	if removedTaskID != "" {
		hasRemaining := false
		for _, item := range w.inbox {
			if item.TaskID == removedTaskID {
				hasRemaining = true
				break
			}
		}
		if !hasRemaining {
			if rec := w.tasks[removedTaskID]; rec != nil {
				rec.RequiresHumanReview = false
			}
		}
	}
}

// syncTaskHumanReviewFromInbox sets RequiresHumanReview=true for any task
// that currently has inbox items. Called after inbox items are loaded or
// reloaded so the task detail view reflects the latest review state without
// requiring a full task detail API call.
func (w *workspaceState) syncTaskHumanReviewFromInbox() {
	for _, item := range w.inbox {
		if item.TaskID == "" {
			continue
		}
		if rec := w.tasks[item.TaskID]; rec != nil {
			rec.RequiresHumanReview = true
		}
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
// nodes that were already loaded. orgSessionID, if non-empty, is the real UUID
// of the org-scope chat session (Frank / General).
func (w *workspaceState) rebuildSidebar(orgSessionID string, chats []SidebarChatItem, projects []SidebarProjectItem) {
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
	taskSessionIDs := make(map[string]string, len(w.taskSessionIDs))
	for taskID, sessionID := range w.taskSessionIDs {
		taskID = strings.TrimSpace(taskID)
		sessionID = strings.TrimSpace(sessionID)
		if taskID == "" || sessionID == "" {
			continue
		}
		taskSessionIDs[taskID] = sessionID
	}

	frankSessionID := generalSessionID
	if orgSessionID != "" {
		frankSessionID = orgSessionID
		w.activeSessionID = orgSessionID
	}

	frankNode := w.nodes[generalSidebarNodeID]
	if frankNode == nil {
		frankNode = &sidebarNode{
			ID:        generalSidebarNodeID,
			Label:     "Frank / General",
			Kind:      sidebarKindSession,
			SessionID: frankSessionID,
		}
	} else {
		frankNode.SessionID = frankSessionID
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
		taskID := ""
		projectID := ""
		if chat.ScopeType == "project_task" {
			taskID = chat.ScopeID
			if strings.TrimSpace(taskID) != "" && strings.TrimSpace(chat.SessionID) != "" {
				taskSessionIDs[taskID] = strings.TrimSpace(chat.SessionID)
			}
		}
		if chat.ScopeType == "project" {
			projectID = chat.ScopeID
		}
		label := strings.TrimSpace(chat.DisplayName)
		if chat.ScopeType == "project" {
			for _, project := range projects {
				if strings.EqualFold(strings.TrimSpace(project.ID), strings.TrimSpace(projectID)) {
					label = ResolveProjectLabel(project.DisplayName, project.Slug, label)
					break
				}
			}
			if strings.TrimSpace(label) == "" {
				label = ResolveProjectLabel("", "", chat.DisplayName)
			}
		}
		newNodes[id] = &sidebarNode{
			ID:           id,
			Label:        label,
			Kind:         sidebarKindSession,
			SessionID:    chat.SessionID,
			SessionScope: chat.ScopeType,
			ProjectID:    projectID,
			TaskID:       taskID,
			WorkStatus:   chat.WorkStatus,
			UpdatedAt:    chat.UpdatedAt,
		}
		newTopLevel = append(newTopLevel, id)
	}

	newTopLevel = append(newTopLevel, "header-projects")

	// Projects
	for _, proj := range projects {
		id := "project-" + proj.ID
		newNodes[id] = &sidebarNode{
			ID:          id,
			Label:       projectSidebarLabel(proj.DisplayName, proj.Slug, proj.IsPaused),
			Kind:        sidebarKindProject,
			ProjectID:   proj.ID,
			ProjectSlug: strings.TrimSpace(proj.Slug),
			Expanded:    expandedProjects[id],
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
	w.taskSessionIDs = taskSessionIDs
}

// existingProjects extracts the current project items from the sidebar nodes.
// EX-494: used to preserve projects when a sidebar reload fails (e.g. rate-limiting).
func (w *workspaceState) existingProjects() []SidebarProjectItem {
	var out []SidebarProjectItem
	for _, id := range w.topLevel {
		if node := w.nodes[id]; node != nil && node.Kind == sidebarKindProject {
			out = append(out, SidebarProjectItem{
				ID:          node.ProjectID,
				Slug:        node.ProjectSlug,
				DisplayName: node.Label,
			})
		}
	}
	return out
}

func (w *workspaceState) knownActiveProjects() []SidebarProjectItem {
	seen := make(map[string]struct{})
	out := make([]SidebarProjectItem, 0)
	add := func(id, displayName, slug, sessionTitle string, updatedAt time.Time) {
		trimmedID := strings.TrimSpace(id)
		if trimmedID == "" {
			return
		}
		if _, exists := seen[trimmedID]; exists {
			return
		}
		resolvedLabel := ResolveProjectLabel(displayName, slug, sessionTitle)
		out = append(out, SidebarProjectItem{
			ID:          trimmedID,
			Slug:        strings.TrimSpace(slug),
			DisplayName: resolvedLabel,
			UpdatedAt:   updatedAt,
		})
		seen[trimmedID] = struct{}{}
	}

	for _, project := range w.existingProjects() {
		add(project.ID, project.DisplayName, project.Slug, "", project.UpdatedAt)
	}
	if w.selectedProject != nil {
		add(w.selectedProject.ID, w.selectedProject.DisplayName, w.selectedProject.Slug, "", time.Time{})
	}
	if strings.TrimSpace(w.selectedProjectID) != "" {
		add(w.selectedProjectID, "", "", "", time.Time{})
	}
	for _, id := range w.topLevel {
		node := w.nodes[id]
		if node == nil || node.Kind != sidebarKindSession || node.SessionScope != "project" {
			continue
		}
		add(node.ProjectID, "", "", node.Label, node.UpdatedAt)
	}
	for _, task := range w.tasks {
		if task == nil {
			continue
		}
		add(task.ProjectID, "", "", "", time.Time{})
	}

	return out
}

func projectSidebarLabel(displayName, slug string, isPaused bool) string {
	label := ResolveProjectLabel(displayName, slug, "")
	if isPaused {
		return label + " [paused]"
	}
	return label
}

func (w *workspaceState) projectDisplayName(projectID string) string {
	trimmedID := strings.TrimSpace(projectID)
	if trimmedID == "" {
		return ""
	}
	var displayName string
	var slug string
	var sessionTitle string
	if w.selectedProject != nil && strings.EqualFold(strings.TrimSpace(w.selectedProject.ID), trimmedID) {
		displayName = w.selectedProject.DisplayName
		slug = w.selectedProject.Slug
	}
	if node := w.nodes["project-"+trimmedID]; node != nil {
		if strings.TrimSpace(displayName) == "" {
			displayName = node.Label
		}
		if strings.TrimSpace(slug) == "" {
			slug = node.ProjectSlug
		}
	}
	for _, id := range w.topLevel {
		node := w.nodes[id]
		if node == nil || node.Kind != sidebarKindSession || node.SessionScope != "project" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(node.ProjectID), trimmedID) && strings.TrimSpace(sessionTitle) == "" {
			sessionTitle = node.Label
		}
	}
	return ResolveProjectLabel(displayName, slug, sessionTitle)
}

// existingChats extracts the current chat session items from the sidebar nodes.
// EX-494: used to preserve chats when a sidebar reload fails (e.g. rate-limiting).
func (w *workspaceState) existingChats() []SidebarChatItem {
	var out []SidebarChatItem
	for _, id := range w.topLevel {
		if node := w.nodes[id]; node != nil && node.Kind == sidebarKindSession && id != generalSidebarNodeID {
			scopeID := node.TaskID
			if node.SessionScope == "project" {
				scopeID = node.ProjectID
			}
			out = append(out, SidebarChatItem{
				SessionID:   node.SessionID,
				DisplayName: node.Label,
				ScopeType:   node.SessionScope,
				ScopeID:     scopeID,
				WorkStatus:  node.WorkStatus,
				UpdatedAt:   node.UpdatedAt,
			})
		}
	}
	return out
}

// setProjectTasks replaces the task children for the given project ID.
// When expandNode is true the project is also marked as expanded in the sidebar.
func (w *workspaceState) setProjectTasks(projectID string, tasks []SidebarTaskItem, expandNode bool) {
	nodeID := "project-" + projectID
	activeTasks := activeSidebarTaskItems(tasks)
	// Remove existing task nodes for this project
	for id, node := range w.nodes {
		if node.Kind == sidebarKindTask && node.ParentID == nodeID {
			delete(w.nodes, id)
		}
	}
	// Add new task nodes and populate w.tasks with basic records.
	// Active tasks (not done/approved/cancelled) get sidebar nodes so they appear
	// under expanded projects. All tasks are seeded into w.tasks so the dashboard
	// board can count and display done tasks as well.
	for _, task := range tasks {
		id := "task-" + task.ID
		label := task.Title
		if task.TaskNumber > 0 {
			label = fmt.Sprintf("OC-%d: %s", task.TaskNumber, task.Title)
		}
		// Only add active tasks to the sidebar tree (PROJECTS section shows open tasks).
		isCompleted := task.WorkStatus == "done" || task.WorkStatus == "approved" || task.WorkStatus == "cancelled"
		if !isCompleted {
			w.nodes[id] = &sidebarNode{
				ID:         id,
				Label:      label,
				Kind:       sidebarKindTask,
				ParentID:   nodeID,
				TaskID:     task.ID,
				TaskNumber: task.TaskNumber,
				WorkStatus: task.WorkStatus,
			}
		}
		// Seed basic task record so the TASK DETAIL center pane renders immediately,
		// and so the dashboard board can include done tasks in boardCounts().
		if existing, exists := w.tasks[task.ID]; exists {
			existing.ProjectID = projectID
			existing.Title = task.Title
			existing.Status = task.WorkStatus
			existing.TaskNumber = task.TaskNumber
			existing.Priority = task.Priority
		} else {
			w.tasks[task.ID] = &taskRecord{
				ID:         task.ID,
				ProjectID:  projectID,
				Title:      task.Title,
				Status:     task.WorkStatus,
				TaskNumber: task.TaskNumber,
				Priority:   task.Priority,
			}
			// Add to taskOrder for the dashboard board (deduplicated).
			w.taskOrder = append(w.taskOrder, task.ID)
		}
	}
	// Re-sort taskOrder by task number descending so dashboard board shows newest tasks first.
	sort.Slice(w.taskOrder, func(i, j int) bool {
		ti := w.tasks[w.taskOrder[i]]
		tj := w.tasks[w.taskOrder[j]]
		if ti == nil || tj == nil {
			return false
		}
		return ti.TaskNumber > tj.TaskNumber
	})
	w.projectTaskLists[strings.TrimSpace(projectID)] = cloneSidebarTaskItems(activeTasks)
	if w.selectedProject != nil && strings.EqualFold(strings.TrimSpace(w.selectedProject.ID), strings.TrimSpace(projectID)) {
		w.selectedProject.Tasks = cloneSidebarTaskItems(activeTasks)
		if w.pendingProjectCursorTaskID != "" {
			w.syncProjectCursorToTask(w.pendingProjectCursorTaskID)
		}
		openTasks := w.openTasksForProject()
		if len(openTasks) == 0 {
			w.projectTaskCursor = 0
		} else if w.projectTaskCursor >= len(openTasks) {
			w.projectTaskCursor = len(openTasks) - 1
		}
	}
	// Expand the project node only when explicitly requested by user interaction.
	if expandNode {
		if proj, ok := w.nodes[nodeID]; ok {
			proj.Expanded = true
		}
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

// applyRealtimeEnvelope is a workspace-level SSE hook called from model.go when
// applyWorkspaceCommand returns nil (no cmd needed). All current workspace event
// handling has been consolidated into applyWorkspaceCommand (model.go) which can
// return tea.Cmd values. The two cases that were here ("task.status.changed" and
// "task.flow.advanced") used wrong event names and were dead code — removed by EX-139.
func (w *workspaceState) applyRealtimeEnvelope(_ EventEnvelope) {}

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
		return fmt.Sprintf("view=dashboard size=%s tasks(queued=%d,in_progress=%d,done=%d,blocked=%d,review=%d) inbox=%d", class, counts.Queued, counts.InProgress, counts.Done, counts.Blocked, counts.Review, len(w.inbox))
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
	case ViewSettings:
		profiles := 0
		if w.settings != nil {
			profiles = len(w.settings.Profiles)
		}
		return fmt.Sprintf("view=settings size=%s profiles=%d", class, profiles)
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
