package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type IssuePipelineActionsHandler struct {
	IssueStore         *store.ProjectIssueStore
	PipelineStepStore  *store.PipelineStepStore
	ProgressionService *IssuePipelineProgressionService
}

type issuePipelineCompleteRequest struct {
	AgentID *string `json:"agent_id"`
	Notes   string  `json:"notes"`
}

type issuePipelineRejectRequest struct {
	AgentID *string `json:"agent_id"`
	Reason  string  `json:"reason"`
}

type issuePipelineActionResponse struct {
	Result IssuePipelineProgressionResult `json:"result"`
	Status issuePipelineStatusResponse    `json:"status"`
}

type issuePipelineStatusResponse struct {
	Issue    issueSummaryPayload        `json:"issue"`
	Pipeline issuePipelineStatusPayload `json:"pipeline"`
}

type issuePipelineStatusPayload struct {
	CurrentStepID       *string                       `json:"current_step_id,omitempty"`
	CurrentStep         *pipelineStepPayload          `json:"current_step,omitempty"`
	PipelineStartedAt   *string                       `json:"pipeline_started_at,omitempty"`
	PipelineCompletedAt *string                       `json:"pipeline_completed_at,omitempty"`
	EllieContextGate    *issuePipelineEllieGateStatus `json:"ellie_context_gate,omitempty"`
	Steps               []pipelineStepPayload         `json:"steps"`
	History             []issuePipelineHistoryPayload `json:"history"`
}

type issuePipelineEllieGateStatus struct {
	Status    string  `json:"status"`
	Error     *string `json:"error,omitempty"`
	CheckedAt *string `json:"checked_at,omitempty"`
}

type issuePipelineHistoryPayload struct {
	ID          string  `json:"id"`
	StepID      string  `json:"step_id"`
	AgentID     *string `json:"agent_id,omitempty"`
	StartedAt   string  `json:"started_at"`
	CompletedAt *string `json:"completed_at,omitempty"`
	Result      string  `json:"result"`
	Notes       string  `json:"notes"`
}

func (h *IssuePipelineActionsHandler) StepComplete(w http.ResponseWriter, r *http.Request) {
	issueID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !uuidRegex.MatchString(issueID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid issue id"})
		return
	}

	var req issuePipelineCompleteRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON payload"})
		return
	}

	progression := h.progressionService()
	if progression == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "pipeline progression service unavailable"})
		return
	}

	result, err := progression.CompleteCurrentStep(r.Context(), issueID, req.AgentID, req.Notes)
	if err != nil {
		sendJSON(w, issuePipelineActionErrorStatus(err), errorResponse{Error: issuePipelineActionErrorMessage(err)})
		return
	}

	status, err := h.loadIssuePipelineStatus(r.Context(), issueID)
	if err != nil {
		sendJSON(w, issuePipelineActionErrorStatus(err), errorResponse{Error: issuePipelineActionErrorMessage(err)})
		return
	}
	sendJSON(w, http.StatusOK, issuePipelineActionResponse{Result: *result, Status: *status})
}

func (h *IssuePipelineActionsHandler) StepReject(w http.ResponseWriter, r *http.Request) {
	issueID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !uuidRegex.MatchString(issueID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid issue id"})
		return
	}

	var req issuePipelineRejectRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON payload"})
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "reason is required"})
		return
	}

	progression := h.progressionService()
	if progression == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "pipeline progression service unavailable"})
		return
	}

	result, err := progression.RejectCurrentStep(r.Context(), issueID, req.AgentID, req.Reason)
	if err != nil {
		sendJSON(w, issuePipelineActionErrorStatus(err), errorResponse{Error: issuePipelineActionErrorMessage(err)})
		return
	}

	status, err := h.loadIssuePipelineStatus(r.Context(), issueID)
	if err != nil {
		sendJSON(w, issuePipelineActionErrorStatus(err), errorResponse{Error: issuePipelineActionErrorMessage(err)})
		return
	}
	sendJSON(w, http.StatusOK, issuePipelineActionResponse{Result: *result, Status: *status})
}

func (h *IssuePipelineActionsHandler) Status(w http.ResponseWriter, r *http.Request) {
	issueID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !uuidRegex.MatchString(issueID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid issue id"})
		return
	}

	status, err := h.loadIssuePipelineStatus(r.Context(), issueID)
	if err != nil {
		sendJSON(w, issuePipelineActionErrorStatus(err), errorResponse{Error: issuePipelineActionErrorMessage(err)})
		return
	}
	sendJSON(w, http.StatusOK, status)
}

func (h *IssuePipelineActionsHandler) loadIssuePipelineStatus(ctx context.Context, issueID string) (*issuePipelineStatusResponse, error) {
	if h.IssueStore == nil || h.PipelineStepStore == nil {
		return nil, errIssueHandlerDatabaseUnavailable
	}

	issue, err := h.IssueStore.GetIssueByID(ctx, issueID)
	if err != nil {
		return nil, err
	}
	state, err := h.PipelineStepStore.GetIssuePipelineState(ctx, issueID)
	if err != nil {
		return nil, err
	}
	steps, err := h.PipelineStepStore.ListStepsByProject(ctx, issue.ProjectID)
	if err != nil {
		return nil, err
	}
	history, err := h.PipelineStepStore.ListIssuePipelineHistory(ctx, issueID)
	if err != nil {
		return nil, err
	}

	stepByID := make(map[string]store.PipelineStep, len(steps))
	for _, step := range steps {
		stepByID[step.ID] = step
	}

	var currentStepPayload *pipelineStepPayload
	if state.CurrentPipelineStepID != nil {
		if step, ok := stepByID[*state.CurrentPipelineStepID]; ok {
			mapped := mapPipelineStep(step)
			currentStepPayload = &mapped
		}
	}
	var ellieGateStatus *issuePipelineEllieGateStatus
	if state.EllieContextGateStatus != nil {
		ellieGateStatus = &issuePipelineEllieGateStatus{
			Status: strings.TrimSpace(*state.EllieContextGateStatus),
			Error:  state.EllieContextGateError,
		}
		if state.EllieContextGateCheckedAt != nil {
			formatted := state.EllieContextGateCheckedAt.UTC().Format(time.RFC3339)
			ellieGateStatus.CheckedAt = &formatted
		}
	}

	return &issuePipelineStatusResponse{
		Issue: toIssueSummaryPayload(*issue, nil, nil),
		Pipeline: issuePipelineStatusPayload{
			CurrentStepID:       state.CurrentPipelineStepID,
			CurrentStep:         currentStepPayload,
			PipelineStartedAt:   formatOptionalPipelineTime(state.PipelineStartedAt),
			PipelineCompletedAt: formatOptionalPipelineTime(state.PipelineCompletedAt),
			EllieContextGate:    ellieGateStatus,
			Steps:               mapPipelineSteps(steps),
			History:             mapIssuePipelineHistoryPayload(history),
		},
	}, nil
}

func (h *IssuePipelineActionsHandler) progressionService() *IssuePipelineProgressionService {
	if h.ProgressionService != nil {
		return h.ProgressionService
	}
	if h.PipelineStepStore == nil {
		return nil
	}
	return &IssuePipelineProgressionService{
		PipelineStepStore: h.PipelineStepStore,
		IssueStore:        h.IssueStore,
	}
}

func mapIssuePipelineHistoryPayload(entries []store.IssuePipelineHistoryEntry) []issuePipelineHistoryPayload {
	out := make([]issuePipelineHistoryPayload, 0, len(entries))
	for _, entry := range entries {
		var completedAt *string
		if entry.CompletedAt != nil {
			formatted := entry.CompletedAt.UTC().Format(time.RFC3339)
			completedAt = &formatted
		}
		out = append(out, issuePipelineHistoryPayload{
			ID:          entry.ID,
			StepID:      entry.StepID,
			AgentID:     entry.AgentID,
			StartedAt:   entry.StartedAt.UTC().Format(time.RFC3339),
			CompletedAt: completedAt,
			Result:      entry.Result,
			Notes:       entry.Notes,
		})
	}
	return out
}

func formatOptionalPipelineTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339)
	return &formatted
}

func issuePipelineActionErrorStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		return http.StatusBadRequest
	case errors.Is(err, store.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, store.ErrValidation):
		return http.StatusBadRequest
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func issuePipelineActionErrorMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		return "workspace is required"
	case errors.Is(err, store.ErrForbidden):
		return "forbidden"
	case errors.Is(err, store.ErrNotFound):
		return "issue not found"
	case errors.Is(err, store.ErrValidation):
		return "invalid pipeline action"
	case errors.Is(err, store.ErrConflict):
		return "invalid pipeline transition"
	default:
		return "internal server error"
	}
}
