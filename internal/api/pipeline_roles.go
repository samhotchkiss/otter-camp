package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type PipelineRolesHandler struct {
	Store *store.PipelineRoleStore
}

type pipelineRoleMember struct {
	AgentID *string `json:"agentId"`
}

type pipelineRolesRequest struct {
	Planner  *pipelineRoleMember `json:"planner"`
	Worker   *pipelineRoleMember `json:"worker"`
	Reviewer *pipelineRoleMember `json:"reviewer"`
}

func (h *PipelineRolesHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "pipeline role store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	assignments, err := h.Store.ListByProject(r.Context(), projectID)
	if err != nil {
		sendJSON(w, pipelineRoleStoreErrorStatus(err), errorResponse{Error: pipelineRoleStoreErrorMessage(err)})
		return
	}

	sendJSON(w, http.StatusOK, pipelineRolesToResponse(assignments))
}

func (h *PipelineRolesHandler) Put(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "pipeline role store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	var req pipelineRolesRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON payload"})
		return
	}
	if req.Planner == nil || req.Worker == nil || req.Reviewer == nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "planner, worker, and reviewer objects are required"})
		return
	}

	upserts := []struct {
		role   string
		member *pipelineRoleMember
	}{
		{role: store.PipelineRolePlanner, member: req.Planner},
		{role: store.PipelineRoleWorker, member: req.Worker},
		{role: store.PipelineRoleReviewer, member: req.Reviewer},
	}
	for _, upsert := range upserts {
		_, err := h.Store.Upsert(r.Context(), store.UpsertPipelineRoleAssignmentInput{
			ProjectID: projectID,
			Role:      upsert.role,
			AgentID:   upsert.member.AgentID,
		})
		if err != nil {
			sendJSON(w, pipelineRoleStoreErrorStatus(err), errorResponse{Error: pipelineRoleStoreErrorMessage(err)})
			return
		}
	}

	assignments, err := h.Store.ListByProject(r.Context(), projectID)
	if err != nil {
		sendJSON(w, pipelineRoleStoreErrorStatus(err), errorResponse{Error: pipelineRoleStoreErrorMessage(err)})
		return
	}
	sendJSON(w, http.StatusOK, pipelineRolesToResponse(assignments))
}

func pipelineRolesToResponse(assignments []store.PipelineRoleAssignment) map[string]pipelineRoleMember {
	resp := map[string]pipelineRoleMember{
		store.PipelineRolePlanner:  {AgentID: nil},
		store.PipelineRoleWorker:   {AgentID: nil},
		store.PipelineRoleReviewer: {AgentID: nil},
	}
	for _, assignment := range assignments {
		resp[assignment.Role] = pipelineRoleMember{AgentID: assignment.AgentID}
	}
	return resp
}

func pipelineRoleStoreErrorStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		return http.StatusBadRequest
	case errors.Is(err, store.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case strings.Contains(err.Error(), "invalid"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func pipelineRoleStoreErrorMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		return "workspace is required"
	case errors.Is(err, store.ErrForbidden):
		return "forbidden"
	case errors.Is(err, store.ErrNotFound):
		return "project or agent not found"
	default:
		return err.Error()
	}
}
