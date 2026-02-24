package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	agentdomain "github.com/samhotchkiss/otter-camp/internal/agent"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type agentTemplateStore interface {
	List(ctx context.Context, organizationID uuid.UUID, includeSystem bool) ([]repo.AgentProfileTemplate, error)
	Create(ctx context.Context, template repo.AgentProfileTemplate) (repo.AgentProfileTemplate, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.AgentProfileTemplate, error)
}

type AgentRouteRegistrar struct {
	agentService agentdomain.AgentService
	templates    agentTemplateStore
}

func NewAgentRouteRegistrar(agentService agentdomain.AgentService, templates agentTemplateStore) AgentRouteRegistrar {
	return AgentRouteRegistrar{
		agentService: agentService,
		templates:    templates,
	}
}

func (r AgentRouteRegistrar) RegisterRoutes(router chi.Router) {
	handlers := agentHandlers{
		service:   r.agentService,
		templates: r.templates,
	}

	router.Get("/agents", handlers.listAgents)
	router.Post("/agents", handlers.createAgent)
	router.Get("/agents/{id}", handlers.getAgent)
	router.Patch("/agents/{id}", handlers.updateAgent)
	router.Post("/agents/{id}/pause", handlers.pauseAgent)
	router.Post("/agents/{id}/unpause", handlers.unpauseAgent)
	router.Post("/agents/{id}/retire", handlers.retireAgent)
	router.Post("/agents/{id}/cancel", handlers.cancelAgent)

	router.Get("/agent-templates", handlers.listAgentTemplates)
	router.Post("/agent-templates", handlers.createAgentTemplate)
	router.Get("/agent-templates/{id}", handlers.getAgentTemplate)
}

type agentHandlers struct {
	service   agentdomain.AgentService
	templates agentTemplateStore
}

type createAgentRequest struct {
	DisplayName          string     `json:"display_name"`
	AgentClass           string     `json:"agent_class"`
	AgentType            string     `json:"agent_type"`
	SystemPrompt         string     `json:"system_prompt"`
	OperatorInstructions string     `json:"operator_instructions"`
	DefaultModelProfile  *string    `json:"default_model_profile_id"`
	ToolAllowList        []string   `json:"tool_allow_list"`
	ToolDenyList         []string   `json:"tool_deny_list"`
	MemoryReadScopes     []string   `json:"memory_read_scopes"`
	PrivateMemory        bool       `json:"private_memory"`
	BudgetCapTokens      *int64     `json:"budget_cap_tokens"`
	BudgetPeriod         *string    `json:"budget_period"`
	TempProjectID        *uuid.UUID `json:"temp_project_id"`
	TempTTLSeconds       *int32     `json:"temp_ttl_seconds"`
}

type updateAgentRequest struct {
	DisplayName          *string   `json:"display_name"`
	AgentType            *string   `json:"agent_type"`
	SystemPrompt         *string   `json:"system_prompt"`
	OperatorInstructions *string   `json:"operator_instructions"`
	DefaultModelProfile  *string   `json:"default_model_profile_id"`
	ToolAllowList        *[]string `json:"tool_allow_list"`
	ToolDenyList         *[]string `json:"tool_deny_list"`
	MemoryReadScopes     *[]string `json:"memory_read_scopes"`
	PrivateMemory        *bool     `json:"private_memory"`
	BudgetCapTokens      *int64    `json:"budget_cap_tokens"`
	BudgetPeriod         *string   `json:"budget_period"`
}

type createAgentTemplateRequest struct {
	DisplayName          string         `json:"display_name"`
	Source               string         `json:"source"`
	SourceAgentID        *uuid.UUID     `json:"source_agent_id"`
	SystemPrompt         string         `json:"system_prompt"`
	OperatorInstructions string         `json:"operator_instructions"`
	AgentType            string         `json:"agent_type"`
	DefaultModelProfile  *string        `json:"default_model_profile_id"`
	ToolAllowList        []string       `json:"tool_allow_list"`
	ToolDenyList         []string       `json:"tool_deny_list"`
	MemoryReadScopes     []string       `json:"memory_read_scopes"`
	PrivateMemory        bool           `json:"private_memory"`
	Metadata             map[string]any `json:"metadata"`
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
	TempExpiresAt         *string    `json:"temp_expires_at"`
	PromotedToAgentID     *uuid.UUID `json:"promoted_to_agent_id"`
	CreatedByType         string     `json:"created_by_type"`
	CreatedByID           uuid.UUID  `json:"created_by_id"`
	CreatedAt             string     `json:"created_at"`
	UpdatedAt             string     `json:"updated_at"`
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
	CreatedAt             string          `json:"created_at"`
	UpdatedAt             string          `json:"updated_at"`
}

func (h agentHandlers) listAgents(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	params := api.ParsePaginationParams(r.URL.Query())
	statusFilters := parseCSVSet(r.URL.Query().Get("lifecycle_status"))
	classFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("agent_class")))
	typeFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("agent_type")))

	items, err := h.service.List(r.Context(), principal.OrganizationID, agentdomain.AgentFilter{})
	if err != nil {
		status, code, message := mapAgentError(err)
		api.Error(w, status, code, message)
		return
	}

	filtered := make([]*repo.Agent, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		if len(statusFilters) > 0 && !statusFilters[strings.ToLower(item.LifecycleStatus)] {
			continue
		}
		if classFilter != "" && strings.ToLower(item.AgentClass) != classFilter {
			continue
		}
		if typeFilter != "" && strings.ToLower(item.AgentType) != typeFilter {
			continue
		}
		filtered = append(filtered, item)
	}

	start := 0
	if strings.TrimSpace(params.Cursor) != "" {
		cursorTime, cursorID, decodeErr := api.PaginationDecoder{}.Decode(params.Cursor)
		if decodeErr != nil {
			api.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "invalid cursor")
			return
		}

		start = len(filtered)
		for i, item := range filtered {
			if item.CreatedAt.After(cursorTime) {
				start = i
				break
			}
			if item.CreatedAt.Equal(cursorTime) && strings.Compare(item.ID.String(), cursorID.String()) > 0 {
				start = i
				break
			}
		}
	}

	end := start + params.Limit
	if end > len(filtered) {
		end = len(filtered)
	}

	page := filtered[start:end]
	response := make([]agentResponse, 0, len(page))
	for _, item := range page {
		response = append(response, toAgentResponse(*item))
	}

	var nextCursor *string
	if end < len(filtered) && len(page) > 0 {
		cursor := api.PaginationEncoder{}.Encode(page[len(page)-1].CreatedAt, page[len(page)-1].ID)
		nextCursor = &cursor
	}
	total := len(filtered)

	api.NewResponder(r.Context()).JSONList(w, http.StatusOK, response, api.PaginationMeta{
		NextCursor: nextCursor,
		Limit:      params.Limit,
		Total:      &total,
	})
}

func (h agentHandlers) createAgent(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	var req createAgentRequest
	if err := decodeJSON(r, &req); err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	if strings.TrimSpace(req.DisplayName) == "" {
		api.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "display_name is required")
		return
	}

	agentClass := strings.ToLower(strings.TrimSpace(req.AgentClass))
	if agentClass == "" {
		api.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "agent_class is required")
		return
	}

	switch agentClass {
	case "staff":
		if !isAdmin(principal.Role) {
			api.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
			return
		}

		created, err := h.service.Create(r.Context(), agentdomain.CreateAgentRequest{
			OrganizationID:        principal.OrganizationID,
			DisplayName:           req.DisplayName,
			SystemPrompt:          req.SystemPrompt,
			OperatorInstructions:  req.OperatorInstructions,
			AgentType:             req.AgentType,
			PrivateMemory:         req.PrivateMemory,
			MemoryReadScopes:      req.MemoryReadScopes,
			ToolAllowList:         req.ToolAllowList,
			ToolDenyList:          req.ToolDenyList,
			DefaultModelProfileID: req.DefaultModelProfile,
			BudgetCapTokens:       req.BudgetCapTokens,
			BudgetPeriod:          req.BudgetPeriod,
			CreatedByType:         "human_user",
			CreatedByID:           principal.UserID,
		})
		if err != nil {
			status, code, message := mapAgentError(err)
			api.Error(w, status, code, message)
			return
		}

		api.NewResponder(r.Context()).JSON(w, http.StatusCreated, toAgentResponse(*created))
		return
	case "temp":
		if !isAtLeastMember(principal.Role) {
			api.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
			return
		}
		if req.TempProjectID == nil || *req.TempProjectID == uuid.Nil {
			api.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "temp_project_id is required for temp agents")
			return
		}

		created, err := h.service.CreateTemp(r.Context(), principal.OrganizationID, agentdomain.CreateTempAgentRequest{
			DisplayName:           req.DisplayName,
			SystemPrompt:          req.SystemPrompt,
			OperatorInstructions:  req.OperatorInstructions,
			AgentType:             req.AgentType,
			PrivateMemory:         req.PrivateMemory,
			MemoryReadScopes:      req.MemoryReadScopes,
			ToolAllowList:         req.ToolAllowList,
			ToolDenyList:          req.ToolDenyList,
			DefaultModelProfileID: req.DefaultModelProfile,
			BudgetCapTokens:       req.BudgetCapTokens,
			BudgetPeriod:          req.BudgetPeriod,
			TempProjectID:         *req.TempProjectID,
			TempTTLSeconds:        req.TempTTLSeconds,
			CreatedByType:         "human_user",
			CreatedByID:           principal.UserID,
		})
		if err != nil {
			status, code, message := mapAgentError(err)
			api.Error(w, status, code, message)
			return
		}

		api.NewResponder(r.Context()).JSON(w, http.StatusCreated, toAgentResponse(*created))
		return
	default:
		api.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "agent_class must be one of: staff, temp")
	}
}

func (h agentHandlers) getAgent(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}

	found, err := h.service.Get(r.Context(), principal.OrganizationID, agentID)
	if err != nil {
		status, code, message := mapAgentError(err)
		api.Error(w, status, code, message)
		return
	}

	api.NewResponder(r.Context()).JSON(w, http.StatusOK, toAgentResponse(*found))
}

func (h agentHandlers) updateAgent(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if !isAdmin(principal.Role) {
		api.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	agentID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}

	var req updateAgentRequest
	if err := decodeJSON(r, &req); err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	updated, err := h.service.Update(r.Context(), principal.OrganizationID, agentID, agentdomain.UpdateAgentRequest{
		DisplayName:           req.DisplayName,
		SystemPrompt:          req.SystemPrompt,
		OperatorInstructions:  req.OperatorInstructions,
		AgentType:             req.AgentType,
		PrivateMemory:         req.PrivateMemory,
		MemoryReadScopes:      req.MemoryReadScopes,
		ToolAllowList:         req.ToolAllowList,
		ToolDenyList:          req.ToolDenyList,
		DefaultModelProfileID: req.DefaultModelProfile,
		BudgetCapTokens:       req.BudgetCapTokens,
		BudgetPeriod:          req.BudgetPeriod,
	})
	if err != nil {
		status, code, message := mapAgentError(err)
		api.Error(w, status, code, message)
		return
	}

	api.NewResponder(r.Context()).JSON(w, http.StatusOK, toAgentResponse(*updated))
}

func (h agentHandlers) pauseAgent(w http.ResponseWriter, r *http.Request) {
	h.handleTransition(w, r, func(ctx context.Context, orgID, agentID uuid.UUID) error {
		return h.service.Pause(ctx, orgID, agentID)
	})
}

func (h agentHandlers) unpauseAgent(w http.ResponseWriter, r *http.Request) {
	h.handleTransition(w, r, func(ctx context.Context, orgID, agentID uuid.UUID) error {
		return h.service.Unpause(ctx, orgID, agentID)
	})
}

func (h agentHandlers) retireAgent(w http.ResponseWriter, r *http.Request) {
	h.handleTransition(w, r, func(ctx context.Context, orgID, agentID uuid.UUID) error {
		return h.service.Retire(ctx, orgID, agentID)
	})
}

func (h agentHandlers) cancelAgent(w http.ResponseWriter, r *http.Request) {
	h.handleTransition(w, r, func(ctx context.Context, orgID, agentID uuid.UUID) error {
		return h.service.Cancel(ctx, orgID, agentID)
	})
}

func (h agentHandlers) handleTransition(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, orgID, agentID uuid.UUID) error) {
	if h.service == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if !isAdmin(principal.Role) {
		api.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	agentID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent id")
		return
	}

	if err := fn(r.Context(), principal.OrganizationID, agentID); err != nil {
		status, code, message := mapAgentError(err)
		api.Error(w, status, code, message)
		return
	}

	updated, err := h.service.Get(r.Context(), principal.OrganizationID, agentID)
	if err != nil {
		status, code, message := mapAgentError(err)
		api.Error(w, status, code, message)
		return
	}

	api.NewResponder(r.Context()).JSON(w, http.StatusOK, toAgentResponse(*updated))
}

func (h agentHandlers) listAgentTemplates(w http.ResponseWriter, r *http.Request) {
	if h.templates == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent template repository unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	templates, err := h.templates.List(r.Context(), principal.OrganizationID, true)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list agent templates")
		return
	}

	response := make([]agentTemplateResponse, 0, len(templates))
	for _, item := range templates {
		response = append(response, toAgentTemplateResponse(item))
	}
	api.NewResponder(r.Context()).JSON(w, http.StatusOK, response)
}

func (h agentHandlers) createAgentTemplate(w http.ResponseWriter, r *http.Request) {
	if h.templates == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent template repository unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if !isAdmin(principal.Role) {
		api.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	var req createAgentTemplateRequest
	if err := decodeJSON(r, &req); err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.DisplayName) == "" {
		api.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "display_name is required")
		return
	}

	metadata := json.RawMessage(`{}`)
	if len(req.Metadata) > 0 {
		encoded, err := json.Marshal(req.Metadata)
		if err != nil {
			api.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "metadata must be valid JSON")
			return
		}
		metadata = encoded
	}

	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "org"
	}

	orgID := principal.OrganizationID
	created, err := h.templates.Create(r.Context(), repo.AgentProfileTemplate{
		OrganizationID:        &orgID,
		DisplayName:           strings.TrimSpace(req.DisplayName),
		Source:                source,
		SourceAgentID:         req.SourceAgentID,
		SystemPrompt:          req.SystemPrompt,
		OperatorInstructions:  req.OperatorInstructions,
		AgentType:             req.AgentType,
		DefaultModelProfileID: req.DefaultModelProfile,
		ToolAllowList:         slices.Clone(req.ToolAllowList),
		ToolDenyList:          slices.Clone(req.ToolDenyList),
		MemoryReadScopes:      slices.Clone(req.MemoryReadScopes),
		PrivateMemory:         req.PrivateMemory,
		Metadata:              metadata,
	})
	if err != nil {
		if errors.Is(err, repo.ErrConflict) {
			api.Error(w, http.StatusConflict, api.ErrCodeConflict, err.Error())
			return
		}
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to create agent template")
		return
	}

	api.NewResponder(r.Context()).JSON(w, http.StatusCreated, toAgentTemplateResponse(created))
}

func (h agentHandlers) getAgentTemplate(w http.ResponseWriter, r *http.Request) {
	if h.templates == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent template repository unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	templateID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid template id")
		return
	}

	template, err := h.templates.GetByID(r.Context(), templateID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			api.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "template not found")
			return
		}
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to fetch agent template")
		return
	}

	if template.OrganizationID != nil && *template.OrganizationID != principal.OrganizationID {
		api.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "template not found")
		return
	}

	api.NewResponder(r.Context()).JSON(w, http.StatusOK, toAgentTemplateResponse(template))
}

func toAgentResponse(agent repo.Agent) agentResponse {
	var tempExpiresAt *string
	if agent.TempExpiresAt != nil {
		value := agent.TempExpiresAt.UTC().Format(time.RFC3339Nano)
		tempExpiresAt = &value
	}

	return agentResponse{
		ID:                    agent.ID,
		OrganizationID:        agent.OrganizationID,
		DisplayName:           agent.DisplayName,
		AgentClass:            agent.AgentClass,
		LifecycleStatus:       agent.LifecycleStatus,
		SystemPrompt:          agent.SystemPrompt,
		OperatorInstructions:  agent.OperatorInstructions,
		AgentType:             agent.AgentType,
		IsStarterTrio:         agent.IsStarterTrio,
		PrivateMemory:         agent.PrivateMemory,
		MemoryReadScopes:      slices.Clone(agent.MemoryReadScopes),
		ToolAllowList:         slices.Clone(agent.ToolAllowList),
		ToolDenyList:          slices.Clone(agent.ToolDenyList),
		DefaultModelProfileID: agent.DefaultModelProfileID,
		BudgetCapTokens:       agent.BudgetCapTokens,
		BudgetPeriod:          agent.BudgetPeriod,
		TempProjectID:         agent.TempProjectID,
		TempTTLSeconds:        agent.TempTTLSeconds,
		TempExpiresAt:         tempExpiresAt,
		PromotedToAgentID:     agent.PromotedToAgentID,
		CreatedByType:         agent.CreatedByType,
		CreatedByID:           agent.CreatedByID,
		CreatedAt:             agent.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:             agent.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func toAgentTemplateResponse(template repo.AgentProfileTemplate) agentTemplateResponse {
	metadata := template.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	return agentTemplateResponse{
		ID:                    template.ID,
		OrganizationID:        template.OrganizationID,
		DisplayName:           template.DisplayName,
		Source:                template.Source,
		SourceAgentID:         template.SourceAgentID,
		SystemPrompt:          template.SystemPrompt,
		OperatorInstructions:  template.OperatorInstructions,
		AgentType:             template.AgentType,
		DefaultModelProfileID: template.DefaultModelProfileID,
		ToolAllowList:         slices.Clone(template.ToolAllowList),
		ToolDenyList:          slices.Clone(template.ToolDenyList),
		MemoryReadScopes:      slices.Clone(template.MemoryReadScopes),
		PrivateMemory:         template.PrivateMemory,
		Metadata:              metadata,
		CreatedAt:             template.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:             template.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func mapAgentError(err error) (int, string, string) {
	switch {
	case errors.Is(err, agentdomain.ErrInvalidTransition):
		return http.StatusConflict, "invalid_transition", err.Error()
	case errors.Is(err, agentdomain.ErrInvalidForTempAgent):
		return http.StatusUnprocessableEntity, "invalid_for_temp_agent", err.Error()
	case errors.Is(err, agentdomain.ErrConcurrentTempLimitReached):
		return http.StatusTooManyRequests, "temp_limit_reached", err.Error()
	case errors.Is(err, agentdomain.ErrStarterTrioProtected):
		return http.StatusForbidden, api.ErrCodeForbidden, err.Error()
	case errors.Is(err, repo.ErrNotFound):
		return http.StatusNotFound, api.ErrCodeNotFound, "resource not found"
	case errors.Is(err, repo.ErrConflict):
		return http.StatusConflict, api.ErrCodeConflict, err.Error()
	case errors.Is(err, agentdomain.ErrDisplayNameRequired),
		errors.Is(err, agentdomain.ErrOrganizationIDRequired),
		errors.Is(err, agentdomain.ErrTempProjectIDRequired),
		errors.Is(err, agentdomain.ErrInvalidCreatedByType),
		errors.Is(err, agentdomain.ErrCreatedByIDRequired):
		return http.StatusBadRequest, api.ErrCodeValidation, err.Error()
	default:
		return http.StatusInternalServerError, api.ErrCodeInternal, "agent request failed"
	}
}

func parseCSVSet(raw string) map[string]bool {
	values := make(map[string]bool)
	for _, item := range strings.Split(raw, ",") {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if normalized == "" {
			continue
		}
		values[normalized] = true
	}
	return values
}

func isAtLeastMember(role string) bool {
	normalized := strings.ToLower(strings.TrimSpace(role))
	return normalized == "admin" || normalized == "member"
}

func isAdmin(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), "admin")
}
