package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/agent"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/audit"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var (
	allowedAgentClasses = map[string]struct{}{
		"staff": {},
		"temp":  {},
	}
	allowedAgentTypes = map[string]struct{}{
		"pm":       {},
		"worker":   {},
		"reviewer": {},
		"general":  {},
	}
	allowedAgentLifecycleStatuses = map[string]struct{}{
		"draft":     {},
		"active":    {},
		"paused":    {},
		"retired":   {},
		"cancelled": {},
		"expired":   {},
		"promoted":  {},
	}
	allowedBudgetPeriods = map[string]struct{}{
		"daily":   {},
		"weekly":  {},
		"monthly": {},
	}
	allowedTemplateSources = map[string]struct{}{
		"org":      {},
		"promoted": {},
	}
)

type agentTemplateRepository interface {
	List(ctx context.Context, organizationID uuid.UUID, includeSystem bool) ([]repo.AgentProfileTemplate, error)
	Create(ctx context.Context, template repo.AgentProfileTemplate) (repo.AgentProfileTemplate, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.AgentProfileTemplate, error)
}

type agentAssignmentLookupRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type projectAssignmentLookupRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

type skillAssignmentLookupRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Skill, error)
}

type agentProjectAssignmentListRepository interface {
	ListByAgent(ctx context.Context, agentID uuid.UUID) ([]repo.AgentProjectAssignment, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.AgentProjectAssignment, error)
}

type agentSkillAttachmentListRepository interface {
	GetByAgentAndSkill(ctx context.Context, agentID, skillID uuid.UUID) (repo.AgentSkillAttachment, error)
	ListByAgent(ctx context.Context, agentID uuid.UUID) ([]repo.AgentSkillAttachment, error)
}

type toolDefinitionLister interface {
	ListEnabled(ctx context.Context) ([]repo.ToolDefinition, error)
}

type AgentRouteRegistrar struct {
	handlers agentHandlers
}

func NewAgentRouteRegistrar(
	service agent.AgentService,
	templates agentTemplateRepository,
	assignments agent.AssignmentService,
	agents agentAssignmentLookupRepository,
	projects projectAssignmentLookupRepository,
	skills skillAssignmentLookupRepository,
	projectAssignments agentProjectAssignmentListRepository,
	skillAttachments agentSkillAttachmentListRepository,
	toolDefs toolDefinitionLister,
	auditRecorders ...audit.AuditRecorder,
) *AgentRouteRegistrar {
	recorder := audit.NewNoopRecorder()
	if len(auditRecorders) > 0 && auditRecorders[0] != nil {
		recorder = auditRecorders[0]
	}
	handlers := newAgentHandlersWithAssignments(service, templates, assignments, agents, projects, skills, projectAssignments, skillAttachments, toolDefs)
	handlers.auditRecorder = recorder

	return &AgentRouteRegistrar{
		handlers: handlers,
	}
}

func (r *AgentRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.With(middleware.RequireAnyScope(requireReadScope("agents")...)).Get("/agents", r.handlers.listAgents)
	router.With(middleware.RequireAnyScope(requireWriteScope("agents")...)).Post("/agents", r.handlers.createAgent)
	router.With(middleware.RequireAnyScope(requireReadScope("agents")...)).Get("/agents/{id}", r.handlers.getAgent)
	router.With(middleware.RequireAnyScope(requireReadScope("agents")...)).Get("/agents/{id}/config", r.handlers.getAgentConfig)
	router.With(middleware.RequireAnyScope(requireReadScope("agents")...)).Get("/agents/{id}/tools", r.handlers.getAgentTools)
	router.With(middleware.RequireAnyScope(requireReadScope("agents")...)).Get("/tools", r.handlers.listAllTools)

	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("agents")...),
	).Patch("/agents/{id}", r.handlers.updateAgent)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("agents")...),
	).Post("/agents/{id}/pause", r.handlers.pauseAgent)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("agents")...),
	).Post("/agents/{id}/unpause", r.handlers.unpauseAgent)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("agents")...),
	).Post("/agents/{id}/retire", r.handlers.retireAgent)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("agents")...),
	).Post("/agents/{id}/cancel", r.handlers.cancelAgent)

	router.With(middleware.RequireAnyScope(requireReadScope("agents")...)).Get("/agent-templates", r.handlers.listAgentTemplates)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("agents")...),
	).Post("/agent-templates", r.handlers.createAgentTemplate)
	router.With(middleware.RequireAnyScope(requireReadScope("agents")...)).Get("/agent-templates/{id}", r.handlers.getAgentTemplate)

	router.With(middleware.RequireAnyScope(requireReadScope("projects")...)).Get("/projects/{id}/agents", r.handlers.listProjectAgents)
	router.With(middleware.RequireAnyScope(requireReadScope("agents")...)).Get("/agents/{id}/project-assignments", r.handlers.listAgentProjectAssignments)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("agents")...),
	).Post("/agents/{id}/project-assignments", r.handlers.createAgentProjectAssignment)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("agents")...),
	).Delete("/agents/{id}/project-assignments/{pid}", r.handlers.deleteAgentProjectAssignment)

	router.With(middleware.RequireAnyScope(requireReadScope("projects")...)).Get("/projects/{id}/agents", r.handlers.listProjectAgents)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Post("/projects/{id}/agents", r.handlers.createProjectAgent)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Delete("/projects/{id}/agents/{agent_id}", r.handlers.deleteProjectAgent)

	router.With(middleware.RequireAnyScope(requireReadScope("agents")...)).Get("/agents/{id}/skills", r.handlers.listAgentSkills)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("agents")...),
	).Post("/agents/{id}/skills", r.handlers.attachAgentSkill)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("agents")...),
	).Patch("/agents/{id}/skills/{sid}", r.handlers.updateAgentSkillPriority)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("agents")...),
	).Delete("/agents/{id}/skills/{sid}", r.handlers.detachAgentSkill)
}

type agentHandlers struct {
	service            agent.AgentService
	templates          agentTemplateRepository
	assignments        agent.AssignmentService
	agents             agentAssignmentLookupRepository
	projects           projectAssignmentLookupRepository
	skills             skillAssignmentLookupRepository
	projectAssignments agentProjectAssignmentListRepository
	skillAttachments   agentSkillAttachmentListRepository
	toolDefs           toolDefinitionLister
	auditRecorder      audit.AuditRecorder
	totals             *agentTotalCache
}

func newAgentHandlers(service agent.AgentService, templates agentTemplateRepository) agentHandlers {
	return agentHandlers{
		service:       service,
		templates:     templates,
		auditRecorder: audit.NewNoopRecorder(),
		totals:        newAgentTotalCache(60 * time.Second),
	}
}

func newAgentHandlersWithAssignments(
	service agent.AgentService,
	templates agentTemplateRepository,
	assignments agent.AssignmentService,
	agents agentAssignmentLookupRepository,
	projects projectAssignmentLookupRepository,
	skills skillAssignmentLookupRepository,
	projectAssignments agentProjectAssignmentListRepository,
	skillAttachments agentSkillAttachmentListRepository,
	toolDefs toolDefinitionLister,
) agentHandlers {
	return agentHandlers{
		service:            service,
		templates:          templates,
		assignments:        assignments,
		agents:             agents,
		projects:           projects,
		skills:             skills,
		projectAssignments: projectAssignments,
		skillAttachments:   skillAttachments,
		toolDefs:           toolDefs,
		auditRecorder:      audit.NewNoopRecorder(),
		totals:             newAgentTotalCache(60 * time.Second),
	}
}

type createAgentRequest struct {
	DisplayName           string     `json:"display_name"`
	AgentClass            string     `json:"agent_class"`
	AgentType             string     `json:"agent_type"`
	SystemPrompt          string     `json:"system_prompt"`
	OperatorInstructions  string     `json:"operator_instructions"`
	DefaultModelProfileID *string    `json:"default_model_profile_id"`
	ToolAllowList         []string   `json:"tool_allow_list"`
	ToolDenyList          []string   `json:"tool_deny_list"`
	MemoryReadScopes      []string   `json:"memory_read_scopes"`
	PrivateMemory         bool       `json:"private_memory"`
	BudgetCapTokens       *int64     `json:"budget_cap_tokens"`
	BudgetPeriod          *string    `json:"budget_period"`
	TempProjectID         *uuid.UUID `json:"temp_project_id"`
	TempTTLSeconds        *int32     `json:"temp_ttl_seconds"`
}

type updateAgentRequest struct {
	DisplayName           *string   `json:"display_name"`
	AgentType             *string   `json:"agent_type"`
	SystemPrompt          *string   `json:"system_prompt"`
	OperatorInstructions  *string   `json:"operator_instructions"`
	DefaultModelProfileID *string   `json:"default_model_profile_id"`
	ToolAllowList         *[]string `json:"tool_allow_list"`
	ToolDenyList          *[]string `json:"tool_deny_list"`
	MemoryReadScopes      *[]string `json:"memory_read_scopes"`
	PrivateMemory         *bool     `json:"private_memory"`
	BudgetCapTokens       *int64    `json:"budget_cap_tokens"`
	BudgetPeriod          *string   `json:"budget_period"`
}

type createAgentTemplateRequest struct {
	DisplayName           string          `json:"display_name"`
	Source                string          `json:"source"`
	SourceAgentID         *uuid.UUID      `json:"source_agent_id"`
	SystemPrompt          string          `json:"system_prompt"`
	OperatorInstructions  string          `json:"operator_instructions"`
	AgentType             string          `json:"agent_type"`
	DefaultModelProfileID *string         `json:"default_model_profile_id"`
	ToolAllowList         []string        `json:"tool_allow_list"`
	ToolDenyList          []string        `json:"tool_deny_list"`
	MemoryReadScopes      []string        `json:"memory_read_scopes"`
	PrivateMemory         bool            `json:"private_memory"`
	Metadata              json.RawMessage `json:"metadata"`
}

type createProjectAssignmentRequest struct {
	ProjectID uuid.UUID `json:"project_id"`
	Role      string    `json:"role"`
}

type createProjectAgentRequest struct {
	AgentID uuid.UUID `json:"agent_id"`
	Role    string    `json:"role"`
}

type createAgentSkillRequest struct {
	SkillID  uuid.UUID `json:"skill_id"`
	Priority *int      `json:"priority"`
}

type updateAgentSkillPriorityRequest struct {
	Priority int `json:"priority"`
}

type agentResponse struct {
	ID                    uuid.UUID  `json:"id"`
	OrganizationID        uuid.UUID  `json:"organization_id"`
	DisplayName           string     `json:"display_name"`
	AgentClass            string     `json:"agent_class"`
	LifecycleStatus       string     `json:"lifecycle_status"`
	SystemPrompt          string     `json:"system_prompt"`
	OperatorInstructions  string     `json:"operator_instructions"`
	AgentType             string     `json:"agent_type"`
	IsStarterTrio         bool       `json:"is_starter_trio"`
	PrivateMemory         bool       `json:"private_memory"`
	MemoryReadScopes      []string   `json:"memory_read_scopes"`
	ToolAllowList         []string   `json:"tool_allow_list"`
	ToolDenyList          []string   `json:"tool_deny_list"`
	DefaultModelProfileID *string    `json:"default_model_profile_id"`
	BudgetCapTokens       *int64     `json:"budget_cap_tokens"`
	BudgetPeriod          *string    `json:"budget_period"`
	TempProjectID         *uuid.UUID `json:"temp_project_id"`
	TempTTLSeconds        *int32     `json:"temp_ttl_seconds"`
	TempExpiresAt         *time.Time `json:"temp_expires_at"`
	PromotedToAgentID     *uuid.UUID `json:"promoted_to_agent_id"`
	CreatedByType         string     `json:"created_by_type"`
	CreatedByID           uuid.UUID  `json:"created_by_id"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

type agentTemplateResponse struct {
	ID                    uuid.UUID       `json:"id"`
	OrganizationID        *uuid.UUID      `json:"organization_id"`
	DisplayName           string          `json:"display_name"`
	Source                string          `json:"source"`
	SourceAgentID         *uuid.UUID      `json:"source_agent_id"`
	SystemPrompt          string          `json:"system_prompt"`
	OperatorInstructions  string          `json:"operator_instructions"`
	AgentType             string          `json:"agent_type"`
	DefaultModelProfileID *string         `json:"default_model_profile_id"`
	ToolAllowList         []string        `json:"tool_allow_list"`
	ToolDenyList          []string        `json:"tool_deny_list"`
	MemoryReadScopes      []string        `json:"memory_read_scopes"`
	PrivateMemory         bool            `json:"private_memory"`
	Metadata              json.RawMessage `json:"metadata"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

type projectAssignmentResponse struct {
	ID         uuid.UUID `json:"id"`
	AgentID    uuid.UUID `json:"agent_id"`
	ProjectID  uuid.UUID `json:"project_id"`
	Role       string    `json:"role"`
	IsActive   bool      `json:"is_active"`
	AssignedAt time.Time `json:"assigned_at"`
}

type projectAssignmentListItemResponse struct {
	ID          uuid.UUID `json:"id"`
	ProjectID   uuid.UUID `json:"project_id"`
	ProjectSlug string    `json:"project_slug"`
	Role        string    `json:"role"`
	IsActive    bool      `json:"is_active"`
	AssignedAt  time.Time `json:"assigned_at"`
}

type projectAgentListItemResponse struct {
	ID               uuid.UUID `json:"id"`
	AgentID          uuid.UUID `json:"agent_id"`
	AgentDisplayName string    `json:"agent_display_name"`
	Role             string    `json:"role"`
	IsActive         bool      `json:"is_active"`
	AssignedAt       time.Time `json:"assigned_at"`
}

type agentSkillAttachmentResponse struct {
	ID         uuid.UUID `json:"id"`
	AgentID    uuid.UUID `json:"agent_id"`
	SkillID    uuid.UUID `json:"skill_id"`
	Priority   int       `json:"priority"`
	IsActive   bool      `json:"is_active"`
	AttachedAt time.Time `json:"attached_at"`
}

type agentSkillAttachmentListItemResponse struct {
	ID         uuid.UUID `json:"id"`
	SkillID    uuid.UUID `json:"skill_id"`
	SkillName  string    `json:"skill_name"`
	Priority   int       `json:"priority"`
	AttachedAt time.Time `json:"attached_at"`
}

type priorityCursor struct {
	P  int    `json:"p"`
	ID string `json:"id"`
}

func (h agentHandlers) listAgents(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	query := r.URL.Query()
	agentClass := strings.ToLower(strings.TrimSpace(query.Get("agent_class")))
	if agentClass != "" {
		if _, ok := allowedAgentClasses[agentClass]; !ok {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "agent_class must be staff or temp")
			return
		}
	}

	lifecycleStatuses, err := parseLifecycleStatuses(query.Get("lifecycle_status"))
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error())
		return
	}

	agentType := strings.ToLower(strings.TrimSpace(query.Get("agent_type")))
	if agentType == "" {
		agentType = strings.ToLower(strings.TrimSpace(query.Get("role")))
	}
	if agentType != "" && !isAllowedAgentType(agentType) {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "agent_type must be one of pm, worker, reviewer, general")
		return
	}
	nameFilter := strings.ToLower(strings.TrimSpace(query.Get("name")))
	if nameFilter == "" {
		nameFilter = strings.ToLower(strings.TrimSpace(query.Get("display_name")))
	}

	params := api.ParsePaginationParams(query)
	var cursorAt time.Time
	var cursorID uuid.UUID
	if params.Cursor != "" {
		decodedAt, decodedID, decodeErr := (api.PaginationDecoder{}).Decode(params.Cursor)
		if decodeErr != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
			return
		}
		cursorAt = decodedAt
		cursorID = decodedID
	}

	filter := agent.AgentFilter{AgentClass: agentClass}
	if len(lifecycleStatuses) == 1 {
		filter.LifecycleStatus = lifecycleStatuses[0]
	}

	items, err := h.service.List(r.Context(), principal.OrganizationID, filter)
	if err != nil {
		status, code, message := mapAgentError(err)
		responder.Error(w, status, code, message)
		return
	}

	filtered := make([]*agent.Agent, 0, len(items))
	statusSet := make(map[string]struct{}, len(lifecycleStatuses))
	for _, status := range lifecycleStatuses {
		statusSet[status] = struct{}{}
	}

	for _, item := range items {
		if item == nil {
			continue
		}
		if len(statusSet) > 0 {
			if _, ok := statusSet[strings.ToLower(strings.TrimSpace(item.LifecycleStatus))]; !ok {
				continue
			}
		}
		if agentType != "" && strings.ToLower(strings.TrimSpace(item.AgentType)) != agentType {
			continue
		}
		if nameFilter != "" && !strings.Contains(strings.ToLower(strings.TrimSpace(item.DisplayName)), nameFilter) {
			continue
		}
		filtered = append(filtered, item)
	}

	sort.Slice(filtered, func(i, j int) bool {
		left := filtered[i]
		right := filtered[j]
		if left.CreatedAt.Equal(right.CreatedAt) {
			return left.ID.String() < right.ID.String()
		}
		return left.CreatedAt.Before(right.CreatedAt)
	})

	startIdx := 0
	if params.Cursor != "" {
		startIdx = len(filtered)
		for i, item := range filtered {
			if item.CreatedAt.After(cursorAt) || (item.CreatedAt.Equal(cursorAt) && item.ID.String() > cursorID.String()) {
				startIdx = i
				break
			}
		}
	}

	remaining := filtered[startIdx:]
	page := remaining
	var nextCursor *string
	if len(page) > params.Limit {
		page = page[:params.Limit]
		encoded := (api.PaginationEncoder{}).Encode(page[len(page)-1].CreatedAt, page[len(page)-1].ID)
		nextCursor = &encoded
	}

	totalKey := cacheKeyForListTotals(principal.OrganizationID, agentClass, lifecycleStatuses, agentType)
	total := h.totals.GetOrSet(totalKey, len(filtered))

	payload := make([]agentResponse, 0, len(page))
	for _, item := range page {
		payload = append(payload, toAgentResponse(item))
	}

	responder.JSONList(w, http.StatusOK, payload, api.PaginationMeta{
		NextCursor: nextCursor,
		Limit:      params.Limit,
		Total:      &total,
	})
}

func (h agentHandlers) createAgent(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	var req createAgentRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	if strings.TrimSpace(req.DisplayName) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "display_name is required")
		return
	}

	agentClass := strings.ToLower(strings.TrimSpace(req.AgentClass))
	if _, ok := allowedAgentClasses[agentClass]; !ok {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "agent_class must be staff or temp")
		return
	}

	agentType := strings.ToLower(strings.TrimSpace(req.AgentType))
	if !isAllowedAgentType(agentType) {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "agent_type must be one of pm, worker, reviewer, general")
		return
	}

	budgetPeriod, err := normalizeBudgetPeriod(req.BudgetPeriod)
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error())
		return
	}

	modelProfileID := normalizeOptionalString(req.DefaultModelProfileID)

	if agentClass == "staff" && !isAdminRole(principal.Role) {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}
	if agentClass == "temp" && !isAdminOrMemberRole(principal.Role) {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	if agentClass == "temp" {
		tempProjectID := uuid.Nil
		if req.TempProjectID != nil {
			tempProjectID = *req.TempProjectID
		}

		created, createErr := h.service.CreateTemp(r.Context(), principal.OrganizationID, agent.CreateTempAgentRequest{
			DisplayName:           strings.TrimSpace(req.DisplayName),
			SystemPrompt:          req.SystemPrompt,
			OperatorInstructions:  req.OperatorInstructions,
			AgentType:             agentType,
			PrivateMemory:         req.PrivateMemory,
			MemoryReadScopes:      req.MemoryReadScopes,
			ToolAllowList:         req.ToolAllowList,
			ToolDenyList:          req.ToolDenyList,
			DefaultModelProfileID: modelProfileID,
			BudgetCapTokens:       req.BudgetCapTokens,
			BudgetPeriod:          budgetPeriod,
			TempProjectID:         tempProjectID,
			TempTTLSeconds:        req.TempTTLSeconds,
			CreatedByType:         "human_user",
			CreatedByID:           principal.UserID,
		})
		if createErr != nil {
			status, code, message := mapAgentError(createErr)
			responder.Error(w, status, code, message)
			return
		}

		h.recordAuditEvent(r.Context(), audit.Event{
			OrgID:         principal.OrganizationID,
			EventType:     audit.EventAgentCreated,
			PrincipalType: "human",
			PrincipalID:   principal.UserID,
			TargetType:    pointerToStringValue("agent"),
			TargetID:      &created.ID,
			IP:            requestClientIP(r),
			Outcome:       "success",
			Metadata: map[string]any{
				"agent_class":       strings.TrimSpace(created.AgentClass),
				"agent_type":        strings.TrimSpace(created.AgentType),
				"lifecycle_status":  strings.TrimSpace(created.LifecycleStatus),
				"default_model_id":  normalizeOptionalString(created.DefaultModelProfileID),
				"display_name":      strings.TrimSpace(created.DisplayName),
				"private_memory_on": created.PrivateMemory,
			},
		})

		responder.JSON(w, http.StatusCreated, toAgentResponse(created))
		return
	}

	created, createErr := h.service.Create(r.Context(), agent.CreateAgentRequest{
		OrganizationID:        principal.OrganizationID,
		DisplayName:           strings.TrimSpace(req.DisplayName),
		SystemPrompt:          req.SystemPrompt,
		OperatorInstructions:  req.OperatorInstructions,
		AgentType:             agentType,
		PrivateMemory:         req.PrivateMemory,
		MemoryReadScopes:      req.MemoryReadScopes,
		ToolAllowList:         req.ToolAllowList,
		ToolDenyList:          req.ToolDenyList,
		DefaultModelProfileID: modelProfileID,
		BudgetCapTokens:       req.BudgetCapTokens,
		BudgetPeriod:          budgetPeriod,
		CreatedByType:         "human_user",
		CreatedByID:           principal.UserID,
	})
	if createErr != nil {
		status, code, message := mapAgentError(createErr)
		responder.Error(w, status, code, message)
		return
	}

	h.recordAuditEvent(r.Context(), audit.Event{
		OrgID:         principal.OrganizationID,
		EventType:     audit.EventAgentCreated,
		PrincipalType: "human",
		PrincipalID:   principal.UserID,
		TargetType:    pointerToStringValue("agent"),
		TargetID:      &created.ID,
		IP:            requestClientIP(r),
		Outcome:       "success",
		Metadata: map[string]any{
			"agent_class":       strings.TrimSpace(created.AgentClass),
			"agent_type":        strings.TrimSpace(created.AgentType),
			"lifecycle_status":  strings.TrimSpace(created.LifecycleStatus),
			"default_model_id":  normalizeOptionalString(created.DefaultModelProfileID),
			"display_name":      strings.TrimSpace(created.DisplayName),
			"private_memory_on": created.PrivateMemory,
		},
	})

	responder.JSON(w, http.StatusCreated, toAgentResponse(created))
}

func (h agentHandlers) getAgent(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}

	found, getErr := h.service.Get(r.Context(), principal.OrganizationID, agentID)
	if getErr != nil {
		status, code, message := mapAgentError(getErr)
		responder.Error(w, status, code, message)
		return
	}

	responder.JSON(w, http.StatusOK, toAgentResponse(found))
}

// agentConfigResponse is the configuration-focused subset of an agent, returned
// by GET /v1/agents/{id}/config. It exposes editable fields that govern agent
// behavior — system prompt, tool policy, model profile, memory scopes, and
// budgets — without the identity/lifecycle metadata present in the full agent
// response.
type agentConfigResponse struct {
	AgentID               uuid.UUID `json:"agent_id"`
	SystemPrompt          string    `json:"system_prompt"`
	OperatorInstructions  string    `json:"operator_instructions"`
	DefaultModelProfileID *string   `json:"default_model_profile_id"`
	MemoryReadScopes      []string  `json:"memory_read_scopes"`
	PrivateMemory         bool      `json:"private_memory"`
	ToolAllowList         []string  `json:"tool_allow_list"`
	ToolDenyList          []string  `json:"tool_deny_list"`
	BudgetCapTokens       *int64    `json:"budget_cap_tokens"`
	BudgetPeriod          *string   `json:"budget_period"`
}

func (h agentHandlers) getAgentConfig(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	agentID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}
	found, getErr := h.service.Get(r.Context(), principal.OrganizationID, agentID)
	if getErr != nil {
		status, code, message := mapAgentError(getErr)
		responder.Error(w, status, code, message)
		return
	}
	cfg := agentConfigResponse{
		AgentID:               found.ID,
		SystemPrompt:          found.SystemPrompt,
		OperatorInstructions:  found.OperatorInstructions,
		DefaultModelProfileID: found.DefaultModelProfileID,
		MemoryReadScopes:      found.MemoryReadScopes,
		PrivateMemory:         found.PrivateMemory,
		ToolAllowList:         found.ToolAllowList,
		ToolDenyList:          found.ToolDenyList,
		BudgetCapTokens:       found.BudgetCapTokens,
		BudgetPeriod:          found.BudgetPeriod,
	}
	if cfg.MemoryReadScopes == nil {
		cfg.MemoryReadScopes = []string{}
	}
	if cfg.ToolAllowList == nil {
		cfg.ToolAllowList = []string{}
	}
	if cfg.ToolDenyList == nil {
		cfg.ToolDenyList = []string{}
	}
	responder.JSON(w, http.StatusOK, cfg)
}

type agentToolResponse struct {
	ID                 uuid.UUID       `json:"id"`
	Name               string          `json:"name"`
	DisplayName        string          `json:"display_name"`
	Description        string          `json:"description"`
	ToolTier           string          `json:"tool_tier"`
	ToolDomain         string          `json:"tool_domain"`
	RequiredCapability *string         `json:"required_capability"`
	InputSchema        json.RawMessage `json:"input_schema"`
	Allowed            bool            `json:"allowed"`
}

// getAgentTools returns the enabled tools visible to an agent, filtered by its
// ToolAllowList and ToolDenyList. If ToolAllowList is non-empty only those tools
// appear; ToolDenyList then removes entries from the result.
func (h agentHandlers) getAgentTools(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	agentID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}
	found, getErr := h.service.Get(r.Context(), principal.OrganizationID, agentID)
	if getErr != nil {
		status, code, message := mapAgentError(getErr)
		responder.Error(w, status, code, message)
		return
	}

	if h.toolDefs == nil {
		responder.JSON(w, http.StatusOK, []agentToolResponse{})
		return
	}

	allTools, listErr := h.toolDefs.ListEnabled(r.Context())
	if listErr != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list tools")
		return
	}

	allowSet := make(map[string]bool, len(found.ToolAllowList))
	for _, name := range found.ToolAllowList {
		allowSet[name] = true
	}
	denySet := make(map[string]bool, len(found.ToolDenyList))
	for _, name := range found.ToolDenyList {
		denySet[name] = true
	}

	result := make([]agentToolResponse, 0, len(allTools))
	for _, t := range allTools {
		if denySet[t.Name] {
			continue
		}
		if len(allowSet) > 0 && !allowSet[t.Name] {
			continue
		}
		result = append(result, agentToolResponse{
			ID:                 t.ID,
			Name:               t.Name,
			DisplayName:        t.DisplayName,
			Description:        t.Description,
			ToolTier:           t.ToolTier,
			ToolDomain:         t.ToolDomain,
			RequiredCapability: t.RequiredCapability,
			InputSchema:        t.InputSchema,
			Allowed:            true,
		})
	}
	responder.JSON(w, http.StatusOK, result)
}

func (h agentHandlers) listAllTools(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.toolDefs == nil {
		responder.JSON(w, http.StatusOK, []agentToolResponse{})
		return
	}
	allTools, err := h.toolDefs.ListEnabled(r.Context())
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list tools")
		return
	}
	result := make([]agentToolResponse, 0, len(allTools))
	for _, t := range allTools {
		result = append(result, agentToolResponse{
			ID:                 t.ID,
			Name:               t.Name,
			DisplayName:        t.DisplayName,
			Description:        t.Description,
			ToolTier:           t.ToolTier,
			ToolDomain:         t.ToolDomain,
			RequiredCapability: t.RequiredCapability,
			InputSchema:        t.InputSchema,
			Allowed:            true,
		})
	}
	responder.JSON(w, http.StatusOK, result)
}

func (h agentHandlers) updateAgent(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}

	var req updateAgentRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	if req.AgentType != nil {
		trimmed := strings.ToLower(strings.TrimSpace(*req.AgentType))
		if !isAllowedAgentType(trimmed) {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "agent_type must be one of pm, worker, reviewer, general")
			return
		}
		req.AgentType = &trimmed
	}

	budgetPeriod, periodErr := normalizeBudgetPeriod(req.BudgetPeriod)
	if periodErr != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, periodErr.Error())
		return
	}

	updated, updateErr := h.service.Update(r.Context(), principal.OrganizationID, agentID, agent.UpdateAgentRequest{
		DisplayName:           req.DisplayName,
		SystemPrompt:          req.SystemPrompt,
		OperatorInstructions:  req.OperatorInstructions,
		AgentType:             req.AgentType,
		PrivateMemory:         req.PrivateMemory,
		MemoryReadScopes:      req.MemoryReadScopes,
		ToolAllowList:         req.ToolAllowList,
		ToolDenyList:          req.ToolDenyList,
		DefaultModelProfileID: req.DefaultModelProfileID,
		BudgetCapTokens:       req.BudgetCapTokens,
		BudgetPeriod:          budgetPeriod,
	})
	if updateErr != nil {
		status, code, message := mapAgentError(updateErr)
		responder.Error(w, status, code, message)
		return
	}

	h.recordAuditEvent(r.Context(), audit.Event{
		OrgID:         principal.OrganizationID,
		EventType:     audit.EventAgentUpdated,
		PrincipalType: "human",
		PrincipalID:   principal.UserID,
		TargetType:    pointerToStringValue("agent"),
		TargetID:      &updated.ID,
		IP:            requestClientIP(r),
		Outcome:       "success",
		Metadata: map[string]any{
			"agent_type":       strings.TrimSpace(updated.AgentType),
			"display_name":     strings.TrimSpace(updated.DisplayName),
			"lifecycle_status": strings.TrimSpace(updated.LifecycleStatus),
		},
	})

	responder.JSON(w, http.StatusOK, toAgentResponse(updated))
}

func (h agentHandlers) pauseAgent(w http.ResponseWriter, r *http.Request) {
	h.transitionAgent(w, r, audit.EventAgentUpdated, func(ctx context.Context, orgID, agentID uuid.UUID) error {
		return h.service.Pause(ctx, orgID, agentID)
	})
}

func (h agentHandlers) unpauseAgent(w http.ResponseWriter, r *http.Request) {
	h.transitionAgent(w, r, audit.EventAgentUpdated, func(ctx context.Context, orgID, agentID uuid.UUID) error {
		return h.service.Unpause(ctx, orgID, agentID)
	})
}

func (h agentHandlers) retireAgent(w http.ResponseWriter, r *http.Request) {
	h.transitionAgent(w, r, audit.EventAgentDeleted, func(ctx context.Context, orgID, agentID uuid.UUID) error {
		return h.service.Retire(ctx, orgID, agentID)
	})
}

func (h agentHandlers) cancelAgent(w http.ResponseWriter, r *http.Request) {
	h.transitionAgent(w, r, audit.EventAgentDeleted, func(ctx context.Context, orgID, agentID uuid.UUID) error {
		return h.service.Cancel(ctx, orgID, agentID)
	})
}

func (h agentHandlers) transitionAgent(w http.ResponseWriter, r *http.Request, eventType string, transition func(ctx context.Context, orgID, agentID uuid.UUID) error) {
	responder := api.NewResponder(r.Context())
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}

	if err := transition(r.Context(), principal.OrganizationID, agentID); err != nil {
		status, code, message := mapAgentError(err)
		responder.Error(w, status, code, message)
		return
	}

	updated, getErr := h.service.Get(r.Context(), principal.OrganizationID, agentID)
	if getErr != nil {
		status, code, message := mapAgentError(getErr)
		responder.Error(w, status, code, message)
		return
	}

	h.recordAuditEvent(r.Context(), audit.Event{
		OrgID:         principal.OrganizationID,
		EventType:     strings.TrimSpace(eventType),
		PrincipalType: "human",
		PrincipalID:   principal.UserID,
		TargetType:    pointerToStringValue("agent"),
		TargetID:      &updated.ID,
		IP:            requestClientIP(r),
		Outcome:       "success",
		Metadata: map[string]any{
			"lifecycle_status": strings.TrimSpace(updated.LifecycleStatus),
		},
	})

	responder.JSON(w, http.StatusOK, toAgentResponse(updated))
}

func (h agentHandlers) listAgentTemplates(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.templates == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent template repository unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	templates, err := h.templates.List(r.Context(), principal.OrganizationID, true)
	if err != nil {
		status, code, message := mapTemplateError(err)
		responder.Error(w, status, code, message)
		return
	}

	payload := make([]agentTemplateResponse, 0, len(templates))
	for _, item := range templates {
		payload = append(payload, toAgentTemplateResponse(item))
	}

	responder.JSON(w, http.StatusOK, payload)
}

func (h agentHandlers) createAgentTemplate(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.templates == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent template repository unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	var req createAgentTemplateRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	if strings.TrimSpace(req.DisplayName) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "display_name is required")
		return
	}

	agentType := strings.ToLower(strings.TrimSpace(req.AgentType))
	if !isAllowedAgentType(agentType) {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "agent_type must be one of pm, worker, reviewer, general")
		return
	}

	source := strings.ToLower(strings.TrimSpace(req.Source))
	if source == "" {
		source = "org"
	}
	if _, ok := allowedTemplateSources[source]; !ok {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "source must be org or promoted")
		return
	}

	created, err := h.templates.Create(r.Context(), repo.AgentProfileTemplate{
		OrganizationID:        &principal.OrganizationID,
		DisplayName:           strings.TrimSpace(req.DisplayName),
		Source:                source,
		SourceAgentID:         req.SourceAgentID,
		SystemPrompt:          req.SystemPrompt,
		OperatorInstructions:  req.OperatorInstructions,
		AgentType:             agentType,
		DefaultModelProfileID: normalizeOptionalString(req.DefaultModelProfileID),
		ToolAllowList:         req.ToolAllowList,
		ToolDenyList:          req.ToolDenyList,
		MemoryReadScopes:      req.MemoryReadScopes,
		PrivateMemory:         req.PrivateMemory,
		Metadata:              req.Metadata,
	})
	if err != nil {
		status, code, message := mapTemplateError(err)
		responder.Error(w, status, code, message)
		return
	}

	responder.JSON(w, http.StatusCreated, toAgentTemplateResponse(created))
}

func (h agentHandlers) getAgentTemplate(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.templates == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent template repository unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	templateID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid template id")
		return
	}

	template, getErr := h.templates.GetByID(r.Context(), templateID)
	if getErr != nil {
		status, code, message := mapTemplateError(getErr)
		responder.Error(w, status, code, message)
		return
	}
	if template.OrganizationID != nil && *template.OrganizationID != principal.OrganizationID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	responder.JSON(w, http.StatusOK, toAgentTemplateResponse(template))
}

func (h agentHandlers) createAgentProjectAssignment(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.assignments == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "assignment service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID, err := parseAgentIDParam(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}

	var req createProjectAssignmentRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	if req.ProjectID == uuid.Nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "project_id is required")
		return
	}

	role := strings.ToLower(strings.TrimSpace(req.Role))
	if role != "pm" && role != "worker" && role != "reviewer" && role != "observer" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "role must be one of pm, worker, reviewer, observer")
		return
	}

	agentRecord, err := h.getScopedAgent(r.Context(), principal.OrganizationID, agentID)
	if err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(agentRecord.LifecycleStatus), "active") {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "agent lifecycle_status must be active")
		return
	}

	if _, err := h.getScopedProject(r.Context(), principal.OrganizationID, req.ProjectID); err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	var assigned *repo.AgentProjectAssignment
	for attempt := 0; attempt < 3; attempt++ {
		assigned, err = h.assignments.AssignToProject(r.Context(), agentID, req.ProjectID, role, agent.AssignmentActor{
			Type: "human_user",
			ID:   principal.UserID,
		})
		if !errors.Is(err, agent.ErrPMConflict) {
			break
		}
	}
	if err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	responder.JSON(w, http.StatusOK, toProjectAssignmentResponse(assigned))
}

func (h agentHandlers) deleteAgentProjectAssignment(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.assignments == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "assignment service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID, err := parseAgentIDParam(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}
	projectID, err := parseProjectIDParam(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}

	if _, err := h.getScopedAgent(r.Context(), principal.OrganizationID, agentID); err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}
	if _, err := h.getScopedProject(r.Context(), principal.OrganizationID, projectID); err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	removed, err := h.assignments.RemoveFromProject(r.Context(), agentID, projectID)
	if err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}
	responder.JSON(w, http.StatusOK, toProjectAssignmentResponse(removed))
}

func (h agentHandlers) listAgentProjectAssignments(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.projectAssignments == nil || h.projects == nil || h.agents == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "assignment repositories unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID, err := parseAgentIDParam(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}
	if _, err := h.getScopedAgent(r.Context(), principal.OrganizationID, agentID); err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	items, err := h.projectAssignments.ListByAgent(r.Context(), agentID)
	if err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	params := api.ParsePaginationParams(r.URL.Query())
	var cursorAt time.Time
	var cursorID uuid.UUID
	if params.Cursor != "" {
		decodedAt, decodedID, decodeErr := (api.PaginationDecoder{}).Decode(params.Cursor)
		if decodeErr != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
			return
		}
		cursorAt = decodedAt
		cursorID = decodedID
	}

	startIdx := 0
	if params.Cursor != "" {
		startIdx = len(items)
		for i, item := range items {
			if item.AssignedAt.Before(cursorAt) || (item.AssignedAt.Equal(cursorAt) && item.ID.String() < cursorID.String()) {
				startIdx = i
				break
			}
		}
	}

	remaining := items[startIdx:]
	page := remaining
	var nextCursor *string
	if len(page) > params.Limit {
		page = page[:params.Limit]
		encoded := (api.PaginationEncoder{}).Encode(page[len(page)-1].AssignedAt, page[len(page)-1].ID)
		nextCursor = &encoded
	}

	payload := make([]projectAssignmentListItemResponse, 0, len(page))
	for _, item := range page {
		project, getErr := h.getScopedProject(r.Context(), principal.OrganizationID, item.ProjectID)
		if getErr != nil {
			status, code, message := mapAssignmentError(getErr)
			responder.Error(w, status, code, message)
			return
		}
		payload = append(payload, toProjectAssignmentListItemResponse(item, project.Slug))
	}

	total := len(items)
	responder.JSONList(w, http.StatusOK, payload, api.PaginationMeta{
		NextCursor: nextCursor,
		Limit:      params.Limit,
		Total:      &total,
	})
}

func (h agentHandlers) createProjectAgent(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.assignments == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "assignment service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	projectID, err := parseProjectPathIDParam(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}

	var req createProjectAgentRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.AgentID == uuid.Nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "agent_id is required")
		return
	}
	role := strings.ToLower(strings.TrimSpace(req.Role))
	if role == "" {
		role = "worker"
	}
	if role != "pm" && role != "worker" && role != "reviewer" && role != "observer" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "role must be one of pm, worker, reviewer, observer")
		return
	}

	if _, err := h.getScopedProject(r.Context(), principal.OrganizationID, projectID); err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}
	agentRecord, err := h.getScopedAgent(r.Context(), principal.OrganizationID, req.AgentID)
	if err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(agentRecord.LifecycleStatus), "active") {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "agent lifecycle_status must be active")
		return
	}

	var assigned *repo.AgentProjectAssignment
	for attempt := 0; attempt < 3; attempt++ {
		assigned, err = h.assignments.AssignToProject(r.Context(), req.AgentID, projectID, role, agent.AssignmentActor{
			Type: "human_user",
			ID:   principal.UserID,
		})
		if !errors.Is(err, agent.ErrPMConflict) {
			break
		}
	}
	if err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	responder.JSON(w, http.StatusOK, toProjectAssignmentResponse(assigned))
}

func (h agentHandlers) deleteProjectAgent(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.assignments == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "assignment service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	projectID, err := parseProjectPathIDParam(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	agentID, err := parseRouteUUIDParam(r, "agent_id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}

	if _, err := h.getScopedProject(r.Context(), principal.OrganizationID, projectID); err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}
	if _, err := h.getScopedAgent(r.Context(), principal.OrganizationID, agentID); err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	removed, err := h.assignments.RemoveFromProject(r.Context(), agentID, projectID)
	if err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}
	responder.JSON(w, http.StatusOK, toProjectAssignmentResponse(removed))
}

func (h agentHandlers) listProjectAgents(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.projectAssignments == nil || h.projects == nil || h.agents == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "assignment repositories unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	projectID, err := parseProjectPathIDParam(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if _, err := h.getScopedProject(r.Context(), principal.OrganizationID, projectID); err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	items, err := h.projectAssignments.ListByProject(r.Context(), projectID)
	if err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	params := api.ParsePaginationParams(r.URL.Query())
	var cursorAt time.Time
	var cursorID uuid.UUID
	if params.Cursor != "" {
		decodedAt, decodedID, decodeErr := (api.PaginationDecoder{}).Decode(params.Cursor)
		if decodeErr != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
			return
		}
		cursorAt = decodedAt
		cursorID = decodedID
	}

	startIdx := 0
	if params.Cursor != "" {
		startIdx = len(items)
		for i, item := range items {
			if item.AssignedAt.Before(cursorAt) || (item.AssignedAt.Equal(cursorAt) && item.ID.String() < cursorID.String()) {
				startIdx = i
				break
			}
		}
	}

	remaining := items[startIdx:]
	page := remaining
	var nextCursor *string
	if len(page) > params.Limit {
		page = page[:params.Limit]
		encoded := (api.PaginationEncoder{}).Encode(page[len(page)-1].AssignedAt, page[len(page)-1].ID)
		nextCursor = &encoded
	}

	payload := make([]projectAgentListItemResponse, 0, len(page))
	for _, item := range page {
		agentRecord, getErr := h.getScopedAgent(r.Context(), principal.OrganizationID, item.AgentID)
		if getErr != nil {
			status, code, message := mapAssignmentError(getErr)
			responder.Error(w, status, code, message)
			return
		}
		payload = append(payload, toProjectAgentListItemResponse(item, agentRecord.DisplayName))
	}

	total := len(items)
	responder.JSONList(w, http.StatusOK, payload, api.PaginationMeta{
		NextCursor: nextCursor,
		Limit:      params.Limit,
		Total:      &total,
	})
}

func (h agentHandlers) listAgentSkills(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.skillAttachments == nil || h.skills == nil || h.agents == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "assignment repositories unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID, err := parseAgentIDParam(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}
	if _, err := h.getScopedAgent(r.Context(), principal.OrganizationID, agentID); err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	items, err := h.skillAttachments.ListByAgent(r.Context(), agentID)
	if err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	params := api.ParsePaginationParams(r.URL.Query())
	cursorPriority := -1
	cursorID := ""
	if params.Cursor != "" {
		decodedPriority, decodedID, decodeErr := decodePriorityCursor(params.Cursor)
		if decodeErr != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
			return
		}
		cursorPriority = decodedPriority
		cursorID = decodedID
	}

	startIdx := 0
	if params.Cursor != "" {
		startIdx = len(items)
		for i, item := range items {
			if item.Priority > cursorPriority || (item.Priority == cursorPriority && item.ID.String() > cursorID) {
				startIdx = i
				break
			}
		}
	}

	remaining := items[startIdx:]
	page := remaining
	var nextCursor *string
	if len(page) > params.Limit {
		page = page[:params.Limit]
		encoded, encodeErr := encodePriorityCursor(page[len(page)-1].Priority, page[len(page)-1].ID)
		if encodeErr != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
			return
		}
		nextCursor = &encoded
	}

	payload := make([]agentSkillAttachmentListItemResponse, 0, len(page))
	for _, item := range page {
		skill, getErr := h.getScopedSkill(r.Context(), principal.OrganizationID, item.SkillID)
		if getErr != nil {
			status, code, message := mapAssignmentError(getErr)
			responder.Error(w, status, code, message)
			return
		}
		payload = append(payload, toAgentSkillAttachmentListItemResponse(item, skill.DisplayName))
	}

	total := len(items)
	responder.JSONList(w, http.StatusOK, payload, api.PaginationMeta{
		NextCursor: nextCursor,
		Limit:      params.Limit,
		Total:      &total,
	})
}

func (h agentHandlers) attachAgentSkill(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.assignments == nil || h.skillAttachments == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "assignment service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID, err := parseAgentIDParam(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}
	if _, err := h.getScopedAgent(r.Context(), principal.OrganizationID, agentID); err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	var req createAgentSkillRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.SkillID == uuid.Nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "skill_id is required")
		return
	}
	priority := 100
	if req.Priority != nil {
		priority = *req.Priority
	}
	if priority < 1 || priority > 1000 {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "priority must be between 1 and 1000")
		return
	}

	if _, err := h.getScopedSkill(r.Context(), principal.OrganizationID, req.SkillID); err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	preexisting, preexistingErr := h.skillAttachments.GetByAgentAndSkill(r.Context(), agentID, req.SkillID)
	hadPreexisting := preexistingErr == nil
	wasReactivated := hadPreexisting && !preexisting.IsActive
	if preexistingErr != nil && !errors.Is(preexistingErr, repo.ErrNotFound) {
		status, code, message := mapAssignmentError(preexistingErr)
		responder.Error(w, status, code, message)
		return
	}

	attached, err := h.assignments.AttachSkill(r.Context(), agentID, req.SkillID, priority, agent.AssignmentActor{
		Type: "human_user",
		ID:   principal.UserID,
	})
	if err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	if hadPreexisting {
		writeJSONWithMeta(w, r.Context(), http.StatusOK, toAgentSkillAttachmentResponse(attached), map[string]any{
			"reactivated": wasReactivated,
		})
		return
	}
	responder.JSON(w, http.StatusCreated, toAgentSkillAttachmentResponse(attached))
}

func (h agentHandlers) detachAgentSkill(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.assignments == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "assignment service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID, err := parseAgentIDParam(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}
	skillID, err := parseSkillIDParam(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid skill id")
		return
	}
	if _, err := h.getScopedAgent(r.Context(), principal.OrganizationID, agentID); err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	detached, err := h.assignments.DetachSkill(r.Context(), agentID, skillID)
	if err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}
	responder.JSON(w, http.StatusOK, toAgentSkillAttachmentResponse(detached))
}

func (h agentHandlers) updateAgentSkillPriority(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.assignments == nil || h.skillAttachments == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "assignment service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID, err := parseAgentIDParam(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}
	skillID, err := parseSkillIDParam(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid skill id")
		return
	}
	if _, err := h.getScopedAgent(r.Context(), principal.OrganizationID, agentID); err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	var req updateAgentSkillPriorityRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.Priority < 1 || req.Priority > 1000 {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "priority must be between 1 and 1000")
		return
	}

	existing, err := h.skillAttachments.GetByAgentAndSkill(r.Context(), agentID, skillID)
	if err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}
	if !existing.IsActive {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	if err := h.assignments.ReorderSkills(r.Context(), agentID, map[uuid.UUID]int{skillID: req.Priority}); err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}

	updated, err := h.skillAttachments.GetByAgentAndSkill(r.Context(), agentID, skillID)
	if err != nil {
		status, code, message := mapAssignmentError(err)
		responder.Error(w, status, code, message)
		return
	}
	responder.JSON(w, http.StatusOK, toAgentSkillAttachmentResponse(&updated))
}

func (h agentHandlers) getScopedAgent(ctx context.Context, organizationID, agentID uuid.UUID) (repo.Agent, error) {
	if h.agents == nil {
		return repo.Agent{}, fmt.Errorf("agent repository unavailable")
	}
	agentRecord, err := h.agents.GetByID(ctx, agentID)
	if err != nil {
		return repo.Agent{}, err
	}
	if agentRecord.OrganizationID != organizationID {
		return repo.Agent{}, repo.ErrNotFound
	}
	return agentRecord, nil
}

func (h agentHandlers) getScopedProject(ctx context.Context, organizationID, projectID uuid.UUID) (repo.Project, error) {
	if h.projects == nil {
		return repo.Project{}, fmt.Errorf("project repository unavailable")
	}
	projectRecord, err := h.projects.GetByID(ctx, projectID)
	if err != nil {
		return repo.Project{}, err
	}
	if projectRecord.OrganizationID != organizationID {
		return repo.Project{}, repo.ErrNotFound
	}
	return projectRecord, nil
}

func (h agentHandlers) getScopedSkill(ctx context.Context, organizationID, skillID uuid.UUID) (repo.Skill, error) {
	if h.skills == nil {
		return repo.Skill{}, fmt.Errorf("skill repository unavailable")
	}
	skillRecord, err := h.skills.GetByID(ctx, skillID)
	if err != nil {
		return repo.Skill{}, err
	}
	if skillRecord.OrganizationID != organizationID && skillRecord.OrganizationID != uuid.Nil {
		return repo.Skill{}, repo.ErrNotFound
	}
	return skillRecord, nil
}

func parseAgentIDParam(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
}

func parseProjectPathIDParam(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
}

func parseProjectIDParam(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(strings.TrimSpace(chi.URLParam(r, "pid")))
}

func parseSkillIDParam(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(strings.TrimSpace(chi.URLParam(r, "sid")))
}

func parseRouteUUIDParam(r *http.Request, key string) (uuid.UUID, error) {
	return uuid.Parse(strings.TrimSpace(chi.URLParam(r, key)))
}

func mapAgentError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, agent.ErrInvalidTransition):
		return http.StatusConflict, "invalid_transition", err.Error()
	case errors.Is(err, agent.ErrInvalidForTempAgent):
		return http.StatusUnprocessableEntity, "invalid_for_temp_agent", err.Error()
	case errors.Is(err, agent.ErrConcurrentTempLimitReached):
		return http.StatusTooManyRequests, "temp_limit_reached", err.Error()
	case errors.Is(err, agent.ErrStarterTrioProtected):
		return http.StatusForbidden, "starter_trio_protected", err.Error()
	case errors.Is(err, repo.ErrNotFound):
		return http.StatusNotFound, api.ErrCodeNotFound, "resource not found"
	case errors.Is(err, repo.ErrConflict):
		return http.StatusConflict, api.ErrCodeConflict, "conflict"
	case errors.Is(err, agent.ErrDisplayNameRequired),
		errors.Is(err, agent.ErrOrganizationIDRequired),
		errors.Is(err, agent.ErrTempProjectIDRequired),
		errors.Is(err, agent.ErrInvalidCreatedByType),
		errors.Is(err, agent.ErrCreatedByIDRequired):
		return http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error()
	default:
		return http.StatusInternalServerError, api.ErrCodeInternal, "request failed"
	}
}

func mapTemplateError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return http.StatusNotFound, api.ErrCodeNotFound, "resource not found"
	case errors.Is(err, repo.ErrConflict):
		return http.StatusConflict, api.ErrCodeConflict, "conflict"
	default:
		return http.StatusInternalServerError, api.ErrCodeInternal, "request failed"
	}
}

func mapAssignmentError(err error) (status int, code, message string) {
	switch {
	case err == nil:
		return http.StatusOK, "", ""
	case errors.Is(err, repo.ErrNotFound):
		return http.StatusNotFound, api.ErrCodeNotFound, "resource not found"
	case errors.Is(err, repo.ErrConflict), errors.Is(err, agent.ErrPMConflict):
		return http.StatusConflict, api.ErrCodeConflict, "conflict"
	case errors.Is(err, agent.ErrAssignmentAgentIDRequired),
		errors.Is(err, agent.ErrAssignmentProjectIDRequired),
		errors.Is(err, agent.ErrAssignmentSkillIDRequired),
		errors.Is(err, agent.ErrAssignmentInvalidRole),
		errors.Is(err, agent.ErrStarterTrioRoleForbidden),
		errors.Is(err, agent.ErrInvalidCreatedByType),
		errors.Is(err, agent.ErrCreatedByIDRequired):
		return http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error()
	default:
		return http.StatusInternalServerError, api.ErrCodeInternal, "request failed"
	}
}

func toProjectAssignmentResponse(item *repo.AgentProjectAssignment) projectAssignmentResponse {
	if item == nil {
		return projectAssignmentResponse{}
	}
	return projectAssignmentResponse{
		ID:         item.ID,
		AgentID:    item.AgentID,
		ProjectID:  item.ProjectID,
		Role:       item.Role,
		IsActive:   item.IsActive,
		AssignedAt: item.AssignedAt,
	}
}

func toProjectAssignmentListItemResponse(item repo.AgentProjectAssignment, projectSlug string) projectAssignmentListItemResponse {
	return projectAssignmentListItemResponse{
		ID:          item.ID,
		ProjectID:   item.ProjectID,
		ProjectSlug: projectSlug,
		Role:        item.Role,
		IsActive:    item.IsActive,
		AssignedAt:  item.AssignedAt,
	}
}

func toProjectAgentListItemResponse(item repo.AgentProjectAssignment, agentDisplayName string) projectAgentListItemResponse {
	return projectAgentListItemResponse{
		ID:               item.ID,
		AgentID:          item.AgentID,
		AgentDisplayName: agentDisplayName,
		Role:             item.Role,
		IsActive:         item.IsActive,
		AssignedAt:       item.AssignedAt,
	}
}

func toAgentSkillAttachmentResponse(item *repo.AgentSkillAttachment) agentSkillAttachmentResponse {
	if item == nil {
		return agentSkillAttachmentResponse{}
	}
	return agentSkillAttachmentResponse{
		ID:         item.ID,
		AgentID:    item.AgentID,
		SkillID:    item.SkillID,
		Priority:   item.Priority,
		IsActive:   item.IsActive,
		AttachedAt: item.AttachedAt,
	}
}

func toAgentSkillAttachmentListItemResponse(item repo.AgentSkillAttachment, skillName string) agentSkillAttachmentListItemResponse {
	return agentSkillAttachmentListItemResponse{
		ID:         item.ID,
		SkillID:    item.SkillID,
		SkillName:  skillName,
		Priority:   item.Priority,
		AttachedAt: item.AttachedAt,
	}
}

func encodePriorityCursor(priority int, id uuid.UUID) (string, error) {
	payload, err := json.Marshal(priorityCursor{
		P:  priority,
		ID: id.String(),
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodePriorityCursor(raw string) (int, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return 0, "", err
	}
	var cursor priorityCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return 0, "", err
	}
	if strings.TrimSpace(cursor.ID) == "" {
		return 0, "", errors.New("cursor id is required")
	}
	if _, err := uuid.Parse(cursor.ID); err != nil {
		return 0, "", err
	}
	return cursor.P, cursor.ID, nil
}

func writeJSONWithMeta(w http.ResponseWriter, ctx context.Context, status int, data any, extraMeta map[string]any) {
	requestID, ok := api.RequestIDFromContext(ctx)
	if !ok || strings.TrimSpace(requestID) == "" {
		requestID = uuid.NewString()
	}

	meta := map[string]any{
		"request_id": requestID,
	}
	for key, value := range extraMeta {
		meta[key] = value
	}

	w.Header().Set(api.HeaderRequestID, requestID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": data,
		"meta": meta,
	})
}

func isAllowedAgentType(agentType string) bool {
	_, ok := allowedAgentTypes[strings.ToLower(strings.TrimSpace(agentType))]
	return ok
}

func normalizeBudgetPeriod(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.ToLower(strings.TrimSpace(*raw))
	if trimmed == "" {
		return nil, nil
	}
	if _, ok := allowedBudgetPeriods[trimmed]; !ok {
		return nil, errors.New("budget_period must be one of daily, weekly, monthly")
	}
	return &trimmed, nil
}

func normalizeOptionalString(raw *string) *string {
	if raw == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*raw)
	return &trimmed
}

func parseLifecycleStatuses(raw string) ([]string, error) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return nil, nil
	}

	parts := strings.Split(normalized, ",")
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		status := strings.ToLower(strings.TrimSpace(part))
		if status == "" {
			continue
		}
		if _, ok := allowedAgentLifecycleStatuses[status]; !ok {
			return nil, errors.New("lifecycle_status contains an invalid value")
		}
		if _, ok := seen[status]; ok {
			continue
		}
		seen[status] = struct{}{}
		out = append(out, status)
	}
	sort.Strings(out)
	return out, nil
}

func cacheKeyForListTotals(orgID uuid.UUID, agentClass string, lifecycleStatuses []string, agentType string) string {
	return orgID.String() + "|" + agentClass + "|" + strings.Join(lifecycleStatuses, ",") + "|" + agentType
}

func isAdminRole(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), "admin")
}

func isAdminOrMemberRole(role string) bool {
	normalized := strings.ToLower(strings.TrimSpace(role))
	return normalized == "admin" || normalized == "member"
}

func (h agentHandlers) recordAuditEvent(ctx context.Context, event audit.Event) {
	if h.auditRecorder == nil {
		return
	}
	h.auditRecorder.RecordAsync(ctx, event)
}

func pointerToStringValue(value string) *string {
	trimmed := strings.TrimSpace(value)
	return &trimmed
}

func toAgentResponse(model *agent.Agent) agentResponse {
	return agentResponse{
		ID:                    model.ID,
		OrganizationID:        model.OrganizationID,
		DisplayName:           model.DisplayName,
		AgentClass:            model.AgentClass,
		LifecycleStatus:       model.LifecycleStatus,
		SystemPrompt:          model.SystemPrompt,
		OperatorInstructions:  model.OperatorInstructions,
		AgentType:             model.AgentType,
		IsStarterTrio:         model.IsStarterTrio,
		PrivateMemory:         model.PrivateMemory,
		MemoryReadScopes:      append([]string{}, model.MemoryReadScopes...),
		ToolAllowList:         append([]string{}, model.ToolAllowList...),
		ToolDenyList:          append([]string{}, model.ToolDenyList...),
		DefaultModelProfileID: model.DefaultModelProfileID,
		BudgetCapTokens:       model.BudgetCapTokens,
		BudgetPeriod:          model.BudgetPeriod,
		TempProjectID:         model.TempProjectID,
		TempTTLSeconds:        model.TempTTLSeconds,
		TempExpiresAt:         model.TempExpiresAt,
		PromotedToAgentID:     model.PromotedToAgentID,
		CreatedByType:         model.CreatedByType,
		CreatedByID:           model.CreatedByID,
		CreatedAt:             model.CreatedAt,
		UpdatedAt:             model.UpdatedAt,
	}
}

func toAgentTemplateResponse(model repo.AgentProfileTemplate) agentTemplateResponse {
	metadata := model.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	return agentTemplateResponse{
		ID:                    model.ID,
		OrganizationID:        model.OrganizationID,
		DisplayName:           model.DisplayName,
		Source:                model.Source,
		SourceAgentID:         model.SourceAgentID,
		SystemPrompt:          model.SystemPrompt,
		OperatorInstructions:  model.OperatorInstructions,
		AgentType:             model.AgentType,
		DefaultModelProfileID: model.DefaultModelProfileID,
		ToolAllowList:         append([]string{}, model.ToolAllowList...),
		ToolDenyList:          append([]string{}, model.ToolDenyList...),
		MemoryReadScopes:      append([]string{}, model.MemoryReadScopes...),
		PrivateMemory:         model.PrivateMemory,
		Metadata:              append(json.RawMessage(nil), metadata...),
		CreatedAt:             model.CreatedAt,
		UpdatedAt:             model.UpdatedAt,
	}
}

type agentTotalCache struct {
	ttl time.Duration
	mu  sync.RWMutex
	val map[string]agentTotalCacheEntry
}

type agentTotalCacheEntry struct {
	total     int
	expiresAt time.Time
}

func newAgentTotalCache(ttl time.Duration) *agentTotalCache {
	return &agentTotalCache{
		ttl: ttl,
		val: make(map[string]agentTotalCacheEntry),
	}
}

func (c *agentTotalCache) GetOrSet(key string, value int) int {
	now := time.Now()

	c.mu.RLock()
	entry, ok := c.val[key]
	c.mu.RUnlock()
	if ok && now.Before(entry.expiresAt) {
		return entry.total
	}

	c.mu.Lock()
	c.val[key] = agentTotalCacheEntry{
		total:     value,
		expiresAt: now.Add(c.ttl),
	}
	c.mu.Unlock()

	return value
}
