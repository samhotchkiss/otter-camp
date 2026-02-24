package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/agent"
	"github.com/samhotchkiss/otter-camp/internal/api"
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

type AgentRouteRegistrar struct {
	handlers agentHandlers
}

func NewAgentRouteRegistrar(service agent.AgentService, templates agentTemplateRepository) *AgentRouteRegistrar {
	return &AgentRouteRegistrar{
		handlers: newAgentHandlers(service, templates),
	}
}

func (r *AgentRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.Get("/agents", r.handlers.listAgents)
	router.Post("/agents", r.handlers.createAgent)
	router.Get("/agents/{id}", r.handlers.getAgent)

	router.With(middleware.RequireRole("admin")).Patch("/agents/{id}", r.handlers.updateAgent)
	router.With(middleware.RequireRole("admin")).Post("/agents/{id}/pause", r.handlers.pauseAgent)
	router.With(middleware.RequireRole("admin")).Post("/agents/{id}/unpause", r.handlers.unpauseAgent)
	router.With(middleware.RequireRole("admin")).Post("/agents/{id}/retire", r.handlers.retireAgent)
	router.With(middleware.RequireRole("admin")).Post("/agents/{id}/cancel", r.handlers.cancelAgent)

	router.Get("/agent-templates", r.handlers.listAgentTemplates)
	router.With(middleware.RequireRole("admin")).Post("/agent-templates", r.handlers.createAgentTemplate)
	router.Get("/agent-templates/{id}", r.handlers.getAgentTemplate)
}

type agentHandlers struct {
	service   agent.AgentService
	templates agentTemplateRepository
	totals    *agentTotalCache
}

func newAgentHandlers(service agent.AgentService, templates agentTemplateRepository) agentHandlers {
	return agentHandlers{
		service:   service,
		templates: templates,
		totals:    newAgentTotalCache(60 * time.Second),
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
	if agentType != "" && !isAllowedAgentType(agentType) {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "agent_type must be one of pm, worker, reviewer, general")
		return
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

	responder.JSON(w, http.StatusOK, toAgentResponse(updated))
}

func (h agentHandlers) pauseAgent(w http.ResponseWriter, r *http.Request) {
	h.transitionAgent(w, r, func(ctx context.Context, orgID, agentID uuid.UUID) error {
		return h.service.Pause(ctx, orgID, agentID)
	})
}

func (h agentHandlers) unpauseAgent(w http.ResponseWriter, r *http.Request) {
	h.transitionAgent(w, r, func(ctx context.Context, orgID, agentID uuid.UUID) error {
		return h.service.Unpause(ctx, orgID, agentID)
	})
}

func (h agentHandlers) retireAgent(w http.ResponseWriter, r *http.Request) {
	h.transitionAgent(w, r, func(ctx context.Context, orgID, agentID uuid.UUID) error {
		return h.service.Retire(ctx, orgID, agentID)
	})
}

func (h agentHandlers) cancelAgent(w http.ResponseWriter, r *http.Request) {
	h.transitionAgent(w, r, func(ctx context.Context, orgID, agentID uuid.UUID) error {
		return h.service.Cancel(ctx, orgID, agentID)
	})
}

func (h agentHandlers) transitionAgent(w http.ResponseWriter, r *http.Request, transition func(ctx context.Context, orgID, agentID uuid.UUID) error) {
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
