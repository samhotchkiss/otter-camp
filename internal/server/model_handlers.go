package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	defaultModelProfileContextWindow = 200000
	defaultModelProfileMaxTokens     = 4096
	defaultInvocationPurpose         = "agent_turn"
)

type modelProviderRepository interface {
	List(ctx context.Context) ([]repo.ModelProvider, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ModelProvider, error)
	Update(ctx context.Context, provider repo.ModelProvider) (repo.ModelProvider, error)
}

type providerConnectionRepository interface {
	Create(ctx context.Context, connection repo.ProviderConnection) (repo.ProviderConnection, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProviderConnection, error)
	ListByProvider(ctx context.Context, organizationID, providerID uuid.UUID) ([]repo.ProviderConnection, error)
	Update(ctx context.Context, connection repo.ProviderConnection) (repo.ProviderConnection, error)
	SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) (repo.ProviderConnection, error)
}

type modelProfileRepository interface {
	Create(ctx context.Context, profile repo.ModelProfile) (repo.ModelProfile, error)
	GetCurrentByLogicalID(ctx context.Context, organizationID uuid.UUID, logicalProfileID string) (repo.ModelProfile, error)
	ListCurrent(ctx context.Context, organizationID uuid.UUID) ([]repo.ModelProfile, error)
	ListCurrentByProvider(ctx context.Context, organizationID, providerID uuid.UUID) ([]repo.ModelProfile, error)
	ListHistoryByLogicalID(ctx context.Context, organizationID uuid.UUID, logicalProfileID string) ([]repo.ModelProfile, error)
	Deprecate(ctx context.Context, currentID uuid.UUID, next repo.ModelProfile) (repo.ModelProfile, error)
}

type modelProfileAssignmentRepository interface {
	Upsert(ctx context.Context, assignment repo.ModelProfileAssignment) (repo.ModelProfileAssignment, error)
	GetByScope(ctx context.Context, organizationID uuid.UUID, scopeType string, scopeID uuid.UUID, invocationPurpose string) (repo.ModelProfileAssignment, error)
	ListByOrg(ctx context.Context, organizationID uuid.UUID) ([]repo.ModelProfileAssignment, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type modelInvocationRepository interface {
	ListByOrg(ctx context.Context, organizationID uuid.UUID) ([]repo.ModelInvocation, error)
	ListByRun(ctx context.Context, organizationID, runID uuid.UUID) ([]repo.ModelInvocation, error)
	ListBySession(ctx context.Context, organizationID, sessionID uuid.UUID) ([]repo.ModelInvocation, error)
}

type ModelRouteRegistrar struct {
	handlers modelHandlers
}

func NewModelRouteRegistrar(pool *pgxpool.Pool) *ModelRouteRegistrar {
	handlers := modelHandlers{pool: pool}
	if pool != nil {
		handlers.providers = repo.NewModelProviderRepo(pool)
		handlers.connections = repo.NewProviderConnectionRepo(pool)
		handlers.profiles = repo.NewModelProfileRepo(pool)
		handlers.assignments = repo.NewModelProfileAssignmentRepo(pool)
		handlers.invocations = repo.NewModelInvocationRepo(pool)
	}
	return &ModelRouteRegistrar{handlers: handlers}
}

func (r *ModelRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.Get("/model/providers", r.handlers.listProviders)
	router.Get("/model/providers/{id}", r.handlers.getProvider)
	router.With(middleware.RequireRole("admin")).Patch("/model/providers/{id}", r.handlers.patchProvider)
	router.Get("/model/providers/{id}/connections", r.handlers.listProviderConnections)
	router.With(middleware.RequireRole("admin")).Post("/model/providers/{id}/connections", r.handlers.createProviderConnection)
	router.With(middleware.RequireRole("admin")).Patch("/model/providers/{id}/connections/{cid}", r.handlers.patchProviderConnection)
	router.With(middleware.RequireRole("admin")).Delete("/model/providers/{id}/connections/{cid}", r.handlers.deleteProviderConnection)

	router.Get("/model/profiles", r.handlers.listProfiles)
	router.With(middleware.RequireRole("admin")).Post("/model/profiles", r.handlers.createProfile)
	router.With(middleware.RequireRole("admin")).Patch("/model/profiles/{logical_profile_id}", r.handlers.patchProfile)
	router.Get("/model/profiles/{logical_profile_id}", r.handlers.getProfile)
	router.Get("/model/profiles/{logical_profile_id}/history", r.handlers.profileHistory)

	router.Get("/model/assignments", r.handlers.listAssignments)
	router.With(middleware.RequireRole("admin")).Put("/model/assignments/{scope_type}/{scope_id}", r.handlers.putAssignment)
	router.With(middleware.RequireRole("admin")).Delete("/model/assignments/{scope_type}/{scope_id}", r.handlers.deleteAssignment)
	router.Get("/model/invocations", r.handlers.listInvocations)
	router.Get("/model/usage-rollup", r.handlers.getUsageRollup)

	router.Get("/usage", r.handlers.getUsage)
	router.Get("/usage/summary", r.handlers.getUsageSummary)
	router.Get("/model/usage-rollup", r.handlers.getUsage) // spec alias
}

type modelHandlers struct {
	pool        *pgxpool.Pool
	providers   modelProviderRepository
	connections providerConnectionRepository
	profiles    modelProfileRepository
	assignments modelProfileAssignmentRepository
	invocations modelInvocationRepository
}

type patchProviderRequest struct {
	DisplayName *string `json:"display_name"`
	IsEnabled   *bool   `json:"is_enabled"`
	Notes       *string `json:"notes"`
}

type providerConnectionRequest struct {
	DisplayName      string  `json:"display_name"`
	APIKeySecretRef  string  `json:"api_key_secret_ref"`
	BaseURL          *string `json:"base_url"`
	FailoverPriority *int    `json:"failover_priority"`
	MaxConcurrent    *int    `json:"max_concurrent"`
}

type patchProviderConnectionRequest struct {
	DisplayName      *string `json:"display_name"`
	APIKeySecretRef  *string `json:"api_key_secret_ref"`
	FailoverPriority *int    `json:"failover_priority"`
	MaxConcurrent    *int    `json:"max_concurrent"`
	IsEnabled        *bool   `json:"is_enabled"`
}

type createProfileRequest struct {
	DisplayName       string   `json:"display_name"`
	ProviderID        string   `json:"provider_id"`
	ModelName         string   `json:"model_name"`
	Temperature       *float64 `json:"temperature"`
	MaxTokens         *int     `json:"max_tokens"`
	FallbackProfileID *string  `json:"fallback_profile_id"`
}

type patchProfileRequest struct {
	DisplayName       *string  `json:"display_name"`
	ProviderID        *string  `json:"provider_id"`
	ModelName         *string  `json:"model_name"`
	Temperature       *float64 `json:"temperature"`
	MaxTokens         *int     `json:"max_tokens"`
	FallbackProfileID *string  `json:"fallback_profile_id"`
}

type putAssignmentRequest struct {
	LogicalProfileID string `json:"logical_profile_id"`
}

type providerResponse struct {
	ID                uuid.UUID `json:"id"`
	Slug              string    `json:"slug"`
	DisplayName       string    `json:"display_name"`
	APIBaseURL        string    `json:"api_base_url"`
	SupportedFeatures []string  `json:"supported_features"`
	IsEnabled         bool      `json:"is_enabled"`
	Notes             *string   `json:"notes"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type providerConnectionResponse struct {
	ID               uuid.UUID `json:"id"`
	OrganizationID   uuid.UUID `json:"organization_id"`
	ProviderID       uuid.UUID `json:"provider_id"`
	DisplayName      string    `json:"display_name"`
	APIKeySecretRef  string    `json:"api_key_secret_ref"`
	BaseURL          *string   `json:"base_url"`
	FailoverPriority int       `json:"failover_priority"`
	MaxConcurrent    int       `json:"max_concurrent"`
	HealthStatus     string    `json:"health_status"`
	IsEnabled        bool      `json:"is_enabled"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type profileResponse struct {
	ID                uuid.UUID  `json:"id"`
	LogicalProfileID  string     `json:"logical_profile_id"`
	Version           int        `json:"version"`
	DisplayName       string     `json:"display_name"`
	ProviderID        uuid.UUID  `json:"provider_id"`
	ModelName         string     `json:"model_name"`
	Temperature       *float64   `json:"temperature"`
	MaxTokens         int        `json:"max_tokens"`
	FallbackProfileID *string    `json:"fallback_profile_id"`
	IsCurrent         bool       `json:"is_current"`
	OrganizationID    *uuid.UUID `json:"organization_id"`
	CreatedAt         time.Time  `json:"created_at"`
}

type assignmentResponse struct {
	ScopeType         string    `json:"scope_type"`
	ScopeID           uuid.UUID `json:"scope_id"`
	LogicalProfileID  string    `json:"logical_profile_id"`
	InvocationPurpose string    `json:"invocation_purpose"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type invocationResponse struct {
	ID                uuid.UUID       `json:"id"`
	OrganizationID    uuid.UUID       `json:"organization_id"`
	ModelProviderID   uuid.UUID       `json:"model_provider_id"`
	ModelProfileID    *string         `json:"model_profile_id"`
	InvocationPurpose string          `json:"invocation_purpose"`
	Status            string          `json:"status"`
	InputTokens       *int            `json:"input_tokens"`
	OutputTokens      *int            `json:"output_tokens"`
	ModelName         string          `json:"model_name"`
	Metadata          json.RawMessage `json:"metadata"`
	SessionID         *uuid.UUID      `json:"session_id"`
	TurnID            *uuid.UUID      `json:"turn_id"`
	RunID             *uuid.UUID      `json:"run_id"`
	CreatedAt         time.Time       `json:"created_at"`
	CompletedAt       *time.Time      `json:"completed_at"`
}

type usageResponse struct {
	Data []usageRow `json:"data"`
	Meta usageMeta  `json:"meta"`
}

type usageSummaryResponse struct {
	PeriodStart         string `json:"period_start"`
	PeriodEnd           string `json:"period_end"`
	TotalInvocations    int64  `json:"total_invocations"`
	TotalInputTokens    int64  `json:"total_input_tokens"`
	TotalOutputTokens   int64  `json:"total_output_tokens"`
	TotalCostMicrocents int64  `json:"total_cost_microcents"`
}

type usageRow struct {
	RollupType          string     `json:"rollup_type"`
	RollupID            *uuid.UUID `json:"rollup_id"`
	DisplayName         *string    `json:"display_name,omitempty"`
	RollupDate          string     `json:"rollup_date"`
	TotalInvocations    int64      `json:"total_invocations"`
	TotalInputTokens    int64      `json:"total_input_tokens"`
	TotalOutputTokens   int64      `json:"total_output_tokens"`
	TotalCostMicrocents int64      `json:"total_cost_microcents"`
}

type usageMeta struct {
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	TotalRows   int    `json:"total_rows"`
}

func (h modelHandlers) listProviders(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if _, ok := h.requirePrincipal(w, r); !ok {
		return
	}
	if h.providers == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model provider repository unavailable")
		return
	}

	items, err := h.providers.List(r.Context())
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	params := api.ParsePaginationParams(r.URL.Query())
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			if params.Order == "asc" {
				return items[i].ID.String() < items[j].ID.String()
			}
			return items[i].ID.String() > items[j].ID.String()
		}
		if params.Order == "asc" {
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	page, nextCursor, err := paginateByTimeID(items, params, func(item repo.ModelProvider) time.Time {
		return item.CreatedAt
	}, func(item repo.ModelProvider) uuid.UUID {
		return item.ID
	})
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
		return
	}

	response := make([]providerResponse, 0, len(page))
	for _, item := range page {
		response = append(response, toProviderResponse(item))
	}
	total := len(items)
	responder.JSONList(w, http.StatusOK, response, api.PaginationMeta{
		NextCursor: nextCursor,
		Limit:      params.Limit,
		Total:      &total,
	})
}

func (h modelHandlers) getProvider(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if _, ok := h.requirePrincipal(w, r); !ok {
		return
	}
	if h.providers == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model provider repository unavailable")
		return
	}
	providerID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "invalid provider id")
		return
	}
	item, err := h.providers.GetByID(r.Context(), providerID)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusOK, toProviderResponse(item))
}

func (h modelHandlers) patchProvider(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if _, ok := h.requirePrincipal(w, r); !ok {
		return
	}
	if h.providers == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model provider repository unavailable")
		return
	}

	providerID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}

	current, err := h.providers.GetByID(r.Context(), providerID)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	var req patchProviderRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	if req.DisplayName != nil {
		trimmed := strings.TrimSpace(*req.DisplayName)
		if trimmed == "" {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "display_name is required")
			return
		}
		current.DisplayName = trimmed
	}
	if req.IsEnabled != nil {
		current.IsEnabled = *req.IsEnabled
	}

	metadata := metadataMap(current.Metadata)
	if req.Notes != nil {
		trimmed := strings.TrimSpace(*req.Notes)
		if trimmed == "" {
			delete(metadata, "notes")
		} else {
			metadata["notes"] = trimmed
		}
		encoded, marshalErr := json.Marshal(metadata)
		if marshalErr != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
			return
		}
		current.Metadata = encoded
	}

	updated, err := h.providers.Update(r.Context(), current)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	responder.JSON(w, http.StatusOK, toProviderResponse(updated))
}

func (h modelHandlers) listProviderConnections(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.providers == nil || h.connections == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model provider connection repository unavailable")
		return
	}

	providerID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	if _, err := h.providers.GetByID(r.Context(), providerID); err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	if statusFilter == "" {
		statusFilter = "active"
	}
	if statusFilter != "active" && statusFilter != "disabled" && statusFilter != "all" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "status must be one of active, disabled, all")
		return
	}

	items, err := h.connections.ListByProvider(r.Context(), principal.OrganizationID, providerID)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	response := make([]providerConnectionResponse, 0, len(items))
	for _, item := range items {
		if statusFilter == "active" && !item.IsEnabled {
			continue
		}
		if statusFilter == "disabled" && item.IsEnabled {
			continue
		}
		response = append(response, toProviderConnectionResponse(item))
	}
	responder.JSON(w, http.StatusOK, response)
}

func (h modelHandlers) createProviderConnection(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.providers == nil || h.connections == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model provider connection repository unavailable")
		return
	}

	providerID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	if _, err := h.providers.GetByID(r.Context(), providerID); err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	var req providerConnectionRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.DisplayName) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "display_name is required")
		return
	}
	if strings.TrimSpace(req.APIKeySecretRef) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "api_key_secret_ref is required")
		return
	}

	connection := repo.ProviderConnection{
		OrganizationID:     principal.OrganizationID,
		ProviderID:         providerID,
		DisplayName:        strings.TrimSpace(req.DisplayName),
		APIKeyRef:          normalizeSecretRef(req.APIKeySecretRef),
		APIBaseURLOverride: normalizeOptionalString(req.BaseURL),
		IsEnabled:          true,
		HealthStatus:       "healthy",
	}
	if req.FailoverPriority != nil {
		connection.FailoverPriority = *req.FailoverPriority
	}
	if req.MaxConcurrent != nil {
		connection.MaxConcurrent = *req.MaxConcurrent
	}

	created, err := h.connections.Create(r.Context(), connection)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusCreated, toProviderConnectionResponse(created))
}

func (h modelHandlers) patchProviderConnection(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.providers == nil || h.connections == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model provider connection repository unavailable")
		return
	}

	providerID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	if _, err := h.providers.GetByID(r.Context(), providerID); err != nil {
		h.respondModelError(responder, w, err)
		return
	}
	connectionID, ok := parsePathUUID(responder, w, r, "cid")
	if !ok {
		return
	}

	current, err := h.connections.GetByID(r.Context(), connectionID)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}
	if current.OrganizationID != principal.OrganizationID || current.ProviderID != providerID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	var req patchProviderConnectionRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	if req.DisplayName != nil {
		trimmed := strings.TrimSpace(*req.DisplayName)
		if trimmed == "" {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "display_name is required")
			return
		}
		current.DisplayName = trimmed
	}
	if req.APIKeySecretRef != nil {
		trimmed := strings.TrimSpace(*req.APIKeySecretRef)
		if trimmed == "" {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "api_key_secret_ref is required")
			return
		}
		current.APIKeyRef = normalizeSecretRef(trimmed)
	}
	if req.FailoverPriority != nil {
		current.FailoverPriority = *req.FailoverPriority
	}
	if req.MaxConcurrent != nil {
		current.MaxConcurrent = *req.MaxConcurrent
	}

	updated, err := h.connections.Update(r.Context(), current)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}
	if req.IsEnabled != nil && updated.IsEnabled != *req.IsEnabled {
		updated, err = h.connections.SetEnabled(r.Context(), updated.ID, *req.IsEnabled)
		if err != nil {
			h.respondModelError(responder, w, err)
			return
		}
	}

	responder.JSON(w, http.StatusOK, toProviderConnectionResponse(updated))
}

func (h modelHandlers) deleteProviderConnection(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.providers == nil || h.connections == nil || h.profiles == nil || h.assignments == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model provider connection repository unavailable")
		return
	}

	providerID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	if _, err := h.providers.GetByID(r.Context(), providerID); err != nil {
		h.respondModelError(responder, w, err)
		return
	}
	connectionID, ok := parsePathUUID(responder, w, r, "cid")
	if !ok {
		return
	}

	current, err := h.connections.GetByID(r.Context(), connectionID)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}
	if current.OrganizationID != principal.OrganizationID || current.ProviderID != providerID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	if current.IsEnabled {
		canDisable, guardErr := h.canDisableProviderConnection(r.Context(), principal.OrganizationID, providerID, current.ID)
		if guardErr != nil {
			h.respondModelError(responder, w, guardErr)
			return
		}
		if !canDisable {
			responder.Error(w, http.StatusConflict, api.ErrCodeConflict, "cannot disable: this is the last active connection for one or more model profiles")
			return
		}
		if _, err := h.connections.SetEnabled(r.Context(), current.ID, false); err != nil {
			h.respondModelError(responder, w, err)
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h modelHandlers) listProfiles(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.profiles == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model profile repository unavailable")
		return
	}

	var providerFilter *uuid.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("provider_id")); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid provider_id")
			return
		}
		providerFilter = &parsed
	}

	items, err := h.profiles.ListCurrent(r.Context(), principal.OrganizationID)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}
	response := make([]profileResponse, 0, len(items))
	for _, item := range items {
		if providerFilter != nil && item.ProviderID != *providerFilter {
			continue
		}
		response = append(response, toProfileResponse(item))
	}

	sort.Slice(response, func(i, j int) bool {
		if response[i].CreatedAt.Equal(response[j].CreatedAt) {
			return response[i].LogicalProfileID < response[j].LogicalProfileID
		}
		return response[i].CreatedAt.After(response[j].CreatedAt)
	})
	responder.JSON(w, http.StatusOK, response)
}

func (h modelHandlers) createProfile(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.providers == nil || h.profiles == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model profile repository unavailable")
		return
	}

	var req createProfileRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.ProviderID) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "provider_id is required")
		return
	}
	providerID, err := uuid.Parse(strings.TrimSpace(req.ProviderID))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid provider_id")
		return
	}
	if strings.TrimSpace(req.ModelName) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "model_name is required")
		return
	}
	if _, err := h.providers.GetByID(r.Context(), providerID); err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	maxTokens := defaultModelProfileMaxTokens
	if req.MaxTokens != nil {
		if *req.MaxTokens <= 0 {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "max_tokens must be greater than zero")
			return
		}
		maxTokens = *req.MaxTokens
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(req.ModelName)
	}

	created, err := h.profiles.Create(r.Context(), repo.ModelProfile{
		LogicalProfileID:    uuid.NewString(),
		OrganizationID:      &principal.OrganizationID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          providerID,
		ModelName:           strings.TrimSpace(req.ModelName),
		DisplayName:         displayName,
		ContextWindowTokens: defaultModelProfileContextWindow,
		MaxOutputTokens:     maxTokens,
		SupportsStreaming:   true,
		SupportsVision:      false,
		Temperature:         req.Temperature,
		InvocationPurpose:   defaultInvocationPurpose,
		FallbackProfileID:   normalizeOptionalString(req.FallbackProfileID),
	})
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	responder.JSON(w, http.StatusCreated, toProfileResponse(created))
}

func (h modelHandlers) patchProfile(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.providers == nil || h.profiles == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model profile repository unavailable")
		return
	}

	logicalProfileID := strings.TrimSpace(chi.URLParam(r, "logical_profile_id"))
	if logicalProfileID == "" {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid logical_profile_id")
		return
	}

	var req patchProfileRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.DisplayName == nil &&
		req.ProviderID == nil &&
		req.ModelName == nil &&
		req.Temperature == nil &&
		req.MaxTokens == nil &&
		req.FallbackProfileID == nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "at least one patch field is required")
		return
	}

	current, err := h.profiles.GetCurrentByLogicalID(r.Context(), principal.OrganizationID, logicalProfileID)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	next := repo.ModelProfile{
		ProviderID:          current.ProviderID,
		ModelName:           current.ModelName,
		DisplayName:         current.DisplayName,
		ContextWindowTokens: current.ContextWindowTokens,
		MaxOutputTokens:     current.MaxOutputTokens,
		SupportsStreaming:   current.SupportsStreaming,
		SupportsVision:      current.SupportsVision,
		Temperature:         current.Temperature,
		InvocationPurpose:   current.InvocationPurpose,
		FallbackProfileID:   current.FallbackProfileID,
	}

	if req.ProviderID != nil {
		parsed, parseErr := uuid.Parse(strings.TrimSpace(*req.ProviderID))
		if parseErr != nil {
			responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid provider_id")
			return
		}
		if _, getErr := h.providers.GetByID(r.Context(), parsed); getErr != nil {
			h.respondModelError(responder, w, getErr)
			return
		}
		next.ProviderID = parsed
	}
	if req.ModelName != nil {
		trimmed := strings.TrimSpace(*req.ModelName)
		if trimmed == "" {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "model_name is required")
			return
		}
		next.ModelName = trimmed
	}
	if req.DisplayName != nil {
		trimmed := strings.TrimSpace(*req.DisplayName)
		if trimmed == "" {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "display_name is required")
			return
		}
		next.DisplayName = trimmed
	}
	if req.MaxTokens != nil {
		if *req.MaxTokens <= 0 {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "max_tokens must be greater than zero")
			return
		}
		next.MaxOutputTokens = *req.MaxTokens
	}
	if req.Temperature != nil {
		next.Temperature = req.Temperature
	}
	if req.FallbackProfileID != nil {
		next.FallbackProfileID = normalizeOptionalString(req.FallbackProfileID)
	}

	updated, err := h.profiles.Deprecate(r.Context(), current.ID, next)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	responder.JSON(w, http.StatusOK, toProfileResponse(updated))
}

func (h modelHandlers) getProfile(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.profiles == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model profile repository unavailable")
		return
	}

	logicalProfileID := strings.TrimSpace(chi.URLParam(r, "logical_profile_id"))
	if logicalProfileID == "" {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid logical_profile_id")
		return
	}

	profile, err := h.profiles.GetCurrentByLogicalID(r.Context(), principal.OrganizationID, logicalProfileID)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusOK, toProfileResponse(profile))
}

func (h modelHandlers) profileHistory(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.profiles == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model profile repository unavailable")
		return
	}

	logicalProfileID := strings.TrimSpace(chi.URLParam(r, "logical_profile_id"))
	if logicalProfileID == "" {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid logical_profile_id")
		return
	}

	history, err := h.profiles.ListHistoryByLogicalID(r.Context(), principal.OrganizationID, logicalProfileID)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}
	if len(history) == 0 {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	response := make([]profileResponse, 0, len(history))
	for _, item := range history {
		response = append(response, toProfileResponse(item))
	}
	responder.JSON(w, http.StatusOK, response)
}

func (h modelHandlers) listAssignments(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.assignments == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model profile assignment repository unavailable")
		return
	}

	var filterScopeType string
	if raw := strings.TrimSpace(r.URL.Query().Get("scope_type")); raw != "" {
		normalized, normalizeErr := normalizeAssignmentScopeType(raw)
		if normalizeErr != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, normalizeErr.Error())
			return
		}
		filterScopeType = normalized
	}

	var filterScopeID *uuid.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("scope_id")); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid scope_id")
			return
		}
		filterScopeID = &parsed
	}

	items, err := h.assignments.ListByOrg(r.Context(), principal.OrganizationID)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	response := make([]assignmentResponse, 0, len(items))
	for _, item := range items {
		if filterScopeType != "" && item.ScopeType != filterScopeType {
			continue
		}
		if filterScopeID != nil && item.ScopeID != *filterScopeID {
			continue
		}
		response = append(response, assignmentResponse{
			ScopeType:         item.ScopeType,
			ScopeID:           item.ScopeID,
			LogicalProfileID:  item.LogicalProfileID,
			InvocationPurpose: item.InvocationPurpose,
			CreatedAt:         item.CreatedAt,
			UpdatedAt:         item.UpdatedAt,
		})
	}
	responder.JSON(w, http.StatusOK, response)
}

func (h modelHandlers) putAssignment(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.assignments == nil || h.profiles == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model profile assignment repository unavailable")
		return
	}

	scopeType, err := normalizeAssignmentScopeType(chi.URLParam(r, "scope_type"))
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error())
		return
	}
	scopeID, ok := parsePathUUID(responder, w, r, "scope_id")
	if !ok {
		return
	}

	if scopeType == "organization" && scopeID != principal.OrganizationID {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	var req putAssignmentRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	req.LogicalProfileID = strings.TrimSpace(req.LogicalProfileID)
	if req.LogicalProfileID == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "logical_profile_id is required")
		return
	}

	if _, err := h.profiles.GetCurrentByLogicalID(r.Context(), principal.OrganizationID, req.LogicalProfileID); err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	assignment, err := h.assignments.Upsert(r.Context(), repo.ModelProfileAssignment{
		OrganizationID:    principal.OrganizationID,
		ScopeType:         scopeType,
		ScopeID:           scopeID,
		LogicalProfileID:  req.LogicalProfileID,
		InvocationPurpose: defaultInvocationPurpose,
	})
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	responder.JSON(w, http.StatusOK, assignmentResponse{
		ScopeType:         assignment.ScopeType,
		ScopeID:           assignment.ScopeID,
		LogicalProfileID:  assignment.LogicalProfileID,
		InvocationPurpose: assignment.InvocationPurpose,
		CreatedAt:         assignment.CreatedAt,
		UpdatedAt:         assignment.UpdatedAt,
	})
}

func (h modelHandlers) deleteAssignment(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.assignments == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model profile assignment repository unavailable")
		return
	}

	scopeType, err := normalizeAssignmentScopeType(chi.URLParam(r, "scope_type"))
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error())
		return
	}
	scopeID, ok := parsePathUUID(responder, w, r, "scope_id")
	if !ok {
		return
	}

	assignment, err := h.assignments.GetByScope(r.Context(), principal.OrganizationID, scopeType, scopeID, defaultInvocationPurpose)
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	if assignment.ScopeType == "organization" && assignment.ScopeID == principal.OrganizationID {
		assignments, listErr := h.assignments.ListByOrg(r.Context(), principal.OrganizationID)
		if listErr != nil {
			h.respondModelError(responder, w, listErr)
			return
		}
		remaining := 0
		for _, item := range assignments {
			if item.ID != assignment.ID {
				remaining++
			}
		}
		if remaining == 0 {
			responder.Error(w, http.StatusConflict, api.ErrCodeConflict, "cannot delete org assignment: at least one assignment must remain")
			return
		}
	}

	if err := h.assignments.Delete(r.Context(), assignment.ID); err != nil {
		h.respondModelError(responder, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h modelHandlers) listInvocations(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.invocations == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "model invocation repository unavailable")
		return
	}

	query := r.URL.Query()
	runIDText := strings.TrimSpace(query.Get("run_id"))
	sessionIDText := strings.TrimSpace(query.Get("session_id"))

	var (
		items []repo.ModelInvocation
		err   error
	)
	switch {
	case runIDText != "":
		runID, parseErr := uuid.Parse(runIDText)
		if parseErr != nil {
			responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid run_id")
			return
		}
		items, err = h.invocations.ListByRun(r.Context(), principal.OrganizationID, runID)
	case sessionIDText != "":
		sessionID, parseErr := uuid.Parse(sessionIDText)
		if parseErr != nil {
			responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid session_id")
			return
		}
		items, err = h.invocations.ListBySession(r.Context(), principal.OrganizationID, sessionID)
	default:
		items, err = h.invocations.ListByOrg(r.Context(), principal.OrganizationID)
	}
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	limit := clampListLimit(query.Get("limit"), 50, 200)
	hasMore := len(items) > limit
	if len(items) > limit {
		items = items[:limit]
	}
	response := make([]invocationResponse, 0, len(items))
	for _, item := range items {
		response = append(response, toInvocationResponse(item))
	}
	responder.JSONList(w, http.StatusOK, response, api.PaginationMeta{
		HasMore: hasMore,
		Limit:   limit,
	})
}

func (h modelHandlers) getUsage(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "usage repository unavailable")
		return
	}

	groupBy := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("group_by")))
	if groupBy == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "group_by is required")
		return
	}
	if !isUsageGroupBy(groupBy) {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "group_by must be one of provider_connection, model_provider, agent, project")
		return
	}
	if !canReadUsage(principal, groupBy) {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	periodRaw := strings.TrimSpace(r.URL.Query().Get("period"))
	if periodRaw == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "period is required")
		return
	}
	periodStart, periodEnd, err := parseUsagePeriod(time.Now().UTC(), periodRaw)
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error())
		return
	}

	rows, err := h.queryUsageRows(r.Context(), usageQuery{
		OrganizationID:    principal.OrganizationID,
		GroupBy:           groupBy,
		PeriodStart:       periodStart,
		PeriodEnd:         periodEnd,
		ModelName:         normalizeOptionalStringValue(r.URL.Query().Get("model_name")),
		InvocationPurpose: normalizeOptionalStringValue(r.URL.Query().Get("invocation_purpose")),
	})
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	responder.JSON(w, http.StatusOK, usageResponse{
		Data: rows,
		Meta: usageMeta{
			PeriodStart: periodStart.Format("2006-01-02"),
			PeriodEnd:   periodEnd.Format("2006-01-02"),
			TotalRows:   len(rows),
		},
	})
}

func (h modelHandlers) getUsageRollup(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "usage repository unavailable")
		return
	}

	groupBy := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("group_by")))
	if groupBy == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "group_by is required")
		return
	}
	if !isUsageRollupGroupBy(groupBy) {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "group_by must be one of model_provider, agent, project, period")
		return
	}
	if groupBy == "period" {
		if !strings.EqualFold(strings.TrimSpace(principal.Role), "admin") {
			responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
			return
		}
	} else if !canReadUsage(principal, groupBy) {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	periodRaw := strings.TrimSpace(r.URL.Query().Get("period"))
	if periodRaw == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "period is required")
		return
	}
	periodStart, periodEnd, err := parseUsagePeriod(time.Now().UTC(), periodRaw)
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error())
		return
	}

	query := usageQuery{
		OrganizationID:    principal.OrganizationID,
		GroupBy:           groupBy,
		PeriodStart:       periodStart,
		PeriodEnd:         periodEnd,
		ModelName:         normalizeOptionalStringValue(r.URL.Query().Get("model_name")),
		InvocationPurpose: normalizeOptionalStringValue(r.URL.Query().Get("invocation_purpose")),
	}

	var rows []usageRow
	if groupBy == "period" {
		rows, err = h.queryUsagePeriodRows(r.Context(), query)
	} else {
		rows, err = h.queryUsageRows(r.Context(), query)
	}
	if err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	responder.JSON(w, http.StatusOK, usageResponse{
		Data: rows,
		Meta: usageMeta{
			PeriodStart: periodStart.Format("2006-01-02"),
			PeriodEnd:   periodEnd.Format("2006-01-02"),
			TotalRows:   len(rows),
		},
	})
}

func (h modelHandlers) getUsageSummary(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "usage repository unavailable")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(principal.Role), "admin") {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	periodStart, periodEnd, _ := parseUsagePeriod(time.Now().UTC(), "30d")

	var summary usageSummaryResponse
	summary.PeriodStart = periodStart.Format("2006-01-02")
	summary.PeriodEnd = periodEnd.Format("2006-01-02")

	if err := h.pool.QueryRow(r.Context(), `
		SELECT
			COALESCE(SUM(total_invocations), 0),
			COALESCE(SUM(total_input_tokens), 0),
			COALESCE(SUM(total_output_tokens), 0),
			COALESCE(SUM(total_cost_microcents), 0)
		FROM model_usage_rollup
		WHERE organization_id = $1
		  AND rollup_type = 'model_provider'
		  AND rollup_id IS NULL
		  AND rollup_date BETWEEN $2 AND $3
	`, principal.OrganizationID, periodStart, periodEnd).Scan(
		&summary.TotalInvocations,
		&summary.TotalInputTokens,
		&summary.TotalOutputTokens,
		&summary.TotalCostMicrocents,
	); err != nil {
		h.respondModelError(responder, w, err)
		return
	}

	responder.JSON(w, http.StatusOK, summary)
}

func (h modelHandlers) queryUsageRows(ctx context.Context, query usageQuery) ([]usageRow, error) {
	args := []any{query.OrganizationID, query.GroupBy, query.PeriodStart, query.PeriodEnd}
	where := []string{
		"mur.organization_id = $1",
		"mur.rollup_type = $2",
		"mur.rollup_date BETWEEN $3 AND $4",
	}
	if query.GroupBy == "model_provider" {
		where = append(where, "mur.rollup_id IS NOT NULL")
	}

	nextArg := 5
	if query.ModelName != nil {
		where = append(where, fmt.Sprintf("mur.model_name = $%d", nextArg))
		args = append(args, *query.ModelName)
		nextArg++
	}
	if query.InvocationPurpose != nil {
		where = append(where, fmt.Sprintf("mur.invocation_purpose = $%d", nextArg))
		args = append(args, *query.InvocationPurpose)
	}

	sql := fmt.Sprintf(`
		SELECT
			mur.rollup_type,
			mur.rollup_id,
			COALESCE(pc.display_name, mp.display_name, ag.display_name, pr.display_name) AS display_name,
			COALESCE(SUM(mur.total_invocations), 0),
			COALESCE(SUM(mur.total_input_tokens), 0),
			COALESCE(SUM(mur.total_output_tokens), 0),
			COALESCE(SUM(mur.total_cost_microcents), 0)
		FROM model_usage_rollup mur
		LEFT JOIN provider_connection pc ON mur.rollup_type = 'provider_connection' AND mur.rollup_id = pc.id
		LEFT JOIN model_provider mp ON mur.rollup_type = 'model_provider' AND mur.rollup_id = mp.id
		LEFT JOIN agent ag ON mur.rollup_type = 'agent' AND mur.rollup_id = ag.id
		LEFT JOIN project pr ON mur.rollup_type = 'project' AND mur.rollup_id = pr.id
		WHERE %s
		GROUP BY mur.rollup_type, mur.rollup_id, pc.display_name, mp.display_name, ag.display_name, pr.display_name
		ORDER BY COALESCE(SUM(mur.total_cost_microcents), 0) DESC, mur.rollup_id
	`, strings.Join(where, " AND "))

	rows, err := h.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	response := make([]usageRow, 0)
	for rows.Next() {
		var row usageRow
		row.RollupDate = query.PeriodEnd.Format("2006-01-02")
		if scanErr := rows.Scan(
			&row.RollupType,
			&row.RollupID,
			&row.DisplayName,
			&row.TotalInvocations,
			&row.TotalInputTokens,
			&row.TotalOutputTokens,
			&row.TotalCostMicrocents,
		); scanErr != nil {
			return nil, scanErr
		}
		response = append(response, row)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	return response, nil
}

func (h modelHandlers) queryUsagePeriodRows(ctx context.Context, query usageQuery) ([]usageRow, error) {
	args := []any{query.OrganizationID, query.PeriodStart, query.PeriodEnd}
	where := []string{
		"mur.organization_id = $1",
		"mur.rollup_date BETWEEN $2 AND $3",
	}

	nextArg := 4
	if query.ModelName != nil {
		where = append(where, fmt.Sprintf("mur.model_name = $%d", nextArg))
		args = append(args, *query.ModelName)
		nextArg++
	}
	if query.InvocationPurpose != nil {
		where = append(where, fmt.Sprintf("mur.invocation_purpose = $%d", nextArg))
		args = append(args, *query.InvocationPurpose)
	}

	sql := fmt.Sprintf(`
		SELECT
			mur.rollup_date,
			COALESCE(SUM(mur.total_invocations), 0),
			COALESCE(SUM(mur.total_input_tokens), 0),
			COALESCE(SUM(mur.total_output_tokens), 0),
			COALESCE(SUM(mur.total_cost_microcents), 0)
		FROM model_usage_rollup mur
		WHERE %s
		GROUP BY mur.rollup_date
		ORDER BY mur.rollup_date ASC
	`, strings.Join(where, " AND "))

	rows, err := h.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	response := make([]usageRow, 0)
	for rows.Next() {
		var (
			rollupDate          time.Time
			totalInvocations    int64
			totalInputTokens    int64
			totalOutputTokens   int64
			totalCostMicrocents int64
		)
		if scanErr := rows.Scan(
			&rollupDate,
			&totalInvocations,
			&totalInputTokens,
			&totalOutputTokens,
			&totalCostMicrocents,
		); scanErr != nil {
			return nil, scanErr
		}
		response = append(response, usageRow{
			RollupType:          "period",
			RollupDate:          rollupDate.UTC().Format("2006-01-02"),
			TotalInvocations:    totalInvocations,
			TotalInputTokens:    totalInputTokens,
			TotalOutputTokens:   totalOutputTokens,
			TotalCostMicrocents: totalCostMicrocents,
		})
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	return response, nil
}

func (h modelHandlers) canDisableProviderConnection(ctx context.Context, organizationID, providerID, connectionID uuid.UUID) (bool, error) {
	connections, err := h.connections.ListByProvider(ctx, organizationID, providerID)
	if err != nil {
		return false, err
	}

	activeOthers := 0
	for _, item := range connections {
		if item.ID != connectionID && item.IsEnabled {
			activeOthers++
		}
	}
	if activeOthers > 0 {
		return true, nil
	}

	currentProfiles, err := h.profiles.ListCurrentByProvider(ctx, organizationID, providerID)
	if err != nil {
		return false, err
	}
	if len(currentProfiles) == 0 {
		return true, nil
	}

	profileIDs := make(map[string]struct{}, len(currentProfiles))
	for _, profile := range currentProfiles {
		profileIDs[strings.TrimSpace(profile.LogicalProfileID)] = struct{}{}
	}

	assignments, err := h.assignments.ListByOrg(ctx, organizationID)
	if err != nil {
		return false, err
	}
	for _, assignment := range assignments {
		if _, exists := profileIDs[strings.TrimSpace(assignment.LogicalProfileID)]; exists {
			return false, nil
		}
	}
	return true, nil
}

func (h modelHandlers) requirePrincipal(w http.ResponseWriter, r *http.Request) (middleware.Principal, bool) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return middleware.Principal{}, false
	}
	return principal, true
}

func (h modelHandlers) respondModelError(responder api.Responder, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
	case errors.Is(err, repo.ErrConflict):
		responder.Error(w, http.StatusConflict, api.ErrCodeConflict, "conflict")
	default:
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
	}
}

func normalizeAssignmentScopeType(scopeType string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(scopeType))
	switch normalized {
	case "org":
		return "organization", nil
	case "organization", "project", "agent", "flow_node":
		return normalized, nil
	default:
		return "", errors.New("scope_type must be one of organization, project, agent, flow_node")
	}
}

func normalizeSecretRef(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "ref:") {
		return trimmed
	}
	return "ref:" + trimmed
}

func metadataMap(metadata json.RawMessage) map[string]any {
	result := map[string]any{}
	if len(metadata) == 0 {
		return result
	}
	_ = json.Unmarshal(metadata, &result)
	return result
}

func providerNotes(metadata json.RawMessage) *string {
	parsed := metadataMap(metadata)
	value, ok := parsed["notes"]
	if !ok {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return &text
}

func toProviderResponse(provider repo.ModelProvider) providerResponse {
	return providerResponse{
		ID:                provider.ID,
		Slug:              provider.Slug,
		DisplayName:       provider.DisplayName,
		APIBaseURL:        provider.APIBaseURL,
		SupportedFeatures: append([]string{}, provider.SupportedFeatures...),
		IsEnabled:         provider.IsEnabled,
		Notes:             providerNotes(provider.Metadata),
		CreatedAt:         provider.CreatedAt,
		UpdatedAt:         provider.UpdatedAt,
	}
}

func toProviderConnectionResponse(connection repo.ProviderConnection) providerConnectionResponse {
	return providerConnectionResponse{
		ID:               connection.ID,
		OrganizationID:   connection.OrganizationID,
		ProviderID:       connection.ProviderID,
		DisplayName:      connection.DisplayName,
		APIKeySecretRef:  connection.APIKeyRef,
		BaseURL:          connection.APIBaseURLOverride,
		FailoverPriority: connection.FailoverPriority,
		MaxConcurrent:    connection.MaxConcurrent,
		HealthStatus:     connection.HealthStatus,
		IsEnabled:        connection.IsEnabled,
		CreatedAt:        connection.CreatedAt,
		UpdatedAt:        connection.UpdatedAt,
	}
}

func toProfileResponse(profile repo.ModelProfile) profileResponse {
	return profileResponse{
		ID:                profile.ID,
		LogicalProfileID:  profile.LogicalProfileID,
		Version:           profile.Version,
		DisplayName:       profile.DisplayName,
		ProviderID:        profile.ProviderID,
		ModelName:         profile.ModelName,
		Temperature:       profile.Temperature,
		MaxTokens:         profile.MaxOutputTokens,
		FallbackProfileID: profile.FallbackProfileID,
		IsCurrent:         profile.IsCurrent,
		OrganizationID:    profile.OrganizationID,
		CreatedAt:         profile.CreatedAt,
	}
}

func toInvocationResponse(item repo.ModelInvocation) invocationResponse {
	metadata := item.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	return invocationResponse{
		ID:                item.ID,
		OrganizationID:    item.OrganizationID,
		ModelProviderID:   item.ModelProviderID,
		ModelProfileID:    item.ModelProfileID,
		InvocationPurpose: item.InvocationPurpose,
		Status:            item.Status,
		InputTokens:       item.InputTokens,
		OutputTokens:      item.OutputTokens,
		ModelName:         item.ModelName,
		Metadata:          metadata,
		SessionID:         item.SessionID,
		TurnID:            item.TurnID,
		RunID:             item.RunID,
		CreatedAt:         item.CreatedAt,
		CompletedAt:       item.CompletedAt,
	}
}

func normalizeOptionalStringValue(raw string) *string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func isUsageGroupBy(value string) bool {
	switch value {
	case "provider_connection", "model_provider", "agent", "project":
		return true
	default:
		return false
	}
}

func isUsageRollupGroupBy(value string) bool {
	switch value {
	case "model_provider", "agent", "project", "period":
		return true
	default:
		return false
	}
}

// Project manager role is represented by "member" in the current RBAC schema.
func canReadUsage(principal middleware.Principal, groupBy string) bool {
	role := strings.ToLower(strings.TrimSpace(principal.Role))
	if role == "admin" {
		return true
	}
	return role == "member" && groupBy == "project"
}

func parseUsagePeriod(now time.Time, raw string) (time.Time, time.Time, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)

	switch normalized {
	case "today":
		return today, today, nil
	case "yesterday":
		day := today.AddDate(0, 0, -1)
		return day, day, nil
	case "7d":
		end := today.AddDate(0, 0, -1)
		return today.AddDate(0, 0, -7), end, nil
	case "30d":
		end := today.AddDate(0, 0, -1)
		return today.AddDate(0, 0, -30), end, nil
	default:
		parsed, err := time.Parse("2006-01-02", normalized)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("period must be one of today, yesterday, 7d, 30d, or YYYY-MM-DD")
		}
		parsed = parsed.UTC()
		return parsed, parsed, nil
	}
}

type usageQuery struct {
	OrganizationID    uuid.UUID
	GroupBy           string
	PeriodStart       time.Time
	PeriodEnd         time.Time
	ModelName         *string
	InvocationPurpose *string
}
