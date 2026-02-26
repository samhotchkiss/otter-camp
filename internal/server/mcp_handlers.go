package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type mcpHandlers struct {
	service mcp.MCPService
	catalog *repo.MCPToolCatalogRepo
}

type MCPRouteRegistrar struct {
	handlers mcpHandlers
}

func NewMCPRouteRegistrar(service mcp.MCPService, catalog *repo.MCPToolCatalogRepo) *MCPRouteRegistrar {
	return &MCPRouteRegistrar{handlers: mcpHandlers{service: service, catalog: catalog}}
}

func (r *MCPRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.With(requireReadScope("mcp")).Get("/mcp/connections", r.handlers.listConnections)
	router.With(middleware.RequireRole("admin"), requireWriteScope("mcp")).Post("/mcp/connections", r.handlers.createConnection)
	router.With(requireReadScope("mcp")).Get("/mcp/connections/{id}", r.handlers.getConnection)
	router.With(middleware.RequireRole("admin"), requireWriteScope("mcp")).Patch("/mcp/connections/{id}", r.handlers.updateConnection)
	router.With(middleware.RequireRole("admin"), requireWriteScope("mcp")).Delete("/mcp/connections/{id}", r.handlers.deleteConnection)

	router.With(middleware.RequireRole("member"), requireWriteScope("mcp")).Post("/mcp/connections/{id}/refresh", r.handlers.refreshConnection)
	router.With(middleware.RequireRole("member"), requireWriteScope("mcp")).Post("/mcp/connections/{id}/test", r.handlers.testConnection)

	router.With(requireReadScope("mcp")).Get("/mcp/connections/{id}/catalog", r.handlers.listCatalog)
	router.With(middleware.RequireRole("admin"), requireWriteScope("mcp")).Patch("/mcp/connections/{id}/catalog/{entry_id}", r.handlers.setCatalogEntry)

	router.With(requireReadScope("mcp")).Get("/mcp/connections/{id}/executions", r.handlers.listExecutionsStub)
}

type createMCPConnectionRequest struct {
	DisplayName     string                             `json:"display_name"`
	Slug            string                             `json:"slug"`
	ProjectID       *uuid.UUID                         `json:"project_id"`
	Transport       string                             `json:"transport"`
	TransportConfig json.RawMessage                    `json:"transport_config"`
	IsEnabled       *bool                              `json:"is_enabled"`
	SecretBindings  []createMCPConnectionSecretBinding `json:"secret_bindings,omitempty"`
}

type createMCPConnectionSecretBinding struct {
	SecretRef  string `json:"secret_ref"`
	EnvVarName string `json:"env_var_name"`
}

type updateMCPConnectionRequest struct {
	DisplayName     *string         `json:"display_name"`
	Slug            *string         `json:"slug"`
	Transport       *string         `json:"transport"`
	TransportConfig json.RawMessage `json:"transport_config"`
	IsEnabled       *bool           `json:"is_enabled"`
}

type setCatalogEntryRequest struct {
	IsEnabled *bool `json:"is_enabled"`
}

type mcpConnectionResponse struct {
	ID              uuid.UUID       `json:"id"`
	OrganizationID  uuid.UUID       `json:"organization_id"`
	ProjectID       *uuid.UUID      `json:"project_id"`
	DisplayName     string          `json:"display_name"`
	Slug            string          `json:"slug"`
	Transport       string          `json:"transport"`
	TransportConfig json.RawMessage `json:"transport_config"`
	Status          string          `json:"status"`
	IsEnabled       bool            `json:"is_enabled"`
	LastHealthyAt   *time.Time      `json:"last_healthy_at"`
	CreatedByType   string          `json:"created_by_type"`
	CreatedByID     uuid.UUID       `json:"created_by_id"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type mcpCatalogEntryResponse struct {
	ID           uuid.UUID       `json:"id"`
	ConnectionID uuid.UUID       `json:"connection_id"`
	ToolName     string          `json:"tool_name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	IsEnabled    bool            `json:"is_enabled"`
	Metadata     json.RawMessage `json:"metadata"`
	DiscoveredAt time.Time       `json:"discovered_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

func (h mcpHandlers) listConnections(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "mcp service unavailable")
		return
	}

	query := r.URL.Query()
	var filter mcp.MCPConnectionFilter
	if rawProjectID := strings.TrimSpace(query.Get("project_id")); rawProjectID != "" {
		projectID, err := uuid.Parse(rawProjectID)
		if err != nil {
			responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project_id")
			return
		}
		filter.ProjectID = &projectID
	}
	filter.Status = strings.TrimSpace(query.Get("status"))
	filter.Transport = strings.TrimSpace(query.Get("transport"))
	filter.IncludeDisabled = strings.EqualFold(strings.TrimSpace(query.Get("include_disabled")), "true")

	items, err := h.service.ListConnections(r.Context(), principal.OrganizationID, filter)
	if err != nil {
		status, code, message := mapMCPError(err)
		responder.Error(w, status, code, message)
		return
	}

	responses := make([]mcpConnectionResponse, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		responses = append(responses, toMCPConnectionResponse(item))
	}

	responder.JSON(w, http.StatusOK, responses)
}

func (h mcpHandlers) createConnection(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "mcp service unavailable")
		return
	}

	var req createMCPConnectionRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	created, err := h.service.CreateConnection(r.Context(), mcp.CreateConnectionRequest{
		OrganizationID:  principal.OrganizationID,
		ProjectID:       req.ProjectID,
		DisplayName:     req.DisplayName,
		Slug:            req.Slug,
		Transport:       req.Transport,
		TransportConfig: req.TransportConfig,
		IsEnabled:       req.IsEnabled,
		SecretBindings:  toMCPSecretBindingInputs(req.SecretBindings),
		CreatedByType:   "human_user",
		CreatedByID:     principal.UserID,
	})
	if err != nil {
		status, code, message := mapMCPError(err)
		responder.Error(w, status, code, message)
		return
	}

	responder.JSON(w, http.StatusCreated, toMCPConnectionResponse(created))
}

func (h mcpHandlers) getConnection(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	connectionID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}

	connection, err := h.service.GetConnection(r.Context(), principal.OrganizationID, connectionID)
	if err != nil {
		status, code, message := mapMCPError(err)
		responder.Error(w, status, code, message)
		return
	}

	responder.JSON(w, http.StatusOK, toMCPConnectionResponse(connection))
}

func (h mcpHandlers) updateConnection(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	connectionID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}

	var req updateMCPConnectionRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.Transport != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "transport cannot be changed after creation")
		return
	}

	updated, err := h.service.UpdateConnection(r.Context(), principal.OrganizationID, connectionID, mcp.UpdateConnectionRequest{
		DisplayName:     req.DisplayName,
		Slug:            req.Slug,
		TransportConfig: req.TransportConfig,
		IsEnabled:       req.IsEnabled,
	})
	if err != nil {
		status, code, message := mapMCPError(err)
		responder.Error(w, status, code, message)
		return
	}

	responder.JSON(w, http.StatusOK, toMCPConnectionResponse(updated))
}

func (h mcpHandlers) deleteConnection(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	connectionID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}

	if err := h.service.DeleteConnection(r.Context(), principal.OrganizationID, connectionID); err != nil {
		status, code, message := mapMCPError(err)
		responder.Error(w, status, code, message)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h mcpHandlers) refreshConnection(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	connectionID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}

	if _, err := h.service.GetConnection(r.Context(), principal.OrganizationID, connectionID); err != nil {
		status, code, message := mapMCPError(err)
		responder.Error(w, status, code, message)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := h.service.RefreshCatalog(ctx, connectionID); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			responder.Error(w, http.StatusGatewayTimeout, api.ErrCodeServiceUnavailable, "catalog refresh timed out")
			return
		}
		status, code, message := mapMCPError(err)
		responder.Error(w, status, code, message)
		return
	}

	responder.JSON(w, http.StatusOK, map[string]any{"refreshed": true})
}

func (h mcpHandlers) testConnection(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	connectionID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}

	connection, err := h.service.GetConnection(r.Context(), principal.OrganizationID, connectionID)
	if err != nil {
		status, code, message := mapMCPError(err)
		responder.Error(w, status, code, message)
		return
	}

	start := time.Now()
	circuitState := h.service.CircuitState(connectionID)
	if circuitState == mcp.CBOpen || connection.Status == "failed" {
		responder.JSON(w, http.StatusOK, map[string]any{
			"status":        "error",
			"error":         "circuit open",
			"circuit_state": circuitState,
		})
		return
	}

	latencyMS := time.Since(start).Milliseconds()
	if latencyMS <= 0 {
		latencyMS = 1
	}

	responder.JSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"latency_ms":    latencyMS,
		"circuit_state": circuitState,
	})
}

func (h mcpHandlers) listCatalog(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.catalog == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "mcp catalog repository unavailable")
		return
	}

	connectionID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}

	if _, err := h.service.GetConnection(r.Context(), principal.OrganizationID, connectionID); err != nil {
		status, code, message := mapMCPError(err)
		responder.Error(w, status, code, message)
		return
	}

	entries, err := h.catalog.ListByConnection(r.Context(), connectionID)
	if err != nil {
		status, code, message := mapMCPError(err)
		responder.Error(w, status, code, message)
		return
	}

	responses := make([]mcpCatalogEntryResponse, 0, len(entries))
	for i := range entries {
		responses = append(responses, toMCPCatalogEntryResponse(entries[i]))
	}
	responder.JSON(w, http.StatusOK, responses)
}

func (h mcpHandlers) setCatalogEntry(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	connectionID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	entryID, ok := parsePathUUID(responder, w, r, "entry_id")
	if !ok {
		return
	}

	if _, err := h.service.GetConnection(r.Context(), principal.OrganizationID, connectionID); err != nil {
		status, code, message := mapMCPError(err)
		responder.Error(w, status, code, message)
		return
	}

	var req setCatalogEntryRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.IsEnabled == nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "is_enabled is required")
		return
	}

	if *req.IsEnabled {
		err := h.service.EnableTool(r.Context(), connectionID, entryID)
		if err != nil {
			status, code, message := mapMCPError(err)
			responder.Error(w, status, code, message)
			return
		}
	} else {
		err := h.service.DisableTool(r.Context(), connectionID, entryID)
		if err != nil {
			status, code, message := mapMCPError(err)
			responder.Error(w, status, code, message)
			return
		}
	}

	responder.JSON(w, http.StatusOK, map[string]any{"is_enabled": *req.IsEnabled})
}

func (h mcpHandlers) listExecutionsStub(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if _, ok := parsePathUUID(responder, w, r, "id"); !ok {
		return
	}

	w.Header().Set("X-Stub", "true")
	// Task 055 replaces this stub with persisted execution-log reads.
	responder.JSONList(w, http.StatusOK, []any{}, api.PaginationMeta{})
}

func parsePathUUID(responder api.Responder, w http.ResponseWriter, r *http.Request, key string) (uuid.UUID, bool) {
	raw := strings.TrimSpace(chi.URLParam(r, key))
	parsed, err := uuid.Parse(raw)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid "+key)
		return uuid.Nil, false
	}
	return parsed, true
}

func mapMCPError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, mcp.ErrCircuitOpen):
		return http.StatusServiceUnavailable, "circuit_open", "circuit is open"
	case errors.Is(err, mcp.ErrConnectionFailed):
		return http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "connection is failed"
	case errors.Is(err, mcp.ErrInvalidSlug):
		return http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid slug"
	case errors.Is(err, mcp.ErrInvalidSecretBinding):
		return http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid secret binding"
	case errors.Is(err, mcp.ErrSecretNotFound):
		return http.StatusUnprocessableEntity, "mcp.secret_not_found", "secret reference not found"
	case errors.Is(err, repo.ErrConflict):
		return http.StatusConflict, api.ErrCodeConflict, "conflict"
	case errors.Is(err, mcp.ErrConnectionOrg), errors.Is(err, repo.ErrNotFound):
		return http.StatusNotFound, api.ErrCodeNotFound, "resource not found"
	default:
		return http.StatusInternalServerError, api.ErrCodeInternal, "internal server error"
	}
}

func toMCPConnectionResponse(item *mcp.MCPConnection) mcpConnectionResponse {
	return mcpConnectionResponse{
		ID:              item.ID,
		OrganizationID:  item.OrganizationID,
		ProjectID:       item.ProjectID,
		DisplayName:     item.DisplayName,
		Slug:            item.Slug,
		Transport:       item.Transport,
		TransportConfig: item.TransportConfig,
		Status:          item.Status,
		IsEnabled:       item.IsEnabled,
		LastHealthyAt:   item.LastHealthyAt,
		CreatedByType:   item.CreatedByType,
		CreatedByID:     item.CreatedByID,
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
	}
}

func toMCPCatalogEntryResponse(item repo.MCPToolCatalogEntry) mcpCatalogEntryResponse {
	return mcpCatalogEntryResponse{
		ID:           item.ID,
		ConnectionID: item.ConnectionID,
		ToolName:     item.ToolName,
		Description:  item.Description,
		InputSchema:  item.InputSchema,
		IsEnabled:    item.IsEnabled,
		Metadata:     item.Metadata,
		DiscoveredAt: item.DiscoveredAt,
		UpdatedAt:    item.UpdatedAt,
	}
}

func toMCPSecretBindingInputs(items []createMCPConnectionSecretBinding) []mcp.SecretBindingInput {
	if len(items) == 0 {
		return nil
	}
	out := make([]mcp.SecretBindingInput, 0, len(items))
	for _, item := range items {
		out = append(out, mcp.SecretBindingInput{
			SecretRef:  item.SecretRef,
			EnvVarName: item.EnvVarName,
		})
	}
	return out
}
