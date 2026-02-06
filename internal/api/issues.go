package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

type IssuesHandler struct {
	IssueStore   *store.ProjectIssueStore
	ProjectStore *store.ProjectStore
	CommitStore  *store.ProjectCommitStore
	ProjectRepos *store.ProjectRepoStore
	DB           *sql.DB
	Hub          *ws.Hub
}

type issueSummaryPayload struct {
	ID                       string  `json:"id"`
	ProjectID                string  `json:"project_id"`
	IssueNumber              int64   `json:"issue_number"`
	Title                    string  `json:"title"`
	Body                     *string `json:"body,omitempty"`
	State                    string  `json:"state"`
	Origin                   string  `json:"origin"`
	DocumentPath             *string `json:"document_path,omitempty"`
	DocumentContent          *string `json:"document_content,omitempty"`
	ApprovalState            string  `json:"approval_state"`
	Kind                     string  `json:"kind"`
	OwnerAgentID             *string `json:"owner_agent_id,omitempty"`
	LastActivityAt           string  `json:"last_activity_at"`
	GitHubNumber             *int64  `json:"github_number,omitempty"`
	GitHubURL                *string `json:"github_url,omitempty"`
	GitHubState              *string `json:"github_state,omitempty"`
	GitHubRepositoryFullName *string `json:"github_repository_full_name,omitempty"`
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

const maxLinkedIssueDocumentBytes = 512 * 1024

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
	var kind *string
	if raw := strings.TrimSpace(r.URL.Query().Get("kind")); raw != "" {
		kind = &raw
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
		Kind:      kind,
		Limit:     limit,
	})
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	issueIDs := make([]string, 0, len(issues))
	for _, issue := range issues {
		issueIDs = append(issueIDs, issue.ID)
	}
	linksByIssueID, err := h.IssueStore.ListGitHubLinksByIssueIDs(r.Context(), issueIDs)
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
		items = append(items, toIssueSummaryPayload(issue, participants, findIssueLink(linksByIssueID, issue.ID)))
	}

	sendJSON(w, http.StatusOK, issueListResponse{Items: items, Total: len(items)})
}

func (h *IssuesHandler) CreateLinkedIssue(w http.ResponseWriter, r *http.Request) {
	if h.IssueStore == nil || h.ProjectStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}
	if _, err := h.ProjectStore.GetByID(r.Context(), projectID); err != nil {
		handleIssueStoreError(w, err)
		return
	}

	var req struct {
		DocumentPath  string `json:"document_path"`
		Title         string `json:"title"`
		ApprovalState string `json:"approval_state"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	documentPath, err := normalizeLinkedPostPath(req.DocumentPath)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = defaultLinkedIssueTitle(documentPath)
	}

	issue, err := h.IssueStore.CreateIssue(r.Context(), store.CreateProjectIssueInput{
		ProjectID:     projectID,
		Title:         title,
		State:         "open",
		Origin:        "local",
		DocumentPath:  &documentPath,
		ApprovalState: req.ApprovalState,
	})
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusCreated, toIssueSummaryPayload(*issue, nil, nil))
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
	linksByIssueID, err := h.IssueStore.ListGitHubLinksByIssueIDs(r.Context(), []string{issueID})
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}
	linkedDocumentContent, err := loadLinkedDocumentContent(issue.ProjectID, issue.DocumentPath)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load linked document"})
		return
	}
	payload := toIssueSummaryPayload(*issue, participants, findIssueLink(linksByIssueID, issue.ID))
	payload.DocumentContent = linkedDocumentContent

	sendJSON(w, http.StatusOK, issueDetailPayload{
		Issue:        payload,
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

func (h *IssuesHandler) TransitionApprovalState(w http.ResponseWriter, r *http.Request) {
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
		ApprovalState string `json:"approval_state"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	before, err := h.IssueStore.GetIssueByID(r.Context(), issueID)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}
	updated, err := h.IssueStore.TransitionApprovalState(r.Context(), issueID, req.ApprovalState)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	participants, err := h.IssueStore.ListParticipants(r.Context(), issueID, false)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}
	linksByIssueID, err := h.IssueStore.ListGitHubLinksByIssueIDs(r.Context(), []string{issueID})
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	h.logIssueApprovalTransition(r.Context(), *before, *updated)

	sendJSON(w, http.StatusOK, toIssueSummaryPayload(*updated, participants, findIssueLink(linksByIssueID, issueID)))
}

func (h *IssuesHandler) Approve(w http.ResponseWriter, r *http.Request) {
	if h.IssueStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	issueID := strings.TrimSpace(chi.URLParam(r, "id"))
	if issueID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "issue id is required"})
		return
	}

	before, err := h.IssueStore.GetIssueByID(r.Context(), issueID)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}
	updated, err := h.IssueStore.TransitionApprovalState(r.Context(), issueID, store.IssueApprovalStateApproved)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	participants, err := h.IssueStore.ListParticipants(r.Context(), issueID, false)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}
	linksByIssueID, err := h.IssueStore.ListGitHubLinksByIssueIDs(r.Context(), []string{issueID})
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	h.logIssueApprovalTransition(r.Context(), *before, *updated)
	h.logIssueApproved(r.Context(), *before, *updated)

	sendJSON(w, http.StatusOK, toIssueSummaryPayload(*updated, participants, findIssueLink(linksByIssueID, issueID)))
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

func toIssueSummaryPayload(
	issue store.ProjectIssue,
	participants []store.ProjectIssueParticipant,
	link *store.ProjectIssueGitHubLink,
) issueSummaryPayload {
	payload := issueSummaryPayload{
		ID:             issue.ID,
		ProjectID:      issue.ProjectID,
		IssueNumber:    issue.IssueNumber,
		Title:          issue.Title,
		Body:           issue.Body,
		State:          issue.State,
		Origin:         issue.Origin,
		DocumentPath:   issue.DocumentPath,
		ApprovalState:  issue.ApprovalState,
		Kind:           inferIssueKind(link),
		OwnerAgentID:   ownerAgentIDFromParticipants(participants),
		LastActivityAt: issue.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if link != nil {
		payload.GitHubNumber = &link.GitHubNumber
		payload.GitHubURL = link.GitHubURL
		gitHubState := link.GitHubState
		payload.GitHubState = &gitHubState
		repo := link.RepositoryFullName
		payload.GitHubRepositoryFullName = &repo
	}
	return payload
}

func inferIssueKind(link *store.ProjectIssueGitHubLink) string {
	if link != nil && link.GitHubURL != nil {
		if strings.Contains(strings.ToLower(strings.TrimSpace(*link.GitHubURL)), "/pull/") {
			return "pull_request"
		}
	}
	return "issue"
}

func findIssueLink(
	links map[string]store.ProjectIssueGitHubLink,
	issueID string,
) *store.ProjectIssueGitHubLink {
	link, ok := links[issueID]
	if !ok {
		return nil
	}
	copy := link
	return &copy
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

func normalizeLinkedPostPath(raw string) (string, error) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return "", errors.New("document_path is required")
	}
	normalized, err := validateContentReadPath(candidate)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(normalized, "/posts/") || !strings.HasSuffix(strings.ToLower(normalized), ".md") {
		return "", errors.New("document_path must point to /posts/*.md")
	}
	return normalized, nil
}

func defaultLinkedIssueTitle(documentPath string) string {
	base := path.Base(strings.TrimSpace(documentPath))
	base = strings.TrimSuffix(base, ".md")
	base = strings.ReplaceAll(base, "-", " ")
	base = strings.ReplaceAll(base, "_", " ")
	base = strings.TrimSpace(base)
	if base == "" {
		return "Post review"
	}
	return "Review: " + base
}

func loadLinkedDocumentContent(projectID string, documentPath *string) (*string, error) {
	if documentPath == nil {
		return nil, nil
	}

	normalized, err := validateContentReadPath(*documentPath)
	if err != nil {
		// Corrupt legacy data should not block issue thread load.
		return nil, nil
	}
	if !strings.HasPrefix(normalized, "/posts/") || !strings.HasSuffix(strings.ToLower(normalized), ".md") {
		return nil, nil
	}

	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, nil
	}
	absolutePath := filepath.Join(contentRootPath(), projectID, filepath.FromSlash(strings.TrimPrefix(normalized, "/")))
	contentBytes, err := os.ReadFile(absolutePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if len(contentBytes) > maxLinkedIssueDocumentBytes {
		return nil, errors.New("linked document is too large")
	}
	if !utf8.Valid(contentBytes) {
		return nil, errors.New("linked document must be valid utf-8")
	}
	content := string(contentBytes)
	return &content, nil
}

func (h *IssuesHandler) logIssueApprovalTransition(
	ctx context.Context,
	before store.ProjectIssue,
	updated store.ProjectIssue,
) {
	if h.DB == nil || before.ApprovalState == updated.ApprovalState {
		return
	}

	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return
	}
	_ = logGitHubActivity(ctx, h.DB, workspaceID, &updated.ProjectID, "issue.approval_state_changed", map[string]any{
		"issue_id":    updated.ID,
		"project_id":  updated.ProjectID,
		"from_state":  before.ApprovalState,
		"to_state":    updated.ApprovalState,
		"issue_state": updated.State,
	})
}

func (h *IssuesHandler) logIssueApproved(
	ctx context.Context,
	before store.ProjectIssue,
	updated store.ProjectIssue,
) {
	if h.DB == nil {
		return
	}
	if before.ApprovalState == store.IssueApprovalStateApproved || updated.ApprovalState != store.IssueApprovalStateApproved {
		return
	}

	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return
	}
	_ = logGitHubActivity(ctx, h.DB, workspaceID, &updated.ProjectID, "issue.approved", map[string]any{
		"issue_id":       updated.ID,
		"project_id":     updated.ProjectID,
		"approval_state": updated.ApprovalState,
		"issue_state":    updated.State,
		"closed_at":      updated.ClosedAt,
	})
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
