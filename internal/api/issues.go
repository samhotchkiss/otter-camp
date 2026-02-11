package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	IssueStore         *store.ProjectIssueStore
	ChatThreadStore    *store.ChatThreadStore
	QuestionnaireStore *store.QuestionnaireStore
	ProjectStore       *store.ProjectStore
	CommitStore        *store.ProjectCommitStore
	ProjectRepos       *store.ProjectRepoStore
	DB                 *sql.DB
	Hub                *ws.Hub
	OpenClawDispatcher openClawMessageDispatcher
}

var errIssueHandlerDatabaseUnavailable = errors.New("database not available")

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
	WorkStatus               string  `json:"work_status"`
	Priority                 string  `json:"priority"`
	DueAt                    *string `json:"due_at,omitempty"`
	NextStep                 *string `json:"next_step,omitempty"`
	NextStepDueAt            *string `json:"next_step_due_at,omitempty"`
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

type issueCommentCreateResponse struct {
	issueCommentPayload
	Delivery *dmDeliveryStatus `json:"delivery,omitempty"`
}

type issueDetailPayload struct {
	Issue          issueSummaryPayload       `json:"issue"`
	Participants   []issueParticipantPayload `json:"participants"`
	Comments       []issueCommentPayload     `json:"comments"`
	Questionnaires []questionnairePayload    `json:"questionnaires"`
}

type issueCommentCreatedEvent struct {
	Type    ws.MessageType      `json:"type"`
	Channel string              `json:"channel"`
	IssueID string              `json:"issue_id"`
	Comment issueCommentPayload `json:"comment"`
}

type openClawIssueCommentDispatchEvent struct {
	Type      string                           `json:"type"`
	Timestamp time.Time                        `json:"timestamp"`
	OrgID     string                           `json:"org_id"`
	Data      openClawIssueCommentDispatchData `json:"data"`
}

type openClawIssueCommentDispatchData struct {
	MessageID        string `json:"message_id"`
	IssueID          string `json:"issue_id"`
	ProjectID        string `json:"project_id"`
	IssueNumber      int64  `json:"issue_number,omitempty"`
	IssueTitle       string `json:"issue_title,omitempty"`
	DocumentPath     string `json:"document_path,omitempty"`
	AgentID          string `json:"agent_id"`
	AgentName        string `json:"agent_name,omitempty"`
	ResponderAgentID string `json:"responder_agent_id"`
	SessionKey       string `json:"session_key"`
	Content          string `json:"content"`
	AuthorAgentID    string `json:"author_agent_id,omitempty"`
	SenderType       string `json:"sender_type,omitempty"`
}

type issueCommentDispatchTarget struct {
	ProjectID        string
	IssueNumber      int64
	IssueTitle       string
	DocumentPath     string
	AgentSlug        string
	AgentName        string
	ResponderAgentID string
	SessionKey       string
}

type issueListResponse struct {
	Items []issueSummaryPayload `json:"items"`
	Total int                   `json:"total"`
}

type issuePatchRequest struct {
	OwnerAgentID  *string `json:"owner_agent_id,omitempty"`
	WorkStatus    *string `json:"work_status,omitempty"`
	Priority      *string `json:"priority,omitempty"`
	DueAt         *string `json:"due_at,omitempty"`
	NextStep      *string `json:"next_step,omitempty"`
	NextStepDueAt *string `json:"next_step_due_at,omitempty"`
	State         *string `json:"state,omitempty"`
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
	var issueNumber *int64
	if raw := strings.TrimSpace(r.URL.Query().Get("issue_number")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "issue_number must be a positive integer"})
			return
		}
		issueNumber = &parsed
	}
	var ownerAgentID *string
	if raw := strings.TrimSpace(r.URL.Query().Get("owner_agent_id")); raw != "" {
		ownerAgentID = &raw
	}
	var workStatus *string
	if raw := strings.TrimSpace(r.URL.Query().Get("work_status")); raw != "" {
		workStatus = &raw
	}
	var priority *string
	if raw := strings.TrimSpace(r.URL.Query().Get("priority")); raw != "" {
		priority = &raw
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
		ProjectID:    projectID,
		State:        state,
		Origin:       origin,
		Kind:         kind,
		IssueNumber:  issueNumber,
		OwnerAgentID: ownerAgentID,
		WorkStatus:   workStatus,
		Priority:     priority,
		Limit:        limit,
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

func (h *IssuesHandler) CreateIssue(w http.ResponseWriter, r *http.Request) {
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
		Title         string  `json:"title"`
		Body          *string `json:"body"`
		OwnerAgentID  *string `json:"owner_agent_id"`
		Priority      string  `json:"priority"`
		WorkStatus    string  `json:"work_status"`
		State         string  `json:"state"`
		ApprovalState string  `json:"approval_state"`
		DueAt         *string `json:"due_at"`
		NextStep      *string `json:"next_step"`
		NextStepDueAt *string `json:"next_step_due_at"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "title is required"})
		return
	}

	dueAt, err := parseOptionalRFC3339(req.DueAt, "due_at")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	nextStepDueAt, err := parseOptionalRFC3339(req.NextStepDueAt, "next_step_due_at")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	issue, err := h.IssueStore.CreateIssue(r.Context(), store.CreateProjectIssueInput{
		ProjectID:     projectID,
		Title:         title,
		Body:          req.Body,
		State:         req.State,
		Origin:        "local",
		DocumentPath:  nil,
		ApprovalState: req.ApprovalState,
		OwnerAgentID:  req.OwnerAgentID,
		WorkStatus:    req.WorkStatus,
		Priority:      req.Priority,
		DueAt:         dueAt,
		NextStep:      req.NextStep,
		NextStepDueAt: nextStepDueAt,
	})
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	if issue.OwnerAgentID != nil {
		if _, err := h.IssueStore.AddParticipant(r.Context(), store.AddProjectIssueParticipantInput{
			IssueID: issue.ID,
			AgentID: *issue.OwnerAgentID,
			Role:    "owner",
		}); err != nil {
			handleIssueStoreError(w, err)
			return
		}
	}

	participants, err := h.IssueStore.ListParticipants(r.Context(), issue.ID, false)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusCreated, toIssueSummaryPayload(*issue, participants, nil))
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
	questionnaires := make([]questionnairePayload, 0)
	if h.QuestionnaireStore != nil {
		records, listErr := h.QuestionnaireStore.ListByContext(r.Context(), store.QuestionnaireContextIssue, issueID)
		if listErr != nil {
			handleQuestionnaireStoreError(w, listErr)
			return
		}
		payloads, mapErr := mapQuestionnairePayloads(records)
		if mapErr != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load questionnaires"})
			return
		}
		questionnaires = payloads
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
		Issue:          payload,
		Participants:   mapIssueParticipants(participants),
		Comments:       mapIssueComments(comments),
		Questionnaires: questionnaires,
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
		SenderType    string `json:"sender_type,omitempty"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}
	req.SenderType = strings.TrimSpace(strings.ToLower(req.SenderType))
	if req.SenderType != "" && req.SenderType != "user" && req.SenderType != "agent" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid sender_type"})
		return
	}

	target, shouldDispatch, dispatchWarning, dispatchErr := h.resolveIssueCommentDispatchTarget(
		r.Context(),
		issueID,
		req.SenderType,
	)
	if dispatchErr != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to resolve issue chat target"})
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
	delivery := &dmDeliveryStatus{
		Attempted: shouldDispatch,
		Delivered: false,
	}
	if shouldDispatch {
		event, err := h.buildIssueCommentDispatchEvent(r.Context(), issueID, response, req.SenderType, target)
		if err != nil {
			delivery.Error = "agent delivery unavailable; message was saved"
		} else {
			dedupeKey := fmt.Sprintf("issue.comment.message:%s", response.ID)
			queuedForRetry := false
			if queued, queueErr := enqueueOpenClawDispatchEvent(r.Context(), h.DB, event.OrgID, event.Type, dedupeKey, event); queueErr != nil {
				log.Printf("issue comment dispatch enqueue failed for comment %s: %v", response.ID, queueErr)
			} else {
				queuedForRetry = queued
			}

			if err := h.dispatchIssueCommentToOpenClaw(event); err != nil {
				if queuedForRetry {
					delivery.Error = openClawDispatchQueuedWarning
				} else {
					delivery.Error = "agent delivery unavailable; message was saved"
				}
			} else {
				delivery.Delivered = true
				if queuedForRetry {
					if err := markOpenClawDispatchDeliveredByKey(r.Context(), h.DB, dedupeKey); err != nil {
						log.Printf("failed to mark issue comment dispatch delivered for comment %s: %v", response.ID, err)
					}
				}
			}
		}
	} else if dispatchWarning != "" {
		delivery.Error = dispatchWarning
	}

	h.touchIssueChatThreadBestEffort(r.Context(), r, issueID, response)
	h.broadcastIssueCommentCreated(r.Context(), issueID, response)
	sendJSON(w, http.StatusCreated, issueCommentCreateResponse{
		issueCommentPayload: response,
		Delivery:            delivery,
	})
}

func (h *IssuesHandler) touchIssueChatThreadBestEffort(
	ctx context.Context,
	r *http.Request,
	issueID string,
	comment issueCommentPayload,
) {
	if h.ChatThreadStore == nil || h.IssueStore == nil || h.DB == nil {
		return
	}

	identity, err := requireSessionIdentity(ctx, h.DB, r)
	if err != nil {
		return
	}
	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)

	issue, err := h.IssueStore.GetIssueByID(workspaceCtx, issueID)
	if err != nil {
		return
	}

	title := strings.TrimSpace(issue.Title)
	if title == "" {
		title = "Issue " + issue.ID
	}
	projectID := issue.ProjectID
	trimmedIssueID := strings.TrimSpace(issue.ID)
	lastMessageAt := time.Now().UTC()
	if createdAt, err := time.Parse(time.RFC3339, comment.CreatedAt); err == nil {
		lastMessageAt = createdAt.UTC()
	}

	if _, err := h.ChatThreadStore.TouchThread(workspaceCtx, store.TouchChatThreadInput{
		UserID:             identity.UserID,
		ProjectID:          &projectID,
		IssueID:            &trimmedIssueID,
		ThreadKey:          "issue:" + trimmedIssueID,
		ThreadType:         store.ChatThreadTypeIssue,
		Title:              title,
		LastMessagePreview: strings.TrimSpace(comment.Body),
		LastMessageAt:      lastMessageAt,
	}); err != nil {
		log.Printf("issues: failed to touch chat thread for issue %s: %v", issueID, err)
	}
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
	if h.DB == nil {
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
	requireHumanReview, err := h.projectRequiresHumanReview(r.Context(), before.ProjectID)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}
	targetApprovalState, statusCode, message := resolveApproveTargetApprovalState(
		before.ApprovalState,
		requireHumanReview,
		middleware.UserFromContext(r.Context()),
	)
	if statusCode != 0 {
		sendJSON(w, statusCode, errorResponse{Error: message})
		return
	}

	updated, err := h.IssueStore.TransitionApprovalState(r.Context(), issueID, targetApprovalState)
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
	if strings.TrimSpace(strings.ToLower(updated.ApprovalState)) == store.IssueApprovalStateApproved {
		h.logIssueApproved(r.Context(), *before, *updated)
	}

	sendJSON(w, http.StatusOK, toIssueSummaryPayload(*updated, participants, findIssueLink(linksByIssueID, issueID)))
}

func resolveApproveTargetApprovalState(currentApprovalState string, requireHumanReview bool, actorID string) (string, int, string) {
	if !requireHumanReview {
		return store.IssueApprovalStateApproved, 0, ""
	}

	normalizedState := strings.TrimSpace(strings.ToLower(currentApprovalState))
	switch normalizedState {
	case store.IssueApprovalStateReadyForReview:
		return store.IssueApprovalStateApprovedByReviewer, 0, ""
	case store.IssueApprovalStateApprovedByReviewer:
		if strings.TrimSpace(actorID) == "" {
			return "", http.StatusForbidden, "human approval required"
		}
		return store.IssueApprovalStateApproved, 0, ""
	default:
		return "", http.StatusConflict, "issue must be ready_for_review for reviewer approval"
	}
}

func (h *IssuesHandler) PatchIssue(w http.ResponseWriter, r *http.Request) {
	if h.IssueStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	issueID := strings.TrimSpace(chi.URLParam(r, "id"))
	if issueID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "issue id is required"})
		return
	}

	var req issuePatchRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	hasUpdate := req.OwnerAgentID != nil ||
		req.WorkStatus != nil ||
		req.Priority != nil ||
		req.DueAt != nil ||
		req.NextStep != nil ||
		req.NextStepDueAt != nil ||
		req.State != nil
	if !hasUpdate {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "no fields to update"})
		return
	}

	dueAt, err := parseOptionalRFC3339(req.DueAt, "due_at")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	nextStepDueAt, err := parseOptionalRFC3339(req.NextStepDueAt, "next_step_due_at")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	input := store.UpdateProjectIssueWorkTrackingInput{
		IssueID: issueID,

		SetOwnerAgentID: req.OwnerAgentID != nil,
		OwnerAgentID:    req.OwnerAgentID,

		SetWorkStatus:    req.WorkStatus != nil,
		SetPriority:      req.Priority != nil,
		SetDueAt:         req.DueAt != nil,
		SetNextStep:      req.NextStep != nil,
		SetNextStepDueAt: req.NextStepDueAt != nil,
		SetState:         req.State != nil,
	}
	if req.WorkStatus != nil {
		input.WorkStatus = *req.WorkStatus
	}
	if req.Priority != nil {
		input.Priority = *req.Priority
	}
	if req.State != nil {
		input.State = *req.State
	}
	if req.DueAt != nil {
		input.DueAt = dueAt
	}
	if req.NextStep != nil {
		input.NextStep = req.NextStep
	}
	if req.NextStepDueAt != nil {
		input.NextStepDueAt = nextStepDueAt
	}

	beforeIssue, err := h.IssueStore.GetIssueByID(r.Context(), issueID)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	updated, err := h.IssueStore.UpdateIssueWorkTracking(r.Context(), input)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}
	if h.ChatThreadStore != nil &&
		!strings.EqualFold(strings.TrimSpace(beforeIssue.State), "closed") &&
		strings.EqualFold(strings.TrimSpace(updated.State), "closed") {
		if _, archiveErr := h.ChatThreadStore.AutoArchiveByIssue(r.Context(), issueID); archiveErr != nil {
			log.Printf("issues: failed auto-archive for closed issue %s: %v", issueID, archiveErr)
		}
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
		WorkStatus:     issue.WorkStatus,
		Priority:       issue.Priority,
		NextStep:       issue.NextStep,
		LastActivityAt: issue.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if payload.OwnerAgentID == nil && issue.OwnerAgentID != nil {
		payload.OwnerAgentID = issue.OwnerAgentID
	}
	if issue.DueAt != nil {
		formatted := issue.DueAt.UTC().Format(time.RFC3339)
		payload.DueAt = &formatted
	}
	if issue.NextStepDueAt != nil {
		formatted := issue.NextStepDueAt.UTC().Format(time.RFC3339)
		payload.NextStepDueAt = &formatted
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

func issueCommentSessionKey(agentSlug, issueID string) string {
	return fmt.Sprintf("agent:%s:issue:%s", strings.TrimSpace(agentSlug), strings.TrimSpace(issueID))
}

func (h *IssuesHandler) resolveIssueCommentDispatchTarget(
	ctx context.Context,
	issueID string,
	senderType string,
) (issueCommentDispatchTarget, bool, string, error) {
	if senderType == "agent" {
		return issueCommentDispatchTarget{}, false, "", nil
	}
	if h.DB == nil {
		return issueCommentDispatchTarget{}, false, "issue agent unavailable; message was saved but not delivered", nil
	}

	var target issueCommentDispatchTarget
	err := h.DB.QueryRowContext(ctx, `
		SELECT
			pi.project_id,
			pi.issue_number,
			pi.title,
			COALESCE(pi.document_path, ''),
			COALESCE(owner.agent_id, ''),
			COALESCE(a.slug, ''),
			COALESCE(a.display_name, '')
		FROM project_issues pi
		LEFT JOIN LATERAL (
			SELECT pip.agent_id
			FROM project_issue_participants pip
			WHERE pip.issue_id = pi.id
			  AND pip.role = 'owner'
			  AND pip.removed_at IS NULL
			ORDER BY pip.joined_at ASC
			LIMIT 1
		) owner ON true
		LEFT JOIN agents a ON a.id = owner.agent_id
		WHERE pi.id = $1
	`, strings.TrimSpace(issueID)).Scan(
		&target.ProjectID,
		&target.IssueNumber,
		&target.IssueTitle,
		&target.DocumentPath,
		&target.ResponderAgentID,
		&target.AgentSlug,
		&target.AgentName,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return issueCommentDispatchTarget{}, false, "issue not found", nil
		}
		return issueCommentDispatchTarget{}, false, "", err
	}

	target.ProjectID = strings.TrimSpace(target.ProjectID)
	target.IssueTitle = strings.TrimSpace(target.IssueTitle)
	target.DocumentPath = strings.TrimSpace(target.DocumentPath)
	target.ResponderAgentID = strings.TrimSpace(target.ResponderAgentID)
	target.AgentSlug = strings.TrimSpace(target.AgentSlug)
	target.AgentName = strings.TrimSpace(target.AgentName)
	if target.ResponderAgentID == "" || target.AgentSlug == "" {
		return issueCommentDispatchTarget{}, false, "issue agent unavailable; message was saved but not delivered", nil
	}
	target.SessionKey = issueCommentSessionKey(target.AgentSlug, issueID)
	return target, true, "", nil
}

func (h *IssuesHandler) buildIssueCommentDispatchEvent(
	ctx context.Context,
	issueID string,
	comment issueCommentPayload,
	senderType string,
	target issueCommentDispatchTarget,
) (openClawIssueCommentDispatchEvent, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return openClawIssueCommentDispatchEvent{}, errors.New("missing workspace")
	}

	event := openClawIssueCommentDispatchEvent{
		Type:      "issue.comment.message",
		Timestamp: time.Now().UTC(),
		OrgID:     workspaceID,
		Data: openClawIssueCommentDispatchData{
			MessageID:        comment.ID,
			IssueID:          strings.TrimSpace(issueID),
			ProjectID:        target.ProjectID,
			IssueNumber:      target.IssueNumber,
			IssueTitle:       target.IssueTitle,
			DocumentPath:     target.DocumentPath,
			AgentID:          target.AgentSlug,
			AgentName:        target.AgentName,
			ResponderAgentID: target.ResponderAgentID,
			SessionKey:       target.SessionKey,
			Content:          comment.Body,
			AuthorAgentID:    strings.TrimSpace(comment.AuthorAgentID),
			SenderType:       strings.TrimSpace(senderType),
		},
	}
	return event, nil
}

func (h *IssuesHandler) dispatchIssueCommentToOpenClaw(
	event openClawIssueCommentDispatchEvent,
) error {
	if h.OpenClawDispatcher == nil {
		return ws.ErrOpenClawNotConnected
	}
	return h.OpenClawDispatcher.SendToOpenClaw(event)
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

func (h *IssuesHandler) projectRequiresHumanReview(ctx context.Context, projectID string) (bool, error) {
	if h.DB == nil {
		return false, errIssueHandlerDatabaseUnavailable
	}

	conn, err := store.WithWorkspace(ctx, h.DB)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	var requireHumanReview bool
	err = conn.QueryRowContext(
		ctx,
		`SELECT require_human_review FROM projects WHERE id = $1`,
		projectID,
	).Scan(&requireHumanReview)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, store.ErrNotFound
		}
		return false, err
	}
	return requireHumanReview, nil
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

func parseOptionalRFC3339(value *string, fieldName string) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return nil, fmt.Errorf("%s must be RFC3339 timestamp", fieldName)
	}
	utc := parsed.UTC()
	return &utc, nil
}

func handleIssueStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
	case errors.Is(err, store.ErrValidation):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request"})
	case errors.Is(err, store.ErrConflict):
		sendJSON(w, http.StatusConflict, errorResponse{Error: "invalid state transition"})
	default:
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	}
}
