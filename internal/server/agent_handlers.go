package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	agentsvc "github.com/samhotchkiss/otter-camp/internal/agent"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	agentClassStaff = "staff"
	agentClassTemp  = "temp"
)

type AgentTemplateRepository interface {
	Create(ctx context.Context, template repo.AgentProfileTemplate) (repo.AgentProfileTemplate, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.AgentProfileTemplate, error)
	List(ctx context.Context, organizationID uuid.UUID, includeSystem bool) ([]repo.AgentProfileTemplate, error)
}

type agentRouteRegistrar struct {
	agentService agentsvc.AgentService
	templateRepo AgentTemplateRepository
}

func NewAgentRouteRegistrar(agentService agentsvc.AgentService, templateRepo AgentTemplateRepository) RouteRegistrar {
	return &agentRouteRegistrar{
		agentService: agentService,
		templateRepo: templateRepo,
	}
}

func (h *agentRouteRegistrar) RegisterRoutes(r chi.Router) {
	r.Route("/agents", func(agents chi.Router) {
		agents.Get("/", h.listAgents)
		agents.Post("/", h.createAgent)
		agents.Get("/{id}", h.getAgent)

		agents.With(middleware.RequireRole("admin")).Patch("/{id}", h.updateAgent)
		agents.With(middleware.RequireRole("admin")).Post("/{id}/pause", h.pauseAgent)
		agents.With(middleware.RequireRole("admin")).Post("/{id}/unpause", h.unpauseAgent)
		agents.With(middleware.RequireRole("admin")).Post("/{id}/retire", h.retireAgent)
		agents.With(middleware.RequireRole("admin")).Post("/{id}/cancel", h.cancelAgent)
	})

	r.Route("/agent-templates", func(templates chi.Router) {
		templates.Get("/", h.listAgentTemplates)
		templates.With(middleware.RequireRole("admin")).Post("/", h.createAgentTemplate)
		templates.Get("/{id}", h.getAgentTemplate)
	})
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

func (h *agentRouteRegistrar) listAgents(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.agentService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	query := r.URL.Query()
	rawClass := strings.ToLower(strings.TrimSpace(query.Get("agent_class")))
	if rawClass != "" && rawClass != agentClassStaff && rawClass != agentClassTemp {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "agent_class must be staff or temp")
		return
	}

	statusFilter := parseStatusFilter(query.Get("lifecycle_status"))
	serviceFilter := agentsvc.AgentFilter{AgentClass: rawClass}
	if len(statusFilter) == 1 {
		for status := range statusFilter {
			serviceFilter.LifecycleStatus = status
		}
	}

	agents, err := h.agentService.List(r.Context(), principal.OrganizationID, serviceFilter)
	if err != nil {
		writeAgentError(w, r, err)
		return
	}

	agentTypeFilter := strings.ToLower(strings.TrimSpace(query.Get("agent_type")))
	filtered := make([]*agentsvc.Agent, 0, len(agents))
	for _, item := range agents {
		if item == nil {
			continue
		}
		if len(statusFilter) > 0 {
			if _, ok := statusFilter[strings.ToLower(item.LifecycleStatus)]; !ok {
				continue
			}
		}
		if agentTypeFilter != "" && strings.ToLower(strings.TrimSpace(item.AgentType)) != agentTypeFilter {
			continue
		}
		filtered = append(filtered, item)
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return bytes.Compare(filtered[i].ID[:], filtered[j].ID[:]) < 0
		}
		return filtered[i].CreatedAt.Before(filtered[j].CreatedAt)
	})

	params := api.ParsePaginationParams(query)
	start := 0
	if params.Cursor != "" {
		cursorTime, cursorID, decodeErr := (api.PaginationDecoder{}).Decode(params.Cursor)
		if decodeErr != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
			return
		}
		for start < len(filtered) && !isAfterCursor(filtered[start], cursorTime, cursorID) {
			start++
		}
	}

	end := start + params.Limit
	if end > len(filtered) {
		end = len(filtered)
	}

	page := make([]agentResponse, 0, end-start)
	for _, item := range filtered[start:end] {
		page = append(page, toAgentResponse(item))
	}

	var nextCursor *string
	if end < len(filtered) && end > start {
		encoded := (api.PaginationEncoder{}).Encode(filtered[end-1].CreatedAt, filtered[end-1].ID)
		nextCursor = &encoded
	}

	total := len(filtered)
	responder.JSONList(w, http.StatusOK, page, api.PaginationMeta{
		NextCursor: nextCursor,
		Limit:      params.Limit,
		Total:      &total,
	})
}

func (h *agentRouteRegistrar) createAgent(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.agentService == nil {
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
		responder.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "invalid request body")
		return
	}

	if strings.TrimSpace(req.DisplayName) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "display_name is required")
		return
	}
	agentClass := strings.ToLower(strings.TrimSpace(req.AgentClass))
	if agentClass == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "agent_class is required")
		return
	}

	switch agentClass {
	case agentClassStaff:
		if !isAdminRole(principal.Role) {
			responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
			return
		}
		created, err := h.agentService.Create(r.Context(), agentsvc.CreateAgentRequest{
			OrganizationID:        principal.OrganizationID,
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
			BudgetPeriod:          req.BudgetPeriod,
			CreatedByType:         "human_user",
			CreatedByID:           principal.UserID,
		})
		if err != nil {
			writeAgentError(w, r, err)
			return
		}
		responder.JSON(w, http.StatusCreated, toAgentResponse(created))
		return
	case agentClassTemp:
		if !canCreateTempRole(principal.Role) {
			responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
			return
		}
		var tempProjectID uuid.UUID
		if req.TempProjectID != nil {
			tempProjectID = *req.TempProjectID
		}
		created, err := h.agentService.CreateTemp(r.Context(), principal.OrganizationID, agentsvc.CreateTempAgentRequest{
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
			BudgetPeriod:          req.BudgetPeriod,
			TempProjectID:         tempProjectID,
			TempTTLSeconds:        req.TempTTLSeconds,
			CreatedByType:         "human_user",
			CreatedByID:           principal.UserID,
		})
		if err != nil {
			writeAgentError(w, r, err)
			return
		}
		responder.JSON(w, http.StatusCreated, toAgentResponse(created))
		return
	default:
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "agent_class must be staff or temp")
		return
	}
}

func (h *agentRouteRegistrar) getAgent(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.agentService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "invalid agent id")
		return
	}

	item, err := h.agentService.Get(r.Context(), principal.OrganizationID, agentID)
	if err != nil {
		writeAgentError(w, r, err)
		return
	}
	responder.JSON(w, http.StatusOK, toAgentResponse(item))
}

func (h *agentRouteRegistrar) updateAgent(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.agentService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "invalid agent id")
		return
	}

	var req updateAgentRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "invalid request body")
		return
	}

	updated, err := h.agentService.Update(r.Context(), principal.OrganizationID, agentID, agentsvc.UpdateAgentRequest{
		DisplayName:           req.DisplayName,
		AgentType:             req.AgentType,
		SystemPrompt:          req.SystemPrompt,
		OperatorInstructions:  req.OperatorInstructions,
		DefaultModelProfileID: req.DefaultModelProfileID,
		ToolAllowList:         req.ToolAllowList,
		ToolDenyList:          req.ToolDenyList,
		MemoryReadScopes:      req.MemoryReadScopes,
		PrivateMemory:         req.PrivateMemory,
		BudgetCapTokens:       req.BudgetCapTokens,
		BudgetPeriod:          req.BudgetPeriod,
	})
	if err != nil {
		writeAgentError(w, r, err)
		return
	}
	responder.JSON(w, http.StatusOK, toAgentResponse(updated))
}

func (h *agentRouteRegistrar) pauseAgent(w http.ResponseWriter, r *http.Request) {
	h.transitionAgent(w, r, "pause")
}

func (h *agentRouteRegistrar) unpauseAgent(w http.ResponseWriter, r *http.Request) {
	h.transitionAgent(w, r, "unpause")
}

func (h *agentRouteRegistrar) retireAgent(w http.ResponseWriter, r *http.Request) {
	h.transitionAgent(w, r, "retire")
}

func (h *agentRouteRegistrar) cancelAgent(w http.ResponseWriter, r *http.Request) {
	h.transitionAgent(w, r, "cancel")
}

func (h *agentRouteRegistrar) transitionAgent(w http.ResponseWriter, r *http.Request, action string) {
	responder := api.NewResponder(r.Context())
	if h.agentService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	agentID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "invalid agent id")
		return
	}

	switch action {
	case "pause":
		err = h.agentService.Pause(r.Context(), principal.OrganizationID, agentID)
	case "unpause":
		err = h.agentService.Unpause(r.Context(), principal.OrganizationID, agentID)
	case "retire":
		err = h.agentService.Retire(r.Context(), principal.OrganizationID, agentID)
	case "cancel":
		err = h.agentService.Cancel(r.Context(), principal.OrganizationID, agentID)
	default:
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "invalid lifecycle action")
		return
	}
	if err != nil {
		writeAgentError(w, r, err)
		return
	}

	responder.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *agentRouteRegistrar) listAgentTemplates(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.templateRepo == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent template repository unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	templates, err := h.templateRepo.List(r.Context(), principal.OrganizationID, true)
	if err != nil {
		writeAgentError(w, r, err)
		return
	}

	response := make([]agentTemplateResponse, 0, len(templates))
	for _, item := range templates {
		if item.OrganizationID != nil && *item.OrganizationID != principal.OrganizationID {
			continue
		}
		response = append(response, toAgentTemplateResponse(item))
	}
	responder.JSON(w, http.StatusOK, response)
}

func (h *agentRouteRegistrar) createAgentTemplate(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.templateRepo == nil {
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
		responder.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "invalid request body")
		return
	}
	if strings.TrimSpace(req.DisplayName) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "display_name is required")
		return
	}
	if strings.TrimSpace(req.AgentType) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "agent_type is required")
		return
	}

	source := strings.ToLower(strings.TrimSpace(req.Source))
	if source == "" {
		source = "org"
	}
	if source != "org" && source != "system" && source != "promoted" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "source must be system, org, or promoted")
		return
	}

	orgID := principal.OrganizationID
	template, err := h.templateRepo.Create(r.Context(), repo.AgentProfileTemplate{
		OrganizationID:        &orgID,
		DisplayName:           req.DisplayName,
		Source:                source,
		SourceAgentID:         req.SourceAgentID,
		SystemPrompt:          req.SystemPrompt,
		OperatorInstructions:  req.OperatorInstructions,
		AgentType:             req.AgentType,
		DefaultModelProfileID: req.DefaultModelProfileID,
		ToolAllowList:         req.ToolAllowList,
		ToolDenyList:          req.ToolDenyList,
		MemoryReadScopes:      req.MemoryReadScopes,
		PrivateMemory:         req.PrivateMemory,
		Metadata:              req.Metadata,
	})
	if err != nil {
		writeAgentError(w, r, err)
		return
	}

	responder.JSON(w, http.StatusCreated, toAgentTemplateResponse(template))
}

func (h *agentRouteRegistrar) getAgentTemplate(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.templateRepo == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "agent template repository unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	templateID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "invalid agent template id")
		return
	}

	item, err := h.templateRepo.GetByID(r.Context(), templateID)
	if err != nil {
		writeAgentError(w, r, err)
		return
	}
	if item.OrganizationID != nil && *item.OrganizationID != principal.OrganizationID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeResourceNotFound, "resource not found")
		return
	}

	responder.JSON(w, http.StatusOK, toAgentTemplateResponse(item))
}

func parseStatusFilter(raw string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, segment := range strings.Split(raw, ",") {
		status := strings.ToLower(strings.TrimSpace(segment))
		if status == "" {
			continue
		}
		out[status] = struct{}{}
	}
	return out
}

func isAfterCursor(item *agentsvc.Agent, cursorTime time.Time, cursorID uuid.UUID) bool {
	if item.CreatedAt.After(cursorTime) {
		return true
	}
	if item.CreatedAt.Before(cursorTime) {
		return false
	}
	return bytes.Compare(item.ID[:], cursorID[:]) > 0
}

func isAdminRole(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), "admin")
}

func canCreateTempRole(role string) bool {
	normalized := strings.ToLower(strings.TrimSpace(role))
	return normalized == "admin" || normalized == "member"
}

func writeAgentError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := mapAgentError(err)
	api.NewResponder(r.Context()).Error(w, status, code, message)
}

func mapAgentError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, agentsvc.ErrInvalidTransition):
		return http.StatusConflict, "invalid_transition", err.Error()
	case errors.Is(err, agentsvc.ErrInvalidForTempAgent):
		return http.StatusUnprocessableEntity, "invalid_for_temp_agent", err.Error()
	case errors.Is(err, agentsvc.ErrConcurrentTempLimitReached):
		return http.StatusTooManyRequests, "temp_limit_reached", err.Error()
	case errors.Is(err, agentsvc.ErrStarterTrioProtected):
		return http.StatusForbidden, "starter_trio_protected", err.Error()
	case errors.Is(err, repo.ErrNotFound):
		return http.StatusNotFound, api.ErrCodeResourceNotFound, "resource not found"
	case errors.Is(err, repo.ErrConflict):
		return http.StatusConflict, api.ErrCodeConflict, "conflict"
	case errors.Is(err, agentsvc.ErrDisplayNameRequired),
		errors.Is(err, agentsvc.ErrOrganizationIDRequired),
		errors.Is(err, agentsvc.ErrTempProjectIDRequired),
		errors.Is(err, agentsvc.ErrInvalidCreatedByType),
		errors.Is(err, agentsvc.ErrCreatedByIDRequired):
		return http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error()
	default:
		return http.StatusInternalServerError, api.ErrCodeInternal, "internal server error"
	}
}

func toAgentResponse(item *agentsvc.Agent) agentResponse {
	return agentResponse{
		ID:                    item.ID,
		OrganizationID:        item.OrganizationID,
		DisplayName:           item.DisplayName,
		AgentClass:            item.AgentClass,
		LifecycleStatus:       item.LifecycleStatus,
		SystemPrompt:          item.SystemPrompt,
		OperatorInstructions:  item.OperatorInstructions,
		AgentType:             item.AgentType,
		IsStarterTrio:         item.IsStarterTrio,
		PrivateMemory:         item.PrivateMemory,
		MemoryReadScopes:      append([]string(nil), item.MemoryReadScopes...),
		ToolAllowList:         append([]string(nil), item.ToolAllowList...),
		ToolDenyList:          append([]string(nil), item.ToolDenyList...),
		DefaultModelProfileID: item.DefaultModelProfileID,
		BudgetCapTokens:       item.BudgetCapTokens,
		BudgetPeriod:          item.BudgetPeriod,
		TempProjectID:         item.TempProjectID,
		TempTTLSeconds:        item.TempTTLSeconds,
		CreatedByType:         item.CreatedByType,
		CreatedByID:           item.CreatedByID,
		CreatedAt:             item.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:             item.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func toAgentTemplateResponse(item repo.AgentProfileTemplate) agentTemplateResponse {
	return agentTemplateResponse{
		ID:                    item.ID,
		OrganizationID:        item.OrganizationID,
		DisplayName:           item.DisplayName,
		Source:                item.Source,
		SourceAgentID:         item.SourceAgentID,
		SystemPrompt:          item.SystemPrompt,
		OperatorInstructions:  item.OperatorInstructions,
		AgentType:             item.AgentType,
		DefaultModelProfileID: item.DefaultModelProfileID,
		ToolAllowList:         append([]string(nil), item.ToolAllowList...),
		ToolDenyList:          append([]string(nil), item.ToolDenyList...),
		MemoryReadScopes:      append([]string(nil), item.MemoryReadScopes...),
		PrivateMemory:         item.PrivateMemory,
		Metadata:              append([]byte(nil), item.Metadata...),
		CreatedAt:             item.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:             item.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}
