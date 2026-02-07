package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

type issueReviewAddressRequest struct {
	AuthorAgentID string  `json:"author_agent_id"`
	Content       string  `json:"content"`
	CommitSubject *string `json:"commit_subject,omitempty"`
	CommitBody    *string `json:"commit_body,omitempty"`
	AuthorName    *string `json:"author_name,omitempty"`
	AuthorEmail   *string `json:"author_email,omitempty"`
}

type issueReviewAddressResponse struct {
	IssueID                  string               `json:"issue_id"`
	ProjectID                string               `json:"project_id"`
	DocumentPath             string               `json:"document_path"`
	AuthorAgentID            string               `json:"author_agent_id"`
	AddressedReviewCommitSHA string               `json:"addressed_review_commit_sha"`
	AddressedInCommitSHA     string               `json:"addressed_in_commit_sha"`
	Commit                   projectCommitPayload `json:"commit"`
}

type issueReviewAddressedEvent struct {
	Type                     ws.MessageType `json:"type"`
	Channel                  string         `json:"channel"`
	IssueID                  string         `json:"issue_id"`
	ProjectID                string         `json:"project_id"`
	DocumentPath             string         `json:"document_path"`
	AddressedReviewCommitSHA string         `json:"addressed_review_commit_sha"`
	AddressedInCommitSHA     string         `json:"addressed_in_commit_sha"`
	AuthorAgentID            string         `json:"author_agent_id"`
	ReviewerAgentID          *string        `json:"reviewer_agent_id,omitempty"`
	SourceURL                string         `json:"source_url"`
}

func (h *IssuesHandler) AddressReview(w http.ResponseWriter, r *http.Request) {
	if h.IssueStore == nil || h.ProjectStore == nil || h.CommitStore == nil || h.ProjectRepos == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	issueID := strings.TrimSpace(chi.URLParam(r, "id"))
	if issueID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "issue id is required"})
		return
	}

	var req issueReviewAddressRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	authorAgentID := strings.TrimSpace(req.AuthorAgentID)
	if !uuidRegex.MatchString(authorAgentID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "author_agent_id is required"})
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
	if !isActiveIssueParticipant(authorAgentID, participants) {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "author must be an active issue participant"})
		return
	}

	pendingVersion, err := h.IssueStore.GetLatestUnaddressedReviewVersion(r.Context(), issue.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			sendJSON(w, http.StatusConflict, errorResponse{Error: "no pending review version to address"})
			return
		}
		handleIssueStoreError(w, err)
		return
	}

	commitType := browserCommitTypeWriting
	authorName := req.AuthorName
	if strings.TrimSpace(optionalStringValue(authorName)) == "" {
		defaultName := "Author " + authorAgentID
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

	updatedVersion, err := h.IssueStore.MarkLatestReviewVersionAddressed(r.Context(), issue.ID, commitResponse.Commit.SHA)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	notificationSent := false
	if pendingVersion.ReviewerAgentID != nil {
		notificationPayload, marshalErr := json.Marshal(map[string]any{
			"source_url": buildIssueReviewSourceURL(
				issue.ProjectID,
				issue.ID,
				updatedVersion.ReviewCommitSHA,
				commitResponse.Commit.SHA,
			),
		})
		if marshalErr != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to encode review notification"})
			return
		}
		_, notificationSent, err = h.IssueStore.CreateReviewNotification(
			r.Context(),
			store.CreateProjectIssueReviewNotificationInput{
				IssueID:              issue.ID,
				NotificationType:     store.IssueReviewNotificationAddressedForReviewer,
				TargetAgentID:        *pendingVersion.ReviewerAgentID,
				ReviewCommitSHA:      updatedVersion.ReviewCommitSHA,
				AddressedInCommitSHA: &commitResponse.Commit.SHA,
				Payload:              notificationPayload,
			},
		)
		if err != nil {
			handleIssueStoreError(w, err)
			return
		}
	}

	h.logIssueReviewAddressed(r.Context(), *issue, pendingVersion, updatedVersion, commitResponse.Commit.SHA, authorAgentID, notificationSent)
	if notificationSent {
		h.broadcastIssueReviewAddressed(r.Context(), *issue, pendingVersion, updatedVersion, commitResponse.Commit.SHA, authorAgentID)
	}

	sendJSON(w, http.StatusCreated, issueReviewAddressResponse{
		IssueID:                  issue.ID,
		ProjectID:                issue.ProjectID,
		DocumentPath:             strings.TrimSpace(*issue.DocumentPath),
		AuthorAgentID:            authorAgentID,
		AddressedReviewCommitSHA: updatedVersion.ReviewCommitSHA,
		AddressedInCommitSHA:     commitResponse.Commit.SHA,
		Commit:                   commitResponse.Commit,
	})
}

func (h *IssuesHandler) logIssueReviewAddressed(
	ctx context.Context,
	issue store.ProjectIssue,
	pendingVersion *store.ProjectIssueReviewVersion,
	updatedVersion *store.ProjectIssueReviewVersion,
	addressedInCommitSHA string,
	authorAgentID string,
	notificationSent bool,
) {
	if h.DB == nil {
		return
	}
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return
	}
	metadata := map[string]any{
		"issue_id":                    issue.ID,
		"project_id":                  issue.ProjectID,
		"addressed_review_commit_sha": updatedVersion.ReviewCommitSHA,
		"addressed_in_commit_sha":     strings.TrimSpace(addressedInCommitSHA),
		"author_agent_id":             strings.TrimSpace(authorAgentID),
	}
	if issue.DocumentPath != nil {
		metadata["document_path"] = strings.TrimSpace(*issue.DocumentPath)
	}
	if pendingVersion != nil && pendingVersion.ReviewerAgentID != nil {
		metadata["reviewer_agent_id"] = *pendingVersion.ReviewerAgentID
	}
	metadata["notification_sent"] = notificationSent
	metadata["source_url"] = buildIssueReviewSourceURL(
		issue.ProjectID,
		issue.ID,
		updatedVersion.ReviewCommitSHA,
		addressedInCommitSHA,
	)
	_ = logGitHubActivity(ctx, h.DB, workspaceID, &issue.ProjectID, "issue.review_addressed", metadata)
}

func (h *IssuesHandler) broadcastIssueReviewAddressed(
	ctx context.Context,
	issue store.ProjectIssue,
	pendingVersion *store.ProjectIssueReviewVersion,
	updatedVersion *store.ProjectIssueReviewVersion,
	addressedInCommitSHA string,
	authorAgentID string,
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
	payload, err := json.Marshal(issueReviewAddressedEvent{
		Type:                     ws.MessageIssueReviewAddressed,
		Channel:                  issueChannel(issue.ID),
		IssueID:                  issue.ID,
		ProjectID:                issue.ProjectID,
		DocumentPath:             documentPath,
		AddressedReviewCommitSHA: updatedVersion.ReviewCommitSHA,
		AddressedInCommitSHA:     strings.TrimSpace(addressedInCommitSHA),
		AuthorAgentID:            strings.TrimSpace(authorAgentID),
		ReviewerAgentID:          pendingVersion.ReviewerAgentID,
		SourceURL: buildIssueReviewSourceURL(
			issue.ProjectID,
			issue.ID,
			updatedVersion.ReviewCommitSHA,
			addressedInCommitSHA,
		),
	})
	if err != nil {
		return
	}
	h.Hub.BroadcastTopic(workspaceID, issueChannel(issue.ID), payload)
}
