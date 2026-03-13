package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/flowpolicy"
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/scheduling"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/validation"
)

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

	ErrSlugTaken                = errors.New("project slug is already taken")
	ErrInvalidSlug              = errors.New("invalid project slug")
	ErrProjectHasActiveTasks    = errors.New("project has active tasks")
	ErrSystemTemplateProtected  = errors.New("system flow template is protected")
	ErrTemplateInUse            = errors.New("flow template is currently in use")
	ErrInvalidCronExpression    = errors.New("invalid cron expression")
	ErrAgentNotFound            = errors.New("agent not found")
	ErrFlowNodeTemplateMismatch = errors.New("flow node does not belong to flow template")
	ErrDisplayNameInvalid       = errors.New("display_name contains HTML tags")
	ErrFlowTemplateReviewPath   = errors.New("flow template must define a work -> review -> completion path")
	ErrReviewNodeEdgesRequired  = errors.New("review nodes must define both approve and reject edges")
	ErrOrganizationIDRequired   = errors.New("organization_id is required")
	ErrProjectIDRequired        = errors.New("project_id is required")
	ErrTemplateIDRequired       = errors.New("template_id is required")
	ErrNodeIDRequired           = errors.New("node_id is required")
	ErrScheduleIDRequired       = errors.New("schedule_id is required")
	ErrProjectPaused            = projectpause.ErrProjectPaused
)

const (
	actorHuman  = "human_user"
	actorAgent  = "agent"
	actorSystem = "system"

	bootstrapBlocksScopeAll = "all"
	bootstrapTemplateSlug   = "bootstrap-governance-gate"
)

type bootstrapSetupTaskSpec struct {
	Slug        string
	Title       string
	Description string
	Assignee    string
}

var bootstrapSetupTaskSpecs = []bootstrapSetupTaskSpec{
	{
		Slug:        "bind-repo-environment",
		Title:       "Bind repo and environment",
		Description: "Confirm the canonical repo/workspace binding, environment records, and credentials required for project execution.",
		Assignee:    "lori",
	},
	{
		Slug:        "staff-project",
		Title:       "Staff the project",
		Description: "Assign the required project roles and verify the initial working roster before execution begins.",
		Assignee:    "lori",
	},
	{
		Slug:        "decompose-workstreams",
		Title:       "Decompose workstreams into bounded tasks",
		Description: "Break the kickoff scope into bounded executable workstreams and capture the task tree shape explicitly.",
		Assignee:    "lori",
	},
	{
		Slug:        "validate-task-shape",
		Title:       "Validate task sizing and dependencies",
		Description: "Verify task scope, dependency ordering, and readiness so oversized or ambiguous work does not enter execution.",
		Assignee:    "lori",
	},
	{
		Slug:        "attach-validate-flow-templates",
		Title:       "Attach and validate flow templates",
		Description: "Attach runnable flow templates to the planned tasks and confirm each required execution path is valid.",
		Assignee:    "lori",
	},
	{
		Slug:        "select-first-wave",
		Title:       "Select first-wave runnable tasks",
		Description: "Choose the first bounded executable tasks that should enter real project execution once bootstrap is approved.",
		Assignee:    "lori",
	},
	{
		Slug:        "record-frank-sign-off",
		Title:       "Request and record Frank sign-off",
		Description: "Collect the final bootstrap review decision from Frank before the project may cross into real execution.",
		Assignee:    "frank",
	},
}

type Project = repo.Project
type FlowTemplate = repo.FlowTemplate
type FlowNode = repo.FlowNode
type TaskSchedule = repo.TaskSchedule
type Task = repo.ProjectTask

type ProjectFilter struct {
	DeliveryMode string
	SlugPrefix   string
	Status       string
}

type CreateProjectRequest struct {
	OrganizationID       uuid.UUID
	Slug                 string
	DisplayName          string
	Description          string
	DeliveryMode         string
	DeployFlowTemplateID *uuid.UUID
	Settings             json.RawMessage
	CreatedByType        string
	CreatedByID          uuid.UUID
}

type UpdateProjectRequest struct {
	Slug                 *string
	DisplayName          *string
	Description          *string
	DeliveryMode         *string
	DeployFlowTemplateID *uuid.UUID
	Settings             *json.RawMessage
	UpdatedByType        string
	UpdatedByID          uuid.UUID
}

type PauseProjectRequest struct {
	Reason       string
	Metadata     json.RawMessage
	PausedByType string
	PausedByID   uuid.UUID
}

type CreateFlowTemplateRequest struct {
	OrganizationID *uuid.UUID
	ProjectID      *uuid.UUID
	Slug           string
	DisplayName    string
	Description    string
	StartNodeID    *uuid.UUID
	IsSystem       bool
	CreatedByType  string
	CreatedByID    uuid.UUID
}

type UpdateFlowTemplateRequest struct {
	Slug          *string
	DisplayName   *string
	Description   *string
	StartNodeID   *uuid.UUID
	UpdatedByType string
	UpdatedByID   uuid.UUID
}

type AddFlowNodeRequest struct {
	DisplayName         string
	NodeType            string
	Position            int
	ActorType           *string
	ActorID             *uuid.UUID
	NextNodeID          *uuid.UUID
	RejectNodeID        *uuid.UUID
	MCPTools            []repo.FlowNodeMCPTool
	ToolDomains         []string
	RequiresHumanReview bool
	MaxVisits           int
	Metadata            json.RawMessage
}

type UpdateFlowNodeRequest struct {
	DisplayName         *string
	NodeType            *string
	Position            *int
	ActorType           *string
	ActorID             *uuid.UUID
	NextNodeID          *uuid.UUID
	RejectNodeID        *uuid.UUID
	MCPTools            *[]repo.FlowNodeMCPTool
	ToolDomains         *[]string
	RequiresHumanReview *bool
	MaxVisits           *int
	Metadata            *json.RawMessage
}

type CreateScheduleRequest struct {
	OrganizationID uuid.UUID
	ProjectID      uuid.UUID
	FlowTemplateID uuid.UUID
	DisplayName    string
	CronExpression string
	OverlapPolicy  string
	MaxDurationMS  *int64
	IsEnabled      bool
	CreatedByType  string
	CreatedByID    uuid.UUID
}

type UpdateScheduleRequest struct {
	FlowTemplateID *uuid.UUID
	DisplayName    *string
	CronExpression *string
	OverlapPolicy  *string
	MaxDurationMS  *int64
	IsEnabled      *bool
}

type ProjectService interface {
	Create(ctx context.Context, req CreateProjectRequest) (*Project, error)
	Get(ctx context.Context, orgID, projectID uuid.UUID) (*Project, error)
	GetBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*Project, error)
	List(ctx context.Context, orgID uuid.UUID, filter ProjectFilter) ([]*Project, error)
	Update(ctx context.Context, orgID, projectID uuid.UUID, req UpdateProjectRequest) (*Project, error)
	Pause(ctx context.Context, orgID, projectID uuid.UUID, req PauseProjectRequest) (*Project, error)
	Resume(ctx context.Context, orgID, projectID uuid.UUID, resumedByType string, resumedByID uuid.UUID) (*Project, error)
	Archive(ctx context.Context, orgID, projectID uuid.UUID) (*Project, error)
	Delete(ctx context.Context, orgID, projectID uuid.UUID) error

	CreateFlowTemplate(ctx context.Context, req CreateFlowTemplateRequest) (*FlowTemplate, error)
	GetFlowTemplate(ctx context.Context, orgID, templateID uuid.UUID) (*FlowTemplate, error)
	ListFlowTemplates(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID) ([]*FlowTemplate, error)
	UpdateFlowTemplate(ctx context.Context, orgID, templateID uuid.UUID, req UpdateFlowTemplateRequest) (*FlowTemplate, error)

	AddFlowNode(ctx context.Context, templateID uuid.UUID, req AddFlowNodeRequest) (*FlowNode, error)
	UpdateFlowNode(ctx context.Context, nodeID uuid.UUID, req UpdateFlowNodeRequest) (*FlowNode, error)
	RemoveFlowNode(ctx context.Context, nodeID uuid.UUID) error
	GetFlowNodes(ctx context.Context, templateID uuid.UUID) ([]*FlowNode, error)

	CreateSchedule(ctx context.Context, req CreateScheduleRequest) (*TaskSchedule, error)
	GetSchedule(ctx context.Context, scheduleID uuid.UUID) (*TaskSchedule, error)
	ListSchedules(ctx context.Context, projectID uuid.UUID) ([]*TaskSchedule, error)
	UpdateSchedule(ctx context.Context, scheduleID uuid.UUID, req UpdateScheduleRequest) (*TaskSchedule, error)
	EnableSchedule(ctx context.Context, scheduleID uuid.UUID) (*TaskSchedule, error)
	DisableSchedule(ctx context.Context, scheduleID uuid.UUID) (*TaskSchedule, error)
	DeleteSchedule(ctx context.Context, scheduleID uuid.UUID) error
}

type projectRepository interface {
	Create(ctx context.Context, project repo.Project) (repo.Project, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
	GetBySlug(ctx context.Context, organizationID uuid.UUID, slug string) (repo.Project, error)
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.Project, error)
	Update(ctx context.Context, project repo.Project) (repo.Project, error)
	Archive(ctx context.Context, id uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type projectRepositoryListAll interface {
	ListAll(ctx context.Context, organizationID uuid.UUID) ([]repo.Project, error)
}

type flowTemplateRepository interface {
	Create(ctx context.Context, template repo.FlowTemplate) (repo.FlowTemplate, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowTemplate, error)
	ListCurrent(ctx context.Context, organizationID, projectID *uuid.UUID) ([]repo.FlowTemplate, error)
	Update(ctx context.Context, template repo.FlowTemplate) (repo.FlowTemplate, error)
	Deprecate(ctx context.Context, currentID uuid.UUID, next repo.FlowTemplate) (repo.FlowTemplate, error)
}

type flowNodeRepository interface {
	Create(ctx context.Context, node repo.FlowNode) (repo.FlowNode, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNode, error)
	Update(ctx context.Context, node repo.FlowNode) (repo.FlowNode, error)
	Delete(ctx context.Context, id uuid.UUID) error
	GetByTemplateOrdered(ctx context.Context, flowTemplateID uuid.UUID) ([]repo.FlowNode, error)
}

type taskScheduleRepository interface {
	Create(ctx context.Context, schedule repo.TaskSchedule) (repo.TaskSchedule, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.TaskSchedule, error)
	List(ctx context.Context, projectID uuid.UUID) ([]repo.TaskSchedule, error)
	Update(ctx context.Context, schedule repo.TaskSchedule) (repo.TaskSchedule, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type taskRepository interface {
	Create(ctx context.Context, task repo.ProjectTask) (repo.ProjectTask, error)
	UpdateMetadata(ctx context.Context, id uuid.UUID, metadata json.RawMessage) (repo.ProjectTask, error)
	Update(ctx context.Context, task repo.ProjectTask) (repo.ProjectTask, error)
}

type bootstrapTaskService interface {
	CreateTask(ctx context.Context, req tasksvc.CreateTaskRequest) (*tasksvc.ProjectTask, error)
}

type projectAssignmentRepository interface {
	Assign(ctx context.Context, assignment repo.AgentProjectAssignment) (repo.AgentProjectAssignment, error)
}

type modelInvocationProjectCleaner interface {
	ClearProjectReferences(ctx context.Context, projectID uuid.UUID) error
}

type chatSessionProjectCleaner interface {
	DeleteProjectScoped(ctx context.Context, projectID uuid.UUID) error
	CloseProjectScoped(ctx context.Context, projectID uuid.UUID) error
}

type projectTaskActivityChecker interface {
	HasActiveProjectTasks(ctx context.Context, projectID uuid.UUID) (bool, error)
}

type agentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.Agent, error)
}

type Options struct {
	Pool    *pgxpool.Pool
	DataDir string

	Projects         projectRepository
	Environments     RepoBindingEnvironmentRepository
	Templates        flowTemplateRepository
	Nodes            flowNodeRepository
	Schedules        taskScheduleRepository
	Tasks            taskRepository
	TaskService      bootstrapTaskService
	Assignments      projectAssignmentRepository
	ModelInvocations modelInvocationProjectCleaner
	ChatSessions     chatSessionProjectCleaner
	Agents           agentRepository

	Events eventbus.EventBus
	Clock  clock.Clock
}

type service struct {
	pool    *pgxpool.Pool
	dataDir string

	projects         projectRepository
	environments     RepoBindingEnvironmentRepository
	templates        flowTemplateRepository
	nodes            flowNodeRepository
	schedules        taskScheduleRepository
	tasks            taskRepository
	taskService      bootstrapTaskService
	assignments      projectAssignmentRepository
	modelInvocations modelInvocationProjectCleaner
	chatSessions     chatSessionProjectCleaner
	agents           agentRepository

	activeTaskChecker projectTaskActivityChecker
	events            eventbus.EventBus
	clock             clock.Clock

	cronParser      *scheduling.CronParser
	scheduleService *scheduling.TaskScheduleService
}

func NewService(opts Options) (ProjectService, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("project service requires a database pool")
	}
	if opts.Events == nil {
		return nil, fmt.Errorf("project service requires an event bus")
	}

	svc := &service{
		pool:       opts.Pool,
		dataDir:    opts.DataDir,
		events:     opts.Events,
		clock:      opts.Clock,
		cronParser: scheduling.NewCronParser(),
	}
	if svc.clock == nil {
		svc.clock = clock.Real{}
	}
	if opts.Projects != nil {
		svc.projects = opts.Projects
	} else {
		svc.projects = repo.NewProjectRepo(opts.Pool)
	}
	if opts.Environments != nil {
		svc.environments = opts.Environments
	} else {
		svc.environments = repo.NewProjectEnvironmentRepo(opts.Pool)
	}
	if opts.Templates != nil {
		svc.templates = opts.Templates
	} else {
		svc.templates = repo.NewFlowTemplateRepo(opts.Pool)
	}
	if opts.Nodes != nil {
		svc.nodes = opts.Nodes
	} else {
		svc.nodes = repo.NewFlowNodeRepo(opts.Pool)
	}
	if opts.Schedules != nil {
		svc.schedules = opts.Schedules
	} else {
		svc.schedules = repo.NewTaskScheduleRepo(opts.Pool)
	}
	if opts.Tasks != nil {
		svc.tasks = opts.Tasks
	} else {
		svc.tasks = repo.NewProjectTaskRepo(opts.Pool)
	}
	if opts.TaskService != nil {
		svc.taskService = opts.TaskService
	} else {
		taskService, taskErr := tasksvc.NewService(tasksvc.Options{
			Pool:     opts.Pool,
			EventBus: opts.Events,
		})
		if taskErr != nil {
			return nil, taskErr
		}
		svc.taskService = taskService
	}
	if opts.Assignments != nil {
		svc.assignments = opts.Assignments
	} else {
		svc.assignments = repo.NewAgentProjectAssignmentRepo(opts.Pool)
	}
	if opts.ModelInvocations != nil {
		svc.modelInvocations = opts.ModelInvocations
	} else {
		svc.modelInvocations = repo.NewModelInvocationRepo(opts.Pool)
	}
	if opts.ChatSessions != nil {
		svc.chatSessions = opts.ChatSessions
	} else {
		svc.chatSessions = repo.NewChatSessionRepo(opts.Pool)
	}
	if opts.Agents != nil {
		svc.agents = opts.Agents
	} else {
		svc.agents = repo.NewAgentRepo(opts.Pool)
	}
	scheduleService, err := scheduling.NewTaskScheduleService(scheduling.TaskScheduleServiceOptions{
		Schedules:  svc.schedules,
		Events:     svc.events,
		Clock:      svc.clock,
		CronParser: svc.cronParser,
	})
	if err != nil {
		return nil, err
	}
	svc.scheduleService = scheduleService

	return svc, nil
}

func (s *service) Create(ctx context.Context, req CreateProjectRequest) (*Project, error) {
	if req.OrganizationID == uuid.Nil {
		return nil, ErrOrganizationIDRequired
	}

	slug, err := validateSlug(req.Slug)
	if err != nil {
		return nil, err
	}

	resolvedSlug, err := s.resolveCreateSlug(ctx, req.OrganizationID, slug)
	if err != nil {
		return nil, err
	}
	slug = resolvedSlug

	createdByType, createdByID, err := normalizeActor(req.CreatedByType, req.CreatedByID)
	if err != nil {
		return nil, err
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if validation.HasHTMLTag(displayName) {
		return nil, ErrDisplayNameInvalid
	}
	settings := withProjectStaffingDefaults(req.Settings)

	created, err := s.projects.Create(ctx, repo.Project{
		OrganizationID:       req.OrganizationID,
		Slug:                 slug,
		DisplayName:          displayName,
		Description:          req.Description,
		DeliveryMode:         strings.TrimSpace(req.DeliveryMode),
		DeployFlowTemplateID: req.DeployFlowTemplateID,
		Settings:             settings,
		CreatedByType:        createdByType,
		CreatedByID:          createdByID,
	})
	if errors.Is(err, repo.ErrConflict) {
		return nil, ErrSlugTaken
	}
	if err != nil {
		return nil, err
	}
	if _, _, err := EnsureCanonicalRepoBinding(ctx, s.environments, s.dataDir, created); err != nil {
		return nil, err
	}
	if bootstrapErr := s.createBootstrapTaskTree(ctx, created, createdByType, createdByID); bootstrapErr != nil {
		return nil, bootstrapErr
	}

	_ = s.publishEvent(ctx, created.OrganizationID, "project.created", createdByType, actorIDPointer(createdByID), map[string]any{
		"project_id": created.ID,
		"slug":       created.Slug,
	})
	_ = s.publishEvent(ctx, created.OrganizationID, "project.staffing_needed", createdByType, actorIDPointer(createdByID), map[string]any{
		"project_id":                  created.ID,
		"slug":                        created.Slug,
		"requires_human_confirmation": true,
		"pm_required":                 true,
	})

	return &created, nil
}

type bootstrapAgents struct {
	loriID  *uuid.UUID
	frankID *uuid.UUID
}

func (s *service) createBootstrapTaskTree(ctx context.Context, projectRecord repo.Project, createdByType string, createdByID uuid.UUID) error {
	if s.templates == nil || s.nodes == nil || s.tasks == nil {
		return nil
	}

	agents, err := s.resolveBootstrapAgents(ctx, projectRecord.OrganizationID)
	if err != nil {
		return err
	}

	orgID := projectRecord.OrganizationID
	projectID := projectRecord.ID
	template, err := s.templates.Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           bootstrapTemplateSlug,
		DisplayName:    "Bootstrap Governance Gate",
		Description:    "Initial governance bootstrap gate for new projects.",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  createdByType,
		CreatedByID:    createdByID,
	})
	if err != nil {
		return err
	}

	setupActorType := "role"
	var setupActorID *uuid.UUID
	setupMetadata := json.RawMessage(`{"role":"project_manager"}`)
	if agents.loriID != nil && *agents.loriID != uuid.Nil {
		setupActorType = "agent"
		setupActorID = agents.loriID
		setupMetadata = json.RawMessage(`{}`)
	}
	reviewActorType := "role"
	var reviewActorID *uuid.UUID
	reviewMetadata := json.RawMessage(`{"role":"reviewer"}`)
	if agents.frankID != nil && *agents.frankID != uuid.Nil {
		reviewActorType = "agent"
		reviewActorID = agents.frankID
		reviewMetadata = json.RawMessage(`{}`)
	}

	setupNode, err := s.nodes.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Lori Setup",
		NodeType:       "work",
		Position:       1,
		ActorType:      &setupActorType,
		ActorID:        setupActorID,
		Metadata:       setupMetadata,
	})
	if err != nil {
		return err
	}
	reviewNode, err := s.nodes.Create(ctx, repo.FlowNode{
		FlowTemplateID:      template.ID,
		DisplayName:         "Frank Review",
		NodeType:            "review",
		Position:            2,
		ActorType:           &reviewActorType,
		ActorID:             reviewActorID,
		RequiresHumanReview: true,
		Metadata:            reviewMetadata,
	})
	if err != nil {
		return err
	}
	mergeNode, err := s.nodes.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Merge",
		NodeType:       flowpolicy.NodeTypeMerge,
		Position:       3,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		return err
	}

	setupNode.NextNodeID = &reviewNode.ID
	if _, err := s.nodes.Update(ctx, setupNode); err != nil {
		return err
	}
	reviewNode.NextNodeID = &mergeNode.ID
	reviewNode.RejectNodeID = &setupNode.ID
	if _, err := s.nodes.Update(ctx, reviewNode); err != nil {
		return err
	}

	template.StartNodeID = &setupNode.ID
	if _, err := s.templates.Update(ctx, template); err != nil {
		return err
	}

	description := "Project bootstrap gate: the canonical setup task tree must complete and Frank must sign off before other tasks can start."
	rootTaskRecord, err := s.taskService.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:           projectRecord.ID,
		Title:               "Bootstrap governance gate",
		Description:         &description,
		FlowTemplateID:      &template.ID,
		BlocksScope:         bootstrapBlocksScopeAll,
		CreatedByType:       createdByType,
		CreatedByID:         createdByID,
		RequiresHumanReview: boolPtr(false),
		Metadata:            json.RawMessage(`{"bootstrap_gate":true,"bootstrap_tree_root":true}`),
	})
	if err != nil {
		return err
	}
	rootTask := repo.ProjectTask(*rootTaskRecord)

	for idx, spec := range bootstrapSetupTaskSpecs {
		assigneeID := agents.loriID
		if spec.Assignee == "frank" {
			assigneeID = agents.frankID
		}
		taskDescription := spec.Description
		childTaskRecord, err := s.taskService.CreateTask(ctx, tasksvc.CreateTaskRequest{
			ProjectID:           projectRecord.ID,
			Title:               spec.Title,
			Description:         &taskDescription,
			FlowTemplateID:      &template.ID,
			AssignedAgentID:     assigneeID,
			CreatedByType:       createdByType,
			CreatedByID:         createdByID,
			RequiresHumanReview: boolPtr(false),
			Metadata:            bootstrapSetupTaskMetadata(rootTask.ID, spec.Slug, idx+1),
		})
		if err != nil {
			return err
		}
		childTask := repo.ProjectTask(*childTaskRecord)
		rootTask.Metadata = taskdecomp.AppendChildTaskID(rootTask.Metadata, childTask.ID)
	}
	if _, err := s.tasks.UpdateMetadata(ctx, rootTask.ID, rootTask.Metadata); err != nil {
		return err
	}

	return nil
}

func boolPtr(value bool) *bool {
	return &value
}

func bootstrapSetupTaskMetadata(parentTaskID uuid.UUID, slug string, order int) json.RawMessage {
	payload := map[string]any{
		"bootstrap_setup_task":         true,
		"bootstrap_step_slug":          strings.TrimSpace(slug),
		"bootstrap_step_order":         order,
		"decomposition_parent_task_id": parentTaskID.String(),
		"workstream_index":             order + 1,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func (s *service) resolveBootstrapAgents(ctx context.Context, organizationID uuid.UUID) (bootstrapAgents, error) {
	result := bootstrapAgents{}
	if s.agents == nil {
		return result, nil
	}
	agents, err := s.agents.List(ctx, organizationID)
	if err != nil {
		return bootstrapAgents{}, err
	}
	for _, agent := range agents {
		if !agent.IsStarterTrio {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(agent.DisplayName)) {
		case "lori":
			id := agent.ID
			result.loriID = &id
		case "frank":
			id := agent.ID
			result.frankID = &id
		}
	}
	return result, nil
}

func (s *service) Get(ctx context.Context, orgID, projectID uuid.UUID) (*Project, error) {
	if orgID == uuid.Nil {
		return nil, ErrOrganizationIDRequired
	}
	if projectID == uuid.Nil {
		return nil, ErrProjectIDRequired
	}

	project, err := s.projects.GetByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project.OrganizationID != orgID {
		return nil, repo.ErrNotFound
	}
	return &project, nil
}

func (s *service) GetBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*Project, error) {
	if orgID == uuid.Nil {
		return nil, ErrOrganizationIDRequired
	}
	normalized, err := validateSlug(slug)
	if err != nil {
		return nil, err
	}

	project, err := s.projects.GetBySlug(ctx, orgID, normalized)
	if err != nil {
		return nil, err
	}
	return &project, nil
}

func (s *service) List(ctx context.Context, orgID uuid.UUID, filter ProjectFilter) ([]*Project, error) {
	if orgID == uuid.Nil {
		return nil, ErrOrganizationIDRequired
	}

	statusFilter := normalizeProjectStatusFilter(filter.Status)

	projects, err := s.projects.List(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if statusFilter != "active" {
		if listAllRepo, ok := s.projects.(projectRepositoryListAll); ok {
			projects, err = listAllRepo.ListAll(ctx, orgID)
			if err != nil {
				return nil, err
			}
		}
	}

	deliveryModeFilter := strings.TrimSpace(filter.DeliveryMode)
	slugPrefixFilter := strings.TrimSpace(filter.SlugPrefix)

	result := make([]*Project, 0, len(projects))
	for i := range projects {
		item := projects[i]
		if statusFilter != "all" {
			status := strings.ToLower(strings.TrimSpace(item.Status))
			if status == "" {
				status = "active"
			}
			if status != statusFilter {
				continue
			}
		}
		if deliveryModeFilter != "" && item.DeliveryMode != deliveryModeFilter {
			continue
		}
		if slugPrefixFilter != "" && !strings.HasPrefix(item.Slug, slugPrefixFilter) {
			continue
		}
		clone := item
		result = append(result, &clone)
	}

	return result, nil
}

func normalizeProjectStatusFilter(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "active":
		return "active"
	case "archived":
		return "archived"
	case "all":
		return "all"
	default:
		return "active"
	}
}

func (s *service) resolveCreateSlug(ctx context.Context, orgID uuid.UUID, requested string) (string, error) {
	slug := strings.TrimSpace(requested)
	projectRecord, err := s.projects.GetBySlug(ctx, orgID, slug)
	if err == nil {
		status := normalizeProjectStatusFilter(projectRecord.Status)
		if status != "archived" {
			return "", ErrSlugTaken
		}
		projects, listErr := s.listProjectsForSlugResolution(ctx, orgID)
		if listErr != nil {
			return "", listErr
		}
		return nextArchivedSlugCandidate(slug, projects), nil
	}
	if !errors.Is(err, repo.ErrNotFound) {
		return "", err
	}
	return slug, nil
}

func (s *service) listProjectsForSlugResolution(ctx context.Context, orgID uuid.UUID) ([]repo.Project, error) {
	if listAllRepo, ok := s.projects.(projectRepositoryListAll); ok {
		return listAllRepo.ListAll(ctx, orgID)
	}
	return s.projects.List(ctx, orgID)
}

func nextArchivedSlugCandidate(base string, projects []repo.Project) string {
	used := make(map[string]struct{}, len(projects))
	for _, item := range projects {
		used[strings.ToLower(strings.TrimSpace(item.Slug))] = struct{}{}
	}
	for attempt := 2; ; attempt++ {
		candidate := fmt.Sprintf("%s-%d", base, attempt)
		if _, ok := used[strings.ToLower(candidate)]; !ok {
			return candidate
		}
	}
}

func (s *service) Update(ctx context.Context, orgID, projectID uuid.UUID, req UpdateProjectRequest) (*Project, error) {
	current, err := s.Get(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}

	slug := current.Slug
	if req.Slug != nil {
		normalized, slugErr := validateSlug(*req.Slug)
		if slugErr != nil {
			return nil, slugErr
		}
		if normalized != current.Slug {
			if _, getErr := s.projects.GetBySlug(ctx, orgID, normalized); getErr == nil {
				return nil, ErrSlugTaken
			} else if !errors.Is(getErr, repo.ErrNotFound) {
				return nil, getErr
			}
		}
		slug = normalized
	}

	updated := *current
	updated.Slug = slug
	if req.DisplayName != nil {
		trimmed := strings.TrimSpace(*req.DisplayName)
		if validation.HasHTMLTag(trimmed) {
			return nil, ErrDisplayNameInvalid
		}
		updated.DisplayName = trimmed
	}
	if req.Description != nil {
		updated.Description = *req.Description
	}
	if req.DeliveryMode != nil {
		updated.DeliveryMode = strings.TrimSpace(*req.DeliveryMode)
	}
	if req.DeployFlowTemplateID != nil {
		updated.DeployFlowTemplateID = req.DeployFlowTemplateID
	}
	if req.Settings != nil {
		updated.Settings = *req.Settings
	}

	stored, err := s.projects.Update(ctx, updated)
	if errors.Is(err, repo.ErrConflict) {
		return nil, ErrSlugTaken
	}
	if err != nil {
		return nil, err
	}

	updatedByType, updatedByID, _ := normalizeActor(req.UpdatedByType, req.UpdatedByID)
	if updatedByType == "" {
		updatedByType = actorSystem
	}

	_ = s.publishEvent(ctx, stored.OrganizationID, "project.updated", updatedByType, actorIDPointer(updatedByID), map[string]any{
		"project_id": stored.ID,
		"slug":       stored.Slug,
	})

	return &stored, nil
}

func (s *service) Pause(ctx context.Context, orgID, projectID uuid.UUID, req PauseProjectRequest) (*Project, error) {
	current, err := s.Get(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}

	currentPause := projectpause.Parse(current.Settings)
	pausedByType, pausedByID, _ := normalizeActor(req.PausedByType, req.PausedByID)
	if pausedByType == "" {
		pausedByType = actorSystem
	}

	updated := *current
	updated.Settings, err = projectpause.ApplyPause(updated.Settings, req.Reason, req.Metadata, s.clock.Now().UTC(), pausedByType, pausedByID)
	if err != nil {
		return nil, err
	}

	stored, err := s.projects.Update(ctx, updated)
	if err != nil {
		return nil, err
	}

	if !currentPause.IsPaused {
		payload := map[string]any{
			"project_id": stored.ID,
			"slug":       stored.Slug,
		}
		if reason := strings.TrimSpace(req.Reason); reason != "" {
			payload["reason"] = reason
		}
		_ = s.publishEvent(ctx, stored.OrganizationID, "project.paused", pausedByType, actorIDPointer(pausedByID), payload)
	}

	return &stored, nil
}

func (s *service) Resume(ctx context.Context, orgID, projectID uuid.UUID, resumedByType string, resumedByID uuid.UUID) (*Project, error) {
	current, err := s.Get(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}

	currentPause := projectpause.Parse(current.Settings)
	if !currentPause.IsPaused {
		return current, nil
	}

	updated := *current
	updated.Settings, err = projectpause.ClearPause(updated.Settings)
	if err != nil {
		return nil, err
	}

	stored, err := s.projects.Update(ctx, updated)
	if err != nil {
		return nil, err
	}

	actorType, actorID, _ := normalizeActor(resumedByType, resumedByID)
	if actorType == "" {
		actorType = actorSystem
	}
	_ = s.publishEvent(ctx, stored.OrganizationID, "project.resumed", actorType, actorIDPointer(actorID), map[string]any{
		"project_id": stored.ID,
		"slug":       stored.Slug,
	})

	return &stored, nil
}

func (s *service) Delete(ctx context.Context, orgID, projectID uuid.UUID) error {
	project, err := s.Get(ctx, orgID, projectID)
	if err != nil {
		return err
	}

	hasActive, err := s.hasActiveProjectTasks(ctx, project.ID)
	if err != nil {
		return err
	}
	if hasActive {
		return ErrProjectHasActiveTasks
	}

	if err := s.cleanupProjectDeleteReferences(ctx, project.ID); err != nil {
		return err
	}

	if err := s.projects.Delete(ctx, project.ID); err != nil {
		return err
	}

	_ = s.publishEvent(ctx, orgID, "project.deleted", actorSystem, nil, map[string]any{
		"project_id": project.ID,
		"slug":       project.Slug,
	})

	return nil
}

func (s *service) cleanupProjectDeleteReferences(ctx context.Context, projectID uuid.UUID) error {
	if s.modelInvocations != nil {
		if err := s.modelInvocations.ClearProjectReferences(ctx, projectID); err != nil {
			return err
		}
	}
	if s.chatSessions != nil {
		if err := s.chatSessions.DeleteProjectScoped(ctx, projectID); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) Archive(ctx context.Context, orgID, projectID uuid.UUID) (*Project, error) {
	project, err := s.Get(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}

	// Close scoped chat sessions first so archive does not leave an archived project with live chats.
	if s.chatSessions != nil {
		if err := s.chatSessions.CloseProjectScoped(ctx, project.ID); err != nil {
			return nil, err
		}
	}
	if err := s.deadLetterClosedProjectAgentTurnJobs(ctx, project.ID); err != nil {
		return nil, err
	}

	if err := s.projects.Archive(ctx, project.ID); err != nil {
		return nil, err
	}

	project.Status = "archived"

	_ = s.publishEvent(ctx, orgID, "project.archived", actorSystem, nil, map[string]any{
		"project_id": project.ID,
		"slug":       project.Slug,
	})

	return project, nil
}

func (s *service) deadLetterClosedProjectAgentTurnJobs(ctx context.Context, projectID uuid.UUID) error {
	if s.pool == nil || projectID == uuid.Nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    last_error = 'project archived: session closed',
		    updated_at = now()
		WHERE jq.status IN ('pending', 'claimed')
		  AND jq.job_type = 'agent_turn'
		  AND EXISTS (
		    SELECT 1
		    FROM chat_session cs
		    WHERE cs.id = (jq.payload->>'session_id')::uuid
		      AND cs.scope_type IN ('project', 'project_task')
		      AND cs.status IN ('closed', 'archived')
		      AND (
		        (cs.scope_type = 'project' AND cs.scope_id = $1)
		        OR EXISTS (
		          SELECT 1
		          FROM project_task pt
		          WHERE pt.id = cs.scope_id
		            AND pt.project_id = $1
		        )
		      )
		  )
	`, projectID)
	if err != nil {
		return fmt.Errorf("dead-letter closed project agent_turn jobs: %w", err)
	}
	return nil
}

func (s *service) CreateFlowTemplate(ctx context.Context, req CreateFlowTemplateRequest) (*FlowTemplate, error) {
	slug, err := validateSlug(req.Slug)
	if err != nil {
		return nil, err
	}
	if req.StartNodeID != nil && *req.StartNodeID != uuid.Nil {
		startNode, startErr := s.nodes.GetByID(ctx, *req.StartNodeID)
		if startErr != nil {
			return nil, startErr
		}
		if err := s.validateFlowTemplateReviewCoverage(ctx, startNode.FlowTemplateID, req.StartNodeID, nil, nil); err != nil {
			return nil, err
		}
	}
	createdByType, createdByID, err := normalizeActor(req.CreatedByType, req.CreatedByID)
	if err != nil {
		return nil, err
	}
	templateName := strings.TrimSpace(req.DisplayName)
	if validation.HasHTMLTag(templateName) {
		return nil, ErrDisplayNameInvalid
	}

	created, err := s.templates.Create(ctx, repo.FlowTemplate{
		OrganizationID: req.OrganizationID,
		ProjectID:      req.ProjectID,
		Slug:           slug,
		DisplayName:    templateName,
		Description:    req.Description,
		StartNodeID:    req.StartNodeID,
		IsSystem:       req.IsSystem,
		CreatedByType:  createdByType,
		CreatedByID:    createdByID,
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (s *service) GetFlowTemplate(ctx context.Context, orgID, templateID uuid.UUID) (*FlowTemplate, error) {
	if templateID == uuid.Nil {
		return nil, ErrTemplateIDRequired
	}

	template, err := s.templates.GetByID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if template.OrganizationID != nil && *template.OrganizationID != orgID {
		return nil, repo.ErrNotFound
	}
	return &template, nil
}

func (s *service) ListFlowTemplates(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID) ([]*FlowTemplate, error) {
	if orgID == uuid.Nil {
		return nil, ErrOrganizationIDRequired
	}

	result := make([]*FlowTemplate, 0)
	appendTemplates := func(items []repo.FlowTemplate) {
		for i := range items {
			item := items[i]
			clone := item
			result = append(result, &clone)
		}
	}

	if projectID != nil {
		items, err := s.templates.ListCurrent(ctx, &orgID, projectID)
		if err != nil {
			return nil, err
		}
		appendTemplates(items)
		return result, nil
	}

	orgTemplates, err := s.templates.ListCurrent(ctx, &orgID, nil)
	if err != nil {
		return nil, err
	}
	appendTemplates(orgTemplates)

	systemTemplates, err := s.templates.ListCurrent(ctx, nil, nil)
	if err != nil {
		return nil, err
	}
	appendTemplates(systemTemplates)

	return result, nil
}

func (s *service) UpdateFlowTemplate(ctx context.Context, orgID, templateID uuid.UUID, req UpdateFlowTemplateRequest) (*FlowTemplate, error) {
	template, err := s.GetFlowTemplate(ctx, orgID, templateID)
	if err != nil {
		return nil, err
	}

	updatedByType, updatedByID, err := normalizeActor(req.UpdatedByType, req.UpdatedByID)
	if err != nil {
		return nil, err
	}

	if req.StartNodeID != nil {
		node, nodeErr := s.nodes.GetByID(ctx, *req.StartNodeID)
		if nodeErr != nil {
			return nil, nodeErr
		}
		if node.FlowTemplateID != template.ID {
			return nil, ErrFlowNodeTemplateMismatch
		}
	}

	inUse, err := s.hasActiveTemplateTasks(ctx, template.ID)
	if err != nil {
		return nil, err
	}

	next := *template
	if req.Slug != nil {
		normalized, slugErr := validateSlug(*req.Slug)
		if slugErr != nil {
			return nil, slugErr
		}
		next.Slug = normalized
	}
	if req.DisplayName != nil {
		trimmed := strings.TrimSpace(*req.DisplayName)
		if validation.HasHTMLTag(trimmed) {
			return nil, ErrDisplayNameInvalid
		}
		next.DisplayName = trimmed
	}
	if req.Description != nil {
		next.Description = *req.Description
	}
	if req.StartNodeID != nil {
		next.StartNodeID = req.StartNodeID
	}
	if err := s.validateFlowTemplateReviewCoverage(ctx, template.ID, next.StartNodeID, nil, nil); err != nil {
		return nil, err
	}

	if inUse {
		versioned, deprecateErr := s.templates.Deprecate(ctx, template.ID, repo.FlowTemplate{
			Slug:          next.Slug,
			DisplayName:   next.DisplayName,
			Description:   next.Description,
			StartNodeID:   next.StartNodeID,
			CreatedByType: updatedByType,
			CreatedByID:   updatedByID,
		})
		if deprecateErr != nil {
			return nil, deprecateErr
		}
		if template.OrganizationID != nil {
			_ = s.publishEvent(ctx, *template.OrganizationID, "flow_template.version_created", updatedByType, actorIDPointer(updatedByID), map[string]any{
				"previous_template_id": template.ID,
				"new_template_id":      versioned.ID,
				"slug":                 versioned.Slug,
				"version":              versioned.Version,
			})
		}
		return &versioned, nil
	}

	stored, err := s.templates.Update(ctx, next)
	if err != nil {
		return nil, err
	}
	return &stored, nil
}

func (s *service) AddFlowNode(ctx context.Context, templateID uuid.UUID, req AddFlowNodeRequest) (*FlowNode, error) {
	if templateID == uuid.Nil {
		return nil, ErrTemplateIDRequired
	}
	template, err := s.templates.GetByID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if err := s.validateNodeActor(ctx, template.OrganizationID, req.ActorType, req.ActorID); err != nil {
		return nil, err
	}
	nodeName := strings.TrimSpace(req.DisplayName)
	if validation.HasHTMLTag(nodeName) {
		return nil, ErrDisplayNameInvalid
	}
	nodeSpec, err := flowpolicy.NormalizeNodeSpec(req.NodeType, req.RequiresHumanReview)
	if err != nil {
		return nil, err
	}

	created, err := s.nodes.Create(ctx, repo.FlowNode{
		FlowTemplateID:      templateID,
		DisplayName:         nodeName,
		NodeType:            nodeSpec.NodeType,
		Position:            req.Position,
		ActorType:           trimStringPointer(req.ActorType),
		ActorID:             req.ActorID,
		NextNodeID:          req.NextNodeID,
		RejectNodeID:        req.RejectNodeID,
		MCPTools:            req.MCPTools,
		ToolDomains:         req.ToolDomains,
		RequiresHumanReview: nodeSpec.RequiresHumanReview,
		MaxVisits:           req.MaxVisits,
		Metadata:            req.Metadata,
	})
	if err != nil {
		return nil, err
	}
	if template.StartNodeID != nil && *template.StartNodeID != uuid.Nil {
		if err := s.validateFlowTemplateReviewCoverage(ctx, template.ID, template.StartNodeID, &created, nil); err != nil {
			if rollbackErr := s.nodes.Delete(ctx, created.ID); rollbackErr != nil {
				return nil, fmt.Errorf("%w: rollback add flow node: %v", err, rollbackErr)
			}
			return nil, err
		}
	}
	return &created, nil
}

func (s *service) UpdateFlowNode(ctx context.Context, nodeID uuid.UUID, req UpdateFlowNodeRequest) (*FlowNode, error) {
	if nodeID == uuid.Nil {
		return nil, ErrNodeIDRequired
	}

	current, err := s.nodes.GetByID(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	template, err := s.templates.GetByID(ctx, current.FlowTemplateID)
	if err != nil {
		return nil, err
	}

	next := current
	if req.DisplayName != nil {
		trimmed := strings.TrimSpace(*req.DisplayName)
		if validation.HasHTMLTag(trimmed) {
			return nil, ErrDisplayNameInvalid
		}
		next.DisplayName = trimmed
	}
	if req.RequiresHumanReview != nil {
		next.RequiresHumanReview = *req.RequiresHumanReview
	}
	if req.NodeType != nil {
		nodeSpec, err := flowpolicy.NormalizeNodeSpec(*req.NodeType, next.RequiresHumanReview)
		if err != nil {
			return nil, err
		}
		next.NodeType = nodeSpec.NodeType
		next.RequiresHumanReview = nodeSpec.RequiresHumanReview
	}
	if req.Position != nil {
		next.Position = *req.Position
	}
	if req.ActorType != nil {
		next.ActorType = trimStringPointer(req.ActorType)
	}
	if req.ActorID != nil {
		next.ActorID = req.ActorID
	}
	if req.NextNodeID != nil {
		next.NextNodeID = req.NextNodeID
	}
	if req.RejectNodeID != nil {
		next.RejectNodeID = req.RejectNodeID
	}
	if req.MCPTools != nil {
		next.MCPTools = *req.MCPTools
	}
	if req.ToolDomains != nil {
		next.ToolDomains = *req.ToolDomains
	}
	if req.MaxVisits != nil {
		next.MaxVisits = *req.MaxVisits
	}
	if req.Metadata != nil {
		next.Metadata = *req.Metadata
	}

	if err := s.validateNodeActor(ctx, template.OrganizationID, next.ActorType, next.ActorID); err != nil {
		return nil, err
	}
	if err := s.validateFlowTemplateReviewCoverage(ctx, template.ID, template.StartNodeID, &next, nil); err != nil {
		return nil, err
	}

	updated, err := s.nodes.Update(ctx, next)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *service) RemoveFlowNode(ctx context.Context, nodeID uuid.UUID) error {
	if nodeID == uuid.Nil {
		return ErrNodeIDRequired
	}

	node, err := s.nodes.GetByID(ctx, nodeID)
	if err != nil {
		return err
	}
	template, err := s.templates.GetByID(ctx, node.FlowTemplateID)
	if err != nil {
		return err
	}
	if template.StartNodeID != nil && *template.StartNodeID != uuid.Nil {
		if err := s.validateFlowTemplateReviewCoverage(ctx, template.ID, template.StartNodeID, nil, &nodeID); err != nil {
			return err
		}
	}

	inUse, err := s.hasActiveTemplateTasks(ctx, node.FlowTemplateID)
	if err != nil {
		return err
	}
	if inUse {
		return ErrTemplateInUse
	}

	return s.nodes.Delete(ctx, nodeID)
}

func (s *service) GetFlowNodes(ctx context.Context, templateID uuid.UUID) ([]*FlowNode, error) {
	if templateID == uuid.Nil {
		return nil, ErrTemplateIDRequired
	}

	nodes, err := s.nodes.GetByTemplateOrdered(ctx, templateID)
	if err != nil {
		return nil, err
	}
	result := make([]*FlowNode, 0, len(nodes))
	for i := range nodes {
		node := nodes[i]
		clone := node
		result = append(result, &clone)
	}
	return result, nil
}

func (s *service) CreateSchedule(ctx context.Context, req CreateScheduleRequest) (*TaskSchedule, error) {
	if req.OrganizationID == uuid.Nil {
		return nil, ErrOrganizationIDRequired
	}
	if req.ProjectID == uuid.Nil {
		return nil, ErrProjectIDRequired
	}
	if req.FlowTemplateID == uuid.Nil {
		return nil, ErrTemplateIDRequired
	}

	cronExpression := strings.TrimSpace(req.CronExpression)
	nextFireAt, err := s.computeNextFireAt(cronExpression)
	if err != nil {
		return nil, err
	}

	createdByType, createdByID, err := normalizeActor(req.CreatedByType, req.CreatedByID)
	if err != nil {
		return nil, err
	}
	scheduleName := strings.TrimSpace(req.DisplayName)
	if validation.HasHTMLTag(scheduleName) {
		return nil, ErrDisplayNameInvalid
	}

	created, err := s.schedules.Create(ctx, repo.TaskSchedule{
		OrganizationID: req.OrganizationID,
		ProjectID:      req.ProjectID,
		FlowTemplateID: req.FlowTemplateID,
		DisplayName:    scheduleName,
		CronExpression: cronExpression,
		OverlapPolicy:  strings.TrimSpace(req.OverlapPolicy),
		MaxDurationMS:  req.MaxDurationMS,
		IsEnabled:      req.IsEnabled,
		NextFireAt:     &nextFireAt,
		CreatedByType:  createdByType,
		CreatedByID:    createdByID,
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (s *service) GetSchedule(ctx context.Context, scheduleID uuid.UUID) (*TaskSchedule, error) {
	if scheduleID == uuid.Nil {
		return nil, ErrScheduleIDRequired
	}

	schedule, err := s.schedules.GetByID(ctx, scheduleID)
	if err != nil {
		return nil, err
	}
	return &schedule, nil
}

func (s *service) ListSchedules(ctx context.Context, projectID uuid.UUID) ([]*TaskSchedule, error) {
	if projectID == uuid.Nil {
		return nil, ErrProjectIDRequired
	}

	schedules, err := s.schedules.List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	result := make([]*TaskSchedule, 0, len(schedules))
	for i := range schedules {
		schedule := schedules[i]
		clone := schedule
		result = append(result, &clone)
	}
	return result, nil
}

func (s *service) UpdateSchedule(ctx context.Context, scheduleID uuid.UUID, req UpdateScheduleRequest) (*TaskSchedule, error) {
	if scheduleID == uuid.Nil {
		return nil, ErrScheduleIDRequired
	}

	current, err := s.schedules.GetByID(ctx, scheduleID)
	if err != nil {
		return nil, err
	}

	next := current
	if req.FlowTemplateID != nil {
		next.FlowTemplateID = *req.FlowTemplateID
	}
	if req.DisplayName != nil {
		trimmed := strings.TrimSpace(*req.DisplayName)
		if validation.HasHTMLTag(trimmed) {
			return nil, ErrDisplayNameInvalid
		}
		next.DisplayName = trimmed
	}
	if req.CronExpression != nil {
		expression := strings.TrimSpace(*req.CronExpression)
		next.CronExpression = expression
		nextFireAt, nextErr := s.computeNextFireAt(expression)
		if nextErr != nil {
			return nil, nextErr
		}
		next.NextFireAt = &nextFireAt
	}
	if req.OverlapPolicy != nil {
		next.OverlapPolicy = strings.TrimSpace(*req.OverlapPolicy)
	}
	if req.MaxDurationMS != nil {
		next.MaxDurationMS = req.MaxDurationMS
	}
	if req.IsEnabled != nil {
		next.IsEnabled = *req.IsEnabled
	}

	updated, err := s.schedules.Update(ctx, next)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *service) DeleteSchedule(ctx context.Context, scheduleID uuid.UUID) error {
	if scheduleID == uuid.Nil {
		return ErrScheduleIDRequired
	}
	return s.schedules.Delete(ctx, scheduleID)
}

func (s *service) EnableSchedule(ctx context.Context, scheduleID uuid.UUID) (*TaskSchedule, error) {
	if scheduleID == uuid.Nil {
		return nil, ErrScheduleIDRequired
	}

	updated, err := s.scheduleService.Enable(ctx, scheduleID)
	if err != nil {
		if errors.Is(err, scheduling.ErrInvalidCronExpression) {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCronExpression, err)
		}
		return nil, err
	}
	return &updated, nil
}

func (s *service) DisableSchedule(ctx context.Context, scheduleID uuid.UUID) (*TaskSchedule, error) {
	if scheduleID == uuid.Nil {
		return nil, ErrScheduleIDRequired
	}

	updated, err := s.scheduleService.Disable(ctx, scheduleID)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func validateSlug(value string) (string, error) {
	slug := strings.TrimSpace(value)
	if slug == "" || len(slug) > 64 {
		return "", ErrInvalidSlug
	}
	if strings.HasPrefix(slug, "-") {
		return "", ErrInvalidSlug
	}
	if !slugPattern.MatchString(slug) {
		return "", ErrInvalidSlug
	}
	return slug, nil
}

func normalizeActor(actorType string, actorID uuid.UUID) (string, uuid.UUID, error) {
	normalizedType := strings.TrimSpace(actorType)
	if normalizedType == "" {
		return actorSystem, uuid.Nil, nil
	}
	switch normalizedType {
	case actorHuman, actorAgent:
		if actorID == uuid.Nil {
			return "", uuid.Nil, fmt.Errorf("created_by_id is required for %s", normalizedType)
		}
		return normalizedType, actorID, nil
	case actorSystem:
		return actorSystem, uuid.Nil, nil
	default:
		return "", uuid.Nil, fmt.Errorf("invalid actor type: %s", normalizedType)
	}
}

func (s *service) computeNextFireAt(expression string) (time.Time, error) {
	schedule, err := s.cronParser.ParseExpression(expression)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %v", ErrInvalidCronExpression, err)
	}
	return schedule.Next(s.clock.Now().UTC()), nil
}

func (s *service) hasActiveProjectTasks(ctx context.Context, projectID uuid.UUID) (bool, error) {
	if s.activeTaskChecker != nil {
		return s.activeTaskChecker.HasActiveProjectTasks(ctx, projectID)
	}

	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM project_task
			WHERE project_id = $1
			  AND work_status NOT IN ('done', 'cancelled')
			LIMIT 1
		)
	`, projectID).Scan(&exists)
	if err != nil {
		if isUndefinedTable(err) {
			return false, nil
		}
		return false, err
	}
	return exists, nil
}

func (s *service) hasActiveTemplateTasks(ctx context.Context, templateID uuid.UUID) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM project_task
			WHERE flow_template_id = $1
			  AND work_status NOT IN ('done', 'cancelled')
			LIMIT 1
		)
	`, templateID).Scan(&exists)
	if err != nil {
		if isUndefinedTable(err) {
			return false, nil
		}
		return false, err
	}
	return exists, nil
}

func (s *service) validateNodeActor(ctx context.Context, orgID *uuid.UUID, actorType *string, actorID *uuid.UUID) error {
	if actorType == nil {
		return nil
	}
	trimmedType := strings.TrimSpace(*actorType)
	if trimmedType != "agent" {
		return nil
	}
	if actorID == nil || *actorID == uuid.Nil {
		return ErrAgentNotFound
	}

	agent, err := s.agents.GetByID(ctx, *actorID)
	if errors.Is(err, repo.ErrNotFound) {
		return ErrAgentNotFound
	}
	if err != nil {
		return err
	}
	if orgID != nil && agent.OrganizationID != *orgID {
		return ErrAgentNotFound
	}
	return nil
}

func (s *service) publishEvent(ctx context.Context, orgID uuid.UUID, eventType, actorType string, actorID *uuid.UUID, payload map[string]any) error {
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.events.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: orgID,
		EventType:      eventType,
		ActorType:      actorType,
		ActorID:        actorID,
		Payload:        encodedPayload,
	})
}

func actorIDPointer(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P01"
	}
	return false
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

func withProjectStaffingDefaults(settings json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(settings))
	if trimmed == "" {
		return json.RawMessage(`{"requires_pm_assignment_before_queue":true}`)
	}

	var payload map[string]any
	if err := json.Unmarshal(settings, &payload); err != nil {
		return settings
	}
	if payload == nil {
		payload = make(map[string]any)
	}
	payload["requires_pm_assignment_before_queue"] = true
	encoded, err := json.Marshal(payload)
	if err != nil {
		return settings
	}
	return encoded
}

func (s *service) validateFlowTemplateReviewCoverage(ctx context.Context, templateID uuid.UUID, startNodeID *uuid.UUID, overrideNode *repo.FlowNode, removedNodeID *uuid.UUID) error {
	if startNodeID == nil || *startNodeID == uuid.Nil {
		return nil
	}

	nodes, err := s.nodes.GetByTemplateOrdered(ctx, templateID)
	if err != nil {
		return err
	}

	if overrideNode != nil {
		replaced := false
		for i := range nodes {
			if nodes[i].ID == overrideNode.ID {
				nodes[i] = *overrideNode
				replaced = true
				break
			}
		}
		if !replaced {
			nodes = append(nodes, *overrideNode)
		}
	}

	if removedNodeID != nil && *removedNodeID != uuid.Nil {
		filtered := make([]repo.FlowNode, 0, len(nodes))
		for i := range nodes {
			if nodes[i].ID == *removedNodeID {
				continue
			}
			node := nodes[i]
			if node.NextNodeID != nil && *node.NextNodeID == *removedNodeID {
				node.NextNodeID = nil
			}
			if node.RejectNodeID != nil && *node.RejectNodeID == *removedNodeID {
				node.RejectNodeID = nil
			}
			filtered = append(filtered, node)
		}
		nodes = filtered
	}

	return ValidateExecutableFlowTemplate(startNodeID, nodes)
}

func ValidateExecutableFlowNodes(nodes []repo.FlowNode) error {
	return mapFlowValidationError(flowpolicy.ValidateExecutableFlowNodes(nodes))
}

func ValidateExecutableFlowTemplate(startNodeID *uuid.UUID, nodes []repo.FlowNode) error {
	return mapFlowValidationError(flowpolicy.ValidateExecutableFlowTemplate(startNodeID, nodes))
}

func validateFlowTemplateReviewPath(startNodeID uuid.UUID, nodes []repo.FlowNode) error {
	return ValidateExecutableFlowTemplate(&startNodeID, nodes)
}

func mapFlowValidationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, flowpolicy.ErrFlowNodeTemplateMismatch):
		return ErrFlowNodeTemplateMismatch
	case errors.Is(err, flowpolicy.ErrExecutableFlowPath):
		return ErrFlowTemplateReviewPath
	default:
		return err
	}
}
