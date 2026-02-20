package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type PipelineStaffingHandler struct {
	Store *store.PipelineStepStore
	DB    *sql.DB
}

type pipelineStaffingRequest struct {
	Assignments []pipelineStaffingAssignmentPayload `json:"assignments"`
}

type pipelineStaffingAssignmentPayload struct {
	StepID  string  `json:"step_id"`
	AgentID *string `json:"agent_id"`
}

func (h *PipelineStaffingHandler) Apply(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "pipeline step store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	var req pipelineStaffingRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON payload"})
		return
	}
	if len(req.Assignments) == 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "assignments are required"})
		return
	}

	assignments := make([]store.PipelineStepStaffingAssignment, 0, len(req.Assignments))
	for _, assignment := range req.Assignments {
		assignments = append(assignments, store.PipelineStepStaffingAssignment{
			StepID:          assignment.StepID,
			AssignedAgentID: assignment.AgentID,
		})
	}

	updated, err := h.Store.ApplyStaffingPlan(r.Context(), projectID, assignments)
	if err != nil {
		sendJSON(w, pipelineStepStoreErrorStatus(err), errorResponse{Error: pipelineStepStoreErrorMessage(err)})
		return
	}

	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if h.DB != nil && workspaceID != "" {
		assignmentAudit := make([]map[string]any, 0, len(req.Assignments))
		for _, assignment := range req.Assignments {
			agentID := ""
			if assignment.AgentID != nil {
				agentID = strings.TrimSpace(*assignment.AgentID)
			}
			assignmentAudit = append(assignmentAudit, map[string]any{
				"step_id":  strings.TrimSpace(assignment.StepID),
				"agent_id": agentID,
			})
		}
		_ = logGitHubActivity(
			r.Context(),
			h.DB,
			workspaceID,
			&projectID,
			"pipeline.staffing_plan_applied",
			map[string]any{
				"project_id":   projectID,
				"assignments":  assignmentAudit,
				"step_count":   len(updated),
				"update_count": len(req.Assignments),
			},
		)
	}

	sendJSON(w, http.StatusOK, pipelineStepListResponse{Items: mapPipelineSteps(updated)})
}
