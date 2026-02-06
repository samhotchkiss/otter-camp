package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

type issueReviewSaveRequest struct {
	ReviewerAgentID string  `json:"reviewer_agent_id"`
	Content         string  `json:"content"`
	CommitSubject   *string `json:"commit_subject,omitempty"`
	CommitBody      *string `json:"commit_body,omitempty"`
	AuthorName      *string `json:"author_name,omitempty"`
	AuthorEmail     *string `json:"author_email,omitempty"`
}

type issueReviewSaveResponse struct {
	IssueID         string               `json:"issue_id"`
	ProjectID       string               `json:"project_id"`
	DocumentPath    string               `json:"document_path"`
	ReviewerAgentID string               `json:"reviewer_agent_id"`
	OwnerAgentID    *string              `json:"owner_agent_id,omitempty"`
	ReviewCommitSHA string               `json:"review_commit_sha"`
	Commit          projectCommitPayload `json:"commit"`
}

type issueReviewSavedEvent struct {
	Type            ws.MessageType `json:"type"`
	Channel         string         `json:"channel"`
	IssueID         string         `json:"issue_id"`
	ProjectID       string         `json:"project_id"`
	DocumentPath    string         `json:"document_path"`
	ReviewCommitSHA string         `json:"review_commit_sha"`
	ReviewerAgentID string         `json:"reviewer_agent_id"`
	OwnerAgentID    *string        `json:"owner_agent_id,omitempty"`
}

func (h *IssuesHandler) SaveReview(w http.ResponseWriter, r *http.Request) {
	if h.IssueStore == nil || h.ProjectStore == nil || h.CommitStore == nil || h.ProjectRepos == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	issueID := strings.TrimSpace(chi.URLParam(r, "id"))
	if issueID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "issue id is required"})
		return
	}

	var req issueReviewSaveRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	reviewerAgentID := strings.TrimSpace(req.ReviewerAgentID)
	if !uuidRegex.MatchString(reviewerAgentID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "reviewer_agent_id is required"})
		return
	}

	issue, err := h.IssueStore.GetIssueByID(r.Context(), issueID)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}
	if issue.DocumentPath == nil || strings.TrimSpace(*issue.DocumentPath) == "" {
		sendJSON(w, http.StatusConflict, errorResponse{Error: "issue has no linked document"})
		return
	}

	participants, err := h.IssueStore.ListParticipants(r.Context(), issue.ID, false)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}
	if !isActiveIssueParticipant(reviewerAgentID, participants) {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "reviewer must be an active issue participant"})
		return
	}
	ownerAgentID := ownerAgentIDFromParticipants(participants)

	commitType := browserCommitTypeReview
	authorName := req.AuthorName
	if strings.TrimSpace(optionalStringValue(authorName)) == "" {
		defaultName := "Reviewer " + reviewerAgentID
		authorName = &defaultName
	}
	commitHandler := &ProjectCommitsHandler{
		ProjectStore: h.ProjectStore,
		CommitStore:  h.CommitStore,
		ProjectRepos: h.ProjectRepos,
	}
	commitResponse, statusCode, err := commitHandler.createBrowserCommit(r.Context(), issue.ProjectID, projectCommitCreateRequest{
		Path:          *issue.DocumentPath,
		Content:       req.Content,
		CommitSubject: req.CommitSubject,
		CommitBody:    req.CommitBody,
		CommitType:    &commitType,
		AuthorName:    authorName,
		AuthorEmail:   req.AuthorEmail,
	})
	if err != nil {
		sendJSON(w, statusCode, errorResponse{Error: err.Error()})
		return
	}

	checkpoint, err := h.IssueStore.UpsertReviewCheckpoint(r.Context(), issue.ID, commitResponse.Commit.SHA)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	h.logIssueReviewSaved(
		r.Context(),
		*issue,
		checkpoint,
		commitResponse.Commit.SHA,
		reviewerAgentID,
		ownerAgentID,
	)
	h.broadcastIssueReviewSaved(
		r.Context(),
		*issue,
		commitResponse.Commit.SHA,
		reviewerAgentID,
		ownerAgentID,
	)

	sendJSON(w, http.StatusCreated, issueReviewSaveResponse{
		IssueID:         issue.ID,
		ProjectID:       issue.ProjectID,
		DocumentPath:    strings.TrimSpace(*issue.DocumentPath),
		ReviewerAgentID: reviewerAgentID,
		OwnerAgentID:    ownerAgentID,
		ReviewCommitSHA: commitResponse.Commit.SHA,
		Commit:          commitResponse.Commit,
	})
}

func isActiveIssueParticipant(agentID string, participants []store.ProjectIssueParticipant) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	for _, participant := range participants {
		if participant.RemovedAt != nil {
			continue
		}
		if strings.TrimSpace(participant.AgentID) == agentID {
			return true
		}
	}
	return false
}

func (h *IssuesHandler) logIssueReviewSaved(
	ctx context.Context,
	issue store.ProjectIssue,
	checkpoint *store.ProjectIssueReviewCheckpoint,
	reviewCommitSHA string,
	reviewerAgentID string,
	ownerAgentID *string,
) {
	if h.DB == nil {
		return
	}
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return
	}
	metadata := map[string]any{
		"issue_id":          issue.ID,
		"project_id":        issue.ProjectID,
		"review_commit_sha": strings.TrimSpace(reviewCommitSHA),
		"reviewer_agent_id": strings.TrimSpace(reviewerAgentID),
	}
	if issue.DocumentPath != nil {
		metadata["document_path"] = strings.TrimSpace(*issue.DocumentPath)
	}
	if checkpoint != nil {
		metadata["checkpoint_id"] = checkpoint.ID
		metadata["checkpoint_sha"] = checkpoint.LastReviewCommitSHA
	}
	if ownerAgentID != nil {
		metadata["owner_agent_id"] = *ownerAgentID
	}
	_ = logGitHubActivity(ctx, h.DB, workspaceID, &issue.ProjectID, "issue.review_saved", metadata)
}

func (h *IssuesHandler) broadcastIssueReviewSaved(
	ctx context.Context,
	issue store.ProjectIssue,
	reviewCommitSHA string,
	reviewerAgentID string,
	ownerAgentID *string,
) {
	if h.Hub == nil {
		return
	}
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return
	}
	documentPath := ""
	if issue.DocumentPath != nil {
		documentPath = strings.TrimSpace(*issue.DocumentPath)
	}
	payload, err := json.Marshal(issueReviewSavedEvent{
		Type:            ws.MessageIssueReviewSaved,
		Channel:         issueChannel(issue.ID),
		IssueID:         issue.ID,
		ProjectID:       issue.ProjectID,
		DocumentPath:    documentPath,
		ReviewCommitSHA: strings.TrimSpace(reviewCommitSHA),
		ReviewerAgentID: strings.TrimSpace(reviewerAgentID),
		OwnerAgentID:    ownerAgentID,
	})
	if err != nil {
		return
	}
	h.Hub.BroadcastTopic(workspaceID, issueChannel(issue.ID), payload)
}
