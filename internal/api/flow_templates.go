package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type FlowTemplatesHandler struct {
	FlowStore *store.ProjectFlowStore
}

type flowTemplatePayload struct {
	ID          string                    `json:"id"`
	ProjectID   string                    `json:"project_id"`
	Name        string                    `json:"name"`
	Description *string                   `json:"description,omitempty"`
	IsDefault   bool                      `json:"is_default"`
	Steps       []flowTemplateStepPayload `json:"steps,omitempty"`
}

type flowTemplateStepPayload struct {
	ID            string  `json:"id"`
	StepKey       string  `json:"step_key"`
	Label         string  `json:"label"`
	Role          string  `json:"role"`
	NodeType      string  `json:"node_type"`
	Objective     string  `json:"objective"`
	ActorType     string  `json:"actor_type"`
	ActorValue    *string `json:"actor_value,omitempty"`
	NextStepKey   *string `json:"next_step_key,omitempty"`
	RejectStepKey *string `json:"reject_step_key,omitempty"`
	StepOrder     int     `json:"step_order"`
}

type flowTemplateListResponse struct {
	Items []flowTemplatePayload `json:"items"`
}

func (h *FlowTemplatesHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.FlowStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "flow template store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}

	templates, err := h.FlowStore.ListTemplatesByProject(r.Context(), projectID)
	if err != nil {
		handleFlowTemplateStoreError(w, err)
		return
	}

	items := make([]flowTemplatePayload, 0, len(templates))
	for _, template := range templates {
		steps, err := h.FlowStore.ListTemplateSteps(r.Context(), template.ID)
		if err != nil {
			handleFlowTemplateStoreError(w, err)
			return
		}
		items = append(items, flowTemplatePayload{
			ID:          template.ID,
			ProjectID:   template.ProjectID,
			Name:        template.Name,
			Description: template.Description,
			IsDefault:   template.IsDefault,
			Steps:       mapFlowTemplateSteps(steps),
		})
	}

	sendJSON(w, http.StatusOK, flowTemplateListResponse{Items: items})
}

func (h *FlowTemplatesHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.FlowStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "flow template store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	flowID := strings.TrimSpace(chi.URLParam(r, "flowID"))
	if projectID == "" || flowID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id and flow id are required"})
		return
	}

	template, err := h.FlowStore.GetTemplateByID(r.Context(), flowID)
	if err != nil {
		handleFlowTemplateStoreError(w, err)
		return
	}
	if template.ProjectID != projectID {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "flow template does not belong to project"})
		return
	}

	steps, err := h.FlowStore.ListTemplateSteps(r.Context(), flowID)
	if err != nil {
		handleFlowTemplateStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, flowTemplatePayload{
		ID:          template.ID,
		ProjectID:   template.ProjectID,
		Name:        template.Name,
		Description: template.Description,
		IsDefault:   template.IsDefault,
		Steps:       mapFlowTemplateSteps(steps),
	})
}

func (h *FlowTemplatesHandler) Create(w http.ResponseWriter, r *http.Request) {
	if h.FlowStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "flow template store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}

	var req struct {
		Name        string                                     `json:"name"`
		Description *string                                    `json:"description"`
		IsDefault   bool                                       `json:"is_default"`
		Steps       []store.CreateProjectFlowTemplateStepInput `json:"steps"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	template, err := h.FlowStore.CreateTemplate(r.Context(), store.CreateProjectFlowTemplateInput{
		ProjectID:   projectID,
		Name:        req.Name,
		Description: req.Description,
		IsDefault:   req.IsDefault,
	})
	if err != nil {
		handleFlowTemplateStoreError(w, err)
		return
	}

	steps := make([]store.ProjectFlowTemplateStep, 0)
	if len(req.Steps) > 0 {
		steps, err = h.FlowStore.ReplaceTemplateSteps(r.Context(), template.ID, req.Steps)
		if err != nil {
			_ = h.FlowStore.DeleteTemplate(r.Context(), template.ID)
			handleFlowTemplateStoreError(w, err)
			return
		}
	}

	sendJSON(w, http.StatusCreated, flowTemplatePayload{
		ID:          template.ID,
		ProjectID:   template.ProjectID,
		Name:        template.Name,
		Description: template.Description,
		IsDefault:   template.IsDefault,
		Steps:       mapFlowTemplateSteps(steps),
	})
}

func (h *FlowTemplatesHandler) Update(w http.ResponseWriter, r *http.Request) {
	if h.FlowStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "flow template store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	flowID := strings.TrimSpace(chi.URLParam(r, "flowID"))
	if projectID == "" || flowID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id and flow id are required"})
		return
	}

	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		IsDefault   *bool   `json:"is_default"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	template, err := h.FlowStore.UpdateTemplate(r.Context(), store.UpdateProjectFlowTemplateInput{
		TemplateID:  flowID,
		Name:        req.Name,
		Description: req.Description,
		IsDefault:   req.IsDefault,
	})
	if err != nil {
		handleFlowTemplateStoreError(w, err)
		return
	}
	if template.ProjectID != projectID {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "flow template does not belong to project"})
		return
	}

	steps, err := h.FlowStore.ListTemplateSteps(r.Context(), flowID)
	if err != nil {
		handleFlowTemplateStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, flowTemplatePayload{
		ID:          template.ID,
		ProjectID:   template.ProjectID,
		Name:        template.Name,
		Description: template.Description,
		IsDefault:   template.IsDefault,
		Steps:       mapFlowTemplateSteps(steps),
	})
}

func (h *FlowTemplatesHandler) UpdateSteps(w http.ResponseWriter, r *http.Request) {
	if h.FlowStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "flow template store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	flowID := strings.TrimSpace(chi.URLParam(r, "flowID"))
	if projectID == "" || flowID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id and flow id are required"})
		return
	}

	var req struct {
		Steps []store.CreateProjectFlowTemplateStepInput `json:"steps"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	template, err := h.FlowStore.GetTemplateByID(r.Context(), flowID)
	if err != nil {
		handleFlowTemplateStoreError(w, err)
		return
	}
	if template.ProjectID != projectID {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "flow template does not belong to project"})
		return
	}

	steps, err := h.FlowStore.ReplaceTemplateSteps(r.Context(), flowID, req.Steps)
	if err != nil {
		handleFlowTemplateStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, flowTemplatePayload{
		ID:          template.ID,
		ProjectID:   template.ProjectID,
		Name:        template.Name,
		Description: template.Description,
		IsDefault:   template.IsDefault,
		Steps:       mapFlowTemplateSteps(steps),
	})
}

func (h *FlowTemplatesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if h.FlowStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "flow template store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	flowID := strings.TrimSpace(chi.URLParam(r, "flowID"))
	if projectID == "" || flowID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id and flow id are required"})
		return
	}

	template, err := h.FlowStore.GetTemplateByID(r.Context(), flowID)
	if err != nil {
		handleFlowTemplateStoreError(w, err)
		return
	}
	if template.ProjectID != projectID {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "flow template does not belong to project"})
		return
	}

	if err := h.FlowStore.DeleteTemplate(r.Context(), flowID); err != nil {
		handleFlowTemplateStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func mapFlowTemplateSteps(steps []store.ProjectFlowTemplateStep) []flowTemplateStepPayload {
	out := make([]flowTemplateStepPayload, 0, len(steps))
	for _, step := range steps {
		out = append(out, flowTemplateStepPayload{
			ID:            step.ID,
			StepKey:       step.StepKey,
			Label:         step.Label,
			Role:          step.Role,
			NodeType:      step.NodeType,
			Objective:     step.Objective,
			ActorType:     step.ActorType,
			ActorValue:    step.ActorValue,
			NextStepKey:   step.NextStepKey,
			RejectStepKey: step.RejectStepKey,
			StepOrder:     step.StepOrder,
		})
	}
	return out
}

func handleFlowTemplateStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workspace is required"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "flow template not found"})
	case errors.Is(err, store.ErrValidation):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	default:
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	}
}
