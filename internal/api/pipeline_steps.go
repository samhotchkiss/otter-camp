package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type PipelineStepsHandler struct {
	Store *store.PipelineStepStore
}

type pipelineStepCreateRequest struct {
	StepNumber      *int    `json:"step_number"`
	Name            *string `json:"name"`
	Description     *string `json:"description"`
	AssignedAgentID *string `json:"assigned_agent_id"`
	StepType        *string `json:"step_type"`
	AutoAdvance     *bool   `json:"auto_advance"`
}

type pipelineStepListResponse struct {
	Items []pipelineStepPayload `json:"items"`
}

type pipelineStepPayload struct {
	ID              string  `json:"id"`
	ProjectID       string  `json:"project_id"`
	StepNumber      int     `json:"step_number"`
	Name            string  `json:"name"`
	Description     string  `json:"description"`
	AssignedAgentID *string `json:"assigned_agent_id,omitempty"`
	StepType        string  `json:"step_type"`
	AutoAdvance     bool    `json:"auto_advance"`
}

func (h *PipelineStepsHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "pipeline step store unavailable"})
		return
	}
	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	steps, err := h.Store.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		sendJSON(w, pipelineStepStoreErrorStatus(err), errorResponse{Error: pipelineStepStoreErrorMessage(err)})
		return
	}
	sendJSON(w, http.StatusOK, pipelineStepListResponse{Items: mapPipelineSteps(steps)})
}

func (h *PipelineStepsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "pipeline step store unavailable"})
		return
	}
	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	var req pipelineStepCreateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON payload"})
		return
	}
	if req.StepNumber == nil || req.Name == nil || req.StepType == nil || req.AutoAdvance == nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "step_number, name, step_type, and auto_advance are required"})
		return
	}

	input := store.CreatePipelineStepInput{
		ProjectID:       projectID,
		StepNumber:      *req.StepNumber,
		Name:            *req.Name,
		AssignedAgentID: req.AssignedAgentID,
		StepType:        *req.StepType,
		AutoAdvance:     *req.AutoAdvance,
	}
	if req.Description != nil {
		input.Description = *req.Description
	}

	record, err := h.Store.CreateStep(r.Context(), input)
	if err != nil {
		sendJSON(w, pipelineStepStoreErrorStatus(err), errorResponse{Error: pipelineStepStoreErrorMessage(err)})
		return
	}
	sendJSON(w, http.StatusCreated, mapPipelineStep(*record))
}

func (h *PipelineStepsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "pipeline step store unavailable"})
		return
	}
	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}
	stepID := strings.TrimSpace(chi.URLParam(r, "stepID"))
	if !uuidRegex.MatchString(stepID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid step id"})
		return
	}

	var body map[string]json.RawMessage
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON payload"})
		return
	}
	if len(body) == 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "no fields to update"})
		return
	}

	allowed := map[string]struct{}{
		"step_number":       {},
		"name":              {},
		"description":       {},
		"assigned_agent_id": {},
		"step_type":         {},
		"auto_advance":      {},
	}
	for key := range body {
		if _, ok := allowed[key]; !ok {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON payload"})
			return
		}
	}

	input := store.UpdatePipelineStepInput{StepID: stepID}
	if raw, ok := body["step_number"]; ok {
		var value int
		if err := json.Unmarshal(raw, &value); err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid step_number"})
			return
		}
		input.SetStepNumber = true
		input.StepNumber = value
	}
	if raw, ok := body["name"]; ok {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid name"})
			return
		}
		input.SetName = true
		input.Name = value
	}
	if raw, ok := body["description"]; ok {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid description"})
			return
		}
		input.SetDescription = true
		input.Description = value
	}
	if raw, ok := body["assigned_agent_id"]; ok {
		input.SetAssignedAgentID = true
		if string(raw) == "null" {
			input.AssignedAgentID = nil
		} else {
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid assigned_agent_id"})
				return
			}
			input.AssignedAgentID = &value
		}
	}
	if raw, ok := body["step_type"]; ok {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid step_type"})
			return
		}
		input.SetStepType = true
		input.StepType = value
	}
	if raw, ok := body["auto_advance"]; ok {
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid auto_advance"})
			return
		}
		input.SetAutoAdvance = true
		input.AutoAdvance = value
	}

	record, err := h.Store.UpdateStep(r.Context(), input)
	if err != nil {
		sendJSON(w, pipelineStepStoreErrorStatus(err), errorResponse{Error: pipelineStepStoreErrorMessage(err)})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(record.ProjectID), projectID) {
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "pipeline step not found"})
		return
	}
	sendJSON(w, http.StatusOK, mapPipelineStep(*record))
}

func (h *PipelineStepsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "pipeline step store unavailable"})
		return
	}
	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}
	stepID := strings.TrimSpace(chi.URLParam(r, "stepID"))
	if !uuidRegex.MatchString(stepID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid step id"})
		return
	}

	steps, err := h.Store.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		sendJSON(w, pipelineStepStoreErrorStatus(err), errorResponse{Error: pipelineStepStoreErrorMessage(err)})
		return
	}
	found := false
	for _, step := range steps {
		if step.ID == stepID {
			found = true
			break
		}
	}
	if !found {
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "pipeline step not found"})
		return
	}

	if err := h.Store.DeleteStep(r.Context(), stepID); err != nil {
		sendJSON(w, pipelineStepStoreErrorStatus(err), errorResponse{Error: pipelineStepStoreErrorMessage(err)})
		return
	}
	sendJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (h *PipelineStepsHandler) Reorder(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "pipeline step store unavailable"})
		return
	}
	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	var req struct {
		StepIDs []string `json:"step_ids"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON payload"})
		return
	}
	if len(req.StepIDs) == 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "step_ids are required"})
		return
	}

	if err := h.Store.ReorderSteps(r.Context(), projectID, req.StepIDs); err != nil {
		sendJSON(w, pipelineStepStoreErrorStatus(err), errorResponse{Error: pipelineStepStoreErrorMessage(err)})
		return
	}

	steps, err := h.Store.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		sendJSON(w, pipelineStepStoreErrorStatus(err), errorResponse{Error: pipelineStepStoreErrorMessage(err)})
		return
	}
	sendJSON(w, http.StatusOK, pipelineStepListResponse{Items: mapPipelineSteps(steps)})
}

func mapPipelineStep(step store.PipelineStep) pipelineStepPayload {
	return pipelineStepPayload{
		ID:              step.ID,
		ProjectID:       step.ProjectID,
		StepNumber:      step.StepNumber,
		Name:            step.Name,
		Description:     step.Description,
		AssignedAgentID: step.AssignedAgentID,
		StepType:        step.StepType,
		AutoAdvance:     step.AutoAdvance,
	}
}

func mapPipelineSteps(steps []store.PipelineStep) []pipelineStepPayload {
	out := make([]pipelineStepPayload, 0, len(steps))
	for _, step := range steps {
		out = append(out, mapPipelineStep(step))
	}
	return out
}

func pipelineStepStoreErrorStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		return http.StatusBadRequest
	case errors.Is(err, store.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, store.ErrValidation):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func pipelineStepStoreErrorMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		return "workspace is required"
	case errors.Is(err, store.ErrForbidden):
		return "forbidden"
	case errors.Is(err, store.ErrNotFound):
		return "pipeline step not found"
	case errors.Is(err, store.ErrValidation):
		return "invalid pipeline step payload"
	default:
		return "internal server error"
	}
}
