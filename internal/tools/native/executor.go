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
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/repo"
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

type chatSessionReader interface {
	Create(ctx context.Context, session repo.ChatSession) (repo.ChatSession, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ChatSession, error)
	ListByOrg(ctx context.Context, organizationID uuid.UUID) ([]repo.ChatSession, error)
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

type flowTemplateReader interface {
	Create(ctx context.Context, template repo.FlowTemplate) (repo.FlowTemplate, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowTemplate, error)
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

type eventPublisher interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
}

type cliExecutor interface {
	Execute(ctx context.Context, input map[string]any) (map[string]any, error)
}

type commandContextFunc func(ctx context.Context, name string, args ...string) *exec.Cmd

type ExecutorOptions struct {
	Pool           *pgxpool.Pool
	DataDir        string
	WorkspaceRoot  string
	Memory         memoryQueryService
	MemoryRecorder memoryRecordService
	CLI            cliExecutor
	Events         eventPublisher
	Command        commandContextFunc
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
	chatSessions   chatSessionReader
	projects       projectReader
	tasks          taskReader
	inbox          inboxReader
	participants   chatParticipantReader
	messages       chatMessageReader
	agents         agentReader
	flowTemplates  flowTemplateReader
	flowNodes      flowNodeReader
	flowExecs      flowExecutionReader
	subtasks       subtaskReader
	schedules      scheduleReader
	mergeQueue     mergeQueueReader
	dependencies   dependencyRepository
	audit          *repo.AuditEventRepo
	memories       *repo.MemoryRepo

	mu         sync.Mutex
	workspaces map[string]SessionWorkDir
}

func NewExecutor(opts ExecutorOptions) *NativeToolExecutor {
	dataDir := strings.TrimSpace(opts.DataDir)
	if dataDir == "" {
		dataDir = "./data"
	}
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
		cli:            opts.CLI,
		events:         opts.Events,
		command:        command,
		workspaces:     make(map[string]SessionWorkDir),
	}

	if opts.Pool != nil {
		exec.chatSessions = repo.NewChatSessionRepo(opts.Pool)
		exec.projects = repo.NewProjectRepo(opts.Pool)
		exec.tasks = repo.NewProjectTaskRepo(opts.Pool)
		exec.inbox = repo.NewInboxItemRepo(opts.Pool)
		exec.participants = repo.NewChatParticipantRepo(opts.Pool)
		exec.messages = repo.NewChatMessageRepo(opts.Pool)
		exec.agents = repo.NewAgentRepo(opts.Pool)
		exec.flowTemplates = repo.NewFlowTemplateRepo(opts.Pool)
		exec.flowNodes = repo.NewFlowNodeRepo(opts.Pool)
		exec.flowExecs = repo.NewFlowNodeExecutionRepo(opts.Pool)
		exec.subtasks = repo.NewProjectSubtaskRepo(opts.Pool)
		exec.schedules = repo.NewTaskScheduleRepo(opts.Pool)
		exec.mergeQueue = repo.NewMergeQueueEntryRepo(opts.Pool)
		exec.dependencies = repo.NewProjectTaskDependencyRepo(opts.Pool)
		exec.audit = repo.NewAuditEventRepo(opts.Pool)
		exec.memories = repo.NewMemoryRepo(opts.Pool)
		if exec.events == nil {
			exec.events = eventbus.New(opts.Pool, nil, eventbus.Config{})
		}
	}

	return exec
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
	case "agent.create_temp":
		return e.handleAgentCreateTemp(ctx, input)
	case "agent.update":
		return e.handleAgentUpdate(ctx, input)
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
	case "flow.get_execution":
		return e.handleFlowGetExecution(ctx, input)
	case "schedule.list":
		return e.handleScheduleList(ctx, input)
	case "merge_queue.status":
		return e.handleMergeQueueStatus(ctx, input)
	case "tui.navigate":
		return e.handleTUINavigate(ctx, input)
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
	}
	if execCtx.SessionID == nil || *execCtx.SessionID == uuid.Nil || e.chatSessions == nil {
		return scope, nil
	}

	session, err := e.chatSessions.GetByID(ctx, *execCtx.SessionID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return scope, nil
		}
		return workspaceScope{}, err
	}
	if session.OrganizationID != scope.organizationID {
		return workspaceScope{}, fmt.Errorf("session organization mismatch")
	}

	scopeType := strings.TrimSpace(strings.ToLower(session.ScopeType))
	switch scopeType {
	case "project":
		scope.projectID = &session.ScopeID
	case "project_task":
		scope.taskID = &session.ScopeID
		if e.tasks != nil {
			taskRecord, taskErr := e.tasks.GetByID(ctx, session.ScopeID)
			if taskErr == nil {
				scope.projectID = &taskRecord.ProjectID
			}
		}
	}

	return scope, nil
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
		cacheKey = scope.organizationID.String()
		base := filepath.Join(e.dataDir, "workspaces", scope.organizationID.String())
		switch {
		case scope.projectID != nil && scope.taskID != nil:
			root = filepath.Join(base, scope.projectID.String(), scope.taskID.String())
			cacheKey += ":project:" + scope.projectID.String() + ":task:" + scope.taskID.String()
		case scope.projectID != nil:
			root = filepath.Join(base, scope.projectID.String(), "general")
			cacheKey += ":project:" + scope.projectID.String() + ":general"
		default:
			root = filepath.Join(base, "general")
			cacheKey += ":general"
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
