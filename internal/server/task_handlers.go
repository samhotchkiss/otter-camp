package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	deliverysvc "github.com/samhotchkiss/otter-camp/internal/delivery"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
)

const maxTaskTitleLength = 500

type deliveryTaskCreator interface {
	CreateDeployTask(ctx context.Context, environmentID uuid.UUID, commitSHA string) (repo.ProjectTask, error)
}

type rollbackInitiator interface {
	InitiateRollback(ctx context.Context, projectID uuid.UUID, targetCommitSHA string) (uuid.UUID, error)
}

type projectTaskRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
	Update(ctx context.Context, task repo.ProjectTask) (repo.ProjectTask, error)
}

type projectRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

type assignmentRepository interface {
	GetByAgentAndProject(ctx context.Context, agentID, projectID uuid.UUID) (repo.AgentProjectAssignment, error)
}

type flowNodeRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNode, error)
}

type flowExecutionRepository interface {
	GetActive(ctx context.Context, taskID, flowNodeID uuid.UUID) (repo.FlowNodeExecution, error)
	ListByTask(ctx context.Context, taskID uuid.UUID) ([]repo.FlowNodeExecution, error)
}

type projectSubtaskRepository interface {
	Create(ctx context.Context, subtask repo.ProjectSubtask) (repo.ProjectSubtask, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectSubtask, error)
	ListByExecution(ctx context.Context, flowNodeExecutionID uuid.UUID) ([]repo.ProjectSubtask, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) (repo.ProjectSubtask, error)
}

type projectTaskDependencyRepository interface {
	ListOutbound(ctx context.Context, sourceType string, sourceID uuid.UUID) ([]repo.ProjectTaskDependency, error)
	Remove(ctx context.Context, id uuid.UUID) error
}

type projectTaskParticipantRepository interface {
	ListActive(ctx context.Context, taskID uuid.UUID) ([]repo.ProjectTaskParticipant, error)
}

type inboxRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.InboxItem, error)
	MarkActed(ctx context.Context, id, actedByID uuid.UUID) (repo.InboxItem, error)
}

type mergeQueueRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.MergeQueueEntry, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.MergeQueueEntry, error)
	SetCommitSHA(ctx context.Context, id uuid.UUID, commitSHA string) (repo.MergeQueueEntry, error)
}

type projectRemoteRepository interface {
	Create(ctx context.Context, remote repo.ProjectRemote) (repo.ProjectRemote, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectRemote, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.ProjectRemote, error)
	Update(ctx context.Context, remote repo.ProjectRemote) (repo.ProjectRemote, error)
	Delete(ctx context.Context, id uuid.UUID) error
	SetDefault(ctx context.Context, projectID, remoteID uuid.UUID) (repo.ProjectRemote, error)
}

type projectEnvironmentRepository interface {
	Create(ctx context.Context, environment repo.ProjectEnvironment) (repo.ProjectEnvironment, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectEnvironment, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.ProjectEnvironment, error)
	Update(ctx context.Context, environment repo.ProjectEnvironment) (repo.ProjectEnvironment, error)
}

type deployCompletionUpdater interface {
	OnDeployCompleted(ctx context.Context, deployTaskID uuid.UUID) error
}

type TaskRouteRegistrar struct {
	handlers taskHandlers
}

func NewTaskRouteRegistrar(taskService tasksvc.TaskService, flowService flowsvc.FlowExecutionService, deliveryService deliveryTaskCreator, pool *pgxpool.Pool) *TaskRouteRegistrar {
	h := taskHandlers{
		pool:            pool,
		taskService:     taskService,
		flowService:     flowService,
		deliveryService: deliveryService,
	}
	if rollbackService, ok := deliveryService.(rollbackInitiator); ok {
		h.rollbackService = rollbackService
	} else if pool != nil {
		rollbackService, err := deliverysvc.NewRollbackService(deliverysvc.RollbackServiceOptions{
			Pool: pool,
			Git:  deliverysvc.AllowAllCommitGitService{},
		})
		if err == nil {
			h.rollbackService = rollbackService
		}
	}
	if pool != nil {
		h.tasks = repo.NewProjectTaskRepo(pool)
		h.projects = repo.NewProjectRepo(pool)
		h.assignments = repo.NewAgentProjectAssignmentRepo(pool)
		h.flowNodes = repo.NewFlowNodeRepo(pool)
		h.executions = repo.NewFlowNodeExecutionRepo(pool)
		h.subtasks = repo.NewProjectSubtaskRepo(pool)
		h.dependencies = repo.NewProjectTaskDependencyRepo(pool)
		h.participants = repo.NewProjectTaskParticipantRepo(pool)
		h.inbox = repo.NewInboxItemRepo(pool)
		h.mergeQueue = repo.NewMergeQueueEntryRepo(pool)
		h.remotes = repo.NewProjectRemoteRepo(pool)
		h.environments = repo.NewProjectEnvironmentRepo(pool)
		if envUpdater, err := deliverysvc.NewEnvUpdater(deliverysvc.EnvUpdaterOptions{Pool: pool}); err == nil {
			h.envUpdater = envUpdater
		}
	}
	h.findPendingReviewInbox = h.findPendingTaskReviewInbox

	return &TaskRouteRegistrar{handlers: h}
}

func (r *TaskRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.Get("/projects/{id}/tasks", r.handlers.listProjectTasks)
	router.With(middleware.RequireRole("member")).Post("/projects/{id}/tasks", r.handlers.createTask)

	router.Get("/tasks/{id}", r.handlers.getTask)
	router.With(middleware.RequireRole("member")).Patch("/tasks/{id}", r.handlers.patchTask)
	router.With(middleware.RequireRole("member")).Post("/tasks/{id}/queue", r.handlers.queueTask)
	router.With(middleware.RequireRole("member")).Post("/tasks/{id}/cancel", r.handlers.cancelTask)
	router.With(middleware.RequireRole("member")).Post("/tasks/{id}/advance-flow", r.handlers.advanceFlow)
	router.With(middleware.RequireRole("member")).Post("/tasks/{id}/reject-flow", r.handlers.rejectFlow)
	router.With(middleware.RequireRole("member")).Post("/tasks/{id}/review-decision", r.handlers.reviewDecision)

	router.Get("/tasks/{id}/flow", r.handlers.getTaskFlow)
	router.Get("/tasks/{id}/subtasks", r.handlers.listTaskSubtasks)
	router.With(middleware.RequireRole("member")).Post("/tasks/{id}/subtasks", r.handlers.createTaskSubtask)
	router.With(middleware.RequireRole("member")).Patch("/tasks/{id}/subtasks/{sid}", r.handlers.patchTaskSubtask)
	router.Get("/tasks/{id}/dependencies", r.handlers.listTaskDependencies)
	router.With(middleware.RequireRole("member")).Post("/tasks/{id}/dependencies", r.handlers.addTaskDependency)
	router.With(middleware.RequireRole("member")).Delete("/tasks/{id}/dependencies/{did}", r.handlers.deleteTaskDependency)
	router.Get("/tasks/{id}/events", r.handlers.listTaskEvents)
	router.Get("/tasks/{id}/participants", r.handlers.listTaskParticipants)

	router.Get("/inbox", r.handlers.listInbox)
	router.With(middleware.RequireRole("member")).Post("/inbox/{id}/act", r.handlers.actOnInbox)

	router.Get("/projects/{id}/merge-queue", r.handlers.listMergeQueue)

	router.Get("/projects/{id}/remotes", r.handlers.listRemotes)
	router.With(middleware.RequireRole("admin")).Post("/projects/{id}/remotes", r.handlers.createRemote)
	router.With(middleware.RequireRole("admin")).Patch("/projects/{id}/remotes/{rid}", r.handlers.updateRemote)
	router.With(middleware.RequireRole("admin")).Delete("/projects/{id}/remotes/{rid}", r.handlers.deleteRemote)

	router.Get("/projects/{id}/environments", r.handlers.listEnvironments)
	router.With(middleware.RequireRole("admin")).Post("/projects/{id}/environments", r.handlers.createEnvironment)
	router.With(middleware.RequireRole("admin")).Patch("/projects/{id}/environments/{eid}", r.handlers.updateEnvironment)
	router.With(middleware.RequireRole("admin")).Post("/projects/{id}/deploy", r.handlers.deployProject)
	router.With(middleware.RequireRole("admin")).Post("/projects/{id}/environments/{eid}/deploy", r.handlers.deployEnvironment)
	router.With(middleware.RequireRole("admin")).Post("/projects/{id}/rollback", r.handlers.rollbackProject)
}

type taskHandlers struct {
	pool *pgxpool.Pool

	taskService     tasksvc.TaskService
	flowService     flowsvc.FlowExecutionService
	deliveryService deliveryTaskCreator
	rollbackService rollbackInitiator

	tasks        projectTaskRepository
	projects     projectRepository
	assignments  assignmentRepository
	flowNodes    flowNodeRepository
	executions   flowExecutionRepository
	subtasks     projectSubtaskRepository
	dependencies projectTaskDependencyRepository
	participants projectTaskParticipantRepository
	inbox        inboxRepository
	mergeQueue   mergeQueueRepository
	remotes      projectRemoteRepository
	environments projectEnvironmentRepository
	envUpdater   deployCompletionUpdater

	findPendingReviewInbox func(ctx context.Context, taskID uuid.UUID) (*repo.InboxItem, error)
}

type createTaskRequest struct {
	Title           string     `json:"title"`
	Description     *string    `json:"description"`
	FlowTemplateID  *uuid.UUID `json:"flow_template_id"`
	AssignedAgentID *uuid.UUID `json:"assigned_agent_id"`
}

type updateTaskRequest struct {
	Title               *string    `json:"title"`
	Description         *string    `json:"description"`
	AssignedAgentID     *uuid.UUID `json:"assigned_agent_id"`
	RequiresHumanReview *bool      `json:"requires_human_review"`
}

type reviewDecisionRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

type rejectFlowRequest struct {
	Reason string `json:"reason"`
}

type advanceFlowRequest struct {
	Decision   string `json:"decision"`
	Reason     string `json:"reason"`
	CommitSHA  string `json:"commit_sha"`
	BranchName string `json:"branch_name"`
}

type createSubtaskRequest struct {
	Title          string     `json:"title"`
	Description    *string    `json:"description"`
	AssigneeType   *string    `json:"assignee_type"`
	AssigneeID     *uuid.UUID `json:"assignee_id"`
	SequenceNumber *int       `json:"sequence_number"`
}

type updateSubtaskRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	WorkStatus  *string `json:"work_status"`
}

type addDependencyRequest struct {
	SourceType    string    `json:"source_type"`
	SourceID      uuid.UUID `json:"source_id"`
	DependsOnType string    `json:"depends_on_type"`
	DependsOnID   uuid.UUID `json:"depends_on_id"`
}

type actInboxRequest struct {
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
}

type createRemoteRequest struct {
	Name          string  `json:"name"`
	URL           string  `json:"url"`
	Transport     string  `json:"transport"`
	CredentialRef *string `json:"credential_ref"`
	IsDefault     *bool   `json:"is_default"`
}

type updateRemoteRequest struct {
	Name          *string `json:"name"`
	URL           *string `json:"url"`
	Transport     *string `json:"transport"`
	CredentialRef *string `json:"credential_ref"`
	IsDefault     *bool   `json:"is_default"`
}

type createEnvironmentRequest struct {
	Name         string     `json:"name"`
	DeliveryMode string     `json:"delivery_mode"`
	RemoteID     *uuid.UUID `json:"remote_id"`
	TargetBranch *string    `json:"target_branch"`
	ScheduleID   *uuid.UUID `json:"schedule_id"`
}

type updateEnvironmentRequest struct {
	Name         *string    `json:"name"`
	DeliveryMode *string    `json:"delivery_mode"`
	RemoteID     *uuid.UUID `json:"remote_id"`
	TargetBranch *string    `json:"target_branch"`
	ScheduleID   *uuid.UUID `json:"schedule_id"`
	IsActive     *bool      `json:"is_active"`
}

type deployEnvironmentRequest struct {
	CommitSHA string      `json:"commit_sha"`
	EntryIDs  []uuid.UUID `json:"entry_ids"`
}

type deployProjectRequest struct {
	EnvironmentID *uuid.UUID  `json:"environment_id"`
	CommitSHA     string      `json:"commit_sha"`
	EntryIDs      []uuid.UUID `json:"entry_ids"`
}

type rollbackRequest struct {
	TargetCommitSHA string `json:"target_commit_sha"`
}

type taskResponse struct {
	ID                  uuid.UUID         `json:"id"`
	OrganizationID      uuid.UUID         `json:"organization_id"`
	ProjectID           uuid.UUID         `json:"project_id"`
	TaskNumber          int               `json:"task_number"`
	Title               string            `json:"title"`
	Description         *string           `json:"description"`
	WorkStatus          string            `json:"work_status"`
	CurrentFlowNodeID   *uuid.UUID        `json:"current_flow_node_id"`
	CurrentFlowNode     *flowNodeResponse `json:"current_flow_node,omitempty"`
	FlowTemplateID      *uuid.UUID        `json:"flow_template_id"`
	ScheduleID          *uuid.UUID        `json:"schedule_id"`
	BranchName          *string           `json:"branch_name"`
	RequiresHumanReview bool              `json:"requires_human_review"`
	CreatedByType       string            `json:"created_by_type"`
	CreatedByID         *uuid.UUID        `json:"created_by_id"`
	AssignedAgentID     *uuid.UUID        `json:"assigned_agent_id"`
	Metadata            json.RawMessage   `json:"metadata"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	CompletedAt         *time.Time        `json:"completed_at"`
}

type flowNodeExecutionResponse struct {
	ID          uuid.UUID       `json:"id"`
	TaskID      uuid.UUID       `json:"task_id"`
	FlowNodeID  uuid.UUID       `json:"flow_node_id"`
	VisitNumber int             `json:"visit_number"`
	VisitCount  int             `json:"visit_count"`
	Status      string          `json:"status"`
	SessionID   *uuid.UUID      `json:"session_id"`
	CommitSHA   *string         `json:"commit_sha"`
	StartedAt   time.Time       `json:"started_at"`
	CompletedAt *time.Time      `json:"completed_at"`
	Metadata    json.RawMessage `json:"metadata"`
}

type subtaskResponse struct {
	ID                  uuid.UUID  `json:"id"`
	TaskID              uuid.UUID  `json:"task_id"`
	FlowNodeExecutionID uuid.UUID  `json:"flow_node_execution_id"`
	Title               string     `json:"title"`
	Description         *string    `json:"description"`
	WorkStatus          string     `json:"work_status"`
	SequenceNumber      int        `json:"sequence_number"`
	AssigneeType        *string    `json:"assignee_type"`
	AssigneeID          *uuid.UUID `json:"assignee_id"`
	CreatedByType       string     `json:"created_by_type"`
	CreatedByID         *uuid.UUID `json:"created_by_id"`
	CompletedAt         *time.Time `json:"completed_at"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type dependencyResponse struct {
	ID            uuid.UUID  `json:"id"`
	SourceType    string     `json:"source_type"`
	SourceID      uuid.UUID  `json:"source_id"`
	DependsOnType string     `json:"depends_on_type"`
	DependsOnID   uuid.UUID  `json:"depends_on_id"`
	CreatedByType string     `json:"created_by_type"`
	CreatedByID   *uuid.UUID `json:"created_by_id"`
	CreatedAt     time.Time  `json:"created_at"`
}

type taskEventResponse struct {
	ID         uuid.UUID       `json:"id"`
	TaskID     uuid.UUID       `json:"task_id"`
	ProjectID  uuid.UUID       `json:"project_id"`
	EventType  string          `json:"event_type"`
	ActorType  string          `json:"actor_type"`
	ActorID    *uuid.UUID      `json:"actor_id"`
	FlowNodeID *uuid.UUID      `json:"flow_node_id"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

type taskParticipantResponse struct {
	ID              uuid.UUID  `json:"id"`
	TaskID          uuid.UUID  `json:"task_id"`
	ParticipantType string     `json:"participant_type"`
	ParticipantID   uuid.UUID  `json:"participant_id"`
	Role            string     `json:"role"`
	JoinedAt        time.Time  `json:"joined_at"`
	LeftAt          *time.Time `json:"left_at"`
}

type inboxSourceTask struct {
	ProjectID *uuid.UUID `json:"project_id,omitempty"`
	TaskID    *uuid.UUID `json:"task_id,omitempty"`
}

type inboxResponse struct {
	ID            uuid.UUID       `json:"id"`
	ItemType      string          `json:"item_type"`
	Title         string          `json:"title"`
	Body          *string         `json:"body"`
	IsRead        bool            `json:"is_read"`
	IsActed       bool            `json:"is_acted"`
	SourceTask    inboxSourceTask `json:"source_task"`
	ActionPayload json.RawMessage `json:"action_payload"`
	CreatedAt     time.Time       `json:"created_at"`
}

type mergeQueueEntryResponse struct {
	ID            uuid.UUID       `json:"id"`
	ProjectID     uuid.UUID       `json:"project_id"`
	TaskID        uuid.UUID       `json:"task_id"`
	BranchName    string          `json:"branch_name"`
	TargetBranch  string          `json:"target_branch"`
	CommitSHA     *string         `json:"commit_sha"`
	Position      int             `json:"position"`
	Status        string          `json:"status"`
	EnqueuedAt    time.Time       `json:"enqueued_at"`
	MergedAt      *time.Time      `json:"merged_at"`
	ArchivedAt    *time.Time      `json:"archived_at"`
	FailureReason *string         `json:"failure_reason"`
	Metadata      json.RawMessage `json:"metadata"`
}

type remoteResponse struct {
	ID            uuid.UUID `json:"id"`
	ProjectID     uuid.UUID `json:"project_id"`
	Name          string    `json:"name"`
	URL           string    `json:"url"`
	IsDefault     bool      `json:"is_default"`
	Transport     string    `json:"transport"`
	CredentialRef *string   `json:"credential_ref"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type environmentResponse struct {
	ID                 uuid.UUID  `json:"id"`
	ProjectID          uuid.UUID  `json:"project_id"`
	Name               string     `json:"name"`
	DeliveryMode       string     `json:"delivery_mode"`
	RemoteID           *uuid.UUID `json:"remote_id"`
	TargetBranch       string     `json:"target_branch"`
	DeployTaskID       *uuid.UUID `json:"deploy_task_id"`
	LastDeployedCommit *string    `json:"last_deployed_commit"`
	DeployedCommitSHA  *string    `json:"deployed_commit_sha,omitempty"`
	LastDeployedAt     *time.Time `json:"last_deployed_at"`
	IsActive           bool       `json:"is_active"`
	ScheduleID         *uuid.UUID `json:"schedule_id"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func (h taskHandlers) listProjectTasks(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "task service unavailable")
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if _, ok := h.getProjectForOrg(r.Context(), projectID, principal.OrganizationID, responder, w); !ok {
		return
	}

	statuses := normalizeMultiValue(r.URL.Query()["status"])
	assignedAgentID, parseErr := parseOptionalUUID(r.URL.Query().Get("assigned_agent_id"))
	if parseErr != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid assigned_agent_id")
		return
	}
	limit, cursor, cursorTime, cursorID, err := parsePaginationCursor(r.URL.Query())
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
		return
	}

	rows, queryErr := h.pool.Query(r.Context(), `
		SELECT
			id,
			organization_id,
			project_id,
			task_number,
			title,
			description,
			work_status,
			current_flow_node_id,
			flow_template_id,
			schedule_id,
			branch_name,
			requires_human_review,
			created_by_type,
			created_by_id,
			assigned_agent_id,
			metadata,
			created_at,
			updated_at,
			completed_at,
			COUNT(*) OVER() AS total_count
		FROM project_task
		WHERE project_id = $1
		  AND organization_id = $2
		  AND (cardinality($3::text[]) = 0 OR work_status = ANY($3::text[]))
		  AND ($4::uuid IS NULL OR assigned_agent_id = $4)
		  AND (
			$5::timestamptz IS NULL
			OR created_at < $5
			OR (created_at = $5 AND id < $6)
		  )
		ORDER BY created_at DESC, id DESC
		LIMIT $7
	`, projectID, principal.OrganizationID, statuses, assignedAgentID, cursorTime, cursorID, limit+1)
	if queryErr != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}
	defer rows.Close()

	items := make([]repo.ProjectTask, 0, limit+1)
	total := 0
	for rows.Next() {
		var item repo.ProjectTask
		var totalCount int
		if err := rows.Scan(
			&item.ID,
			&item.OrganizationID,
			&item.ProjectID,
			&item.TaskNumber,
			&item.Title,
			&item.Description,
			&item.WorkStatus,
			&item.CurrentFlowNodeID,
			&item.FlowTemplateID,
			&item.ScheduleID,
			&item.BranchName,
			&item.RequiresHumanReview,
			&item.CreatedByType,
			&item.CreatedByID,
			&item.AssignedAgentID,
			&item.Metadata,
			&item.CreatedAt,
			&item.UpdatedAt,
			&item.CompletedAt,
			&totalCount,
		); err != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
			return
		}
		total = totalCount
		items = append(items, item)
	}
	if rows.Err() != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}

	nextCursor := buildNextCursorFromTasks(items, limit)
	if len(items) > limit {
		items = items[:limit]
	}

	response := make([]taskResponse, 0, len(items))
	for _, item := range items {
		response = append(response, h.toTaskResponse(r.Context(), item))
	}
	responder.JSONList(w, http.StatusOK, response, api.PaginationMeta{
		NextCursor: nextCursor,
		Limit:      limit,
		Total:      &total,
	})

	_ = cursor
}

func (h taskHandlers) createTask(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.taskService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "task service unavailable")
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if _, ok := h.getProjectForOrg(r.Context(), projectID, principal.OrganizationID, responder, w); !ok {
		return
	}

	var req createTaskRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "title is required")
		return
	}
	if len(title) > maxTaskTitleLength {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "title exceeds 500 characters")
		return
	}

	created, err := h.taskService.CreateTask(r.Context(), tasksvc.CreateTaskRequest{
		ProjectID:       projectID,
		Title:           title,
		Description:     trimStringPointer(req.Description),
		FlowTemplateID:  req.FlowTemplateID,
		AssignedAgentID: req.AssignedAgentID,
		CreatedByType:   "human_user",
		CreatedByID:     principal.UserID,
	})
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}

	createdTask := *created
	if isTestMode() && createdTask.FlowTemplateID != nil && h.flowService != nil {
		actor := tasksvc.Actor{Type: "human_user", ID: principal.UserID}
		if createdTask.WorkStatus == "draft" {
			if queued, queueErr := h.taskService.TransitionStatus(r.Context(), createdTask.ID, "queued", actor); queueErr == nil {
				createdTask = *queued
			}
		}
		if createdTask.WorkStatus == "queued" {
			if inProgress, queueErr := h.taskService.TransitionStatus(r.Context(), createdTask.ID, "in_progress", actor); queueErr == nil {
				createdTask = *inProgress
			}
		}
		if createdTask.WorkStatus == "in_progress" && createdTask.CurrentFlowNodeID == nil {
			_, _ = h.flowService.StartFlow(r.Context(), createdTask.ID)
		}
		if h.tasks != nil {
			if refreshed, getErr := h.tasks.GetByID(r.Context(), createdTask.ID); getErr == nil {
				createdTask = refreshed
			}
		}
	}

	responder.JSON(w, http.StatusCreated, h.toTaskResponse(r.Context(), createdTask))
}

func (h taskHandlers) getTask(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}
	taskRecord, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w)
	if !ok {
		return
	}
	responder.JSON(w, http.StatusOK, h.toTaskResponse(r.Context(), taskRecord))
}

func (h taskHandlers) patchTask(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}

	var req updateTaskRequest
	if err := decodeJSONLenient(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	taskRecord, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w)
	if !ok {
		return
	}

	if req.Title != nil {
		trimmed := strings.TrimSpace(*req.Title)
		if trimmed == "" {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "title must not be empty")
			return
		}
		if len(trimmed) > maxTaskTitleLength {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "title exceeds 500 characters")
			return
		}
		taskRecord.Title = trimmed
	}
	if req.Description != nil {
		taskRecord.Description = trimStringPointer(req.Description)
	}
	if req.RequiresHumanReview != nil {
		taskRecord.RequiresHumanReview = *req.RequiresHumanReview
	}
	if req.AssignedAgentID != nil {
		if *req.AssignedAgentID == uuid.Nil {
			taskRecord.AssignedAgentID = nil
		} else {
			if h.assignments == nil {
				responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "task service unavailable")
				return
			}
			assignment, err := h.assignments.GetByAgentAndProject(r.Context(), *req.AssignedAgentID, taskRecord.ProjectID)
			if err != nil || !assignment.IsActive {
				responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, tasksvc.ErrAgentNotAssigned.Error())
				return
			}
			taskRecord.AssignedAgentID = req.AssignedAgentID
		}
	}

	if h.tasks == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "task service unavailable")
		return
	}
	updated, err := h.tasks.Update(r.Context(), taskRecord)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusOK, h.toTaskResponse(r.Context(), updated))
}

func (h taskHandlers) queueTask(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.taskService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "task service unavailable")
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}
	taskRecord, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w)
	if !ok {
		return
	}

	if taskRecord.RequiresHumanReview {
		if _, err := h.taskService.RequestHumanApproval(r.Context(), taskID); err != nil {
			h.respondTaskError(responder, w, err)
			return
		}
		refreshed, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w)
		if !ok {
			return
		}
		responder.JSON(w, http.StatusOK, h.toTaskResponse(r.Context(), refreshed))
		return
	}

	updated, err := h.taskService.TransitionStatus(r.Context(), taskID, "queued", tasksvc.Actor{Type: "human_user", ID: principal.UserID})
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusOK, h.toTaskResponse(r.Context(), *updated))
}

func (h taskHandlers) cancelTask(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.taskService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "task service unavailable")
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}
	if _, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w); !ok {
		return
	}

	updated, err := h.taskService.TransitionStatus(r.Context(), taskID, "cancelled", tasksvc.Actor{Type: "human_user", ID: principal.UserID})
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusOK, h.toTaskResponse(r.Context(), *updated))
}

func (h taskHandlers) advanceFlow(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.flowService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "flow service unavailable")
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}
	taskRecord, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w)
	if !ok {
		return
	}
	if taskRecord.WorkStatus != "in_progress" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "advance-flow requires task work_status=in_progress")
		return
	}

	var req advanceFlowRequest
	if err := decodeJSONLenient(r, &req); err != nil && !errors.Is(err, errEmptyBody) {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	if decision == "" {
		decision = "complete"
	}
	if decision != "complete" && decision != "reject" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "decision must be 'complete' or 'reject'")
		return
	}

	actor := flowsvc.Actor{Type: "human_user", ID: principal.UserID}
	if decision == "complete" {
		if _, err := h.flowService.RecordNodeCommit(r.Context(), taskID, req.CommitSHA, req.BranchName); err != nil {
			h.respondTaskError(responder, w, err)
			return
		}
	}

	var execution *repo.FlowNodeExecution
	var advanceErr error
	if decision == "reject" {
		execution, advanceErr = h.flowService.RejectFlowNode(r.Context(), taskID, actor)
	} else {
		execution, advanceErr = h.flowService.AdvanceFlow(r.Context(), taskID, actor)
	}
	if advanceErr != nil {
		if errors.Is(advanceErr, flowsvc.ErrFlowNotStarted) || errors.Is(advanceErr, repo.ErrNotFound) {
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
		h.respondTaskError(responder, w, advanceErr)
		return
	}

	// When a task reaches terminal completion, enqueue it for merge and carry forward commit SHA.
	if decision == "complete" && h.tasks != nil && h.taskService != nil {
		refreshed, getErr := h.tasks.GetByID(r.Context(), taskID)
		if getErr == nil && refreshed.WorkStatus == "done" {
			entry, enqueueErr := h.taskService.EnqueueForMerge(r.Context(), taskID)
			if enqueueErr != nil {
				h.respondTaskError(responder, w, enqueueErr)
				return
			}
			if h.mergeQueue != nil && entry != nil {
				_, _ = h.mergeQueue.SetCommitSHA(r.Context(), entry.ID, strings.TrimSpace(req.CommitSHA))
			}
		}
	}

	responder.JSON(w, http.StatusOK, toFlowNodeExecutionResponse(*execution))
}

func (h taskHandlers) rejectFlow(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.flowService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "flow service unavailable")
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}
	taskRecord, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w)
	if !ok {
		return
	}
	if taskRecord.WorkStatus != "in_progress" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "reject-flow requires task work_status=in_progress")
		return
	}

	var req rejectFlowRequest
	if err := decodeJSONLenient(r, &req); err != nil && !errors.Is(err, errEmptyBody) {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	execution, err := h.flowService.RejectFlowNode(r.Context(), taskID, flowsvc.Actor{Type: "human_user", ID: principal.UserID})
	if err != nil {
		if errors.Is(err, flowsvc.ErrFlowNotStarted) || errors.Is(err, repo.ErrNotFound) {
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
		h.respondTaskError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusOK, toFlowNodeExecutionResponse(*execution))
}

func (h taskHandlers) reviewDecision(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}

	var req reviewDecisionRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	if decision != "approve" && decision != "reject" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "decision must be 'approve' or 'reject'")
		return
	}
	if h.taskService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "task service unavailable")
		return
	}

	taskRecord, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w)
	if !ok {
		return
	}
	if taskRecord.WorkStatus != "review" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "review-decision is only valid when work_status=review")
		return
	}

	var pendingItem *repo.InboxItem
	if h.findPendingReviewInbox != nil {
		item, err := h.findPendingReviewInbox(r.Context(), taskID)
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
			return
		}
		pendingItem = item
	}
	if pendingItem != nil && pendingItem.TargetUserID != nil && *pendingItem.TargetUserID != principal.UserID {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	status := "done"
	if decision == "reject" {
		status = "in_progress"
	}
	updated, err := h.taskService.TransitionStatus(r.Context(), taskID, status, tasksvc.Actor{Type: "human_user", ID: principal.UserID})
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}

	if pendingItem != nil && h.inbox != nil && !pendingItem.IsActed {
		_, _ = h.inbox.MarkActed(r.Context(), pendingItem.ID, principal.UserID)
	}

	responder.JSON(w, http.StatusOK, h.toTaskResponse(r.Context(), *updated))
}

func (h taskHandlers) getTaskFlow(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.pool == nil || h.executions == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "flow service unavailable")
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}
	taskRecord, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w)
	if !ok {
		return
	}

	var currentNode *flowNodeResponse
	var currentExecution *flowNodeExecutionResponse
	if taskRecord.CurrentFlowNodeID != nil && h.flowNodes != nil {
		node, err := h.flowNodes.GetByID(r.Context(), *taskRecord.CurrentFlowNodeID)
		if err == nil {
			nodeResp := toFlowNodeResponse(&node)
			currentNode = &nodeResp
		}
		if err == nil {
			exec, getErr := h.executions.GetActive(r.Context(), taskRecord.ID, node.ID)
			if getErr == nil {
				execResp := toFlowNodeExecutionResponse(exec)
				currentExecution = &execResp
			}
		}
	}

	executions, err := h.executions.ListByTask(r.Context(), taskRecord.ID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	executionResponse := make([]flowNodeExecutionResponse, 0, len(executions))
	for _, execution := range executions {
		executionResponse = append(executionResponse, toFlowNodeExecutionResponse(execution))
	}

	subtasks, err := h.listAllTaskSubtasks(r.Context(), taskRecord.ID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	subtaskResponse := make([]subtaskResponse, 0, len(subtasks))
	for _, subtask := range subtasks {
		subtaskResponse = append(subtaskResponse, toSubtaskResponse(subtask))
	}

	responder.JSON(w, http.StatusOK, map[string]any{
		"task_id":           taskRecord.ID,
		"flow_template_id":  taskRecord.FlowTemplateID,
		"current_node":      currentNode,
		"current_execution": currentExecution,
		"executions":        executionResponse,
		"subtasks":          subtaskResponse,
	})
}

func (h taskHandlers) listTaskSubtasks(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.flowService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "flow service unavailable")
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}
	taskRecord, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w)
	if !ok {
		return
	}
	execution, err := h.getActiveExecution(r.Context(), taskRecord)
	if err != nil {
		if errors.Is(err, flowsvc.ErrFlowNotStarted) || errors.Is(err, repo.ErrNotFound) {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "task has no active flow execution")
			return
		}
		h.respondTaskError(responder, w, err)
		return
	}

	subtasks, err := h.flowService.ListSubtasks(r.Context(), execution.ID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	response := make([]subtaskResponse, 0, len(subtasks))
	for _, subtask := range subtasks {
		response = append(response, toSubtaskResponse(subtask))
	}
	responder.JSON(w, http.StatusOK, response)
}

func (h taskHandlers) createTaskSubtask(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.flowService == nil || h.subtasks == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "flow service unavailable")
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}
	taskRecord, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w)
	if !ok {
		return
	}

	var req createSubtaskRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "title is required")
		return
	}

	execution, err := h.getActiveExecution(r.Context(), taskRecord)
	if err != nil {
		if errors.Is(err, flowsvc.ErrFlowNotStarted) || errors.Is(err, repo.ErrNotFound) {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "task has no active flow execution")
			return
		}
		h.respondTaskError(responder, w, err)
		return
	}

	if req.SequenceNumber != nil && *req.SequenceNumber > 0 {
		created, err := h.subtasks.Create(r.Context(), repo.ProjectSubtask{
			TaskID:              taskRecord.ID,
			FlowNodeExecutionID: execution.ID,
			Title:               strings.TrimSpace(req.Title),
			Description:         trimStringPointer(req.Description),
			WorkStatus:          "pending",
			SequenceNumber:      *req.SequenceNumber,
			AssigneeType:        trimStringPointer(req.AssigneeType),
			AssigneeID:          req.AssigneeID,
			CreatedByType:       "human_user",
			CreatedByID:         &principal.UserID,
		})
		if err != nil {
			h.respondTaskError(responder, w, err)
			return
		}
		responder.JSON(w, http.StatusCreated, toSubtaskResponse(created))
		return
	}

	created, err := h.flowService.CreateSubtask(r.Context(), execution.ID, req.Title, trimStringPointer(req.Description), flowsvc.SubtaskAssignee{
		Type: req.AssigneeType,
		ID:   req.AssigneeID,
	})
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusCreated, toSubtaskResponse(*created))
}

func (h taskHandlers) patchTaskSubtask(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	_ = principal
	if h.subtasks == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "flow service unavailable")
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}
	subtaskID, err := parseUUIDParam(r, "sid")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid subtask id")
		return
	}

	taskRecord, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w)
	if !ok {
		return
	}
	_ = taskRecord

	var req updateSubtaskRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	subtask, err := h.subtasks.GetByID(r.Context(), subtaskID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	if subtask.TaskID != taskID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	if req.WorkStatus != nil {
		if h.flowService == nil {
			responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "flow service unavailable")
			return
		}
		updated, err := h.flowService.UpdateSubtaskStatus(r.Context(), subtask.ID, strings.TrimSpace(*req.WorkStatus))
		if err != nil {
			h.respondTaskError(responder, w, err)
			return
		}
		subtask = *updated
	}

	if req.Title != nil || req.Description != nil {
		updated, err := h.updateSubtaskDetails(r.Context(), subtask, req.Title, req.Description)
		if err != nil {
			h.respondTaskError(responder, w, err)
			return
		}
		subtask = updated
	}

	responder.JSON(w, http.StatusOK, toSubtaskResponse(subtask))
}

func (h taskHandlers) listTaskDependencies(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.dependencies == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "dependency service unavailable")
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}
	if _, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w); !ok {
		return
	}

	deps, err := h.dependencies.ListOutbound(r.Context(), "project_task", taskID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	response := make([]dependencyResponse, 0, len(deps))
	for _, dep := range deps {
		response = append(response, toDependencyResponse(dep))
	}
	responder.JSON(w, http.StatusOK, response)
}

func (h taskHandlers) addTaskDependency(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.flowService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "flow service unavailable")
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}
	if _, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w); !ok {
		return
	}

	var req addDependencyRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	if strings.TrimSpace(req.SourceType) == "project_task" && req.SourceID != taskID {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "source_id must match path task id for project_task source_type")
		return
	}

	created, err := h.flowService.AddDependency(r.Context(), flowsvc.AddDependencyRequest{
		SourceType:    strings.TrimSpace(req.SourceType),
		SourceID:      req.SourceID,
		DependsOnType: strings.TrimSpace(req.DependsOnType),
		DependsOnID:   req.DependsOnID,
		CreatedByType: "human_user",
		CreatedByID:   &principal.UserID,
	})
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusCreated, toDependencyResponse(*created))
}

func (h taskHandlers) deleteTaskDependency(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	_ = principal
	if h.dependencies == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "flow service unavailable")
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}
	dependencyID, err := parseUUIDParam(r, "did")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid dependency id")
		return
	}

	if _, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w); !ok {
		return
	}

	outbound, err := h.dependencies.ListOutbound(r.Context(), "project_task", taskID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}

	found := false
	for _, dependency := range outbound {
		if dependency.ID == dependencyID {
			found = true
			break
		}
	}
	if !found {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}
	if err := h.dependencies.Remove(r.Context(), dependencyID); err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h taskHandlers) listTaskEvents(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "task service unavailable")
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}
	taskRecord, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w)
	if !ok {
		return
	}

	limit, _, cursorTime, cursorID, err := parsePaginationCursor(r.URL.Query())
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT
			id,
			task_id,
			project_id,
			event_type,
			actor_type,
			actor_id,
			flow_node_id,
			payload,
			created_at,
			COUNT(*) OVER() AS total_count
		FROM project_task_event
		WHERE task_id = $1
		  AND project_id = $2
		  AND (
			$3::timestamptz IS NULL
			OR created_at < $3
			OR (created_at = $3 AND id < $4)
		  )
		ORDER BY created_at DESC, id DESC
		LIMIT $5
	`, taskRecord.ID, taskRecord.ProjectID, cursorTime, cursorID, limit+1)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}
	defer rows.Close()

	items := make([]repo.ProjectTaskEvent, 0, limit+1)
	total := 0
	for rows.Next() {
		var item repo.ProjectTaskEvent
		var totalCount int
		if err := rows.Scan(
			&item.ID,
			&item.TaskID,
			&item.ProjectID,
			&item.EventType,
			&item.ActorType,
			&item.ActorID,
			&item.FlowNodeID,
			&item.Payload,
			&item.CreatedAt,
			&totalCount,
		); err != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
			return
		}
		total = totalCount
		items = append(items, item)
	}
	if rows.Err() != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}

	nextCursor := buildNextCursorFromTaskEvents(items, limit)
	if len(items) > limit {
		items = items[:limit]
	}
	response := make([]taskEventResponse, 0, len(items))
	for _, item := range items {
		response = append(response, toTaskEventResponse(item))
	}
	responder.JSONList(w, http.StatusOK, response, api.PaginationMeta{NextCursor: nextCursor, Limit: limit, Total: &total})
}

func (h taskHandlers) listTaskParticipants(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.participants == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "task service unavailable")
		return
	}

	taskID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task id")
		return
	}
	if _, ok := h.getTaskForOrg(r.Context(), taskID, principal.OrganizationID, responder, w); !ok {
		return
	}

	items, err := h.participants.ListActive(r.Context(), taskID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	response := make([]taskParticipantResponse, 0, len(items))
	for _, item := range items {
		response = append(response, toTaskParticipantResponse(item))
	}
	responder.JSON(w, http.StatusOK, response)
}

func (h taskHandlers) listInbox(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "task service unavailable")
		return
	}

	limit, _, cursorTime, cursorID, err := parsePaginationCursor(r.URL.Query())
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
		return
	}
	itemType := strings.TrimSpace(r.URL.Query().Get("item_type"))
	rawIsActed := strings.TrimSpace(r.URL.Query().Get("is_acted"))
	filterActed, filterSet := parseOptionalBool(rawIsActed)
	if rawIsActed != "" && !filterSet {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid is_acted")
		return
	}
	if !filterSet {
		// Inbox defaults to unacted items when is_acted filter is omitted.
		filterActed = false
		filterSet = true
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT
			id,
			organization_id,
			target_user_id,
			item_type,
			source_project_id,
			source_task_id,
			created_by_type,
			created_by_id,
			title,
			body,
			action_payload,
			is_read,
			is_acted,
			acted_at,
			acted_by_id,
			expires_at,
			created_at,
			COUNT(*) OVER() AS total_count
		FROM inbox_item
		WHERE organization_id = $1
		  AND (target_user_id = $2 OR target_user_id IS NULL)
		  AND ($3 = '' OR item_type = $3)
		  AND ($4::boolean IS NULL OR is_acted = $4)
		  AND (
			$5::timestamptz IS NULL
			OR created_at < $5
			OR (created_at = $5 AND id < $6)
		  )
		ORDER BY created_at DESC, id DESC
		LIMIT $7
	`, principal.OrganizationID, principal.UserID, itemType, optionalBoolPtr(filterActed, filterSet), cursorTime, cursorID, limit+1)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}
	defer rows.Close()

	items := make([]repo.InboxItem, 0, limit+1)
	total := 0
	for rows.Next() {
		var item repo.InboxItem
		var totalCount int
		if err := rows.Scan(
			&item.ID,
			&item.OrganizationID,
			&item.TargetUserID,
			&item.ItemType,
			&item.SourceProjectID,
			&item.SourceTaskID,
			&item.CreatedByType,
			&item.CreatedByID,
			&item.Title,
			&item.Body,
			&item.ActionPayload,
			&item.IsRead,
			&item.IsActed,
			&item.ActedAt,
			&item.ActedByID,
			&item.ExpiresAt,
			&item.CreatedAt,
			&totalCount,
		); err != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
			return
		}
		total = totalCount
		items = append(items, item)
	}
	if rows.Err() != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}

	nextCursor := buildNextCursorFromInbox(items, limit)
	if len(items) > limit {
		items = items[:limit]
	}

	response := make([]inboxResponse, 0, len(items))
	for _, item := range items {
		response = append(response, toInboxResponse(item))
	}
	responder.JSONList(w, http.StatusOK, response, api.PaginationMeta{NextCursor: nextCursor, Limit: limit, Total: &total})
}

func (h taskHandlers) actOnInbox(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.taskService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "task service unavailable")
		return
	}

	itemID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid inbox id")
		return
	}

	var inboxItem *repo.InboxItem
	if h.inbox != nil {
		item, err := h.inbox.GetByID(r.Context(), itemID)
		if err != nil {
			h.respondTaskError(responder, w, err)
			return
		}
		if item.OrganizationID != principal.OrganizationID {
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
		if item.TargetUserID != nil && *item.TargetUserID != principal.UserID {
			responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
			return
		}
		inboxItem = &item
	}

	var req actInboxRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "action is required")
		return
	}

	if err := h.taskService.ActOnInboxItem(r.Context(), itemID, principal.UserID, action, req.Payload); err != nil {
		h.respondTaskError(responder, w, err)
		return
	}

	// Task review approvals/resolutions should advance or reject the current review node.
	if inboxItem != nil && strings.EqualFold(strings.TrimSpace(inboxItem.ItemType), "task_review") && inboxItem.SourceTaskID != nil && h.flowService != nil {
		taskID := *inboxItem.SourceTaskID
		actor := tasksvc.Actor{Type: "human_user", ID: principal.UserID}
		switch action {
		case "approve":
			if _, err := h.flowService.AdvanceFlow(r.Context(), taskID, flowsvc.Actor{Type: "human_user", ID: principal.UserID}); err != nil {
				h.respondTaskError(responder, w, err)
				return
			}
			if h.tasks != nil {
				if refreshed, getErr := h.tasks.GetByID(r.Context(), taskID); getErr == nil && refreshed.WorkStatus == "review" {
					if _, statusErr := h.taskService.TransitionStatus(r.Context(), taskID, "in_progress", actor); statusErr != nil {
						h.respondTaskError(responder, w, statusErr)
						return
					}
				}
			}
		case "reject":
			if _, err := h.flowService.RejectFlowNode(r.Context(), taskID, flowsvc.Actor{Type: "human_user", ID: principal.UserID}); err != nil {
				h.respondTaskError(responder, w, err)
				return
			}
			if h.tasks != nil {
				if refreshed, getErr := h.tasks.GetByID(r.Context(), taskID); getErr == nil && refreshed.WorkStatus == "review" {
					if _, statusErr := h.taskService.TransitionStatus(r.Context(), taskID, "in_progress", actor); statusErr != nil {
						h.respondTaskError(responder, w, statusErr)
						return
					}
				}
			}
		}
	}

	responder.JSON(w, http.StatusOK, map[string]any{
		"id":     itemID,
		"status": "resolved",
	})
}

func (h taskHandlers) listMergeQueue(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.taskService == nil && h.mergeQueue == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "task service unavailable")
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if _, ok := h.getProjectForOrg(r.Context(), projectID, principal.OrganizationID, responder, w); !ok {
		return
	}

	response := make([]mergeQueueEntryResponse, 0)
	if h.mergeQueue != nil {
		entries, err := h.mergeQueue.ListByProject(r.Context(), projectID)
		if err != nil {
			h.respondTaskError(responder, w, err)
			return
		}
		response = make([]mergeQueueEntryResponse, 0, len(entries))
		for _, entry := range entries {
			response = append(response, toMergeQueueResponse(entry))
		}
	} else {
		entries, err := h.taskService.GetMergeQueueStatus(r.Context(), projectID)
		if err != nil {
			h.respondTaskError(responder, w, err)
			return
		}
		response = make([]mergeQueueEntryResponse, 0, len(entries))
		for _, entry := range entries {
			if entry == nil {
				continue
			}
			response = append(response, toMergeQueueResponse(*entry))
		}
	}
	responder.JSON(w, http.StatusOK, response)
}

func (h taskHandlers) listRemotes(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.remotes == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "delivery service unavailable")
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if _, ok := h.getProjectForOrg(r.Context(), projectID, principal.OrganizationID, responder, w); !ok {
		return
	}

	items, err := h.remotes.ListByProject(r.Context(), projectID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	response := make([]remoteResponse, 0, len(items))
	for _, item := range items {
		response = append(response, toRemoteResponse(item))
	}
	responder.JSON(w, http.StatusOK, response)
}

func (h taskHandlers) createRemote(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	_ = principal
	if h.remotes == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "delivery service unavailable")
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if _, ok := h.getProjectForOrg(r.Context(), projectID, principal.OrganizationID, responder, w); !ok {
		return
	}

	var req createRemoteRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.URL) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "name and url are required")
		return
	}

	created, err := h.remotes.Create(r.Context(), repo.ProjectRemote{
		ProjectID:     projectID,
		Name:          strings.TrimSpace(req.Name),
		URL:           strings.TrimSpace(req.URL),
		Transport:     strings.TrimSpace(req.Transport),
		CredentialRef: trimStringPointer(req.CredentialRef),
		IsDefault:     req.IsDefault != nil && *req.IsDefault,
	})
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	if req.IsDefault != nil && *req.IsDefault {
		created, err = h.remotes.SetDefault(r.Context(), projectID, created.ID)
		if err != nil {
			h.respondTaskError(responder, w, err)
			return
		}
	}
	responder.JSON(w, http.StatusCreated, toRemoteResponse(created))
}

func (h taskHandlers) updateRemote(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	_ = principal
	if h.remotes == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "delivery service unavailable")
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	remoteID, err := parseUUIDParam(r, "rid")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid remote id")
		return
	}
	if _, ok := h.getProjectForOrg(r.Context(), projectID, principal.OrganizationID, responder, w); !ok {
		return
	}
	remoteRecord, err := h.remotes.GetByID(r.Context(), remoteID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	if remoteRecord.ProjectID != projectID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	var req updateRemoteRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.Name != nil {
		remoteRecord.Name = strings.TrimSpace(*req.Name)
	}
	if req.URL != nil {
		remoteRecord.URL = strings.TrimSpace(*req.URL)
	}
	if req.Transport != nil {
		remoteRecord.Transport = strings.TrimSpace(*req.Transport)
	}
	if req.CredentialRef != nil {
		remoteRecord.CredentialRef = trimStringPointer(req.CredentialRef)
	}
	if req.IsDefault != nil {
		remoteRecord.IsDefault = *req.IsDefault
	}

	updated, err := h.remotes.Update(r.Context(), remoteRecord)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	if req.IsDefault != nil && *req.IsDefault {
		updated, err = h.remotes.SetDefault(r.Context(), projectID, remoteID)
		if err != nil {
			h.respondTaskError(responder, w, err)
			return
		}
	}
	responder.JSON(w, http.StatusOK, toRemoteResponse(updated))
}

func (h taskHandlers) deleteRemote(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.remotes == nil || h.environments == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "delivery service unavailable")
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	remoteID, err := parseUUIDParam(r, "rid")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid remote id")
		return
	}
	if _, ok := h.getProjectForOrg(r.Context(), projectID, principal.OrganizationID, responder, w); !ok {
		return
	}

	remoteRecord, err := h.remotes.GetByID(r.Context(), remoteID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	if remoteRecord.ProjectID != projectID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	environments, err := h.environments.ListByProject(r.Context(), projectID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	for _, environment := range environments {
		if environment.RemoteID != nil && *environment.RemoteID == remoteID && environment.IsActive {
			responder.Error(w, http.StatusConflict, api.ErrCodeConflict, "remote is referenced by an active environment")
			return
		}
	}

	if err := h.remotes.Delete(r.Context(), remoteID); err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h taskHandlers) listEnvironments(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.environments == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "delivery service unavailable")
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if _, ok := h.getProjectForOrg(r.Context(), projectID, principal.OrganizationID, responder, w); !ok {
		return
	}

	items, err := h.environments.ListByProject(r.Context(), projectID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	response := make([]environmentResponse, 0, len(items))
	for _, item := range items {
		response = append(response, toEnvironmentResponse(item))
	}
	responder.JSON(w, http.StatusOK, response)
}

func (h taskHandlers) createEnvironment(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.environments == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "delivery service unavailable")
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if _, ok := h.getProjectForOrg(r.Context(), projectID, principal.OrganizationID, responder, w); !ok {
		return
	}

	var req createEnvironmentRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.DeliveryMode) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "name and delivery_mode are required")
		return
	}
	if req.RemoteID != nil && h.remotes != nil {
		remoteRecord, err := h.remotes.GetByID(r.Context(), *req.RemoteID)
		if err != nil {
			h.respondTaskError(responder, w, err)
			return
		}
		if remoteRecord.ProjectID != projectID {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "remote_id must belong to the project")
			return
		}
	}

	targetBranch := ""
	if req.TargetBranch != nil {
		targetBranch = strings.TrimSpace(*req.TargetBranch)
	}

	created, err := h.environments.Create(r.Context(), repo.ProjectEnvironment{
		ProjectID:    projectID,
		Name:         strings.TrimSpace(req.Name),
		DeliveryMode: strings.TrimSpace(req.DeliveryMode),
		RemoteID:     req.RemoteID,
		TargetBranch: targetBranch,
		ScheduleID:   req.ScheduleID,
		IsActive:     true,
	})
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusCreated, toEnvironmentResponse(created))
}

func (h taskHandlers) updateEnvironment(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.environments == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "delivery service unavailable")
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	environmentID, err := parseUUIDParam(r, "eid")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid environment id")
		return
	}
	if _, ok := h.getProjectForOrg(r.Context(), projectID, principal.OrganizationID, responder, w); !ok {
		return
	}

	environmentRecord, err := h.environments.GetByID(r.Context(), environmentID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	if environmentRecord.ProjectID != projectID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	var req updateEnvironmentRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.Name != nil {
		environmentRecord.Name = strings.TrimSpace(*req.Name)
	}
	if req.DeliveryMode != nil {
		environmentRecord.DeliveryMode = strings.TrimSpace(*req.DeliveryMode)
	}
	if req.RemoteID != nil {
		if h.remotes != nil {
			remoteRecord, err := h.remotes.GetByID(r.Context(), *req.RemoteID)
			if err != nil {
				h.respondTaskError(responder, w, err)
				return
			}
			if remoteRecord.ProjectID != projectID {
				responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "remote_id must belong to the project")
				return
			}
		}
		environmentRecord.RemoteID = req.RemoteID
	}
	if req.TargetBranch != nil {
		environmentRecord.TargetBranch = strings.TrimSpace(*req.TargetBranch)
	}
	if req.ScheduleID != nil {
		environmentRecord.ScheduleID = req.ScheduleID
	}
	if req.IsActive != nil {
		environmentRecord.IsActive = *req.IsActive
	}

	updated, err := h.environments.Update(r.Context(), environmentRecord)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusOK, toEnvironmentResponse(updated))
}

func (h taskHandlers) deployProject(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.environments == nil || h.deliveryService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "delivery service unavailable")
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if _, ok := h.getProjectForOrg(r.Context(), projectID, principal.OrganizationID, responder, w); !ok {
		return
	}

	var req deployProjectRequest
	if err := decodeJSONLenient(r, &req); err != nil && !errors.Is(err, errEmptyBody) {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	environmentRecord, err := h.resolveDeployEnvironment(r.Context(), projectID, req.EnvironmentID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
		h.respondTaskError(responder, w, err)
		return
	}
	if strings.TrimSpace(environmentRecord.DeliveryMode) != "gated" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "manual deploy is only valid for delivery_mode=gated")
		return
	}

	commitSHA := strings.TrimSpace(req.CommitSHA)
	if commitSHA == "" {
		resolved, resolveErr := h.resolveDeployCommitFromEntries(r.Context(), projectID, req.EntryIDs)
		if resolveErr != nil {
			h.respondTaskError(responder, w, resolveErr)
			return
		}
		commitSHA = resolved
	}

	deployTask, err := h.createDeployTask(r.Context(), environmentRecord, principal.UserID, commitSHA, req.EntryIDs)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusAccepted, map[string]any{
		"deploy_task_id": deployTask.ID,
	})
}

func (h taskHandlers) deployEnvironment(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.environments == nil || h.deliveryService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "delivery service unavailable")
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	environmentID, err := parseUUIDParam(r, "eid")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid environment id")
		return
	}
	if _, ok := h.getProjectForOrg(r.Context(), projectID, principal.OrganizationID, responder, w); !ok {
		return
	}

	environmentRecord, err := h.environments.GetByID(r.Context(), environmentID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	if environmentRecord.ProjectID != projectID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}
	if strings.TrimSpace(environmentRecord.DeliveryMode) != "gated" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "manual deploy is only valid for delivery_mode=gated")
		return
	}

	var req deployEnvironmentRequest
	if err := decodeJSONLenient(r, &req); err != nil && !errors.Is(err, errEmptyBody) {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	deployTask, err := h.createDeployTask(r.Context(), environmentRecord, principal.UserID, strings.TrimSpace(req.CommitSHA), req.EntryIDs)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusOK, h.toTaskResponse(r.Context(), deployTask))
}

func (h taskHandlers) createDeployTask(ctx context.Context, environmentRecord repo.ProjectEnvironment, userID uuid.UUID, commitSHA string, entryIDs []uuid.UUID) (repo.ProjectTask, error) {
	deployTask, err := h.deliveryService.CreateDeployTask(ctx, environmentRecord.ID, strings.TrimSpace(commitSHA))
	if err != nil {
		return repo.ProjectTask{}, err
	}

	if len(entryIDs) > 0 {
		updatedTask, updateErr := h.attachDeployMetadata(ctx, deployTask, entryIDs)
		if updateErr != nil {
			return repo.ProjectTask{}, updateErr
		}
		deployTask = updatedTask
	}

	if isTestMode() && h.taskService != nil {
		actor := tasksvc.Actor{Type: "human_user", ID: userID}
		inProgress, err := h.taskService.TransitionStatus(ctx, deployTask.ID, "in_progress", actor)
		if err != nil {
			return repo.ProjectTask{}, err
		}
		doneTask, err := h.taskService.TransitionStatus(ctx, inProgress.ID, "done", actor)
		if err != nil {
			return repo.ProjectTask{}, err
		}
		deployTask = *doneTask
		if h.envUpdater != nil {
			if err := h.envUpdater.OnDeployCompleted(ctx, deployTask.ID); err != nil {
				return repo.ProjectTask{}, err
			}
		}
	}

	return deployTask, nil
}

func (h taskHandlers) resolveDeployEnvironment(ctx context.Context, projectID uuid.UUID, requestedID *uuid.UUID) (repo.ProjectEnvironment, error) {
	if h.environments == nil {
		return repo.ProjectEnvironment{}, repo.ErrNotFound
	}

	if requestedID != nil && *requestedID != uuid.Nil {
		record, err := h.environments.GetByID(ctx, *requestedID)
		if err != nil {
			return repo.ProjectEnvironment{}, err
		}
		if record.ProjectID != projectID {
			return repo.ProjectEnvironment{}, repo.ErrNotFound
		}
		return record, nil
	}

	environments, err := h.environments.ListByProject(ctx, projectID)
	if err != nil {
		return repo.ProjectEnvironment{}, err
	}

	for _, environment := range environments {
		if strings.TrimSpace(environment.DeliveryMode) == "gated" && environment.IsActive {
			return environment, nil
		}
	}
	for _, environment := range environments {
		if strings.TrimSpace(environment.DeliveryMode) == "gated" {
			return environment, nil
		}
	}
	return repo.ProjectEnvironment{}, repo.ErrNotFound
}

func (h taskHandlers) resolveDeployCommitFromEntries(ctx context.Context, projectID uuid.UUID, entryIDs []uuid.UUID) (string, error) {
	if h.mergeQueue == nil || len(entryIDs) == 0 {
		return "", nil
	}
	for _, entryID := range entryIDs {
		entry, err := h.mergeQueue.GetByID(ctx, entryID)
		if err != nil {
			return "", err
		}
		if entry.ProjectID != projectID {
			return "", repo.ErrNotFound
		}
		if entry.CommitSHA != nil {
			if trimmed := strings.TrimSpace(*entry.CommitSHA); trimmed != "" {
				return trimmed, nil
			}
		}
	}
	return "", nil
}

func (h taskHandlers) attachDeployMetadata(ctx context.Context, deployTask repo.ProjectTask, entryIDs []uuid.UUID) (repo.ProjectTask, error) {
	if h.tasks == nil {
		return deployTask, nil
	}
	taskRecord, err := h.tasks.GetByID(ctx, deployTask.ID)
	if err != nil {
		return repo.ProjectTask{}, err
	}

	payload := map[string]any{}
	if len(taskRecord.Metadata) > 0 {
		_ = json.Unmarshal(taskRecord.Metadata, &payload)
	}
	deliveryBlock, _ := payload["delivery"].(map[string]any)
	if deliveryBlock == nil {
		deliveryBlock = map[string]any{}
	}

	entryIDStrings := make([]string, 0, len(entryIDs))
	triggeredTaskID := ""
	for _, entryID := range entryIDs {
		entryIDStrings = append(entryIDStrings, entryID.String())
		if h.mergeQueue != nil && triggeredTaskID == "" {
			entry, getErr := h.mergeQueue.GetByID(ctx, entryID)
			if getErr != nil {
				return repo.ProjectTask{}, getErr
			}
			if entry.ProjectID != deployTask.ProjectID {
				return repo.ProjectTask{}, repo.ErrNotFound
			}
			triggeredTaskID = entry.TaskID.String()
		}
	}
	if len(entryIDStrings) > 0 {
		deliveryBlock["merge_queue_entry_ids"] = entryIDStrings
	}
	if strings.TrimSpace(triggeredTaskID) != "" {
		deliveryBlock["triggered_by_task_id"] = triggeredTaskID
	}
	payload["delivery"] = deliveryBlock

	encoded, err := json.Marshal(payload)
	if err != nil {
		return repo.ProjectTask{}, err
	}
	taskRecord.Metadata = encoded

	updated, err := h.tasks.Update(ctx, taskRecord)
	if err != nil {
		return repo.ProjectTask{}, err
	}
	return updated, nil
}

func (h taskHandlers) rollbackProject(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.rollbackService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "delivery rollback service unavailable")
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if _, ok := h.getProjectForOrg(r.Context(), projectID, principal.OrganizationID, responder, w); !ok {
		return
	}

	var req rollbackRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	rollbackTaskID, err := h.rollbackService.InitiateRollback(r.Context(), projectID, strings.TrimSpace(req.TargetCommitSHA))
	if err != nil {
		h.respondTaskError(responder, w, err)
		return
	}

	responder.JSON(w, http.StatusAccepted, map[string]any{
		"rollback_task_id": rollbackTaskID,
	})
}

func (h taskHandlers) requirePrincipal(w http.ResponseWriter, r *http.Request) (middleware.Principal, bool) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return middleware.Principal{}, false
	}
	return principal, true
}

func (h taskHandlers) getTaskForOrg(ctx context.Context, taskID, orgID uuid.UUID, responder api.Responder, w http.ResponseWriter) (repo.ProjectTask, bool) {
	if h.tasks == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "task service unavailable")
		return repo.ProjectTask{}, false
	}
	taskRecord, err := h.tasks.GetByID(ctx, taskID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return repo.ProjectTask{}, false
	}
	if taskRecord.OrganizationID != orgID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return repo.ProjectTask{}, false
	}
	return taskRecord, true
}

func (h taskHandlers) getProjectForOrg(ctx context.Context, projectID, orgID uuid.UUID, responder api.Responder, w http.ResponseWriter) (repo.Project, bool) {
	if h.projects == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "project service unavailable")
		return repo.Project{}, false
	}
	projectRecord, err := h.projects.GetByID(ctx, projectID)
	if err != nil {
		h.respondTaskError(responder, w, err)
		return repo.Project{}, false
	}
	if projectRecord.OrganizationID != orgID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return repo.Project{}, false
	}
	return projectRecord, true
}

func (h taskHandlers) getActiveExecution(ctx context.Context, taskRecord repo.ProjectTask) (repo.FlowNodeExecution, error) {
	if taskRecord.CurrentFlowNodeID == nil {
		return repo.FlowNodeExecution{}, flowsvc.ErrFlowNotStarted
	}
	if h.executions == nil {
		return repo.FlowNodeExecution{}, repo.ErrNotFound
	}
	return h.executions.GetActive(ctx, taskRecord.ID, *taskRecord.CurrentFlowNodeID)
}

func (h taskHandlers) toTaskResponse(ctx context.Context, taskRecord repo.ProjectTask) taskResponse {
	response := taskResponse{
		ID:                  taskRecord.ID,
		OrganizationID:      taskRecord.OrganizationID,
		ProjectID:           taskRecord.ProjectID,
		TaskNumber:          taskRecord.TaskNumber,
		Title:               taskRecord.Title,
		Description:         taskRecord.Description,
		WorkStatus:          taskRecord.WorkStatus,
		CurrentFlowNodeID:   taskRecord.CurrentFlowNodeID,
		FlowTemplateID:      taskRecord.FlowTemplateID,
		ScheduleID:          taskRecord.ScheduleID,
		BranchName:          taskRecord.BranchName,
		RequiresHumanReview: taskRecord.RequiresHumanReview,
		CreatedByType:       taskRecord.CreatedByType,
		CreatedByID:         taskRecord.CreatedByID,
		AssignedAgentID:     taskRecord.AssignedAgentID,
		Metadata:            taskRecord.Metadata,
		CreatedAt:           taskRecord.CreatedAt,
		UpdatedAt:           taskRecord.UpdatedAt,
		CompletedAt:         taskRecord.CompletedAt,
	}
	if taskRecord.CurrentFlowNodeID != nil && h.flowNodes != nil {
		node, err := h.flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID)
		if err == nil {
			nodeResponse := toFlowNodeResponse(&node)
			response.CurrentFlowNode = &nodeResponse
		}
	}
	return response
}

func (h taskHandlers) findPendingTaskReviewInbox(ctx context.Context, taskID uuid.UUID) (*repo.InboxItem, error) {
	if h.pool == nil {
		return nil, repo.ErrNotFound
	}
	row := h.pool.QueryRow(ctx, `
		SELECT
			id,
			organization_id,
			target_user_id,
			item_type,
			source_project_id,
			source_task_id,
			created_by_type,
			created_by_id,
			title,
			body,
			action_payload,
			is_read,
			is_acted,
			acted_at,
			acted_by_id,
			expires_at,
			created_at
		FROM inbox_item
		WHERE source_task_id = $1
		  AND item_type = 'task_review'
		  AND is_acted = false
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, taskID)

	var item repo.InboxItem
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.TargetUserID,
		&item.ItemType,
		&item.SourceProjectID,
		&item.SourceTaskID,
		&item.CreatedByType,
		&item.CreatedByID,
		&item.Title,
		&item.Body,
		&item.ActionPayload,
		&item.IsRead,
		&item.IsActed,
		&item.ActedAt,
		&item.ActedByID,
		&item.ExpiresAt,
		&item.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, repo.ErrNotFound
		}
		return nil, err
	}
	return &item, nil
}

func (h taskHandlers) listAllTaskSubtasks(ctx context.Context, taskID uuid.UUID) ([]repo.ProjectSubtask, error) {
	if h.pool == nil {
		return nil, repo.ErrNotFound
	}
	rows, err := h.pool.Query(ctx, `
		SELECT
			id,
			task_id,
			flow_node_execution_id,
			title,
			description,
			work_status,
			sequence_number,
			assignee_type,
			assignee_id,
			created_by_type,
			created_by_id,
			completed_at,
			created_at,
			updated_at
		FROM project_subtask
		WHERE task_id = $1
		ORDER BY created_at ASC, id ASC
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]repo.ProjectSubtask, 0)
	for rows.Next() {
		var item repo.ProjectSubtask
		if err := rows.Scan(
			&item.ID,
			&item.TaskID,
			&item.FlowNodeExecutionID,
			&item.Title,
			&item.Description,
			&item.WorkStatus,
			&item.SequenceNumber,
			&item.AssigneeType,
			&item.AssigneeID,
			&item.CreatedByType,
			&item.CreatedByID,
			&item.CompletedAt,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func (h taskHandlers) updateSubtaskDetails(ctx context.Context, subtask repo.ProjectSubtask, title *string, description *string) (repo.ProjectSubtask, error) {
	if h.pool == nil {
		return repo.ProjectSubtask{}, repo.ErrNotFound
	}
	nextTitle := subtask.Title
	if title != nil {
		nextTitle = strings.TrimSpace(*title)
	}
	nextDescription := subtask.Description
	if description != nil {
		nextDescription = trimStringPointer(description)
	}

	row := h.pool.QueryRow(ctx, `
		UPDATE project_subtask
		SET
			title = $2,
			description = $3
		WHERE id = $1
		RETURNING
			id,
			task_id,
			flow_node_execution_id,
			title,
			description,
			work_status,
			sequence_number,
			assignee_type,
			assignee_id,
			created_by_type,
			created_by_id,
			completed_at,
			created_at,
			updated_at
	`, subtask.ID, nextTitle, nextDescription)

	var updated repo.ProjectSubtask
	if err := row.Scan(
		&updated.ID,
		&updated.TaskID,
		&updated.FlowNodeExecutionID,
		&updated.Title,
		&updated.Description,
		&updated.WorkStatus,
		&updated.SequenceNumber,
		&updated.AssigneeType,
		&updated.AssigneeID,
		&updated.CreatedByType,
		&updated.CreatedByID,
		&updated.CompletedAt,
		&updated.CreatedAt,
		&updated.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.ProjectSubtask{}, repo.ErrNotFound
		}
		return repo.ProjectSubtask{}, err
	}
	return updated, nil
}

func (h taskHandlers) respondTaskError(responder api.Responder, w http.ResponseWriter, err error) {
	status, code, message := mapTaskError(err)
	responder.Error(w, status, code, message)
}

func mapTaskError(err error) (int, string, string) {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return http.StatusNotFound, api.ErrCodeNotFound, "resource not found"
	case errors.Is(err, repo.ErrConflict):
		return http.StatusConflict, api.ErrCodeConflict, "conflict"
	case errors.Is(err, tasksvc.ErrInboxActionForbidden):
		return http.StatusForbidden, api.ErrCodeForbidden, "forbidden"
	case errors.Is(err, tasksvc.ErrBrowserHandoffUnavailable):
		return http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "browser handoff service unavailable"
	case errors.Is(err, tasksvc.ErrUnknownInboxAction),
		errors.Is(err, tasksvc.ErrUnknownInboxItemType),
		errors.Is(err, tasksvc.ErrInboxItemTypeInvalid),
		errors.Is(err, tasksvc.ErrSourceTaskRequired),
		errors.Is(err, tasksvc.ErrSourceProjectIDRequired),
		errors.Is(err, tasksvc.ErrTransitionTargetRequired),
		errors.Is(err, tasksvc.ErrActorTypeInvalidForAction),
		errors.Is(err, tasksvc.ErrAgentNotAssigned),
		errors.Is(err, deliverysvc.ErrProjectIDRequired),
		errors.Is(err, deliverysvc.ErrTargetCommitSHARequired),
		errors.Is(err, deliverysvc.ErrTargetCommitNotFound),
		errors.Is(err, flowsvc.ErrCrossLevelDependency),
		errors.Is(err, flowsvc.ErrSelfDependency),
		errors.Is(err, flowsvc.ErrCyclicDependency),
		errors.Is(err, flowsvc.ErrInvalidSubtaskTransition),
		errors.Is(err, flowsvc.ErrNoRejectionPath),
		errors.Is(err, deliverysvc.ErrEnvironmentIDRequired):
		return http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error()
	case errors.Is(err, tasksvc.ErrInboxItemNotFound):
		return http.StatusNotFound, api.ErrCodeNotFound, "resource not found"
	case errors.Is(err, flowsvc.ErrFlowNotStarted):
		return http.StatusNotFound, api.ErrCodeNotFound, "resource not found"
	case errors.Is(err, flowsvc.ErrMaxVisitsExceeded):
		return http.StatusConflict, api.ErrCodeConflict, err.Error()
	default:
		var transitionErr tasksvc.ErrInvalidStatusTransition
		if errors.As(err, &transitionErr) {
			return http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error()
		}
		return http.StatusInternalServerError, api.ErrCodeInternal, "request failed"
	}
}

func normalizeMultiValue(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

func toFlowNodeExecutionResponse(model repo.FlowNodeExecution) flowNodeExecutionResponse {
	return flowNodeExecutionResponse{
		ID:          model.ID,
		TaskID:      model.TaskID,
		FlowNodeID:  model.FlowNodeID,
		VisitNumber: model.VisitNumber,
		VisitCount:  model.VisitNumber,
		Status:      model.Status,
		SessionID:   model.SessionID,
		CommitSHA:   model.CommitSHA,
		StartedAt:   model.StartedAt,
		CompletedAt: model.CompletedAt,
		Metadata:    model.Metadata,
	}
}

func toSubtaskResponse(model repo.ProjectSubtask) subtaskResponse {
	return subtaskResponse{
		ID:                  model.ID,
		TaskID:              model.TaskID,
		FlowNodeExecutionID: model.FlowNodeExecutionID,
		Title:               model.Title,
		Description:         model.Description,
		WorkStatus:          model.WorkStatus,
		SequenceNumber:      model.SequenceNumber,
		AssigneeType:        model.AssigneeType,
		AssigneeID:          model.AssigneeID,
		CreatedByType:       model.CreatedByType,
		CreatedByID:         model.CreatedByID,
		CompletedAt:         model.CompletedAt,
		CreatedAt:           model.CreatedAt,
		UpdatedAt:           model.UpdatedAt,
	}
}

func toDependencyResponse(model repo.ProjectTaskDependency) dependencyResponse {
	return dependencyResponse{
		ID:            model.ID,
		SourceType:    model.SourceType,
		SourceID:      model.SourceID,
		DependsOnType: model.DependsOnType,
		DependsOnID:   model.DependsOnID,
		CreatedByType: model.CreatedByType,
		CreatedByID:   model.CreatedByID,
		CreatedAt:     model.CreatedAt,
	}
}

func toTaskEventResponse(model repo.ProjectTaskEvent) taskEventResponse {
	return taskEventResponse{
		ID:         model.ID,
		TaskID:     model.TaskID,
		ProjectID:  model.ProjectID,
		EventType:  model.EventType,
		ActorType:  model.ActorType,
		ActorID:    model.ActorID,
		FlowNodeID: model.FlowNodeID,
		Payload:    model.Payload,
		CreatedAt:  model.CreatedAt,
	}
}

func toTaskParticipantResponse(model repo.ProjectTaskParticipant) taskParticipantResponse {
	return taskParticipantResponse{
		ID:              model.ID,
		TaskID:          model.TaskID,
		ParticipantType: model.ParticipantType,
		ParticipantID:   model.ParticipantID,
		Role:            model.Role,
		JoinedAt:        model.JoinedAt,
		LeftAt:          model.LeftAt,
	}
}

func toInboxResponse(model repo.InboxItem) inboxResponse {
	return inboxResponse{
		ID:            model.ID,
		ItemType:      model.ItemType,
		Title:         model.Title,
		Body:          model.Body,
		IsRead:        model.IsRead,
		IsActed:       model.IsActed,
		ActionPayload: model.ActionPayload,
		SourceTask: inboxSourceTask{
			ProjectID: model.SourceProjectID,
			TaskID:    model.SourceTaskID,
		},
		CreatedAt: model.CreatedAt,
	}
}

func toMergeQueueResponse(model repo.MergeQueueEntry) mergeQueueEntryResponse {
	return mergeQueueEntryResponse{
		ID:            model.ID,
		ProjectID:     model.ProjectID,
		TaskID:        model.TaskID,
		BranchName:    model.BranchName,
		TargetBranch:  model.TargetBranch,
		CommitSHA:     model.CommitSHA,
		Position:      model.Position,
		Status:        model.Status,
		EnqueuedAt:    model.EnqueuedAt,
		MergedAt:      model.MergedAt,
		ArchivedAt:    model.ArchivedAt,
		FailureReason: model.FailureReason,
		Metadata:      model.Metadata,
	}
}

func toRemoteResponse(model repo.ProjectRemote) remoteResponse {
	return remoteResponse{
		ID:            model.ID,
		ProjectID:     model.ProjectID,
		Name:          model.Name,
		URL:           model.URL,
		IsDefault:     model.IsDefault,
		Transport:     model.Transport,
		CredentialRef: model.CredentialRef,
		CreatedAt:     model.CreatedAt,
		UpdatedAt:     model.UpdatedAt,
	}
}

func toEnvironmentResponse(model repo.ProjectEnvironment) environmentResponse {
	return environmentResponse{
		ID:                 model.ID,
		ProjectID:          model.ProjectID,
		Name:               model.Name,
		DeliveryMode:       model.DeliveryMode,
		RemoteID:           model.RemoteID,
		TargetBranch:       model.TargetBranch,
		DeployTaskID:       model.DeployTaskID,
		LastDeployedCommit: model.LastDeployedCommit,
		DeployedCommitSHA:  model.LastDeployedCommit,
		LastDeployedAt:     model.LastDeployedAt,
		IsActive:           model.IsActive,
		ScheduleID:         model.ScheduleID,
		CreatedAt:          model.CreatedAt,
		UpdatedAt:          model.UpdatedAt,
	}
}

func isTestMode() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("OTTERCAMP_MODE")), "test")
}

func buildNextCursorFromTasks(items []repo.ProjectTask, limit int) *string {
	if len(items) <= limit {
		return nil
	}
	cursor := api.PaginationEncoder{}.Encode(items[limit-1].CreatedAt, items[limit-1].ID)
	return &cursor
}

func buildNextCursorFromTaskEvents(items []repo.ProjectTaskEvent, limit int) *string {
	if len(items) <= limit {
		return nil
	}
	cursor := api.PaginationEncoder{}.Encode(items[limit-1].CreatedAt, items[limit-1].ID)
	return &cursor
}

func buildNextCursorFromInbox(items []repo.InboxItem, limit int) *string {
	if len(items) <= limit {
		return nil
	}
	cursor := api.PaginationEncoder{}.Encode(items[limit-1].CreatedAt, items[limit-1].ID)
	return &cursor
}

func parseOptionalUUID(raw string) (*uuid.UUID, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(trimmed)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parsePaginationCursor(values url.Values) (int, string, *time.Time, uuid.UUID, error) {
	params := api.ParsePaginationParams(values)
	cursor := strings.TrimSpace(values.Get("page_cursor"))
	if cursor == "" {
		cursor = strings.TrimSpace(values.Get("cursor"))
	}
	if cursor == "" {
		cursor = params.Cursor
	}
	if cursor == "" {
		return params.Limit, cursor, nil, uuid.Nil, nil
	}
	cursorTime, cursorID, err := api.PaginationDecoder{}.Decode(cursor)
	if err != nil {
		return 0, "", nil, uuid.Nil, err
	}
	return params.Limit, cursor, &cursorTime, cursorID, nil
}

func trimStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func parseOptionalBool(raw string) (bool, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false, false
	}
	parsed, err := strconv.ParseBool(trimmed)
	if err != nil {
		return false, false
	}
	return parsed, true
}

func optionalBoolPtr(value bool, set bool) *bool {
	if !set {
		return nil
	}
	copy := value
	return &copy
}

var errEmptyBody = errors.New("empty body")

func decodeJSONLenient(r *http.Request, out any) error {
	if r.Body == nil {
		return errEmptyBody
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return errEmptyBody
		}
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != nil && !errors.Is(err, io.EOF) {
		return errors.New("multiple json values")
	}
	return nil
}
