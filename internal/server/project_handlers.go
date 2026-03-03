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

	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/scheduling"
)

type skillOrgRepo interface {
	ListByOrg(ctx context.Context, organizationID uuid.UUID, includeInactive bool) ([]repo.Skill, error)
	ListByProject(ctx context.Context, projectID uuid.UUID, includeInactive bool) ([]repo.Skill, error)
}

type ProjectRouteRegistrar struct {
	handlers projectHandlers
}

func NewProjectRouteRegistrar(service projectsvc.ProjectService, skills skillOrgRepo) *ProjectRouteRegistrar {
	return &ProjectRouteRegistrar{handlers: projectHandlers{service: service, skills: skills}}
}

func (r *ProjectRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.With(middleware.RequireAnyScope(requireReadScope("projects")...)).Get("/projects", r.handlers.listProjects)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Post("/projects", r.handlers.createProject)
	router.With(middleware.RequireAnyScope(requireReadScope("projects")...)).Get("/projects/{id}", r.handlers.getProject)
	router.With(
		middleware.RequireRole("member"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Patch("/projects/{id}", r.handlers.updateProject)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Delete("/projects/{id}", r.handlers.deleteProject)
	router.With(
		middleware.RequireRole("admin"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Post("/projects/{id}/archive", r.handlers.archiveProject)

	router.With(middleware.RequireAnyScope(requireReadScope("projects")...)).Get("/projects/{id}/flow-templates", r.handlers.listProjectFlowTemplates)
	router.With(
		middleware.RequireRole("member"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Post("/projects/{id}/flow-templates", r.handlers.createProjectFlowTemplate)
	router.With(middleware.RequireAnyScope(requireReadScope("projects")...)).Get("/flow-templates", r.handlers.listFlowTemplates)
	router.With(middleware.RequireAnyScope(requireReadScope("projects")...)).Get("/flow-templates/{id}", r.handlers.getFlowTemplate)
	router.With(
		middleware.RequireRole("member"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Patch("/flow-templates/{id}", r.handlers.updateFlowTemplate)

	router.With(middleware.RequireAnyScope(requireReadScope("projects")...)).Get("/flow-templates/{id}/nodes", r.handlers.listFlowNodes)
	router.With(
		middleware.RequireRole("member"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Post("/flow-templates/{id}/nodes", r.handlers.addFlowNode)
	router.With(
		middleware.RequireRole("member"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Patch("/flow-templates/{id}/nodes/{node_id}", r.handlers.updateFlowNode)
	router.With(
		middleware.RequireRole("member"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Delete("/flow-templates/{id}/nodes/{node_id}", r.handlers.deleteFlowNode)

	router.With(middleware.RequireAnyScope(requireReadScope("projects")...)).Get("/skills", r.handlers.listSkills)
	router.With(middleware.RequireAnyScope(requireReadScope("projects")...)).Get("/skills/catalog", r.handlers.listSkills) // spec alias
	router.With(middleware.RequireAnyScope(requireReadScope("projects")...)).Get("/projects/{id}/skills", r.handlers.listProjectSkills)

	router.With(middleware.RequireAnyScope(requireReadScope("projects")...)).Get("/projects/{id}/schedules", r.handlers.listSchedules)
	router.With(
		middleware.RequireRole("member"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Post("/projects/{id}/schedules", r.handlers.createSchedule)
	router.With(
		middleware.RequireRole("member"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Patch("/projects/{id}/schedules/{schedule_id}", r.handlers.updateSchedule)
	router.With(
		middleware.RequireRole("member"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Delete("/projects/{id}/schedules/{schedule_id}", r.handlers.deleteSchedule)
	router.With(
		middleware.RequireRole("member"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Post("/projects/{id}/schedules/{schedule_id}/enable", r.handlers.enableSchedule)
	router.With(
		middleware.RequireRole("member"),
		middleware.RequireAnyScope(requireWriteScope("projects")...),
	).Post("/projects/{id}/schedules/{schedule_id}/disable", r.handlers.disableSchedule)
}

type projectHandlers struct {
	service projectsvc.ProjectService
	skills  skillOrgRepo
}

var scheduleCronParser = scheduling.NewCronParser()

type createProjectRequest struct {
	Slug                 string          `json:"slug"`
	DisplayName          string          `json:"display_name"`
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	DeliveryMode         string          `json:"delivery_mode"`
	DeployFlowTemplateID *uuid.UUID      `json:"deploy_flow_template_id"`
	Settings             json.RawMessage `json:"settings"`
}

type updateProjectRequest struct {
	Slug                 *string          `json:"slug"`
	DisplayName          *string          `json:"display_name"`
	Description          *string          `json:"description"`
	DeliveryMode         *string          `json:"delivery_mode"`
	DeployFlowTemplateID *uuid.UUID       `json:"deploy_flow_template_id"`
	Settings             *json.RawMessage `json:"settings"`
}

type createFlowTemplateRequest struct {
	Slug        string     `json:"slug"`
	DisplayName string     `json:"display_name"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	StartNodeID *uuid.UUID `json:"start_node_id"`
}

type updateFlowTemplateRequest struct {
	Slug        *string    `json:"slug"`
	DisplayName *string    `json:"display_name"`
	Description *string    `json:"description"`
	StartNodeID *uuid.UUID `json:"start_node_id"`
}

type createFlowNodeRequest struct {
	DisplayName         string                 `json:"display_name"`
	Name                string                 `json:"name"`
	NodeType            string                 `json:"node_type"`
	ActorType           *string                `json:"actor_type"`
	ActorID             *uuid.UUID             `json:"actor_id"`
	NextNodeID          *uuid.UUID             `json:"next_node_id"`
	RejectNodeID        *uuid.UUID             `json:"reject_node_id"`
	MCPTools            []repo.FlowNodeMCPTool `json:"mcp_tools"`
	ToolDomains         []string               `json:"tool_domains"`
	RequiresHumanReview bool                   `json:"requires_human_review"`
	MaxVisits           int                    `json:"max_visits"`
	Position            int                    `json:"position"`
	Metadata            json.RawMessage        `json:"metadata"`
}

type updateFlowNodeRequest struct {
	DisplayName         *string                 `json:"display_name"`
	NodeType            *string                 `json:"node_type"`
	ActorType           *string                 `json:"actor_type"`
	ActorID             *uuid.UUID              `json:"actor_id"`
	NextNodeID          *uuid.UUID              `json:"next_node_id"`
	RejectNodeID        *uuid.UUID              `json:"reject_node_id"`
	MCPTools            *[]repo.FlowNodeMCPTool `json:"mcp_tools"`
	ToolDomains         *[]string               `json:"tool_domains"`
	RequiresHumanReview *bool                   `json:"requires_human_review"`
	MaxVisits           *int                    `json:"max_visits"`
	Position            *int                    `json:"position"`
	Metadata            *json.RawMessage        `json:"metadata"`
}

type createScheduleRequest struct {
	DisplayName    string    `json:"display_name"`
	FlowTemplateID uuid.UUID `json:"flow_template_id"`
	CronExpression string    `json:"cron_expression"`
	OverlapPolicy  string    `json:"overlap_policy"`
	MaxDurationMS  *int64    `json:"max_duration_ms"`
	IsEnabled      bool      `json:"is_enabled"`
}

type updateScheduleRequest struct {
	DisplayName    *string    `json:"display_name"`
	FlowTemplateID *uuid.UUID `json:"flow_template_id"`
	CronExpression *string    `json:"cron_expression"`
	OverlapPolicy  *string    `json:"overlap_policy"`
	MaxDurationMS  *int64     `json:"max_duration_ms"`
	IsEnabled      *bool      `json:"is_enabled"`
}

type projectResponse struct {
	ID                   uuid.UUID       `json:"id"`
	OrganizationID       uuid.UUID       `json:"organization_id"`
	Slug                 string          `json:"slug"`
	DisplayName          string          `json:"display_name"`
	Description          string          `json:"description"`
	DeliveryMode         string          `json:"delivery_mode"`
	Status               string          `json:"status"`
	DeployFlowTemplateID *uuid.UUID      `json:"deploy_flow_template_id"`
	Settings             json.RawMessage `json:"settings"`
	CreatedByType        string          `json:"created_by_type"`
	CreatedByID          uuid.UUID       `json:"created_by_id"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

type flowTemplateResponse struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID *uuid.UUID `json:"organization_id"`
	ProjectID      *uuid.UUID `json:"project_id"`
	Slug           string     `json:"slug"`
	DisplayName    string     `json:"display_name"`
	Description    string     `json:"description"`
	IsCurrent      bool       `json:"is_current"`
	Version        int        `json:"version"`
	StartNodeID    *uuid.UUID `json:"start_node_id"`
	IsSystem       bool       `json:"is_system"`
	CreatedByType  string     `json:"created_by_type"`
	CreatedByID    uuid.UUID  `json:"created_by_id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type flowNodeResponse struct {
	ID                  uuid.UUID              `json:"id"`
	FlowTemplateID      uuid.UUID              `json:"flow_template_id"`
	DisplayName         string                 `json:"display_name"`
	NodeType            string                 `json:"node_type"`
	Position            int                    `json:"position"`
	ActorType           *string                `json:"actor_type"`
	ActorID             *uuid.UUID             `json:"actor_id"`
	NextNodeID          *uuid.UUID             `json:"next_node_id"`
	RejectNodeID        *uuid.UUID             `json:"reject_node_id"`
	MCPTools            []repo.FlowNodeMCPTool `json:"mcp_tools"`
	ToolDomains         []string               `json:"tool_domains"`
	RequiresHumanReview bool                   `json:"requires_human_review"`
	MaxVisits           int                    `json:"max_visits"`
	Metadata            json.RawMessage        `json:"metadata"`
	CreatedAt           time.Time              `json:"created_at"`
	UpdatedAt           time.Time              `json:"updated_at"`
}

type scheduleResponse struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	ProjectID      uuid.UUID  `json:"project_id"`
	FlowTemplateID uuid.UUID  `json:"flow_template_id"`
	DisplayName    string     `json:"display_name"`
	CronExpression string     `json:"cron_expression"`
	OverlapPolicy  string     `json:"overlap_policy"`
	MaxDurationMS  *int64     `json:"max_duration_ms"`
	IsEnabled      bool       `json:"is_enabled"`
	LastFiredAt    *time.Time `json:"last_fired_at"`
	NextFireAt     *time.Time `json:"next_fire_at"`
	CreatedByType  string     `json:"created_by_type"`
	CreatedByID    uuid.UUID  `json:"created_by_id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (h projectHandlers) listProjects(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	params := api.ParsePaginationParams(r.URL.Query())
	filter := projectsvc.ProjectFilter{
		DeliveryMode: strings.TrimSpace(r.URL.Query().Get("delivery_mode")),
		SlugPrefix:   strings.TrimSpace(r.URL.Query().Get("slug_prefix")),
	}
	items, err := h.service.List(r.Context(), principal.OrganizationID, filter)
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}

	sortProjectsByCreatedAt(items, params.Order == "asc")
	page, nextCursor, err := paginateByTimeID(items, params, func(item *projectsvc.Project) time.Time {
		return item.CreatedAt
	}, func(item *projectsvc.Project) uuid.UUID {
		return item.ID
	})
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
		return
	}

	payload := make([]projectResponse, 0, len(page))
	for _, item := range page {
		payload = append(payload, toProjectResponse(item))
	}
	total := len(items)
	responder.JSONList(w, http.StatusOK, payload, api.PaginationMeta{
		NextCursor: nextCursor,
		Limit:      params.Limit,
		Total:      &total,
	})
}

func (h projectHandlers) createProject(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	var req createProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(req.Name)
	}
	if displayName == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "display_name is required")
		return
	}

	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		slug = slugifyProjectName(displayName)
	}

	deliveryMode := strings.TrimSpace(req.DeliveryMode)
	if deliveryMode == "" {
		deliveryMode = "gated"
	}
	switch deliveryMode {
	case "gated", "continuous", "scheduled":
		// valid
	default:
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "delivery_mode must be one of: gated, continuous, scheduled")
		return
	}

	created, err := h.service.Create(r.Context(), projectsvc.CreateProjectRequest{
		OrganizationID:       principal.OrganizationID,
		Slug:                 slug,
		DisplayName:          displayName,
		Description:          req.Description,
		DeliveryMode:         deliveryMode,
		DeployFlowTemplateID: req.DeployFlowTemplateID,
		Settings:             normalizeJSONMap(req.Settings),
		CreatedByType:        "human_user",
		CreatedByID:          principal.UserID,
	})
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}

	responder.JSON(w, http.StatusCreated, toProjectResponse(created))
}

func (h projectHandlers) getProject(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}

	projectRecord, getErr := h.service.Get(r.Context(), principal.OrganizationID, projectID)
	if getErr != nil {
		h.respondProjectError(responder, w, getErr)
		return
	}
	responder.JSON(w, http.StatusOK, toProjectResponse(projectRecord))
}

func (h projectHandlers) updateProject(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}

	var req updateProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.DeliveryMode != nil {
		trimmed := strings.TrimSpace(*req.DeliveryMode)
		if trimmed == "" {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "delivery_mode must not be empty")
			return
		}
		req.DeliveryMode = &trimmed
	}

	updated, updateErr := h.service.Update(r.Context(), principal.OrganizationID, projectID, projectsvc.UpdateProjectRequest{
		Slug:                 req.Slug,
		DisplayName:          req.DisplayName,
		Description:          req.Description,
		DeliveryMode:         req.DeliveryMode,
		DeployFlowTemplateID: req.DeployFlowTemplateID,
		Settings:             normalizeJSONMapPointer(req.Settings),
		UpdatedByType:        "human_user",
		UpdatedByID:          principal.UserID,
	})
	if updateErr != nil {
		h.respondProjectError(responder, w, updateErr)
		return
	}
	responder.JSON(w, http.StatusOK, toProjectResponse(updated))
}

func (h projectHandlers) deleteProject(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}

	if deleteErr := h.service.Delete(r.Context(), principal.OrganizationID, projectID); deleteErr != nil {
		h.respondProjectError(responder, w, deleteErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h projectHandlers) archiveProject(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}

	archived, archiveErr := h.service.Archive(r.Context(), principal.OrganizationID, projectID)
	if archiveErr != nil {
		h.respondProjectError(responder, w, archiveErr)
		return
	}
	responder.JSON(w, http.StatusOK, toProjectResponse(archived))
}

func (h projectHandlers) listProjectFlowTemplates(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if _, err := h.service.Get(r.Context(), principal.OrganizationID, projectID); err != nil {
		h.respondProjectError(responder, w, err)
		return
	}

	params := api.ParsePaginationParams(r.URL.Query())
	items, err := h.service.ListFlowTemplates(r.Context(), principal.OrganizationID, &projectID)
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	sortFlowTemplatesByCreatedAt(items, params.Order == "asc")
	page, nextCursor, err := paginateByTimeID(items, params, func(item *projectsvc.FlowTemplate) time.Time {
		return item.CreatedAt
	}, func(item *projectsvc.FlowTemplate) uuid.UUID {
		return item.ID
	})
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
		return
	}

	payload := make([]flowTemplateResponse, 0, len(page))
	for _, item := range page {
		payload = append(payload, toFlowTemplateResponse(item))
	}
	total := len(items)
	responder.JSONList(w, http.StatusOK, payload, api.PaginationMeta{
		NextCursor: nextCursor,
		Limit:      params.Limit,
		Total:      &total,
	})
}

func (h projectHandlers) createProjectFlowTemplate(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if _, err := h.service.Get(r.Context(), principal.OrganizationID, projectID); err != nil {
		h.respondProjectError(responder, w, err)
		return
	}

	var req createFlowTemplateRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Slug) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "slug is required")
		return
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(req.Name)
	}
	if displayName == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "display_name is required")
		return
	}

	orgID := principal.OrganizationID
	created, err := h.service.CreateFlowTemplate(r.Context(), projectsvc.CreateFlowTemplateRequest{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           strings.TrimSpace(req.Slug),
		DisplayName:    displayName,
		Description:    req.Description,
		StartNodeID:    req.StartNodeID,
		CreatedByType:  "human_user",
		CreatedByID:    principal.UserID,
	})
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusCreated, toFlowTemplateResponse(created))
}

func (h projectHandlers) listFlowTemplates(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	params := api.ParsePaginationParams(r.URL.Query())
	items, err := h.service.ListFlowTemplates(r.Context(), principal.OrganizationID, nil)
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}

	sortFlowTemplatesByCreatedAt(items, params.Order == "asc")
	page, nextCursor, err := paginateByTimeID(items, params, func(item *projectsvc.FlowTemplate) time.Time {
		return item.CreatedAt
	}, func(item *projectsvc.FlowTemplate) uuid.UUID {
		return item.ID
	})
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
		return
	}

	payload := make([]flowTemplateResponse, 0, len(page))
	for _, item := range page {
		payload = append(payload, toFlowTemplateResponse(item))
	}
	total := len(items)
	responder.JSONList(w, http.StatusOK, payload, api.PaginationMeta{
		NextCursor: nextCursor,
		Limit:      params.Limit,
		Total:      &total,
	})
}

func (h projectHandlers) getFlowTemplate(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	templateID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid flow template id")
		return
	}
	template, getErr := h.service.GetFlowTemplate(r.Context(), principal.OrganizationID, templateID)
	if getErr != nil {
		h.respondProjectError(responder, w, getErr)
		return
	}
	responder.JSON(w, http.StatusOK, toFlowTemplateResponse(template))
}

func (h projectHandlers) updateFlowTemplate(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	templateID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid flow template id")
		return
	}
	template, getErr := h.service.GetFlowTemplate(r.Context(), principal.OrganizationID, templateID)
	if getErr != nil {
		h.respondProjectError(responder, w, getErr)
		return
	}
	if h.isSystemTemplateReadOnlyForPrincipal(principal, template) {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	var req updateFlowTemplateRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	updated, updateErr := h.service.UpdateFlowTemplate(r.Context(), principal.OrganizationID, templateID, projectsvc.UpdateFlowTemplateRequest{
		Slug:          req.Slug,
		DisplayName:   req.DisplayName,
		Description:   req.Description,
		StartNodeID:   req.StartNodeID,
		UpdatedByType: "human_user",
		UpdatedByID:   principal.UserID,
	})
	if updateErr != nil {
		h.respondProjectError(responder, w, updateErr)
		return
	}
	responder.JSON(w, http.StatusOK, toFlowTemplateResponse(updated))
}

func (h projectHandlers) listFlowNodes(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	templateID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid flow template id")
		return
	}
	if _, err := h.service.GetFlowTemplate(r.Context(), principal.OrganizationID, templateID); err != nil {
		h.respondProjectError(responder, w, err)
		return
	}

	params := api.ParsePaginationParams(r.URL.Query())
	items, err := h.service.GetFlowNodes(r.Context(), templateID)
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	sortFlowNodes(items)
	page, nextCursor, err := paginateByTimeID(items, params, func(item *projectsvc.FlowNode) time.Time {
		return item.CreatedAt
	}, func(item *projectsvc.FlowNode) uuid.UUID {
		return item.ID
	})
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
		return
	}

	payload := make([]flowNodeResponse, 0, len(page))
	for _, item := range page {
		payload = append(payload, toFlowNodeResponse(item))
	}
	total := len(items)
	responder.JSONList(w, http.StatusOK, payload, api.PaginationMeta{
		NextCursor: nextCursor,
		Limit:      params.Limit,
		Total:      &total,
	})
}

func (h projectHandlers) addFlowNode(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	templateID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid flow template id")
		return
	}
	template, err := h.service.GetFlowTemplate(r.Context(), principal.OrganizationID, templateID)
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	if h.isSystemTemplateReadOnlyForPrincipal(principal, template) {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	var req createFlowNodeRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(req.Name)
	}
	if displayName == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "display_name is required")
		return
	}
	nodeType := strings.TrimSpace(req.NodeType)
	switch nodeType {
	case "agent_work":
		nodeType = "work"
	case "human_review":
		nodeType = "review"
	}
	if nodeType == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "node_type is required")
		return
	}

	created, createErr := h.service.AddFlowNode(r.Context(), templateID, projectsvc.AddFlowNodeRequest{
		DisplayName:         displayName,
		NodeType:            nodeType,
		Position:            req.Position,
		ActorType:           req.ActorType,
		ActorID:             req.ActorID,
		NextNodeID:          req.NextNodeID,
		RejectNodeID:        req.RejectNodeID,
		MCPTools:            append([]repo.FlowNodeMCPTool{}, req.MCPTools...),
		ToolDomains:         append([]string{}, req.ToolDomains...),
		RequiresHumanReview: req.RequiresHumanReview,
		MaxVisits:           req.MaxVisits,
		Metadata:            normalizeJSONMap(req.Metadata),
	})
	if createErr != nil {
		h.respondProjectError(responder, w, createErr)
		return
	}
	responder.JSON(w, http.StatusCreated, toFlowNodeResponse(created))
}

func (h projectHandlers) updateFlowNode(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	templateID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid flow template id")
		return
	}
	nodeID, err := parseUUIDParam(r, "node_id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid flow node id")
		return
	}
	template, err := h.service.GetFlowTemplate(r.Context(), principal.OrganizationID, templateID)
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	if h.isSystemTemplateReadOnlyForPrincipal(principal, template) {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}
	if !h.nodeBelongsToTemplate(r, templateID, nodeID) {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	var req updateFlowNodeRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	updated, updateErr := h.service.UpdateFlowNode(r.Context(), nodeID, projectsvc.UpdateFlowNodeRequest{
		DisplayName:         req.DisplayName,
		NodeType:            req.NodeType,
		Position:            req.Position,
		ActorType:           req.ActorType,
		ActorID:             req.ActorID,
		NextNodeID:          req.NextNodeID,
		RejectNodeID:        req.RejectNodeID,
		MCPTools:            req.MCPTools,
		ToolDomains:         req.ToolDomains,
		RequiresHumanReview: req.RequiresHumanReview,
		MaxVisits:           req.MaxVisits,
		Metadata:            req.Metadata,
	})
	if updateErr != nil {
		h.respondProjectError(responder, w, updateErr)
		return
	}
	responder.JSON(w, http.StatusOK, toFlowNodeResponse(updated))
}

func (h projectHandlers) deleteFlowNode(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	templateID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid flow template id")
		return
	}
	nodeID, err := parseUUIDParam(r, "node_id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid flow node id")
		return
	}
	template, err := h.service.GetFlowTemplate(r.Context(), principal.OrganizationID, templateID)
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	if h.isSystemTemplateReadOnlyForPrincipal(principal, template) {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}
	if !h.nodeBelongsToTemplate(r, templateID, nodeID) {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	if err := h.service.RemoveFlowNode(r.Context(), nodeID); err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h projectHandlers) listSchedules(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if _, err := h.service.Get(r.Context(), principal.OrganizationID, projectID); err != nil {
		h.respondProjectError(responder, w, err)
		return
	}

	params := api.ParsePaginationParams(r.URL.Query())
	items, err := h.service.ListSchedules(r.Context(), projectID)
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	sortSchedulesByCreatedAt(items, params.Order == "asc")
	page, nextCursor, err := paginateByTimeID(items, params, func(item *projectsvc.TaskSchedule) time.Time {
		return item.CreatedAt
	}, func(item *projectsvc.TaskSchedule) uuid.UUID {
		return item.ID
	})
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
		return
	}

	payload := make([]scheduleResponse, 0, len(page))
	for _, item := range page {
		payload = append(payload, toScheduleResponse(item))
	}
	total := len(items)
	responder.JSONList(w, http.StatusOK, payload, api.PaginationMeta{
		NextCursor: nextCursor,
		Limit:      params.Limit,
		Total:      &total,
	})
}

func (h projectHandlers) createSchedule(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if _, err := h.service.Get(r.Context(), principal.OrganizationID, projectID); err != nil {
		h.respondProjectError(responder, w, err)
		return
	}

	var req createScheduleRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.DisplayName) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "display_name is required")
		return
	}
	if req.FlowTemplateID == uuid.Nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "flow_template_id is required")
		return
	}
	if strings.TrimSpace(req.CronExpression) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "cron_expression is required")
		return
	}
	if err := scheduleCronParser.ValidateExpression(req.CronExpression); err != nil {
		h.respondProjectError(responder, w, fmt.Errorf("%w: %v", projectsvc.ErrInvalidCronExpression, err))
		return
	}

	created, createErr := h.service.CreateSchedule(r.Context(), projectsvc.CreateScheduleRequest{
		OrganizationID: principal.OrganizationID,
		ProjectID:      projectID,
		FlowTemplateID: req.FlowTemplateID,
		DisplayName:    strings.TrimSpace(req.DisplayName),
		CronExpression: strings.TrimSpace(req.CronExpression),
		OverlapPolicy:  strings.TrimSpace(req.OverlapPolicy),
		MaxDurationMS:  req.MaxDurationMS,
		IsEnabled:      req.IsEnabled,
		CreatedByType:  "human_user",
		CreatedByID:    principal.UserID,
	})
	if createErr != nil {
		h.respondProjectError(responder, w, createErr)
		return
	}
	responder.JSON(w, http.StatusCreated, toScheduleResponse(created))
}

func (h projectHandlers) updateSchedule(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	scheduleID, err := parseUUIDParam(r, "schedule_id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid schedule id")
		return
	}

	current, err := h.service.GetSchedule(r.Context(), scheduleID)
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	if current.OrganizationID != principal.OrganizationID || current.ProjectID != projectID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	var req updateScheduleRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.CronExpression != nil {
		if err := scheduleCronParser.ValidateExpression(*req.CronExpression); err != nil {
			h.respondProjectError(responder, w, fmt.Errorf("%w: %v", projectsvc.ErrInvalidCronExpression, err))
			return
		}
	}

	updated, updateErr := h.service.UpdateSchedule(r.Context(), scheduleID, projectsvc.UpdateScheduleRequest{
		FlowTemplateID: req.FlowTemplateID,
		DisplayName:    req.DisplayName,
		CronExpression: req.CronExpression,
		OverlapPolicy:  req.OverlapPolicy,
		MaxDurationMS:  req.MaxDurationMS,
		IsEnabled:      req.IsEnabled,
	})
	if updateErr != nil {
		h.respondProjectError(responder, w, updateErr)
		return
	}
	responder.JSON(w, http.StatusOK, toScheduleResponse(updated))
}

func (h projectHandlers) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	scheduleID, err := parseUUIDParam(r, "schedule_id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid schedule id")
		return
	}
	current, err := h.service.GetSchedule(r.Context(), scheduleID)
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	if current.OrganizationID != principal.OrganizationID || current.ProjectID != projectID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}
	if err := h.service.DeleteSchedule(r.Context(), scheduleID); err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h projectHandlers) enableSchedule(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	scheduleID, err := parseUUIDParam(r, "schedule_id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid schedule id")
		return
	}
	current, err := h.service.GetSchedule(r.Context(), scheduleID)
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	if current.OrganizationID != principal.OrganizationID || current.ProjectID != projectID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	updated, err := h.service.EnableSchedule(r.Context(), scheduleID)
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusOK, map[string]any{
		"schedule_id": updated.ID,
		"next_run_at": updated.NextFireAt,
	})
}

func (h projectHandlers) disableSchedule(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}

	projectID, err := parseUUIDParam(r, "id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	scheduleID, err := parseUUIDParam(r, "schedule_id")
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid schedule id")
		return
	}
	current, err := h.service.GetSchedule(r.Context(), scheduleID)
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	if current.OrganizationID != principal.OrganizationID || current.ProjectID != projectID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	updated, err := h.service.DisableSchedule(r.Context(), scheduleID)
	if err != nil {
		h.respondProjectError(responder, w, err)
		return
	}
	responder.JSON(w, http.StatusOK, map[string]any{
		"schedule_id": updated.ID,
		"enabled":     updated.IsEnabled,
	})
}

func (h projectHandlers) requirePrincipal(w http.ResponseWriter, r *http.Request) (middleware.Principal, bool) {
	responder := api.NewResponder(r.Context())
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "project service unavailable")
		return middleware.Principal{}, false
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return middleware.Principal{}, false
	}
	return principal, true
}

func (h projectHandlers) isSystemTemplateReadOnlyForPrincipal(principal middleware.Principal, template *projectsvc.FlowTemplate) bool {
	if template == nil || template.OrganizationID != nil {
		return false
	}
	return !isProjectAdminRole(principal.Role)
}

func isProjectAdminRole(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), "admin")
}

func (h projectHandlers) nodeBelongsToTemplate(r *http.Request, templateID, nodeID uuid.UUID) bool {
	nodes, err := h.service.GetFlowNodes(r.Context(), templateID)
	if err != nil {
		return false
	}
	for _, node := range nodes {
		if node != nil && node.ID == nodeID {
			return true
		}
	}
	return false
}

type mappedProjectError struct {
	Status  int
	Code    string
	Message string
	Details any
}

func mapProjectError(err error) mappedProjectError {
	switch {
	case errors.Is(err, projectsvc.ErrSlugTaken):
		return mappedProjectError{Status: http.StatusConflict, Code: api.ErrCodeConflict, Message: "conflict"}
	case errors.Is(err, projectsvc.ErrProjectHasActiveTasks):
		return mappedProjectError{Status: http.StatusConflict, Code: "project_has_active_tasks", Message: "project has active tasks"}
	case errors.Is(err, projectsvc.ErrSystemTemplateProtected):
		return mappedProjectError{Status: http.StatusForbidden, Code: api.ErrCodeForbidden, Message: "forbidden"}
	case errors.Is(err, projectsvc.ErrTemplateInUse):
		return mappedProjectError{
			Status:  http.StatusConflict,
			Code:    "template_in_use",
			Message: "A new template version was not created because no modifications were requested",
		}
	case errors.Is(err, projectsvc.ErrInvalidCronExpression):
		return mappedProjectError{
			Status:  http.StatusUnprocessableEntity,
			Code:    "invalid_cron_expression",
			Message: projectsvc.ErrInvalidCronExpression.Error(),
			Details: map[string]any{"detail": cronErrorDetail(err)},
		}
	case errors.Is(err, projectsvc.ErrAgentNotFound):
		return mappedProjectError{Status: http.StatusUnprocessableEntity, Code: api.ErrCodeValidation, Message: projectsvc.ErrAgentNotFound.Error()}
	case errors.Is(err, projectsvc.ErrInvalidSlug),
		errors.Is(err, projectsvc.ErrDisplayNameInvalid),
		errors.Is(err, projectsvc.ErrFlowNodeTemplateMismatch),
		errors.Is(err, projectsvc.ErrOrganizationIDRequired),
		errors.Is(err, projectsvc.ErrProjectIDRequired),
		errors.Is(err, projectsvc.ErrTemplateIDRequired),
		errors.Is(err, projectsvc.ErrNodeIDRequired),
		errors.Is(err, projectsvc.ErrScheduleIDRequired):
		return mappedProjectError{Status: http.StatusUnprocessableEntity, Code: api.ErrCodeValidation, Message: err.Error()}
	case errors.Is(err, repo.ErrNotFound):
		return mappedProjectError{Status: http.StatusNotFound, Code: api.ErrCodeNotFound, Message: "resource not found"}
	case errors.Is(err, repo.ErrConflict):
		return mappedProjectError{Status: http.StatusConflict, Code: api.ErrCodeConflict, Message: "conflict"}
	default:
		return mappedProjectError{Status: http.StatusInternalServerError, Code: api.ErrCodeInternal, Message: "request failed"}
	}
}

func cronErrorDetail(err error) string {
	trimmed := strings.TrimSpace(err.Error())
	prefix := projectsvc.ErrInvalidCronExpression.Error() + ":"
	if strings.HasPrefix(trimmed, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
	}
	return trimmed
}

func (h projectHandlers) respondProjectError(responder api.Responder, w http.ResponseWriter, err error) {
	mapped := mapProjectError(err)
	if mapped.Details != nil {
		responder.ErrorWithDetails(w, mapped.Status, mapped.Code, mapped.Message, mapped.Details)
		return
	}
	responder.Error(w, mapped.Status, mapped.Code, mapped.Message)
}

func sortProjectsByCreatedAt(items []*projectsvc.Project, asc bool) {
	sort.Slice(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.CreatedAt.Equal(right.CreatedAt) {
			if asc {
				return left.ID.String() < right.ID.String()
			}
			return left.ID.String() > right.ID.String()
		}
		if asc {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.CreatedAt.After(right.CreatedAt)
	})
}

func sortFlowTemplatesByCreatedAt(items []*projectsvc.FlowTemplate, asc bool) {
	sort.Slice(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.CreatedAt.Equal(right.CreatedAt) {
			if asc {
				return left.ID.String() < right.ID.String()
			}
			return left.ID.String() > right.ID.String()
		}
		if asc {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.CreatedAt.After(right.CreatedAt)
	})
}

func sortFlowNodes(items []*projectsvc.FlowNode) {
	sort.Slice(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.Position != right.Position {
			return left.Position < right.Position
		}
		if left.CreatedAt.Equal(right.CreatedAt) {
			return left.ID.String() < right.ID.String()
		}
		return left.CreatedAt.Before(right.CreatedAt)
	})
}

func sortSchedulesByCreatedAt(items []*projectsvc.TaskSchedule, asc bool) {
	sort.Slice(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.CreatedAt.Equal(right.CreatedAt) {
			if asc {
				return left.ID.String() < right.ID.String()
			}
			return left.ID.String() > right.ID.String()
		}
		if asc {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.CreatedAt.After(right.CreatedAt)
	})
}

func paginateByTimeID[T any](items []T, params api.PaginationParams, getCreatedAt func(T) time.Time, getID func(T) uuid.UUID) ([]T, *string, error) {
	start := 0
	if strings.TrimSpace(params.Cursor) != "" {
		cursorTime, cursorID, err := (api.PaginationDecoder{}).Decode(params.Cursor)
		if err != nil {
			return nil, nil, err
		}

		start = len(items)
		for i, item := range items {
			itemTime := getCreatedAt(item).UTC()
			itemID := getID(item)
			if itemID == cursorID && itemTime.Equal(cursorTime) {
				start = i + 1
				break
			}

			if params.Order == "asc" {
				if itemTime.After(cursorTime) || (itemTime.Equal(cursorTime) && itemID.String() > cursorID.String()) {
					start = i
					break
				}
				continue
			}
			if itemTime.Before(cursorTime) || (itemTime.Equal(cursorTime) && itemID.String() < cursorID.String()) {
				start = i
				break
			}
		}
	}

	remaining := items[start:]
	page := remaining
	var nextCursor *string
	if len(page) > params.Limit {
		page = page[:params.Limit]
		last := page[len(page)-1]
		encoded := (api.PaginationEncoder{}).Encode(getCreatedAt(last), getID(last))
		nextCursor = &encoded
	}
	return page, nextCursor, nil
}

func parseUUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	return uuid.Parse(strings.TrimSpace(chi.URLParam(r, name)))
}

func normalizeJSONMap(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	return value
}

func normalizeJSONMapPointer(value *json.RawMessage) *json.RawMessage {
	if value == nil {
		return nil
	}
	normalized := normalizeJSONMap(*value)
	return &normalized
}

func toProjectResponse(model *projectsvc.Project) projectResponse {
	settings := normalizeJSONMap(model.Settings)
	return projectResponse{
		ID:                   model.ID,
		OrganizationID:       model.OrganizationID,
		Slug:                 model.Slug,
		DisplayName:          model.DisplayName,
		Description:          model.Description,
		DeliveryMode:         model.DeliveryMode,
		Status:               model.Status,
		DeployFlowTemplateID: model.DeployFlowTemplateID,
		Settings:             settings,
		CreatedByType:        model.CreatedByType,
		CreatedByID:          model.CreatedByID,
		CreatedAt:            model.CreatedAt,
		UpdatedAt:            model.UpdatedAt,
	}
}

func toFlowTemplateResponse(model *projectsvc.FlowTemplate) flowTemplateResponse {
	return flowTemplateResponse{
		ID:             model.ID,
		OrganizationID: model.OrganizationID,
		ProjectID:      model.ProjectID,
		Slug:           model.Slug,
		DisplayName:    model.DisplayName,
		Description:    model.Description,
		IsCurrent:      model.IsCurrent,
		Version:        model.Version,
		StartNodeID:    model.StartNodeID,
		IsSystem:       model.IsSystem,
		CreatedByType:  model.CreatedByType,
		CreatedByID:    model.CreatedByID,
		CreatedAt:      model.CreatedAt,
		UpdatedAt:      model.UpdatedAt,
	}
}

func toFlowNodeResponse(model *projectsvc.FlowNode) flowNodeResponse {
	metadata := normalizeJSONMap(model.Metadata)
	return flowNodeResponse{
		ID:                  model.ID,
		FlowTemplateID:      model.FlowTemplateID,
		DisplayName:         model.DisplayName,
		NodeType:            model.NodeType,
		Position:            model.Position,
		ActorType:           model.ActorType,
		ActorID:             model.ActorID,
		NextNodeID:          model.NextNodeID,
		RejectNodeID:        model.RejectNodeID,
		MCPTools:            append([]repo.FlowNodeMCPTool{}, model.MCPTools...),
		ToolDomains:         append([]string{}, model.ToolDomains...),
		RequiresHumanReview: model.RequiresHumanReview,
		MaxVisits:           model.MaxVisits,
		Metadata:            metadata,
		CreatedAt:           model.CreatedAt,
		UpdatedAt:           model.UpdatedAt,
	}
}

// slugifyProjectName converts a human-readable name into a lowercase slug
// suitable for use as a project URL slug. Used when a slug is not explicitly
// provided in a create-project request.
func slugifyProjectName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	// Strip characters that are not alphanumeric or hyphen
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	s = b.String()
	// Collapse runs of hyphens
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if s == "" {
		s = "project-" + strings.ToLower(uuid.NewString()[:8])
	}
	return s
}

func toScheduleResponse(model *projectsvc.TaskSchedule) scheduleResponse {
	return scheduleResponse{
		ID:             model.ID,
		OrganizationID: model.OrganizationID,
		ProjectID:      model.ProjectID,
		FlowTemplateID: model.FlowTemplateID,
		DisplayName:    model.DisplayName,
		CronExpression: model.CronExpression,
		OverlapPolicy:  model.OverlapPolicy,
		MaxDurationMS:  model.MaxDurationMS,
		IsEnabled:      model.IsEnabled,
		LastFiredAt:    model.LastFiredAt,
		NextFireAt:     model.NextFireAt,
		CreatedByType:  model.CreatedByType,
		CreatedByID:    model.CreatedByID,
		CreatedAt:      model.CreatedAt,
		UpdatedAt:      model.UpdatedAt,
	}
}

type skillResponse struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	ProjectID      *uuid.UUID `json:"project_id"`
	Slug           string     `json:"slug"`
	DisplayName    string     `json:"display_name"`
	Description    string     `json:"description"`
	FilePath       string     `json:"file_path"`
	Version        int        `json:"version"`
	IsActive       bool       `json:"is_active"`
	CreatedByType  string     `json:"created_by_type"`
	CreatedByID    uuid.UUID  `json:"created_by_id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func toSkillResponse(s repo.Skill) skillResponse {
	return skillResponse{
		ID:             s.ID,
		OrganizationID: s.OrganizationID,
		ProjectID:      s.ProjectID,
		Slug:           s.Slug,
		DisplayName:    s.DisplayName,
		Description:    s.Description,
		FilePath:       s.FilePath,
		Version:        s.Version,
		IsActive:       s.IsActive,
		CreatedByType:  s.CreatedByType,
		CreatedByID:    s.CreatedByID,
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
	}
}

// listSkills returns all org-level skills. Pass ?include_inactive=true to
// include disabled skills.
func (h projectHandlers) listSkills(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.skills == nil {
		responder.JSON(w, http.StatusOK, []skillResponse{})
		return
	}
	includeInactive := r.URL.Query().Get("include_inactive") == "true"
	items, err := h.skills.ListByOrg(r.Context(), principal.OrganizationID, includeInactive)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list skills")
		return
	}
	out := make([]skillResponse, 0, len(items))
	for _, s := range items {
		out = append(out, toSkillResponse(s))
	}
	responder.JSON(w, http.StatusOK, out)
}

// listProjectSkills returns skills scoped to a specific project.
func (h projectHandlers) listProjectSkills(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if _, ok := middleware.PrincipalFromContext(r.Context()); !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	projectID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project id")
		return
	}
	if h.skills == nil {
		responder.JSON(w, http.StatusOK, []skillResponse{})
		return
	}
	includeInactive := r.URL.Query().Get("include_inactive") == "true"
	items, err := h.skills.ListByProject(r.Context(), projectID, includeInactive)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list project skills")
		return
	}
	out := make([]skillResponse, 0, len(items))
	for _, s := range items {
		out = append(out, toSkillResponse(s))
	}
	responder.JSON(w, http.StatusOK, out)
}
