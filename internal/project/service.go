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
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/scheduling"
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
	ErrOrganizationIDRequired   = errors.New("organization_id is required")
	ErrProjectIDRequired        = errors.New("project_id is required")
	ErrTemplateIDRequired       = errors.New("template_id is required")
	ErrNodeIDRequired           = errors.New("node_id is required")
	ErrScheduleIDRequired       = errors.New("schedule_id is required")
)

const (
	actorHuman  = "human_user"
	actorAgent  = "agent"
	actorSystem = "system"
)

type Project = repo.Project
type FlowTemplate = repo.FlowTemplate
type FlowNode = repo.FlowNode
type TaskSchedule = repo.TaskSchedule

type ProjectFilter struct {
	DeliveryMode string
	SlugPrefix   string
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
	Delete(ctx context.Context, id uuid.UUID) error
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

type agentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type Options struct {
	Pool *pgxpool.Pool

	Projects  projectRepository
	Templates flowTemplateRepository
	Nodes     flowNodeRepository
	Schedules taskScheduleRepository
	Agents    agentRepository

	Events eventbus.EventBus
	Clock  clock.Clock
}

type service struct {
	pool *pgxpool.Pool

	projects  projectRepository
	templates flowTemplateRepository
	nodes     flowNodeRepository
	schedules taskScheduleRepository
	agents    agentRepository

	events eventbus.EventBus
	clock  clock.Clock

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

	if _, err := s.projects.GetBySlug(ctx, req.OrganizationID, slug); err == nil {
		return nil, ErrSlugTaken
	} else if !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}

	createdByType, createdByID, err := normalizeActor(req.CreatedByType, req.CreatedByID)
	if err != nil {
		return nil, err
	}

	created, err := s.projects.Create(ctx, repo.Project{
		OrganizationID:       req.OrganizationID,
		Slug:                 slug,
		DisplayName:          strings.TrimSpace(req.DisplayName),
		Description:          req.Description,
		DeliveryMode:         strings.TrimSpace(req.DeliveryMode),
		DeployFlowTemplateID: req.DeployFlowTemplateID,
		Settings:             req.Settings,
		CreatedByType:        createdByType,
		CreatedByID:          createdByID,
	})
	if errors.Is(err, repo.ErrConflict) {
		return nil, ErrSlugTaken
	}
	if err != nil {
		return nil, err
	}

	_ = s.publishEvent(ctx, created.OrganizationID, "project.created", createdByType, actorIDPointer(createdByID), map[string]any{
		"project_id": created.ID,
		"slug":       created.Slug,
	})

	return &created, nil
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

	projects, err := s.projects.List(ctx, orgID)
	if err != nil {
		return nil, err
	}

	deliveryModeFilter := strings.TrimSpace(filter.DeliveryMode)
	slugPrefixFilter := strings.TrimSpace(filter.SlugPrefix)

	result := make([]*Project, 0, len(projects))
	for i := range projects {
		item := projects[i]
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
		updated.DisplayName = strings.TrimSpace(*req.DisplayName)
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

	if err := s.projects.Delete(ctx, project.ID); err != nil {
		return err
	}

	_ = s.publishEvent(ctx, orgID, "project.deleted", actorSystem, nil, map[string]any{
		"project_id": project.ID,
		"slug":       project.Slug,
	})

	return nil
}

func (s *service) CreateFlowTemplate(ctx context.Context, req CreateFlowTemplateRequest) (*FlowTemplate, error) {
	slug, err := validateSlug(req.Slug)
	if err != nil {
		return nil, err
	}
	createdByType, createdByID, err := normalizeActor(req.CreatedByType, req.CreatedByID)
	if err != nil {
		return nil, err
	}

	created, err := s.templates.Create(ctx, repo.FlowTemplate{
		OrganizationID: req.OrganizationID,
		ProjectID:      req.ProjectID,
		Slug:           slug,
		DisplayName:    strings.TrimSpace(req.DisplayName),
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
	if template.IsSystem {
		return nil, ErrSystemTemplateProtected
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
		next.DisplayName = strings.TrimSpace(*req.DisplayName)
	}
	if req.Description != nil {
		next.Description = *req.Description
	}
	if req.StartNodeID != nil {
		next.StartNodeID = req.StartNodeID
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

	created, err := s.nodes.Create(ctx, repo.FlowNode{
		FlowTemplateID:      templateID,
		DisplayName:         strings.TrimSpace(req.DisplayName),
		NodeType:            strings.TrimSpace(req.NodeType),
		Position:            req.Position,
		ActorType:           trimStringPointer(req.ActorType),
		ActorID:             req.ActorID,
		NextNodeID:          req.NextNodeID,
		RejectNodeID:        req.RejectNodeID,
		MCPTools:            req.MCPTools,
		ToolDomains:         req.ToolDomains,
		RequiresHumanReview: req.RequiresHumanReview,
		MaxVisits:           req.MaxVisits,
		Metadata:            req.Metadata,
	})
	if err != nil {
		return nil, err
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
		next.DisplayName = strings.TrimSpace(*req.DisplayName)
	}
	if req.NodeType != nil {
		next.NodeType = strings.TrimSpace(*req.NodeType)
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
	if req.RequiresHumanReview != nil {
		next.RequiresHumanReview = *req.RequiresHumanReview
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

	created, err := s.schedules.Create(ctx, repo.TaskSchedule{
		OrganizationID: req.OrganizationID,
		ProjectID:      req.ProjectID,
		FlowTemplateID: req.FlowTemplateID,
		DisplayName:    strings.TrimSpace(req.DisplayName),
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
		next.DisplayName = strings.TrimSpace(*req.DisplayName)
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
