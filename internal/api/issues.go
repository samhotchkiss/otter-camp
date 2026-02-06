package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

type IssuesHandler struct {
	IssueStore *store.ProjectIssueStore
	Hub        *ws.Hub
}

type issueSummaryPayload struct {
	ID           string  `json:"id"`
	ProjectID    string  `json:"project_id"`
	IssueNumber  int64   `json:"issue_number"`
	Title        string  `json:"title"`
	Body         *string `json:"body,omitempty"`
	State        string  `json:"state"`
	Origin       string  `json:"origin"`
	OwnerAgentID *string `json:"owner_agent_id,omitempty"`
}

type issueParticipantPayload struct {
	ID        string  `json:"id"`
	AgentID   string  `json:"agent_id"`
	Role      string  `json:"role"`
	RemovedAt *string `json:"removed_at,omitempty"`
}

type issueCommentPayload struct {
	ID            string `json:"id"`
	AuthorAgentID string `json:"author_agent_id"`
	Body          string `json:"body"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type issueDetailPayload struct {
	Issue        issueSummaryPayload       `json:"issue"`
	Participants []issueParticipantPayload `json:"participants"`
	Comments     []issueCommentPayload     `json:"comments"`
}

type issueCommentCreatedEvent struct {
	Type    ws.MessageType      `json:"type"`
	Channel string              `json:"channel"`
	IssueID string              `json:"issue_id"`
	Comment issueCommentPayload `json:"comment"`
}

type issueListResponse struct {
	Items []issueSummaryPayload `json:"items"`
	Total int                   `json:"total"`
}

func (h *IssuesHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.IssueStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project_id is required"})
		return
	}

	var state *string
	if raw := strings.TrimSpace(r.URL.Query().Get("state")); raw != "" {
		state = &raw
	}
	var origin *string
	if raw := strings.TrimSpace(r.URL.Query().Get("origin")); raw != "" {
		origin = &raw
	}

	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be a positive integer"})
			return
		}
		limit = parsed
	}

	issues, err := h.IssueStore.ListIssues(r.Context(), store.ProjectIssueFilter{
		ProjectID: projectID,
		State:     state,
		Origin:    origin,
		Limit:     limit,
	})
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	items := make([]issueSummaryPayload, 0, len(issues))
	for _, issue := range issues {
		participants, err := h.IssueStore.ListParticipants(r.Context(), issue.ID, false)
		if err != nil {
			handleIssueStoreError(w, err)
			return
		}
		items = append(items, issueSummaryPayload{
			ID:           issue.ID,
			ProjectID:    issue.ProjectID,
			IssueNumber:  issue.IssueNumber,
			Title:        issue.Title,
			Body:         issue.Body,
			State:        issue.State,
			Origin:       issue.Origin,
			OwnerAgentID: ownerAgentIDFromParticipants(participants),
		})
	}

	sendJSON(w, http.StatusOK, issueListResponse{Items: items, Total: len(items)})
}

func (h *IssuesHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.IssueStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	issueID := strings.TrimSpace(chi.URLParam(r, "id"))
	if issueID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "issue id is required"})
		return
	}

	issue, err := h.IssueStore.GetIssueByID(r.Context(), issueID)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}
	participants, err := h.IssueStore.ListParticipants(r.Context(), issueID, true)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}
	comments, err := h.IssueStore.ListComments(r.Context(), issueID, 200, 0)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, issueDetailPayload{
		Issue: issueSummaryPayload{
			ID:           issue.ID,
			ProjectID:    issue.ProjectID,
			IssueNumber:  issue.IssueNumber,
			Title:        issue.Title,
			Body:         issue.Body,
			State:        issue.State,
			Origin:       issue.Origin,
			OwnerAgentID: ownerAgentIDFromParticipants(participants),
		},
		Participants: mapIssueParticipants(participants),
		Comments:     mapIssueComments(comments),
	})
}

func (h *IssuesHandler) CreateComment(w http.ResponseWriter, r *http.Request) {
	if h.IssueStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	issueID := strings.TrimSpace(chi.URLParam(r, "id"))
	if issueID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "issue id is required"})
		return
	}

	var req struct {
		AuthorAgentID string `json:"author_agent_id"`
		Body          string `json:"body"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	comment, err := h.IssueStore.CreateComment(r.Context(), store.CreateProjectIssueCommentInput{
		IssueID:       issueID,
		AuthorAgentID: req.AuthorAgentID,
		Body:          req.Body,
	})
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	response := issueCommentPayload{
		ID:            comment.ID,
		AuthorAgentID: comment.AuthorAgentID,
		Body:          comment.Body,
		CreatedAt:     comment.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     comment.UpdatedAt.UTC().Format(time.RFC3339),
	}

	h.broadcastIssueCommentCreated(r.Context(), issueID, response)
	sendJSON(w, http.StatusCreated, response)
}

func (h *IssuesHandler) AddParticipant(w http.ResponseWriter, r *http.Request) {
	if h.IssueStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}
	issueID := strings.TrimSpace(chi.URLParam(r, "id"))
	if issueID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "issue id is required"})
		return
	}

	var req struct {
		AgentID string `json:"agent_id"`
		Role    string `json:"role"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	participant, err := h.IssueStore.AddParticipant(r.Context(), store.AddProjectIssueParticipantInput{
		IssueID: issueID,
		AgentID: req.AgentID,
		Role:    req.Role,
	})
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusCreated, mapIssueParticipant(*participant))
}

func (h *IssuesHandler) RemoveParticipant(w http.ResponseWriter, r *http.Request) {
	if h.IssueStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}
	issueID := strings.TrimSpace(chi.URLParam(r, "id"))
	if issueID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "issue id is required"})
		return
	}
	agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
	if agentID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "agent id is required"})
		return
	}

	if err := h.IssueStore.RemoveParticipant(r.Context(), issueID, agentID); err != nil {
		handleIssueStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

func ownerAgentIDFromParticipants(participants []store.ProjectIssueParticipant) *string {
	for _, participant := range participants {
		if participant.Role == "owner" && participant.RemovedAt == nil {
			id := participant.AgentID
			return &id
		}
	}
	return nil
}

func mapIssueParticipants(participants []store.ProjectIssueParticipant) []issueParticipantPayload {
	out := make([]issueParticipantPayload, 0, len(participants))
	for _, participant := range participants {
		out = append(out, mapIssueParticipant(participant))
	}
	return out
}

func mapIssueParticipant(participant store.ProjectIssueParticipant) issueParticipantPayload {
	var removedAt *string
	if participant.RemovedAt != nil {
		formatted := participant.RemovedAt.UTC().Format(time.RFC3339)
		removedAt = &formatted
	}
	return issueParticipantPayload{
		ID:        participant.ID,
		AgentID:   participant.AgentID,
		Role:      participant.Role,
		RemovedAt: removedAt,
	}
}

func mapIssueComments(comments []store.ProjectIssueComment) []issueCommentPayload {
	out := make([]issueCommentPayload, 0, len(comments))
	for _, comment := range comments {
		out = append(out, issueCommentPayload{
			ID:            comment.ID,
			AuthorAgentID: comment.AuthorAgentID,
			Body:          comment.Body,
			CreatedAt:     comment.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt:     comment.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func (h *IssuesHandler) broadcastIssueCommentCreated(
	ctx context.Context,
	issueID string,
	comment issueCommentPayload,
) {
	if h.Hub == nil {
		return
	}
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return
	}

	channel := issueChannel(issueID)
	payload, err := json.Marshal(issueCommentCreatedEvent{
		Type:    ws.MessageIssueCommentCreated,
		Channel: channel,
		IssueID: strings.TrimSpace(issueID),
		Comment: comment,
	})
	if err != nil {
		return
	}

	h.Hub.BroadcastTopic(workspaceID, channel, payload)
}

func issueChannel(issueID string) string {
	return "issue:" + strings.TrimSpace(issueID)
}

func handleIssueStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
	default:
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	}
}
