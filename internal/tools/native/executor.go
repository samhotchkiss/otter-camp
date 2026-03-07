package native

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	agentsvc "github.com/samhotchkiss/otter-camp/internal/agent"
	chatsvc "github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/profiles"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
)

const (
	defaultReadMaxBytes    = 1 * 1024 * 1024
	hardReadMaxBytes       = 10 * 1024 * 1024
	defaultListMaxEntries  = 1000
	defaultSearchMaxResult = 100
	hardSearchMaxResult    = 500
	diffOutputMaxBytes     = 200 * 1024
	defaultGitLogLimit     = 20
	maxGitLogLimit         = 100
)

type memoryQueryService interface {
	Query(ctx context.Context, req memory.RetrievalRequest) (memory.RetrievalResult, error)
}

type memoryRecordService interface {
	RecordExplicit(ctx context.Context, agentID uuid.UUID, content, scope, sensitivity string, tags []string) (uuid.UUID, string, error)
}

type memoryWriter interface {
	Create(ctx context.Context, memory repo.Memory) (repo.Memory, error)
}

type chatSessionReader interface {
	Create(ctx context.Context, session repo.ChatSession) (repo.ChatSession, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ChatSession, error)
	ListByOrg(ctx context.Context, organizationID uuid.UUID) ([]repo.ChatSession, error)
	Close(ctx context.Context, id uuid.UUID) (repo.ChatSession, error)
}

type organizationReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Organization, error)
}

type projectReader interface {
	Create(ctx context.Context, project repo.Project) (repo.Project, error)
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.Project, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
	GetBySlug(ctx context.Context, organizationID uuid.UUID, slug string) (repo.Project, error)
	Update(ctx context.Context, project repo.Project) (repo.Project, error)
	Archive(ctx context.Context, id uuid.UUID) error
}

type taskReader interface {
	Create(ctx context.Context, task repo.ProjectTask) (repo.ProjectTask, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
	ListByProject(ctx context.Context, projectID uuid.UUID, statuses ...string) ([]repo.ProjectTask, error)
	SetFlowNode(ctx context.Context, id uuid.UUID, flowNodeID *uuid.UUID) (repo.ProjectTask, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) (repo.ProjectTask, error)
	Update(ctx context.Context, task repo.ProjectTask) (repo.ProjectTask, error)
}

type inboxReader interface {
	Create(ctx context.Context, item repo.InboxItem) (repo.InboxItem, error)
	ListForUser(ctx context.Context, organizationID, userID uuid.UUID, options repo.InboxListOptions) ([]repo.InboxItem, error)
	ListBroadcast(ctx context.Context, organizationID uuid.UUID, options repo.InboxListOptions) ([]repo.InboxItem, error)
	MarkActed(ctx context.Context, id, actedByID uuid.UUID) (repo.InboxItem, error)
}

type chatParticipantReader interface {
	Create(ctx context.Context, participant repo.ChatParticipant) (repo.ChatParticipant, error)
	ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatParticipant, error)
}

type chatMessageReader interface {
	Create(ctx context.Context, message repo.ChatMessage) (repo.ChatMessage, error)
	ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatMessage, error)
}

type agentReader interface {
	Create(ctx context.Context, agent repo.Agent) (repo.Agent, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.Agent, error)
	Update(ctx context.Context, agent repo.Agent) (repo.Agent, error)
}

type agentService interface {
	Create(ctx context.Context, req agentsvc.CreateAgentRequest) (*agentsvc.Agent, error)
	Unpause(ctx context.Context, orgID, agentID uuid.UUID) error
}

type flowTemplateReader interface {
	Create(ctx context.Context, template repo.FlowTemplate) (repo.FlowTemplate, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowTemplate, error)
	GetCurrentBySlug(ctx context.Context, organizationID, projectID *uuid.UUID, slug string) (repo.FlowTemplate, error)
	ListCurrent(ctx context.Context, organizationID, projectID *uuid.UUID) ([]repo.FlowTemplate, error)
	Update(ctx context.Context, template repo.FlowTemplate) (repo.FlowTemplate, error)
}

type flowNodeReader interface {
	Create(ctx context.Context, node repo.FlowNode) (repo.FlowNode, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNode, error)
	GetByTemplateOrdered(ctx context.Context, flowTemplateID uuid.UUID) ([]repo.FlowNode, error)
	Update(ctx context.Context, node repo.FlowNode) (repo.FlowNode, error)
}

type flowExecutionReader interface {
	Complete(ctx context.Context, id uuid.UUID) (repo.FlowNodeExecution, error)
	Create(ctx context.Context, execution repo.FlowNodeExecution) (repo.FlowNodeExecution, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNodeExecution, error)
	ListByTask(ctx context.Context, taskID uuid.UUID) ([]repo.FlowNodeExecution, error)
	RecordCommitSHA(ctx context.Context, id uuid.UUID, commitSHA string) (repo.FlowNodeExecution, error)
	Reject(ctx context.Context, id uuid.UUID) (repo.FlowNodeExecution, error)
}

type subtaskReader interface {
	Create(ctx context.Context, subtask repo.ProjectSubtask) (repo.ProjectSubtask, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectSubtask, error)
	ListByExecution(ctx context.Context, flowNodeExecutionID uuid.UUID) ([]repo.ProjectSubtask, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) (repo.ProjectSubtask, error)
}

type scheduleReader interface {
	Create(ctx context.Context, schedule repo.TaskSchedule) (repo.TaskSchedule, error)
	Delete(ctx context.Context, id uuid.UUID) error
	GetByID(ctx context.Context, id uuid.UUID) (repo.TaskSchedule, error)
	List(ctx context.Context, projectID uuid.UUID) ([]repo.TaskSchedule, error)
	Update(ctx context.Context, schedule repo.TaskSchedule) (repo.TaskSchedule, error)
}

type mergeQueueReader interface {
	ListActive(ctx context.Context, projectID uuid.UUID) ([]repo.MergeQueueEntry, error)
}

type dependencyRepository interface {
	Add(ctx context.Context, dependency repo.ProjectTaskDependency) (repo.ProjectTaskDependency, error)
	CheckCycle(ctx context.Context, sourceType string, sourceID, dependsOnID uuid.UUID) (bool, error)
	Remove(ctx context.Context, id uuid.UUID) error
}

type projectAssigner interface {
	Assign(ctx context.Context, assignment repo.AgentProjectAssignment) (repo.AgentProjectAssignment, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.AgentProjectAssignment, error)
}

type projectEnvironmentReader interface {
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.ProjectEnvironment, error)
}

type planningArtifactStore interface {
	UpsertVersion(ctx context.Context, artifact repo.PlanningArtifactUpsert) (repo.PlanningArtifact, bool, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.PlanningArtifact, error)
	ListBySourceTask(ctx context.Context, taskID uuid.UUID) ([]repo.PlanningArtifact, error)
	ListVersions(ctx context.Context, artifactID uuid.UUID) ([]repo.PlanningArtifactVersion, error)
}

type eventPublisher interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
}

type subscribedEventPublisher interface {
	eventPublisher
	Subscribe(consumerName string, orgID *uuid.UUID, handler eventbus.EventHandler) eventbus.Subscription
}

type publishOnlyEventBus struct {
	publisher eventPublisher
}

func (b publishOnlyEventBus) Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error {
	return b.publisher.Publish(ctx, tx, event)
}

func (publishOnlyEventBus) Subscribe(string, *uuid.UUID, eventbus.EventHandler) eventbus.Subscription {
	return eventbus.Subscription{}
}

func (publishOnlyEventBus) Unsubscribe(eventbus.Subscription) {}

type cliExecutor interface {
	Execute(ctx context.Context, input map[string]any) (map[string]any, error)
}

type secretResolver interface {
	Get(ctx context.Context, orgID uuid.UUID, slug string) (string, error)
}

type commandContextFunc func(ctx context.Context, name string, args ...string) *exec.Cmd

type profileCatalog interface {
	Search(category, roleType, query string, limit int) []profiles.RosterEntry
	GetByRoleID(roleID string) (*profiles.ProfileDetail, bool)
	Categories() []string
}

type ExecutorOptions struct {
	Pool           *pgxpool.Pool
	DataDir        string
	WorkspaceRoot  string
	Memory         memoryQueryService
	MemoryRecorder memoryRecordService
	AgentService   agentService
	CLI            cliExecutor
	Events         eventPublisher
	Command        commandContextFunc
	Secrets        secretResolver
	Profiles       profileCatalog
}

type NativeToolExecutor struct {
	pool           *pgxpool.Pool
	dataDir        string
	explicitRoot   string
	memory         memoryQueryService
	memoryRecorder memoryRecordService
	cli            cliExecutor
	events         eventPublisher
	command        commandContextFunc
	flowService    flowsvc.FlowExecutionService
	flowServiceErr error
	chatSessions   chatSessionReader
	organizations  organizationReader
	projects       projectReader
	tasks          taskReader
	inbox          inboxReader
	participants   chatParticipantReader
	messages       chatMessageReader
	agents         agentReader
	agentService   agentService
	flowTemplates  flowTemplateReader
	flowNodes      flowNodeReader
	flowExecs      flowExecutionReader
	subtasks       subtaskReader
	schedules      scheduleReader
	mergeQueue     mergeQueueReader
	dependencies   dependencyRepository
	assignments    projectAssigner
	environments   projectEnvironmentReader
	planningAssets planningArtifactStore
	audit          *repo.AuditEventRepo
	memories       memoryWriter
	secrets        secretResolver
	profiles       profileCatalog

	mu         sync.Mutex
	workspaces map[string]SessionWorkDir
}

func NewExecutor(opts ExecutorOptions) *NativeToolExecutor {
	dataDir := resolveDataDir(opts.DataDir)
	command := opts.Command
	if command == nil {
		command = exec.CommandContext
	}
	exec := &NativeToolExecutor{
		pool:           opts.Pool,
		dataDir:        dataDir,
		explicitRoot:   strings.TrimSpace(opts.WorkspaceRoot),
		memory:         opts.Memory,
		memoryRecorder: opts.MemoryRecorder,
		agentService:   opts.AgentService,
		cli:            opts.CLI,
		events:         opts.Events,
		command:        command,
		secrets:        opts.Secrets,
		profiles:       opts.Profiles,
		workspaces:     make(map[string]SessionWorkDir),
	}

	if opts.Pool != nil {
		agentRepo := repo.NewAgentRepo(opts.Pool)
		exec.chatSessions = repo.NewChatSessionRepo(opts.Pool)
		exec.organizations = repo.NewOrgRepo(opts.Pool)
		exec.projects = repo.NewProjectRepo(opts.Pool)
		exec.tasks = repo.NewProjectTaskRepo(opts.Pool)
		exec.inbox = repo.NewInboxItemRepo(opts.Pool)
		exec.participants = repo.NewChatParticipantRepo(opts.Pool)
		exec.messages = repo.NewChatMessageRepo(opts.Pool)
		exec.agents = agentRepo
		exec.flowTemplates = repo.NewFlowTemplateRepo(opts.Pool)
		exec.flowNodes = repo.NewFlowNodeRepo(opts.Pool)
		exec.flowExecs = repo.NewFlowNodeExecutionRepo(opts.Pool)
		exec.subtasks = repo.NewProjectSubtaskRepo(opts.Pool)
		exec.schedules = repo.NewTaskScheduleRepo(opts.Pool)
		exec.mergeQueue = repo.NewMergeQueueEntryRepo(opts.Pool)
		exec.dependencies = repo.NewProjectTaskDependencyRepo(opts.Pool)
		exec.assignments = repo.NewAgentProjectAssignmentRepo(opts.Pool)
		exec.environments = repo.NewProjectEnvironmentRepo(opts.Pool)
		exec.planningAssets = repo.NewPlanningArtifactRepo(opts.Pool)
		exec.audit = repo.NewAuditEventRepo(opts.Pool)
		exec.memories = repo.NewMemoryRepo(opts.Pool)
		if exec.events == nil {
			exec.events = eventbus.New(opts.Pool, nil, eventbus.Config{})
		}
		if exec.agentService == nil {
			var agentEvents eventbus.EventBus = publishOnlyEventBus{publisher: exec.events}
			if bus, ok := exec.events.(eventbus.EventBus); ok {
				agentEvents = bus
			}
			agentService, agentErr := agentsvc.NewService(agentsvc.Options{
				Pool:   opts.Pool,
				Agents: agentRepo,
				Events: agentEvents,
			})
			if agentErr == nil {
				exec.agentService = agentService
			}
		}

		chatService, chatErr := chatsvc.NewService(chatsvc.Options{
			Pool:   opts.Pool,
			Events: exec.events,
		})
		if chatErr != nil {
			exec.flowServiceErr = chatErr
		} else {
			flowSessionBridge, bridgeErr := projectsvc.NewFlowSessionBridge(projectsvc.FlowSessionBridgeOptions{
				Pool:  opts.Pool,
				Chats: chatService,
			})
			if bridgeErr != nil {
				exec.flowServiceErr = bridgeErr
			} else {
				var flowEvents subscribedEventPublisher = publishOnlyEventBus{publisher: exec.events}
				if bus, ok := exec.events.(subscribedEventPublisher); ok {
					flowEvents = bus
				}
				flowService, flowErr := flowsvc.NewService(flowsvc.Options{
					Pool:          opts.Pool,
					Events:        flowEvents,
					SessionBridge: flowSessionBridge,
				})
				if flowErr != nil {
					exec.flowServiceErr = flowErr
				} else {
					exec.flowService = flowService
				}
			}
		}
	}

	return exec
}

func resolveDataDir(raw string) string {
	return workspace.ResolveDataDir(raw)
}

func (e *NativeToolExecutor) Execute(ctx context.Context, toolName string, input map[string]any) (map[string]any, error) {
	switch strings.TrimSpace(toolName) {
	case "file.read":
		return e.handleFileRead(ctx, input)
	case "file.list":
		return e.handleFileList(ctx, input)
	case "file.search":
		return e.handleFileSearch(ctx, input)
	case "file.write":
		return e.handleFileWrite(ctx, input)
	case "file.edit":
		return e.handleFileEdit(ctx, input)
	case "file.delete":
		return e.handleFileDelete(ctx, input)
	case "git.status":
		return e.handleGitStatus(ctx, input)
	case "git.diff":
		return e.handleGitDiff(ctx, input)
	case "git.log":
		return e.handleGitLog(ctx, input)
	case "git.commit":
		return e.handleGitCommit(ctx, input)
	case "git.push":
		return e.handleGitPush(ctx, input)
	case "cli.execute":
		return e.handleCLIExecute(ctx, input)
	case "memory.query":
		return e.handleMemoryQuery(ctx, input)
	case "memory.record":
		return e.handleMemoryRecord(ctx, input)
	case "project.list":
		return e.handleProjectList(ctx, input)
	case "project.get":
		return e.handleProjectGet(ctx, input)
	case "project.create":
		return e.handleProjectCreate(ctx, input)
	case "project.update":
		return e.handleProjectUpdate(ctx, input)
	case "project.archive":
		return e.handleProjectArchive(ctx, input)
	case "task.list":
		return e.handleTaskList(ctx, input)
	case "task.get":
		return e.handleTaskGet(ctx, input)
	case "task.create":
		return e.handleTaskCreate(ctx, input)
	case "task.update":
		return e.handleTaskUpdate(ctx, input)
	case "task.add_dependency":
		return e.handleTaskAddDependency(ctx, input)
	case "task.remove_dependency":
		return e.handleTaskRemoveDependency(ctx, input)
	case "subtask.create":
		return e.handleSubtaskCreate(ctx, input)
	case "subtask.update":
		return e.handleSubtaskUpdate(ctx, input)
	case "flow.advance":
		return e.handleFlowAdvance(ctx, input)
	case "flow.review_decision":
		return e.handleFlowReviewDecision(ctx, input)
	case "flow.create_template":
		return e.handleFlowCreateTemplate(ctx, input)
	case "schedule.create":
		return e.handleScheduleCreate(ctx, input)
	case "schedule.update":
		return e.handleScheduleUpdate(ctx, input)
	case "schedule.delete":
		return e.handleScheduleDelete(ctx, input)
	case "agent.create_staff":
		return e.handleAgentCreateStaff(ctx, input)
	case "agent.create_temp":
		return e.handleAgentCreateTemp(ctx, input)
	case "agent.update":
		return e.handleAgentUpdate(ctx, input)
	case "agent.assign_project":
		return e.handleAgentAssignProject(ctx, input)
	case "session.create":
		return e.handleSessionCreate(ctx, input)
	case "session.invite_agent":
		return e.handleSessionInviteAgent(ctx, input)
	case "message.send":
		return e.handleMessageSend(ctx, input)
	case "email.compose":
		return e.handleEmailCompose(ctx, input)
	case "slack.post":
		return e.handleSlackPost(ctx, input)
	case "inbox.list":
		return e.handleInboxList(ctx, input)
	case "session.list":
		return e.handleSessionList(ctx, input)
	case "session.get":
		return e.handleSessionGet(ctx, input)
	case "session.history":
		return e.handleSessionHistory(ctx, input)
	case "agent.list":
		return e.handleAgentList(ctx, input)
	case "agent.get":
		return e.handleAgentGet(ctx, input)
	case "flow.get_template":
		return e.handleFlowGetTemplate(ctx, input)
	case "flow.list_templates":
		return e.handleFlowListTemplates(ctx, input)
	case "flow.get_execution":
		return e.handleFlowGetExecution(ctx, input)
	case "schedule.list":
		return e.handleScheduleList(ctx, input)
	case "merge_queue.status":
		return e.handleMergeQueueStatus(ctx, input)
	case "tui.navigate":
		return e.handleTUINavigate(ctx, input)
	case "web.search":
		return e.handleWebSearch(ctx, input)
	case "web.fetch":
		return e.handleWebFetch(ctx, input)
	case "staffing.browse_profiles":
		return e.handleStaffingBrowseProfiles(ctx, input)
	case "staffing.get_profile":
		return e.handleStaffingGetProfile(ctx, input)
	default:
		return nil, ErrUnknownTool
	}
}

type workspaceScope struct {
	organizationID uuid.UUID
	agentID        *uuid.UUID
	sessionID      *uuid.UUID
	projectID      *uuid.UUID
	taskID         *uuid.UUID
}

func (e *NativeToolExecutor) resolveScope(ctx context.Context) (workspaceScope, error) {
	execCtx := mcp.ExecutionContextFromContext(ctx)
	if execCtx.OrganizationID == uuid.Nil {
		return workspaceScope{}, fmt.Errorf("execution context is missing organization_id")
	}
	scope := workspaceScope{
		organizationID: execCtx.OrganizationID,
		agentID:        execCtx.AgentID,
		sessionID:      execCtx.SessionID,
		projectID:      copyScopeUUID(execCtx.ProjectID),
		taskID:         copyScopeUUID(execCtx.TaskID),
	}
	if execCtx.SessionID == nil || *execCtx.SessionID == uuid.Nil || e.chatSessions == nil {
		return e.finalizeTaskScope(ctx, scope)
	}

	session, err := e.chatSessions.GetByID(ctx, *execCtx.SessionID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return e.finalizeTaskScope(ctx, scope)
		}
		return workspaceScope{}, err
	}
	if session.OrganizationID != scope.organizationID {
		return workspaceScope{}, fmt.Errorf("session organization mismatch")
	}

	scopeType := strings.TrimSpace(strings.ToLower(session.ScopeType))
	switch scopeType {
	case "project":
		if scope.projectID != nil && *scope.projectID != session.ScopeID {
			return workspaceScope{}, taskScopeResolutionError("session project binding mismatch")
		}
		projectID := session.ScopeID
		scope.projectID = &projectID
	case "project_task":
		if scope.taskID != nil && *scope.taskID != session.ScopeID {
			return workspaceScope{}, taskScopeResolutionError("session task binding mismatch")
		}
		taskID := session.ScopeID
		scope.taskID = &taskID
	}

	return e.finalizeTaskScope(ctx, scope)
}

func (e *NativeToolExecutor) workspaceForContext(ctx context.Context) (SessionWorkDir, workspaceScope, error) {
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return SessionWorkDir{}, workspaceScope{}, err
	}

	root := strings.TrimSpace(e.explicitRoot)
	cacheKey := ""
	if root != "" {
		cacheKey = "explicit:" + root
	} else {
		orgSlug, projectSlug, slugErr := e.resolveWorkspaceSlugs(ctx, scope)
		if slugErr != nil {
			return SessionWorkDir{}, workspaceScope{}, slugErr
		}
		base := filepath.Join(e.dataDir, "workspaces")
		if projectSlug != "" {
			cacheKey = "org:" + orgSlug + ":project:" + projectSlug
			root = filepath.Join(base, projectSlug)
		} else {
			cacheKey = "org:" + orgSlug + ":general"
			root = filepath.Join(base, "general")
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if existing, ok := e.workspaces[cacheKey]; ok {
		return existing, scope, nil
	}
	wd, err := NewSessionWorkDir(root)
	if err != nil {
		return SessionWorkDir{}, workspaceScope{}, err
	}

	e.workspaces[cacheKey] = wd
	return wd, scope, nil
}

func (e *NativeToolExecutor) resolveWorkspaceSlugs(ctx context.Context, scope workspaceScope) (string, string, error) {
	if e.organizations == nil {
		return "", "", fmt.Errorf("organization repository is required for workspace resolution")
	}
	org, err := e.organizations.GetByID(ctx, scope.organizationID)
	if err != nil {
		return "", "", fmt.Errorf("resolve organization slug: %w", err)
	}
	orgSlug := strings.TrimSpace(org.Slug)
	if orgSlug == "" {
		return "", "", fmt.Errorf("organization %s has empty slug", scope.organizationID)
	}

	if scope.projectID == nil {
		return orgSlug, "", nil
	}

	if e.projects == nil {
		return "", "", fmt.Errorf("project repository is required for workspace resolution")
	}
	projectRecord, err := e.projects.GetByID(ctx, *scope.projectID)
	if err != nil {
		return "", "", fmt.Errorf("resolve project slug: %w", err)
	}
	if projectRecord.OrganizationID != scope.organizationID {
		return "", "", fmt.Errorf("project organization mismatch")
	}
	projectSlug := strings.TrimSpace(projectRecord.Slug)
	if projectSlug == "" {
		return "", "", fmt.Errorf("project %s has empty slug", projectRecord.ID)
	}

	return orgSlug, projectSlug, nil
}

func (e *NativeToolExecutor) finalizeTaskScope(ctx context.Context, scope workspaceScope) (workspaceScope, error) {
	if scope.taskID == nil || *scope.taskID == uuid.Nil {
		return scope, nil
	}
	if e.tasks == nil {
		return workspaceScope{}, taskScopeResolutionError("task repository unavailable")
	}
	taskRecord, err := e.tasks.GetByID(ctx, *scope.taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return workspaceScope{}, taskScopeResolutionError("task record not found")
		}
		return workspaceScope{}, err
	}
	if taskRecord.OrganizationID != uuid.Nil && taskRecord.OrganizationID != scope.organizationID {
		return workspaceScope{}, taskScopeResolutionError("task organization mismatch")
	}
	if taskRecord.ProjectID == uuid.Nil {
		return workspaceScope{}, taskScopeResolutionError("project binding missing")
	}
	if scope.projectID != nil && *scope.projectID != taskRecord.ProjectID {
		return workspaceScope{}, taskScopeResolutionError("project binding mismatch")
	}
	projectID := taskRecord.ProjectID
	scope.projectID = &projectID
	return scope, nil
}

func taskScopeResolutionError(detail string) error {
	trimmed := strings.TrimSpace(detail)
	if trimmed == "" {
		return fmt.Errorf("internal invariant: task-scoped execution is missing bound task context")
	}
	return fmt.Errorf("internal invariant: task-scoped execution is missing bound task context: %s", trimmed)
}

func copyScopeUUID(id *uuid.UUID) *uuid.UUID {
	if id == nil || *id == uuid.Nil {
		return nil
	}
	value := *id
	return &value
}

func (e *NativeToolExecutor) resolveInputPath(ctx context.Context, input map[string]any, key string) (SessionWorkDir, workspaceScope, string, error) {
	wd, scope, err := e.workspaceForContext(ctx)
	if err != nil {
		return SessionWorkDir{}, workspaceScope{}, "", err
	}

	pathValue, ok := readString(input, key)
	if !ok || pathValue == "" {
		return wd, scope, wd.Root(), nil
	}
	resolved, err := wd.ResolvePath(pathValue)
	if err != nil {
		return SessionWorkDir{}, workspaceScope{}, "", err
	}
	return wd, scope, resolved, nil
}

func (e *NativeToolExecutor) resolveExistingPath(wd SessionWorkDir, rawPath string) (string, error) {
	resolved, err := wd.ResolvePath(rawPath)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(resolved); err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", err
	}
	if !isWithinRoot(wd.Root(), realPath) {
		return "", ErrPathTraversal
	}
	return realPath, nil
}

func renderPath(root, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return filepath.ToSlash(target)
	}
	if rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
