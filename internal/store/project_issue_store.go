package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

type ProjectIssue struct {
	ID            string     `json:"id"`
	OrgID         string     `json:"org_id"`
	ProjectID     string     `json:"project_id"`
	ParentIssueID *string    `json:"parent_issue_id,omitempty"`
	IssueNumber   int64      `json:"issue_number"`
	Title         string     `json:"title"`
	Body          *string    `json:"body,omitempty"`
	State         string     `json:"state"`
	Origin        string     `json:"origin"`
	DocumentPath  *string    `json:"document_path,omitempty"`
	ApprovalState string     `json:"approval_state"`
	OwnerAgentID  *string    `json:"owner_agent_id,omitempty"`
	WorkStatus    string     `json:"work_status"`
	Priority      string     `json:"priority"`
	DueAt         *time.Time `json:"due_at,omitempty"`
	NextStep      *string    `json:"next_step,omitempty"`
	NextStepDueAt *time.Time `json:"next_step_due_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ClosedAt      *time.Time `json:"closed_at,omitempty"`
}

type CreateProjectIssueInput struct {
	ProjectID     string
	ParentIssueID *string
	Title         string
	Body          *string
	State         string
	Origin        string
	DocumentPath  *string
	ApprovalState string
	OwnerAgentID  *string
	WorkStatus    string
	Priority      string
	DueAt         *time.Time
	NextStep      *string
	NextStepDueAt *time.Time
	ClosedAt      *time.Time
}

type CreateProjectSubIssueInput struct {
	Title      string
	Body       *string
	Priority   string
	WorkStatus string
}

type UpdateProjectIssueWorkTrackingInput struct {
	IssueID string

	SetOwnerAgentID bool
	OwnerAgentID    *string

	SetParentIssueID bool
	ParentIssueID    *string

	SetWorkStatus bool
	WorkStatus    string

	SetPriority bool
	Priority    string

	SetDueAt bool
	DueAt    *time.Time

	SetNextStep bool
	NextStep    *string

	SetNextStepDueAt bool
	NextStepDueAt    *time.Time

	SetState bool
	State    string
}

type UpsertProjectIssueFromGitHubInput struct {
	ProjectID          string
	RepositoryFullName string
	GitHubNumber       int64
	Title              string
	Body               *string
	State              string
	GitHubURL          *string
	ClosedAt           *time.Time
}

type ProjectIssueFilter struct {
	ProjectID     string
	ParentIssueID *string
	State         *string
	Origin        *string
	Kind          *string
	IssueNumber   *int64
	OwnerAgentID  *string
	WorkStatus    *string
	Priority      *string
	Limit         int
}

type ProjectIssueGitHubLink struct {
	ID                 string    `json:"id"`
	OrgID              string    `json:"org_id"`
	IssueID            string    `json:"issue_id"`
	RepositoryFullName string    `json:"repository_full_name"`
	GitHubNumber       int64     `json:"github_number"`
	GitHubURL          *string   `json:"github_url,omitempty"`
	GitHubState        string    `json:"github_state"`
	LastSyncedAt       time.Time `json:"last_synced_at"`
}

type UpsertProjectIssueGitHubLinkInput struct {
	IssueID            string
	RepositoryFullName string
	GitHubNumber       int64
	GitHubURL          *string
	GitHubState        string
}

type ProjectIssueSyncCheckpoint struct {
	ID                 string    `json:"id"`
	OrgID              string    `json:"org_id"`
	ProjectID          string    `json:"project_id"`
	RepositoryFullName string    `json:"repository_full_name"`
	Resource           string    `json:"resource"`
	Cursor             *string   `json:"cursor,omitempty"`
	LastSyncedAt       time.Time `json:"last_synced_at"`
}

type ProjectIssueReviewCheckpoint struct {
	ID                  string    `json:"id"`
	OrgID               string    `json:"org_id"`
	IssueID             string    `json:"issue_id"`
	LastReviewCommitSHA string    `json:"last_review_commit_sha"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type ProjectIssueReviewVersion struct {
	ID                   string     `json:"id"`
	OrgID                string     `json:"org_id"`
	IssueID              string     `json:"issue_id"`
	DocumentPath         string     `json:"document_path"`
	ReviewCommitSHA      string     `json:"review_commit_sha"`
	ReviewerAgentID      *string    `json:"reviewer_agent_id,omitempty"`
	AddressedInCommitSHA *string    `json:"addressed_in_commit_sha,omitempty"`
	AddressedAt          *time.Time `json:"addressed_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type ProjectIssueReviewNotification struct {
	ID                   string          `json:"id"`
	OrgID                string          `json:"org_id"`
	IssueID              string          `json:"issue_id"`
	NotificationType     string          `json:"notification_type"`
	TargetAgentID        string          `json:"target_agent_id"`
	ReviewCommitSHA      string          `json:"review_commit_sha"`
	AddressedInCommitSHA string          `json:"addressed_in_commit_sha"`
	Payload              json.RawMessage `json:"payload"`
	CreatedAt            time.Time       `json:"created_at"`
}

type CreateProjectIssueReviewNotificationInput struct {
	IssueID              string
	NotificationType     string
	TargetAgentID        string
	ReviewCommitSHA      string
	AddressedInCommitSHA *string
	Payload              json.RawMessage
}

type IssueRoleAssignment struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	ProjectID string    `json:"project_id"`
	Role      string    `json:"role"`
	AgentID   *string   `json:"agent_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type UpsertIssueRoleAssignmentInput struct {
	ProjectID string
	Role      string
	AgentID   *string
}

type ProjectIssueCounts struct {
	Total        int `json:"total"`
	Open         int `json:"open"`
	Closed       int `json:"closed"`
	GitHubOrigin int `json:"github_origin"`
	LocalOrigin  int `json:"local_origin"`
	PullRequests int `json:"pull_requests"`
}

type UpsertProjectIssueSyncCheckpointInput struct {
	ProjectID          string
	RepositoryFullName string
	Resource           string
	Cursor             *string
	LastSyncedAt       *time.Time
}

type ProjectIssueParticipant struct {
	ID        string     `json:"id"`
	OrgID     string     `json:"org_id"`
	IssueID   string     `json:"issue_id"`
	AgentID   string     `json:"agent_id"`
	Role      string     `json:"role"`
	JoinedAt  time.Time  `json:"joined_at"`
	RemovedAt *time.Time `json:"removed_at,omitempty"`
}

type AddProjectIssueParticipantInput struct {
	IssueID string
	AgentID string
	Role    string
}

type ProjectIssueComment struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"org_id"`
	IssueID       string    `json:"issue_id"`
	AuthorAgentID string    `json:"author_agent_id"`
	Body          string    `json:"body"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type CreateProjectIssueCommentInput struct {
	IssueID       string
	AuthorAgentID string
	Body          string
}

type ProjectIssueStore struct {
	db *sql.DB
}

func NewProjectIssueStore(db *sql.DB) *ProjectIssueStore {
	return &ProjectIssueStore{db: db}
}

const (
	IssueApprovalStateDraft          = "draft"
	IssueApprovalStateReadyForReview = "ready_for_review"
	IssueApprovalStateNeedsChanges   = "needs_changes"
	IssueApprovalStateApproved       = "approved"

	IssueWorkStatusQueued       = "queued"
	IssueWorkStatusReady        = "ready"
	IssueWorkStatusPlanning     = "planning"
	IssueWorkStatusReadyForWork = "ready_for_work"
	IssueWorkStatusInProgress   = "in_progress"
	IssueWorkStatusBlocked      = "blocked"
	IssueWorkStatusReview       = "review"
	IssueWorkStatusFlagged      = "flagged"
	IssueWorkStatusDone         = "done"
	IssueWorkStatusCancelled    = "cancelled"

	IssuePriorityP0 = "P0"
	IssuePriorityP1 = "P1"
	IssuePriorityP2 = "P2"
	IssuePriorityP3 = "P3"

	IssueReviewNotificationSavedForOwner        = "review_saved_for_owner"
	IssueReviewNotificationAddressedForReviewer = "review_addressed_for_reviewer"
)

func normalizeIssueApprovalState(state string) string {
	return strings.TrimSpace(strings.ToLower(state))
}

func isValidIssueApprovalState(state string) bool {
	switch normalizeIssueApprovalState(state) {
	case IssueApprovalStateDraft, IssueApprovalStateReadyForReview, IssueApprovalStateNeedsChanges, IssueApprovalStateApproved:
		return true
	default:
		return false
	}
}

func canTransitionIssueApprovalState(currentState, nextState string) bool {
	current := normalizeIssueApprovalState(currentState)
	next := normalizeIssueApprovalState(nextState)
	if current == next {
		return true
	}

	switch current {
	case IssueApprovalStateDraft:
		return next == IssueApprovalStateReadyForReview
	case IssueApprovalStateReadyForReview:
		return next == IssueApprovalStateNeedsChanges || next == IssueApprovalStateApproved
	case IssueApprovalStateNeedsChanges:
		return next == IssueApprovalStateReadyForReview
	case IssueApprovalStateApproved:
		return false
	default:
		return false
	}
}

func defaultApprovalStateForLegacyState(issueState string) string {
	if normalizeIssueState(issueState) == "closed" {
		return IssueApprovalStateApproved
	}
	return IssueApprovalStateDraft
}

func normalizeIssueWorkStatus(status string) string {
	return strings.TrimSpace(strings.ToLower(status))
}

func isValidIssueWorkStatus(status string) bool {
	switch normalizeIssueWorkStatus(status) {
	case IssueWorkStatusQueued,
		IssueWorkStatusReady,
		IssueWorkStatusPlanning,
		IssueWorkStatusReadyForWork,
		IssueWorkStatusInProgress,
		IssueWorkStatusBlocked,
		IssueWorkStatusReview,
		IssueWorkStatusFlagged,
		IssueWorkStatusDone,
		IssueWorkStatusCancelled:
		return true
	default:
		return false
	}
}

func defaultIssueWorkStatusForState(issueState string) string {
	if normalizeIssueState(issueState) == "closed" {
		return IssueWorkStatusDone
	}
	return IssueWorkStatusQueued
}

func canTransitionIssueWorkStatus(currentStatus, nextStatus string) bool {
	current := normalizeIssueWorkStatus(currentStatus)
	next := normalizeIssueWorkStatus(nextStatus)
	if current == next {
		return true
	}

	switch current {
	case IssueWorkStatusQueued:
		return next == IssueWorkStatusReady ||
			next == IssueWorkStatusInProgress ||
			next == IssueWorkStatusBlocked ||
			next == IssueWorkStatusCancelled ||
			next == IssueWorkStatusDone
	case IssueWorkStatusReady:
		return next == IssueWorkStatusPlanning || next == IssueWorkStatusBlocked || next == IssueWorkStatusCancelled
	case IssueWorkStatusPlanning:
		return next == IssueWorkStatusReady ||
			next == IssueWorkStatusReadyForWork ||
			next == IssueWorkStatusBlocked ||
			next == IssueWorkStatusCancelled
	case IssueWorkStatusReadyForWork:
		return next == IssueWorkStatusPlanning ||
			next == IssueWorkStatusInProgress ||
			next == IssueWorkStatusBlocked ||
			next == IssueWorkStatusCancelled
	case IssueWorkStatusInProgress:
		return next == IssueWorkStatusReadyForWork ||
			next == IssueWorkStatusReview ||
			next == IssueWorkStatusBlocked ||
			next == IssueWorkStatusCancelled ||
			next == IssueWorkStatusDone
	case IssueWorkStatusBlocked:
		return next == IssueWorkStatusReady ||
			next == IssueWorkStatusPlanning ||
			next == IssueWorkStatusReadyForWork ||
			next == IssueWorkStatusInProgress ||
			next == IssueWorkStatusCancelled
	case IssueWorkStatusReview:
		return next == IssueWorkStatusInProgress ||
			next == IssueWorkStatusFlagged ||
			next == IssueWorkStatusDone ||
			next == IssueWorkStatusCancelled
	case IssueWorkStatusFlagged:
		return next == IssueWorkStatusQueued ||
			next == IssueWorkStatusPlanning ||
			next == IssueWorkStatusInProgress ||
			next == IssueWorkStatusCancelled
	case IssueWorkStatusDone, IssueWorkStatusCancelled:
		return next == IssueWorkStatusQueued || next == IssueWorkStatusReady
	default:
		return false
	}
}

func claimTransitionIssueWorkStatus(currentStatus string) (string, error) {
	switch normalizeIssueWorkStatus(currentStatus) {
	case IssueWorkStatusQueued:
		return IssueWorkStatusInProgress, nil
	case IssueWorkStatusReady:
		return IssueWorkStatusPlanning, nil
	case IssueWorkStatusReadyForWork:
		return IssueWorkStatusInProgress, nil
	case IssueWorkStatusReview:
		return IssueWorkStatusReview, nil
	default:
		return "", fmt.Errorf("issue is not claimable from current work_status")
	}
}

func releaseTransitionIssueWorkStatus(currentStatus string) (string, error) {
	switch normalizeIssueWorkStatus(currentStatus) {
	case IssueWorkStatusPlanning:
		return IssueWorkStatusReady, nil
	case IssueWorkStatusInProgress:
		return IssueWorkStatusReadyForWork, nil
	case IssueWorkStatusReview:
		return IssueWorkStatusReview, nil
	default:
		return "", fmt.Errorf("issue is not releasable from current work_status")
	}
}

func queueWorkStatusForRole(role string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "planner":
		return IssueWorkStatusReady, nil
	case "worker":
		return IssueWorkStatusReadyForWork, nil
	case "reviewer":
		return IssueWorkStatusReview, nil
	default:
		return "", fmt.Errorf("role must be planner, worker, or reviewer")
	}
}

func normalizeIssuePriority(priority string) string {
	return strings.ToUpper(strings.TrimSpace(priority))
}

func isValidIssuePriority(priority string) bool {
	switch normalizeIssuePriority(priority) {
	case IssuePriorityP0, IssuePriorityP1, IssuePriorityP2, IssuePriorityP3:
		return true
	default:
		return false
	}
}

func normalizeIssueRoleAssignmentRole(role string) string {
	return strings.TrimSpace(strings.ToLower(role))
}

func isValidIssueRoleAssignmentRole(role string) bool {
	switch normalizeIssueRoleAssignmentRole(role) {
	case "planner", "worker", "reviewer":
		return true
	default:
		return false
	}
}

func normalizeOptionalIssueText(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func normalizeOptionalIssueAgentID(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	if !uuidRegex.MatchString(trimmed) {
		return nil, fmt.Errorf("invalid owner_agent_id")
	}
	return &trimmed, nil
}

func normalizeOptionalIssueID(value *string, field string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	if !uuidRegex.MatchString(trimmed) {
		return nil, fmt.Errorf("invalid %s", field)
	}
	return &trimmed, nil
}

func isValidIssueReviewNotificationType(value string) bool {
	switch strings.TrimSpace(value) {
	case IssueReviewNotificationSavedForOwner, IssueReviewNotificationAddressedForReviewer:
		return true
	default:
		return false
	}
}

func normalizeIssueDocumentPath(documentPath *string) (*string, error) {
	if documentPath == nil {
		return nil, nil
	}

	value := strings.TrimSpace(strings.ReplaceAll(*documentPath, "\\", "/"))
	if value == "" {
		return nil, nil
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}

	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return nil, fmt.Errorf("document_path must not traverse directories")
		}
	}

	clean := path.Clean(value)
	if !strings.HasPrefix(clean, "/posts/") || !strings.HasSuffix(strings.ToLower(clean), ".md") {
		return nil, fmt.Errorf("document_path must point to /posts/*.md")
	}

	return &clean, nil
}

func (s *ProjectIssueStore) CreateIssue(ctx context.Context, input CreateProjectIssueInput) (*ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	projectID := strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}

	state := normalizeIssueState(input.State)
	if state == "" {
		state = "open"
	}
	if !isValidIssueState(state) {
		return nil, fmt.Errorf("invalid state")
	}

	origin := strings.TrimSpace(strings.ToLower(input.Origin))
	if origin == "" {
		origin = "local"
	}
	if origin != "local" && origin != "github" {
		return nil, fmt.Errorf("origin must be local or github")
	}

	documentPath, err := normalizeIssueDocumentPath(input.DocumentPath)
	if err != nil {
		return nil, err
	}

	approvalState := normalizeIssueApprovalState(input.ApprovalState)
	if approvalState == "" {
		approvalState = defaultApprovalStateForLegacyState(state)
	}
	if !isValidIssueApprovalState(approvalState) {
		return nil, fmt.Errorf("invalid approval_state")
	}

	ownerAgentID, err := normalizeOptionalIssueAgentID(input.OwnerAgentID)
	if err != nil {
		return nil, err
	}
	parentIssueID, err := normalizeOptionalIssueID(input.ParentIssueID, "parent_issue_id")
	if err != nil {
		return nil, err
	}

	workStatus := normalizeIssueWorkStatus(input.WorkStatus)
	if workStatus == "" {
		workStatus = defaultIssueWorkStatusForState(state)
	}
	if !isValidIssueWorkStatus(workStatus) {
		return nil, fmt.Errorf("invalid work_status")
	}
	if state == "closed" && workStatus != IssueWorkStatusDone && workStatus != IssueWorkStatusCancelled {
		return nil, fmt.Errorf("closed issues require done or cancelled work_status")
	}

	priority := normalizeIssuePriority(input.Priority)
	if priority == "" {
		priority = IssuePriorityP2
	}
	if !isValidIssuePriority(priority) {
		return nil, fmt.Errorf("invalid priority")
	}

	nextStep := normalizeOptionalIssueText(input.NextStep)
	nextStepDueAt := input.NextStepDueAt
	if nextStepDueAt != nil && nextStep == nil {
		return nil, fmt.Errorf("next_step is required when next_step_due_at is set")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureProjectVisible(ctx, tx, projectID); err != nil {
		return nil, err
	}
	if ownerAgentID != nil {
		if err := ensureAgentVisible(ctx, tx, *ownerAgentID); err != nil {
			return nil, err
		}
	}
	if parentIssueID != nil {
		if err := ensureIssueVisible(ctx, tx, *parentIssueID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("parent_issue_id not found")
			}
			return nil, err
		}
		if err := ensureIssueBelongsToProject(ctx, tx, *parentIssueID, projectID); err != nil {
			if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("parent_issue_id must belong to the same project")
			}
			return nil, err
		}
	}

	var nextIssueNumber int64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(issue_number), 0) + 1 FROM project_issues WHERE project_id = $1`,
		projectID,
	).Scan(&nextIssueNumber); err != nil {
		return nil, fmt.Errorf("failed to allocate issue number: %w", err)
	}

	record, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issues (
			org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state,
			owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, closed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at`,
		workspaceID,
		projectID,
		nextIssueNumber,
		title,
		nullableString(input.Body),
		state,
		origin,
		nullableString(documentPath),
		approvalState,
		nullableString(ownerAgentID),
		nullableString(parentIssueID),
		workStatus,
		priority,
		input.DueAt,
		nullableString(nextStep),
		nextStepDueAt,
		input.ClosedAt,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create issue: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit issue create: %w", err)
	}
	return &record, nil
}

func (s *ProjectIssueStore) CreateSubIssuesBatch(
	ctx context.Context,
	parentIssueID string,
	items []CreateProjectSubIssueInput,
) ([]ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	parentIssueID = strings.TrimSpace(parentIssueID)
	if !uuidRegex.MatchString(parentIssueID) {
		return nil, fmt.Errorf("invalid parent_issue_id")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("items are required")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	parent, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`SELECT id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at
			FROM project_issues
			WHERE id = $1
			FOR UPDATE`,
		parentIssueID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load parent issue: %w", err)
	}
	if parent.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	nextIssueNumber := parent.IssueNumber + 1
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(issue_number), 0) + 1 FROM project_issues WHERE project_id = $1`,
		parent.ProjectID,
	).Scan(&nextIssueNumber); err != nil {
		return nil, fmt.Errorf("failed to allocate issue number range: %w", err)
	}

	created := make([]ProjectIssue, 0, len(items))
	for _, item := range items {
		title := strings.TrimSpace(item.Title)
		if title == "" {
			return nil, fmt.Errorf("title is required")
		}

		priority := normalizeIssuePriority(item.Priority)
		if priority == "" {
			priority = IssuePriorityP2
		}
		if !isValidIssuePriority(priority) {
			return nil, fmt.Errorf("invalid priority")
		}

		workStatus := normalizeIssueWorkStatus(item.WorkStatus)
		if workStatus == "" {
			workStatus = IssueWorkStatusQueued
		}
		if !isValidIssueWorkStatus(workStatus) {
			return nil, fmt.Errorf("invalid work_status")
		}

		body := normalizeOptionalIssueText(item.Body)
		record, err := scanProjectIssue(tx.QueryRowContext(
			ctx,
			`INSERT INTO project_issues (
				org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state,
				owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, closed_at
			) VALUES ($1,$2,$3,$4,$5,'open','local',NULL,$6,NULL,$7,$8,$9,NULL,NULL,NULL,NULL)
			RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at`,
			workspaceID,
			parent.ProjectID,
			nextIssueNumber,
			title,
			nullableString(body),
			IssueApprovalStateDraft,
			parentIssueID,
			workStatus,
			priority,
		))
		if err != nil {
			return nil, fmt.Errorf("failed to create sub-issue: %w", err)
		}
		created = append(created, record)
		nextIssueNumber++
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit sub-issues batch: %w", err)
	}
	return created, nil
}

func (s *ProjectIssueStore) TransitionApprovalState(
	ctx context.Context,
	issueID string,
	nextState string,
) (*ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}

	normalizedNext := normalizeIssueApprovalState(nextState)
	if !isValidIssueApprovalState(normalizedNext) {
		return nil, fmt.Errorf("invalid approval_state")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`SELECT id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at
			FROM project_issues
			WHERE id = $1
			FOR UPDATE`,
		issueID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load issue: %w", err)
	}
	if current.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	currentState := normalizeIssueApprovalState(current.ApprovalState)
	if currentState == "" {
		currentState = defaultApprovalStateForLegacyState(current.State)
	}
	if !canTransitionIssueApprovalState(currentState, normalizedNext) {
		return nil, fmt.Errorf("invalid approval_state transition")
	}

	updated := current
	if currentState != normalizedNext {
		updateQuery := `UPDATE project_issues
				SET approval_state = $2
				WHERE id = $1
				RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at`
		if normalizedNext == IssueApprovalStateApproved {
			updateQuery = `UPDATE project_issues
				SET approval_state = $2,
					state = 'closed',
					work_status = CASE WHEN work_status = '` + IssueWorkStatusCancelled + `' THEN work_status ELSE '` + IssueWorkStatusDone + `' END,
					closed_at = COALESCE(closed_at, NOW())
				WHERE id = $1
				RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at`
		}

		updated, err = scanProjectIssue(tx.QueryRowContext(
			ctx,
			updateQuery,
			issueID,
			normalizedNext,
		))
		if err != nil {
			return nil, fmt.Errorf("failed to update approval_state: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *ProjectIssueStore) TransitionWorkStatus(
	ctx context.Context,
	issueID string,
	nextStatus string,
) (*ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}

	normalizedNext := normalizeIssueWorkStatus(nextStatus)
	if !isValidIssueWorkStatus(normalizedNext) {
		return nil, fmt.Errorf("invalid work_status")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`SELECT id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at
			FROM project_issues
			WHERE id = $1
			FOR UPDATE`,
		issueID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load issue: %w", err)
	}
	if current.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	currentStatus := normalizeIssueWorkStatus(current.WorkStatus)
	if currentStatus == "" {
		currentStatus = defaultIssueWorkStatusForState(current.State)
	}
	if !canTransitionIssueWorkStatus(currentStatus, normalizedNext) {
		return nil, fmt.Errorf("invalid work_status transition")
	}

	nextIssueState := current.State
	var nextClosedAt any
	switch normalizedNext {
	case IssueWorkStatusDone, IssueWorkStatusCancelled:
		nextIssueState = "closed"
		nextClosedAt = current.ClosedAt
	case IssueWorkStatusQueued,
		IssueWorkStatusReady,
		IssueWorkStatusPlanning,
		IssueWorkStatusReadyForWork,
		IssueWorkStatusInProgress,
		IssueWorkStatusBlocked,
		IssueWorkStatusReview,
		IssueWorkStatusFlagged:
		nextIssueState = "open"
		nextClosedAt = nil
	}

	updateQuery := `UPDATE project_issues
		SET work_status = $2,
			state = $3,
			closed_at = CASE
				WHEN $3 = 'closed' THEN COALESCE(closed_at, NOW())
				ELSE $4
			END
		WHERE id = $1
		RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at`
	updated, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		updateQuery,
		issueID,
		normalizedNext,
		nextIssueState,
		nextClosedAt,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to update work_status: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *ProjectIssueStore) ClaimIssue(
	ctx context.Context,
	issueID string,
	agentID string,
) (*ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}
	agentID = strings.TrimSpace(agentID)
	if !uuidRegex.MatchString(agentID) {
		return nil, fmt.Errorf("invalid agent_id")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`SELECT id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at
			FROM project_issues
			WHERE id = $1
			FOR UPDATE`,
		issueID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load issue: %w", err)
	}
	if current.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	if current.OwnerAgentID != nil && strings.TrimSpace(*current.OwnerAgentID) != "" && *current.OwnerAgentID != agentID {
		return nil, fmt.Errorf("issue is already claimed")
	}
	if err := ensureAgentVisible(ctx, tx, agentID); err != nil {
		return nil, err
	}

	currentStatus := normalizeIssueWorkStatus(current.WorkStatus)
	if currentStatus == "" {
		currentStatus = defaultIssueWorkStatusForState(current.State)
	}
	nextStatus, err := claimTransitionIssueWorkStatus(currentStatus)
	if err != nil {
		return nil, err
	}
	if !canTransitionIssueWorkStatus(currentStatus, nextStatus) {
		return nil, fmt.Errorf("invalid work_status transition")
	}

	updated, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`UPDATE project_issues
			SET owner_agent_id = $2,
				work_status = $3,
				state = 'open',
				closed_at = NULL
			WHERE id = $1
			RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at`,
		issueID,
		agentID,
		nextStatus,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to claim issue: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *ProjectIssueStore) ReleaseIssue(
	ctx context.Context,
	issueID string,
) (*ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`SELECT id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at
			FROM project_issues
			WHERE id = $1
			FOR UPDATE`,
		issueID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load issue: %w", err)
	}
	if current.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	if current.OwnerAgentID == nil || strings.TrimSpace(*current.OwnerAgentID) == "" {
		return nil, fmt.Errorf("issue is not claimed")
	}

	currentStatus := normalizeIssueWorkStatus(current.WorkStatus)
	if currentStatus == "" {
		currentStatus = defaultIssueWorkStatusForState(current.State)
	}
	nextStatus, err := releaseTransitionIssueWorkStatus(currentStatus)
	if err != nil {
		return nil, err
	}
	if !canTransitionIssueWorkStatus(currentStatus, nextStatus) {
		return nil, fmt.Errorf("invalid work_status transition")
	}

	updated, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`UPDATE project_issues
			SET owner_agent_id = NULL,
				work_status = $2,
				state = 'open',
				closed_at = NULL
			WHERE id = $1
			RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at`,
		issueID,
		nextStatus,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to release issue: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *ProjectIssueStore) ClaimNextQueueIssue(
	ctx context.Context,
	projectID string,
	role string,
	agentID string,
) (*ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}
	agentID = strings.TrimSpace(agentID)
	if !uuidRegex.MatchString(agentID) {
		return nil, fmt.Errorf("invalid agent_id")
	}

	workStatus, err := queueWorkStatusForRole(role)
	if err != nil {
		return nil, err
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureProjectVisible(ctx, tx, projectID); err != nil {
		return nil, err
	}
	if err := ensureAgentVisible(ctx, tx, agentID); err != nil {
		return nil, err
	}

	candidate, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`SELECT id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at
			FROM project_issues
			WHERE project_id = $1
			  AND work_status = $2
			  AND state = 'open'
			  AND owner_agent_id IS NULL
			ORDER BY CASE priority
				WHEN 'P0' THEN 0
				WHEN 'P1' THEN 1
				WHEN 'P2' THEN 2
				WHEN 'P3' THEN 3
				ELSE 4
			END ASC, updated_at ASC, issue_number ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED`,
		projectID,
		workStatus,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to select next queue issue: %w", err)
	}
	if candidate.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	nextStatus, err := claimTransitionIssueWorkStatus(candidate.WorkStatus)
	if err != nil {
		return nil, err
	}
	if !canTransitionIssueWorkStatus(candidate.WorkStatus, nextStatus) {
		return nil, fmt.Errorf("invalid work_status transition")
	}

	updated, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`UPDATE project_issues
			SET owner_agent_id = $2,
				work_status = $3,
				state = 'open',
				closed_at = NULL
			WHERE id = $1
			RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at`,
		candidate.ID,
		agentID,
		nextStatus,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to claim next queue issue: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *ProjectIssueStore) UpdateIssueWorkTracking(
	ctx context.Context,
	input UpdateProjectIssueWorkTrackingInput,
) (*ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID := strings.TrimSpace(input.IssueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`SELECT id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at
			FROM project_issues
			WHERE id = $1
			FOR UPDATE`,
		issueID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load issue: %w", err)
	}
	if current.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	nextOwnerAgentID := current.OwnerAgentID
	if input.SetOwnerAgentID {
		normalizedOwner, err := normalizeOptionalIssueAgentID(input.OwnerAgentID)
		if err != nil {
			return nil, err
		}
		if normalizedOwner != nil {
			if err := ensureAgentVisible(ctx, tx, *normalizedOwner); err != nil {
				return nil, err
			}
		}
		nextOwnerAgentID = normalizedOwner
	}
	nextParentIssueID := current.ParentIssueID
	if input.SetParentIssueID {
		normalizedParent, err := normalizeOptionalIssueID(input.ParentIssueID, "parent_issue_id")
		if err != nil {
			return nil, err
		}
		if normalizedParent != nil {
			if *normalizedParent == issueID {
				return nil, fmt.Errorf("parent_issue_id cannot reference the issue itself")
			}
			if err := ensureIssueVisible(ctx, tx, *normalizedParent); err != nil {
				if errors.Is(err, ErrNotFound) {
					return nil, fmt.Errorf("parent_issue_id not found")
				}
				return nil, err
			}
			if err := ensureIssueBelongsToProject(ctx, tx, *normalizedParent, current.ProjectID); err != nil {
				if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
					return nil, fmt.Errorf("parent_issue_id must belong to the same project")
				}
				return nil, err
			}
		}
		nextParentIssueID = normalizedParent
	}

	currentWorkStatus := normalizeIssueWorkStatus(current.WorkStatus)
	if currentWorkStatus == "" {
		currentWorkStatus = defaultIssueWorkStatusForState(current.State)
	}
	nextWorkStatus := currentWorkStatus
	if input.SetWorkStatus {
		normalizedWorkStatus := normalizeIssueWorkStatus(input.WorkStatus)
		if !isValidIssueWorkStatus(normalizedWorkStatus) {
			return nil, fmt.Errorf("invalid work_status")
		}
		if !canTransitionIssueWorkStatus(currentWorkStatus, normalizedWorkStatus) {
			return nil, fmt.Errorf("invalid work_status transition")
		}
		nextWorkStatus = normalizedWorkStatus
	}

	nextPriority := normalizeIssuePriority(current.Priority)
	if nextPriority == "" {
		nextPriority = IssuePriorityP2
	}
	if input.SetPriority {
		normalizedPriority := normalizeIssuePriority(input.Priority)
		if !isValidIssuePriority(normalizedPriority) {
			return nil, fmt.Errorf("invalid priority")
		}
		nextPriority = normalizedPriority
	}

	nextDueAt := current.DueAt
	if input.SetDueAt {
		nextDueAt = input.DueAt
	}

	nextNextStep := current.NextStep
	if input.SetNextStep {
		nextNextStep = normalizeOptionalIssueText(input.NextStep)
	}

	nextNextStepDueAt := current.NextStepDueAt
	if input.SetNextStepDueAt {
		nextNextStepDueAt = input.NextStepDueAt
	}

	if nextNextStepDueAt != nil && nextNextStep == nil {
		return nil, fmt.Errorf("next_step is required when next_step_due_at is set")
	}

	nextState := normalizeIssueState(current.State)
	if nextState == "" {
		nextState = "open"
	}

	if input.SetState {
		normalizedState := normalizeIssueState(input.State)
		if !isValidIssueState(normalizedState) {
			return nil, fmt.Errorf("invalid state")
		}
		nextState = normalizedState
	}

	if nextWorkStatus == IssueWorkStatusDone || nextWorkStatus == IssueWorkStatusCancelled {
		nextState = "closed"
	}
	if nextState == "open" && (nextWorkStatus == IssueWorkStatusDone || nextWorkStatus == IssueWorkStatusCancelled) {
		if currentWorkStatus != nextWorkStatus {
			return nil, fmt.Errorf("open issues cannot use terminal work_status")
		}
		if !canTransitionIssueWorkStatus(currentWorkStatus, IssueWorkStatusQueued) {
			return nil, fmt.Errorf("invalid work_status transition")
		}
		nextWorkStatus = IssueWorkStatusQueued
	}
	if nextState == "closed" && nextWorkStatus != IssueWorkStatusDone && nextWorkStatus != IssueWorkStatusCancelled {
		if !canTransitionIssueWorkStatus(currentWorkStatus, IssueWorkStatusDone) {
			return nil, fmt.Errorf("invalid work_status transition")
		}
		nextWorkStatus = IssueWorkStatusDone
	}

	updated, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`UPDATE project_issues
			SET owner_agent_id = $2,
				parent_issue_id = $3,
				work_status = $4,
				priority = $5,
				due_at = $6,
				next_step = $7,
				next_step_due_at = $8,
				state = $9,
				closed_at = CASE
					WHEN $9 = 'closed' THEN COALESCE(closed_at, NOW())
					ELSE NULL
				END
			WHERE id = $1
			RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at`,
		issueID,
		nullableString(nextOwnerAgentID),
		nullableString(nextParentIssueID),
		nextWorkStatus,
		nextPriority,
		nextDueAt,
		nullableString(nextNextStep),
		nextNextStepDueAt,
		nextState,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to update issue work tracking: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *ProjectIssueStore) UpsertIssueFromGitHub(
	ctx context.Context,
	input UpsertProjectIssueFromGitHubInput,
) (*ProjectIssue, bool, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, false, ErrNoWorkspace
	}

	projectID := strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, false, fmt.Errorf("invalid project_id")
	}
	repo := strings.TrimSpace(input.RepositoryFullName)
	if repo == "" {
		return nil, false, fmt.Errorf("repository_full_name is required")
	}
	if input.GitHubNumber <= 0 {
		return nil, false, fmt.Errorf("github_number must be greater than zero")
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, false, fmt.Errorf("title is required")
	}
	state := normalizeIssueState(input.State)
	if state == "" {
		state = "open"
	}
	if !isValidIssueState(state) {
		return nil, false, fmt.Errorf("invalid state")
	}
	approvalState := defaultApprovalStateForLegacyState(state)

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureProjectVisible(ctx, tx, projectID); err != nil {
		return nil, false, err
	}

	existingIssue, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`SELECT i.id, i.org_id, i.project_id, i.issue_number, i.title, i.body, i.state, i.origin, i.document_path, i.approval_state, i.owner_agent_id, i.parent_issue_id, i.work_status, i.priority, i.due_at, i.next_step, i.next_step_due_at, i.created_at, i.updated_at, i.closed_at
			FROM project_issues i
			JOIN project_issue_github_links l ON l.issue_id = i.id
			WHERE i.project_id = $1 AND l.repository_full_name = $2 AND l.github_number = $3
			LIMIT 1`,
		projectID,
		repo,
		input.GitHubNumber,
	))
	created := false
	var issue ProjectIssue
	switch {
	case err == nil:
		issue, err = scanProjectIssue(tx.QueryRowContext(
			ctx,
			`UPDATE project_issues
				SET title = $2,
					body = $3,
					state = $4,
					origin = 'github',
					approval_state = $5,
					work_status = CASE
						WHEN $4 = 'closed' THEN '`+IssueWorkStatusDone+`'
						WHEN work_status = '`+IssueWorkStatusDone+`' THEN '`+IssueWorkStatusQueued+`'
						ELSE work_status
					END,
					closed_at = $6
				WHERE id = $1
				RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at`,
			existingIssue.ID,
			title,
			nullableString(input.Body),
			state,
			approvalState,
			input.ClosedAt,
		))
		if err != nil {
			return nil, false, fmt.Errorf("failed to update github issue: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		created = true
		var nextIssueNumber int64
		if err := tx.QueryRowContext(
			ctx,
			`SELECT COALESCE(MAX(issue_number), 0) + 1 FROM project_issues WHERE project_id = $1`,
			projectID,
		).Scan(&nextIssueNumber); err != nil {
			return nil, false, fmt.Errorf("failed to allocate issue number: %w", err)
		}

		workStatus := defaultIssueWorkStatusForState(state)
		issue, err = scanProjectIssue(tx.QueryRowContext(
			ctx,
			`INSERT INTO project_issues (
				org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, work_status, priority, closed_at
			) VALUES ($1,$2,$3,$4,$5,$6,'github',NULL,$7,$8,$9,$10)
			RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at`,
			workspaceID,
			projectID,
			nextIssueNumber,
			title,
			nullableString(input.Body),
			state,
			approvalState,
			workStatus,
			IssuePriorityP2,
			input.ClosedAt,
		))
		if err != nil {
			return nil, false, fmt.Errorf("failed to insert github issue: %w", err)
		}
	default:
		return nil, false, fmt.Errorf("failed to load github issue mapping: %w", err)
	}

	link, err := scanProjectIssueGitHubLink(tx.QueryRowContext(
		ctx,
		`UPDATE project_issue_github_links
			SET repository_full_name = $2,
				github_number = $3,
				github_url = $4,
				github_state = $5,
				last_synced_at = NOW()
			WHERE issue_id = $1
			RETURNING id, org_id, issue_id, repository_full_name, github_number, github_url, github_state, last_synced_at`,
		issue.ID,
		repo,
		input.GitHubNumber,
		nullableString(input.GitHubURL),
		state,
	))
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, fmt.Errorf("failed to update issue github link: %w", err)
		}
		link, err = scanProjectIssueGitHubLink(tx.QueryRowContext(
			ctx,
			`INSERT INTO project_issue_github_links (
				org_id, issue_id, repository_full_name, github_number, github_url, github_state, last_synced_at
			) VALUES ($1,$2,$3,$4,$5,$6,NOW())
			ON CONFLICT (org_id, repository_full_name, github_number)
			DO UPDATE SET
				issue_id = EXCLUDED.issue_id,
				github_url = EXCLUDED.github_url,
				github_state = EXCLUDED.github_state,
				last_synced_at = NOW()
			RETURNING id, org_id, issue_id, repository_full_name, github_number, github_url, github_state, last_synced_at`,
			workspaceID,
			issue.ID,
			repo,
			input.GitHubNumber,
			nullableString(input.GitHubURL),
			state,
		))
		if err != nil {
			return nil, false, fmt.Errorf("failed to insert issue github link: %w", err)
		}
	}

	if link.OrgID != workspaceID {
		return nil, false, ErrForbidden
	}

	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("failed to commit github issue upsert: %w", err)
	}
	return &issue, created, nil
}

func (s *ProjectIssueStore) ListIssues(ctx context.Context, filter ProjectIssueFilter) ([]ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	projectID := strings.TrimSpace(filter.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	query := `SELECT i.id, i.org_id, i.project_id, i.issue_number, i.title, i.body, i.state, i.origin, i.document_path, i.approval_state, i.owner_agent_id, i.parent_issue_id, i.work_status, i.priority, i.due_at, i.next_step, i.next_step_due_at, i.created_at, i.updated_at, i.closed_at
		FROM project_issues i
		LEFT JOIN project_issue_github_links l ON l.issue_id = i.id
		WHERE i.project_id = $1`
	args := []any{projectID}
	argPos := 2

	if filter.State != nil && strings.TrimSpace(*filter.State) != "" {
		state := normalizeIssueState(*filter.State)
		if !isValidIssueState(state) {
			return nil, fmt.Errorf("invalid state filter")
		}
		query += fmt.Sprintf(" AND i.state = $%d", argPos)
		args = append(args, state)
		argPos++
	}
	if filter.Origin != nil && strings.TrimSpace(*filter.Origin) != "" {
		origin := strings.TrimSpace(strings.ToLower(*filter.Origin))
		if origin != "local" && origin != "github" {
			return nil, fmt.Errorf("invalid origin filter")
		}
		query += fmt.Sprintf(" AND i.origin = $%d", argPos)
		args = append(args, origin)
		argPos++
	}
	if filter.Kind != nil && strings.TrimSpace(*filter.Kind) != "" {
		kind := strings.TrimSpace(strings.ToLower(*filter.Kind))
		switch kind {
		case "issue":
			query += ` AND (l.github_url IS NULL OR l.github_url NOT ILIKE '%/pull/%')`
		case "pull_request":
			query += ` AND l.github_url ILIKE '%/pull/%'`
		default:
			return nil, fmt.Errorf("invalid kind filter")
		}
	}
	if filter.IssueNumber != nil {
		if *filter.IssueNumber <= 0 {
			return nil, fmt.Errorf("invalid issue_number filter")
		}
		query += fmt.Sprintf(" AND i.issue_number = $%d", argPos)
		args = append(args, *filter.IssueNumber)
		argPos++
	}
	if filter.ParentIssueID != nil && strings.TrimSpace(*filter.ParentIssueID) != "" {
		parentIssueID := strings.TrimSpace(*filter.ParentIssueID)
		if !uuidRegex.MatchString(parentIssueID) {
			return nil, fmt.Errorf("invalid parent_issue_id filter")
		}
		query += fmt.Sprintf(" AND i.parent_issue_id = $%d", argPos)
		args = append(args, parentIssueID)
		argPos++
	}
	if filter.OwnerAgentID != nil && strings.TrimSpace(*filter.OwnerAgentID) != "" {
		ownerAgentID := strings.TrimSpace(*filter.OwnerAgentID)
		if !uuidRegex.MatchString(ownerAgentID) {
			return nil, fmt.Errorf("invalid owner_agent_id filter")
		}
		query += fmt.Sprintf(" AND i.owner_agent_id = $%d", argPos)
		args = append(args, ownerAgentID)
		argPos++
	}
	if filter.WorkStatus != nil && strings.TrimSpace(*filter.WorkStatus) != "" {
		workStatus := normalizeIssueWorkStatus(*filter.WorkStatus)
		if !isValidIssueWorkStatus(workStatus) {
			return nil, fmt.Errorf("invalid work_status filter")
		}
		query += fmt.Sprintf(" AND i.work_status = $%d", argPos)
		args = append(args, workStatus)
		argPos++
	}
	if filter.Priority != nil && strings.TrimSpace(*filter.Priority) != "" {
		priority := normalizeIssuePriority(*filter.Priority)
		if !isValidIssuePriority(priority) {
			return nil, fmt.Errorf("invalid priority filter")
		}
		query += fmt.Sprintf(" AND i.priority = $%d", argPos)
		args = append(args, priority)
		argPos++
	}

	query += fmt.Sprintf(" ORDER BY i.issue_number DESC LIMIT $%d", argPos)
	args = append(args, limit)

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list issues: %w", err)
	}
	defer rows.Close()

	items := make([]ProjectIssue, 0)
	for rows.Next() {
		issue, err := scanProjectIssue(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan issue row: %w", err)
		}
		if issue.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		items = append(items, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read issue rows: %w", err)
	}
	return items, nil
}

func (s *ProjectIssueStore) ListIssueRoleAssignments(
	ctx context.Context,
	projectID string,
) ([]IssueRoleAssignment, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := ensureProjectVisible(ctx, conn, projectID); err != nil {
		return nil, err
	}

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, project_id, role, agent_id, created_at, updated_at
			FROM issue_role_assignments
			WHERE project_id = $1
			ORDER BY CASE role
				WHEN 'planner' THEN 0
				WHEN 'worker' THEN 1
				WHEN 'reviewer' THEN 2
				ELSE 3
			END, created_at ASC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list issue role assignments: %w", err)
	}
	defer rows.Close()

	items := make([]IssueRoleAssignment, 0)
	for rows.Next() {
		item, err := scanIssueRoleAssignment(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan issue role assignment: %w", err)
		}
		if item.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading issue role assignments: %w", err)
	}

	return items, nil
}

func (s *ProjectIssueStore) UpsertIssueRoleAssignment(
	ctx context.Context,
	input UpsertIssueRoleAssignmentInput,
) (*IssueRoleAssignment, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	projectID := strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	role := normalizeIssueRoleAssignmentRole(input.Role)
	if !isValidIssueRoleAssignmentRole(role) {
		return nil, fmt.Errorf("role must be planner, worker, or reviewer")
	}

	agentID, err := normalizeOptionalIssueID(input.AgentID, "agent_id")
	if err != nil {
		return nil, err
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureProjectVisible(ctx, tx, projectID); err != nil {
		return nil, err
	}
	if agentID != nil {
		if err := ensureAgentVisible(ctx, tx, *agentID); err != nil {
			return nil, err
		}
	}

	record, err := scanIssueRoleAssignment(tx.QueryRowContext(
		ctx,
		`INSERT INTO issue_role_assignments (
			org_id, project_id, role, agent_id
		) VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, role)
		DO UPDATE SET
			agent_id = EXCLUDED.agent_id,
			updated_at = NOW()
		RETURNING id, org_id, project_id, role, agent_id, created_at, updated_at`,
		workspaceID,
		projectID,
		role,
		nullableString(agentID),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to upsert issue role assignment: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *ProjectIssueStore) ListGitHubLinksByIssueIDs(
	ctx context.Context,
	issueIDs []string,
) (map[string]ProjectIssueGitHubLink, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	normalizedIDs := make([]string, 0, len(issueIDs))
	seen := make(map[string]struct{}, len(issueIDs))
	for _, raw := range issueIDs {
		issueID := strings.TrimSpace(raw)
		if !uuidRegex.MatchString(issueID) {
			return nil, fmt.Errorf("invalid issue_id")
		}
		if _, ok := seen[issueID]; ok {
			continue
		}
		seen[issueID] = struct{}{}
		normalizedIDs = append(normalizedIDs, issueID)
	}

	links := make(map[string]ProjectIssueGitHubLink, len(normalizedIDs))
	if len(normalizedIDs) == 0 {
		return links, nil
	}

	placeholders := make([]string, len(normalizedIDs))
	args := make([]any, len(normalizedIDs))
	for i, issueID := range normalizedIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = issueID
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, issue_id, repository_full_name, github_number, github_url, github_state, last_synced_at
			FROM project_issue_github_links
			WHERE issue_id IN (`+strings.Join(placeholders, ",")+`)`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list issue github links: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		link, err := scanProjectIssueGitHubLink(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan issue github link row: %w", err)
		}
		if link.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		links[link.IssueID] = link
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read issue github link rows: %w", err)
	}
	return links, nil
}

func (s *ProjectIssueStore) GetIssueByID(ctx context.Context, issueID string) (*ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	issue, err := scanProjectIssue(conn.QueryRowContext(
		ctx,
		`SELECT id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at
			FROM project_issues
			WHERE id = $1`,
		issueID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get issue: %w", err)
	}
	if issue.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	return &issue, nil
}

func (s *ProjectIssueStore) ListIssuesByDocumentPath(
	ctx context.Context,
	projectID string,
	documentPath string,
) ([]ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	documentPath = strings.TrimSpace(documentPath)
	if documentPath == "" {
		return nil, fmt.Errorf("document_path is required")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at
			FROM project_issues
			WHERE project_id = $1 AND document_path = $2
			ORDER BY created_at ASC`,
		projectID,
		documentPath,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list issues by document_path: %w", err)
	}
	defer rows.Close()

	items := make([]ProjectIssue, 0)
	for rows.Next() {
		item, err := scanProjectIssue(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan issue: %w", err)
		}
		if item.OrgID != workspaceID {
			continue
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading issue rows: %w", err)
	}
	return items, nil
}

func (s *ProjectIssueStore) UpdateIssueDocumentPath(
	ctx context.Context,
	issueID string,
	documentPath *string,
) (*ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}

	normalizedPath, err := normalizeIssueDocumentPath(documentPath)
	if err != nil {
		return nil, err
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	updated, err := scanProjectIssue(conn.QueryRowContext(
		ctx,
		`UPDATE project_issues
			SET document_path = $2
			WHERE id = $1
			RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, owner_agent_id, parent_issue_id, work_status, priority, due_at, next_step, next_step_due_at, created_at, updated_at, closed_at`,
		issueID,
		nullableString(normalizedPath),
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to update issue document_path: %w", err)
	}
	if updated.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	return &updated, nil
}

func (s *ProjectIssueStore) UpsertReviewCheckpoint(
	ctx context.Context,
	issueID string,
	lastReviewCommitSHA string,
) (*ProjectIssueReviewCheckpoint, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}
	lastReviewCommitSHA = strings.TrimSpace(lastReviewCommitSHA)
	if lastReviewCommitSHA == "" {
		return nil, fmt.Errorf("last_review_commit_sha is required")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureIssueVisible(ctx, tx, issueID); err != nil {
		return nil, err
	}

	record, err := scanProjectIssueReviewCheckpoint(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issue_review_checkpoints (
			org_id, issue_id, last_review_commit_sha
		) VALUES ($1,$2,$3)
		ON CONFLICT (issue_id)
		DO UPDATE SET
			last_review_commit_sha = EXCLUDED.last_review_commit_sha,
			updated_at = NOW()
		RETURNING id, org_id, issue_id, last_review_commit_sha, updated_at`,
		workspaceID,
		issueID,
		lastReviewCommitSHA,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to upsert review checkpoint: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit review checkpoint upsert: %w", err)
	}
	return &record, nil
}

func (s *ProjectIssueStore) GetReviewCheckpoint(
	ctx context.Context,
	issueID string,
) (*ProjectIssueReviewCheckpoint, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	record, err := scanProjectIssueReviewCheckpoint(conn.QueryRowContext(
		ctx,
		`SELECT id, org_id, issue_id, last_review_commit_sha, updated_at
			FROM project_issue_review_checkpoints
			WHERE issue_id = $1`,
		issueID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get review checkpoint: %w", err)
	}
	if record.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	return &record, nil
}

func (s *ProjectIssueStore) UpsertReviewVersion(
	ctx context.Context,
	issueID string,
	documentPath string,
	reviewCommitSHA string,
	reviewerAgentID *string,
) (*ProjectIssueReviewVersion, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}
	documentPath = strings.TrimSpace(documentPath)
	if documentPath == "" {
		return nil, fmt.Errorf("document_path is required")
	}
	normalizedPath, err := normalizeIssueDocumentPath(&documentPath)
	if err != nil || normalizedPath == nil {
		return nil, fmt.Errorf("invalid document_path")
	}

	reviewCommitSHA = strings.TrimSpace(reviewCommitSHA)
	if reviewCommitSHA == "" {
		return nil, fmt.Errorf("review_commit_sha is required")
	}

	reviewerID := ""
	if reviewerAgentID != nil {
		reviewerID = strings.TrimSpace(*reviewerAgentID)
	}
	if reviewerID != "" && !uuidRegex.MatchString(reviewerID) {
		return nil, fmt.Errorf("invalid reviewer_agent_id")
	}
	var reviewerIDPtr *string
	if reviewerID != "" {
		reviewerIDPtr = &reviewerID
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureIssueVisible(ctx, tx, issueID); err != nil {
		return nil, err
	}
	if reviewerID != "" {
		if err := ensureAgentVisible(ctx, tx, reviewerID); err != nil {
			return nil, err
		}
	}

	record, err := scanProjectIssueReviewVersion(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issue_review_versions (
			org_id, issue_id, document_path, review_commit_sha, reviewer_agent_id
		) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (issue_id, review_commit_sha)
		DO UPDATE SET
			document_path = EXCLUDED.document_path,
			reviewer_agent_id = COALESCE(EXCLUDED.reviewer_agent_id, project_issue_review_versions.reviewer_agent_id),
			updated_at = NOW()
		RETURNING id, org_id, issue_id, document_path, review_commit_sha, reviewer_agent_id, addressed_in_commit_sha, addressed_at, created_at, updated_at`,
		workspaceID,
		issueID,
		*normalizedPath,
		reviewCommitSHA,
		nullableString(reviewerIDPtr),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to upsert review version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit review version upsert: %w", err)
	}
	return &record, nil
}

func (s *ProjectIssueStore) ListReviewVersions(
	ctx context.Context,
	issueID string,
) ([]ProjectIssueReviewVersion, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, issue_id, document_path, review_commit_sha, reviewer_agent_id, addressed_in_commit_sha, addressed_at, created_at, updated_at
			FROM project_issue_review_versions
			WHERE issue_id = $1
			ORDER BY created_at DESC, review_commit_sha DESC`,
		issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list review versions: %w", err)
	}
	defer rows.Close()

	items := make([]ProjectIssueReviewVersion, 0)
	for rows.Next() {
		record, err := scanProjectIssueReviewVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan review version row: %w", err)
		}
		if record.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read review version rows: %w", err)
	}
	return items, nil
}

func (s *ProjectIssueStore) MarkLatestReviewVersionAddressed(
	ctx context.Context,
	issueID string,
	addressedCommitSHA string,
) (*ProjectIssueReviewVersion, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}
	addressedCommitSHA = strings.TrimSpace(addressedCommitSHA)
	if addressedCommitSHA == "" {
		return nil, fmt.Errorf("addressed_commit_sha is required")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureIssueVisible(ctx, tx, issueID); err != nil {
		return nil, err
	}

	record, err := scanProjectIssueReviewVersion(tx.QueryRowContext(
		ctx,
		`WITH latest AS (
			SELECT id
			FROM project_issue_review_versions
			WHERE issue_id = $1 AND addressed_in_commit_sha IS NULL
			ORDER BY created_at DESC, review_commit_sha DESC
			LIMIT 1
			FOR UPDATE
		)
		UPDATE project_issue_review_versions
			SET addressed_in_commit_sha = $2,
				addressed_at = NOW(),
				updated_at = NOW()
		WHERE id IN (SELECT id FROM latest)
		RETURNING id, org_id, issue_id, document_path, review_commit_sha, reviewer_agent_id, addressed_in_commit_sha, addressed_at, created_at, updated_at`,
		issueID,
		addressedCommitSHA,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to mark latest review version addressed: %w", err)
	}
	if record.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit review version update: %w", err)
	}
	return &record, nil
}

func (s *ProjectIssueStore) GetLatestUnaddressedReviewVersion(
	ctx context.Context,
	issueID string,
) (*ProjectIssueReviewVersion, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	record, err := scanProjectIssueReviewVersion(conn.QueryRowContext(
		ctx,
		`SELECT id, org_id, issue_id, document_path, review_commit_sha, reviewer_agent_id, addressed_in_commit_sha, addressed_at, created_at, updated_at
			FROM project_issue_review_versions
			WHERE issue_id = $1 AND addressed_in_commit_sha IS NULL
			ORDER BY created_at DESC, review_commit_sha DESC
			LIMIT 1`,
		issueID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get latest unaddressed review version: %w", err)
	}
	if record.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	return &record, nil
}

func (s *ProjectIssueStore) CreateReviewNotification(
	ctx context.Context,
	input CreateProjectIssueReviewNotificationInput,
) (*ProjectIssueReviewNotification, bool, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, false, ErrNoWorkspace
	}

	issueID := strings.TrimSpace(input.IssueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, false, fmt.Errorf("invalid issue_id")
	}
	notificationType := strings.TrimSpace(input.NotificationType)
	if !isValidIssueReviewNotificationType(notificationType) {
		return nil, false, fmt.Errorf("invalid notification_type")
	}
	targetAgentID := strings.TrimSpace(input.TargetAgentID)
	if !uuidRegex.MatchString(targetAgentID) {
		return nil, false, fmt.Errorf("invalid target_agent_id")
	}
	reviewCommitSHA := strings.TrimSpace(input.ReviewCommitSHA)
	if reviewCommitSHA == "" {
		return nil, false, fmt.Errorf("review_commit_sha is required")
	}
	addressedInCommitSHA := ""
	if input.AddressedInCommitSHA != nil {
		addressedInCommitSHA = strings.TrimSpace(*input.AddressedInCommitSHA)
	}

	payload := input.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureIssueVisible(ctx, tx, issueID); err != nil {
		return nil, false, err
	}
	if err := ensureAgentVisible(ctx, tx, targetAgentID); err != nil {
		return nil, false, err
	}

	record, err := scanProjectIssueReviewNotification(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issue_review_notifications (
			org_id, issue_id, notification_type, target_agent_id, review_commit_sha, addressed_in_commit_sha, payload
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (issue_id, notification_type, target_agent_id, review_commit_sha, addressed_in_commit_sha)
		DO NOTHING
		RETURNING id, org_id, issue_id, notification_type, target_agent_id, review_commit_sha, addressed_in_commit_sha, payload, created_at`,
		workspaceID,
		issueID,
		notificationType,
		targetAgentID,
		reviewCommitSHA,
		addressedInCommitSHA,
		payload,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if err := tx.Commit(); err != nil {
				return nil, false, fmt.Errorf("failed to commit duplicate review notification check: %w", err)
			}
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to create review notification: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("failed to commit review notification create: %w", err)
	}
	return &record, true, nil
}

func (s *ProjectIssueStore) UpsertGitHubLink(
	ctx context.Context,
	input UpsertProjectIssueGitHubLinkInput,
) (*ProjectIssueGitHubLink, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID := strings.TrimSpace(input.IssueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}
	repo := strings.TrimSpace(input.RepositoryFullName)
	if repo == "" {
		return nil, fmt.Errorf("repository_full_name is required")
	}
	if input.GitHubNumber <= 0 {
		return nil, fmt.Errorf("github_number must be greater than zero")
	}
	state := normalizeIssueState(input.GitHubState)
	if state == "" {
		state = "open"
	}
	if !isValidIssueState(state) {
		return nil, fmt.Errorf("invalid github_state")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureIssueVisible(ctx, tx, issueID); err != nil {
		return nil, err
	}

	record, err := scanProjectIssueGitHubLink(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issue_github_links (
			org_id, issue_id, repository_full_name, github_number, github_url, github_state, last_synced_at
		) VALUES ($1,$2,$3,$4,$5,$6,NOW())
		ON CONFLICT (issue_id)
		DO UPDATE SET
			repository_full_name = EXCLUDED.repository_full_name,
			github_number = EXCLUDED.github_number,
			github_url = EXCLUDED.github_url,
			github_state = EXCLUDED.github_state,
			last_synced_at = NOW()
		RETURNING id, org_id, issue_id, repository_full_name, github_number, github_url, github_state, last_synced_at`,
		workspaceID,
		issueID,
		repo,
		input.GitHubNumber,
		nullableString(input.GitHubURL),
		state,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to upsert issue github link: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit issue github link upsert: %w", err)
	}
	return &record, nil
}

func (s *ProjectIssueStore) UpsertSyncCheckpoint(
	ctx context.Context,
	input UpsertProjectIssueSyncCheckpointInput,
) (*ProjectIssueSyncCheckpoint, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	projectID := strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}
	repo := strings.TrimSpace(input.RepositoryFullName)
	if repo == "" {
		return nil, fmt.Errorf("repository_full_name is required")
	}
	resource := strings.TrimSpace(strings.ToLower(input.Resource))
	if resource == "" {
		return nil, fmt.Errorf("resource is required")
	}

	lastSynced := time.Now().UTC()
	if input.LastSyncedAt != nil && !input.LastSyncedAt.IsZero() {
		lastSynced = input.LastSyncedAt.UTC()
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureProjectVisible(ctx, tx, projectID); err != nil {
		return nil, err
	}

	record, err := scanProjectIssueSyncCheckpoint(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issue_sync_checkpoints (
			org_id, project_id, repository_full_name, resource, cursor, last_synced_at
		) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (project_id, repository_full_name, resource)
		DO UPDATE SET
			cursor = EXCLUDED.cursor,
			last_synced_at = EXCLUDED.last_synced_at
		RETURNING id, org_id, project_id, repository_full_name, resource, cursor, last_synced_at`,
		workspaceID,
		projectID,
		repo,
		resource,
		nullableString(input.Cursor),
		lastSynced,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to upsert issue sync checkpoint: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit issue sync checkpoint upsert: %w", err)
	}
	return &record, nil
}

func (s *ProjectIssueStore) ListSyncCheckpoints(
	ctx context.Context,
	projectID string,
) ([]ProjectIssueSyncCheckpoint, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, project_id, repository_full_name, resource, cursor, last_synced_at
			FROM project_issue_sync_checkpoints
			WHERE project_id = $1
			ORDER BY last_synced_at DESC, repository_full_name ASC, resource ASC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list issue sync checkpoints: %w", err)
	}
	defer rows.Close()

	items := make([]ProjectIssueSyncCheckpoint, 0)
	for rows.Next() {
		record, err := scanProjectIssueSyncCheckpoint(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan issue sync checkpoint row: %w", err)
		}
		if record.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read issue sync checkpoint rows: %w", err)
	}
	return items, nil
}

func (s *ProjectIssueStore) GetProjectIssueCounts(
	ctx context.Context,
	projectID string,
) (*ProjectIssueCounts, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var counts ProjectIssueCounts
	err = conn.QueryRowContext(
		ctx,
		`SELECT
			COUNT(*)::int AS total,
			COUNT(*) FILTER (WHERE i.state = 'open')::int AS open_count,
			COUNT(*) FILTER (WHERE i.state = 'closed')::int AS closed_count,
			COUNT(*) FILTER (WHERE i.origin = 'github')::int AS github_origin_count,
			COUNT(*) FILTER (WHERE i.origin = 'local')::int AS local_origin_count,
			COUNT(*) FILTER (
				WHERE l.github_url IS NOT NULL AND l.github_url ILIKE '%/pull/%'
			)::int AS pull_request_count
		FROM project_issues i
		LEFT JOIN project_issue_github_links l ON l.issue_id = i.id
		WHERE i.project_id = $1`,
		projectID,
	).Scan(
		&counts.Total,
		&counts.Open,
		&counts.Closed,
		&counts.GitHubOrigin,
		&counts.LocalOrigin,
		&counts.PullRequests,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load issue counts: %w", err)
	}

	if err := ensureProjectVisible(ctx, conn, projectID); err != nil {
		return nil, err
	}

	return &counts, nil
}

func (s *ProjectIssueStore) AddParticipant(
	ctx context.Context,
	input AddProjectIssueParticipantInput,
) (*ProjectIssueParticipant, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID := strings.TrimSpace(input.IssueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}
	agentID := strings.TrimSpace(input.AgentID)
	if !uuidRegex.MatchString(agentID) {
		return nil, fmt.Errorf("invalid agent_id")
	}

	role := strings.TrimSpace(strings.ToLower(input.Role))
	if role == "" {
		role = "collaborator"
	}
	if role != "owner" && role != "collaborator" {
		return nil, fmt.Errorf("role must be owner or collaborator")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureIssueVisible(ctx, tx, issueID); err != nil {
		return nil, err
	}
	if err := ensureAgentVisible(ctx, tx, agentID); err != nil {
		return nil, err
	}

	existing, err := loadActiveIssueParticipant(ctx, tx, issueID, agentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed to load existing participant: %w", err)
	}
	if err == nil {
		if existing.Role == role {
			return &existing, nil
		}
		updated, err := scanProjectIssueParticipant(tx.QueryRowContext(
			ctx,
			`UPDATE project_issue_participants
				SET role = $1
			  WHERE id = $2
			  RETURNING id, org_id, issue_id, agent_id, role, joined_at, removed_at`,
			role,
			existing.ID,
		))
		if err != nil {
			return nil, fmt.Errorf("failed to update participant role: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("failed to commit participant update: %w", err)
		}
		return &updated, nil
	}

	record, err := scanProjectIssueParticipant(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issue_participants (
			org_id, issue_id, agent_id, role, joined_at, removed_at
		) VALUES ($1,$2,$3,$4,NOW(),NULL)
		RETURNING id, org_id, issue_id, agent_id, role, joined_at, removed_at`,
		workspaceID,
		issueID,
		agentID,
		role,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to add issue participant: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit participant add: %w", err)
	}
	return &record, nil
}

func (s *ProjectIssueStore) RemoveParticipant(ctx context.Context, issueID, agentID string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return fmt.Errorf("invalid issue_id")
	}
	agentID = strings.TrimSpace(agentID)
	if !uuidRegex.MatchString(agentID) {
		return fmt.Errorf("invalid agent_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	result, err := conn.ExecContext(
		ctx,
		`UPDATE project_issue_participants
			SET removed_at = NOW()
		  WHERE org_id = $1 AND issue_id = $2 AND agent_id = $3 AND removed_at IS NULL`,
		workspaceID,
		issueID,
		agentID,
	)
	if err != nil {
		return fmt.Errorf("failed to remove participant: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to inspect participant removal result: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *ProjectIssueStore) ListParticipants(
	ctx context.Context,
	issueID string,
	includeRemoved bool,
) ([]ProjectIssueParticipant, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `SELECT id, org_id, issue_id, agent_id, role, joined_at, removed_at
		FROM project_issue_participants
		WHERE issue_id = $1`
	if !includeRemoved {
		query += ` AND removed_at IS NULL`
	}
	query += ` ORDER BY CASE WHEN role = 'owner' THEN 0 ELSE 1 END ASC, joined_at ASC`

	rows, err := conn.QueryContext(ctx, query, issueID)
	if err != nil {
		return nil, fmt.Errorf("failed to list participants: %w", err)
	}
	defer rows.Close()

	participants := make([]ProjectIssueParticipant, 0)
	for rows.Next() {
		participant, err := scanProjectIssueParticipant(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan participant row: %w", err)
		}
		if participant.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		participants = append(participants, participant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read participant rows: %w", err)
	}
	return participants, nil
}

func (s *ProjectIssueStore) CreateComment(
	ctx context.Context,
	input CreateProjectIssueCommentInput,
) (*ProjectIssueComment, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID := strings.TrimSpace(input.IssueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}
	authorID := strings.TrimSpace(input.AuthorAgentID)
	if !uuidRegex.MatchString(authorID) {
		return nil, fmt.Errorf("invalid author_agent_id")
	}
	body := strings.TrimSpace(input.Body)
	if body == "" {
		return nil, fmt.Errorf("body is required")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureIssueVisible(ctx, tx, issueID); err != nil {
		return nil, err
	}
	if err := ensureAgentVisible(ctx, tx, authorID); err != nil {
		return nil, err
	}

	record, err := scanProjectIssueComment(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issue_comments (
			org_id, issue_id, author_agent_id, body
		) VALUES ($1,$2,$3,$4)
		RETURNING id, org_id, issue_id, author_agent_id, body, created_at, updated_at`,
		workspaceID,
		issueID,
		authorID,
		body,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create issue comment: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit issue comment create: %w", err)
	}
	return &record, nil
}

func (s *ProjectIssueStore) ListComments(
	ctx context.Context,
	issueID string,
	limit int,
	offset int,
) ([]ProjectIssueComment, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, issue_id, author_agent_id, body, created_at, updated_at
			FROM project_issue_comments
			WHERE issue_id = $1
			ORDER BY created_at ASC, id ASC
			LIMIT $2 OFFSET $3`,
		issueID,
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list issue comments: %w", err)
	}
	defer rows.Close()

	comments := make([]ProjectIssueComment, 0)
	for rows.Next() {
		comment, err := scanProjectIssueComment(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan issue comment row: %w", err)
		}
		if comment.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		comments = append(comments, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read issue comment rows: %w", err)
	}
	return comments, nil
}

func ensureProjectVisible(ctx context.Context, q Querier, projectID string) error {
	var visible bool
	err := q.QueryRowContext(ctx, `SELECT TRUE FROM projects WHERE id = $1`, projectID).Scan(&visible)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func ensureIssueVisible(ctx context.Context, q Querier, issueID string) error {
	var visible bool
	err := q.QueryRowContext(ctx, `SELECT TRUE FROM project_issues WHERE id = $1`, issueID).Scan(&visible)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func ensureIssueBelongsToProject(ctx context.Context, q Querier, issueID string, projectID string) error {
	var actualProjectID string
	err := q.QueryRowContext(ctx, `SELECT project_id FROM project_issues WHERE id = $1`, issueID).Scan(&actualProjectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if strings.TrimSpace(actualProjectID) != strings.TrimSpace(projectID) {
		return ErrForbidden
	}
	return nil
}

func ensureAgentVisible(ctx context.Context, q Querier, agentID string) error {
	var visible bool
	err := q.QueryRowContext(ctx, `SELECT TRUE FROM agents WHERE id = $1`, agentID).Scan(&visible)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func loadActiveIssueParticipant(
	ctx context.Context,
	q Querier,
	issueID string,
	agentID string,
) (ProjectIssueParticipant, error) {
	return scanProjectIssueParticipant(q.QueryRowContext(
		ctx,
		`SELECT id, org_id, issue_id, agent_id, role, joined_at, removed_at
			FROM project_issue_participants
			WHERE issue_id = $1 AND agent_id = $2 AND removed_at IS NULL`,
		issueID,
		agentID,
	))
}

func scanProjectIssue(scanner interface{ Scan(...any) error }) (ProjectIssue, error) {
	var issue ProjectIssue
	var body sql.NullString
	var documentPath sql.NullString
	var ownerAgentID sql.NullString
	var parentIssueID sql.NullString
	var dueAt sql.NullTime
	var nextStep sql.NullString
	var nextStepDueAt sql.NullTime
	var closedAt sql.NullTime

	err := scanner.Scan(
		&issue.ID,
		&issue.OrgID,
		&issue.ProjectID,
		&issue.IssueNumber,
		&issue.Title,
		&body,
		&issue.State,
		&issue.Origin,
		&documentPath,
		&issue.ApprovalState,
		&ownerAgentID,
		&parentIssueID,
		&issue.WorkStatus,
		&issue.Priority,
		&dueAt,
		&nextStep,
		&nextStepDueAt,
		&issue.CreatedAt,
		&issue.UpdatedAt,
		&closedAt,
	)
	if err != nil {
		return issue, err
	}
	if body.Valid {
		issue.Body = &body.String
	}
	if documentPath.Valid {
		issue.DocumentPath = &documentPath.String
	}
	if ownerAgentID.Valid {
		issue.OwnerAgentID = &ownerAgentID.String
	}
	if parentIssueID.Valid {
		issue.ParentIssueID = &parentIssueID.String
	}
	issue.ApprovalState = normalizeIssueApprovalState(issue.ApprovalState)
	if issue.ApprovalState == "" {
		issue.ApprovalState = defaultApprovalStateForLegacyState(issue.State)
	}
	issue.WorkStatus = normalizeIssueWorkStatus(issue.WorkStatus)
	if issue.WorkStatus == "" {
		issue.WorkStatus = defaultIssueWorkStatusForState(issue.State)
	}
	issue.Priority = normalizeIssuePriority(issue.Priority)
	if issue.Priority == "" {
		issue.Priority = IssuePriorityP2
	}
	if dueAt.Valid {
		issue.DueAt = &dueAt.Time
	}
	if nextStep.Valid {
		issue.NextStep = &nextStep.String
	}
	if nextStepDueAt.Valid {
		issue.NextStepDueAt = &nextStepDueAt.Time
	}
	if closedAt.Valid {
		issue.ClosedAt = &closedAt.Time
	}
	return issue, nil
}

func scanIssueRoleAssignment(scanner interface{ Scan(...any) error }) (IssueRoleAssignment, error) {
	var assignment IssueRoleAssignment
	var agentID sql.NullString

	err := scanner.Scan(
		&assignment.ID,
		&assignment.OrgID,
		&assignment.ProjectID,
		&assignment.Role,
		&agentID,
		&assignment.CreatedAt,
		&assignment.UpdatedAt,
	)
	if err != nil {
		return assignment, err
	}
	if agentID.Valid {
		assignment.AgentID = &agentID.String
	}
	return assignment, nil
}

func scanProjectIssueGitHubLink(scanner interface{ Scan(...any) error }) (ProjectIssueGitHubLink, error) {
	var link ProjectIssueGitHubLink
	var githubURL sql.NullString

	err := scanner.Scan(
		&link.ID,
		&link.OrgID,
		&link.IssueID,
		&link.RepositoryFullName,
		&link.GitHubNumber,
		&githubURL,
		&link.GitHubState,
		&link.LastSyncedAt,
	)
	if err != nil {
		return link, err
	}
	if githubURL.Valid {
		link.GitHubURL = &githubURL.String
	}
	return link, nil
}

func scanProjectIssueSyncCheckpoint(scanner interface{ Scan(...any) error }) (ProjectIssueSyncCheckpoint, error) {
	var checkpoint ProjectIssueSyncCheckpoint
	var cursor sql.NullString

	err := scanner.Scan(
		&checkpoint.ID,
		&checkpoint.OrgID,
		&checkpoint.ProjectID,
		&checkpoint.RepositoryFullName,
		&checkpoint.Resource,
		&cursor,
		&checkpoint.LastSyncedAt,
	)
	if err != nil {
		return checkpoint, err
	}
	if cursor.Valid {
		checkpoint.Cursor = &cursor.String
	}
	return checkpoint, nil
}

func scanProjectIssueReviewCheckpoint(
	scanner interface{ Scan(...any) error },
) (ProjectIssueReviewCheckpoint, error) {
	var checkpoint ProjectIssueReviewCheckpoint
	err := scanner.Scan(
		&checkpoint.ID,
		&checkpoint.OrgID,
		&checkpoint.IssueID,
		&checkpoint.LastReviewCommitSHA,
		&checkpoint.UpdatedAt,
	)
	if err != nil {
		return checkpoint, err
	}
	return checkpoint, nil
}

func scanProjectIssueReviewVersion(
	scanner interface{ Scan(...any) error },
) (ProjectIssueReviewVersion, error) {
	var version ProjectIssueReviewVersion
	var reviewerAgentID sql.NullString
	var addressedInCommitSHA sql.NullString
	var addressedAt sql.NullTime

	err := scanner.Scan(
		&version.ID,
		&version.OrgID,
		&version.IssueID,
		&version.DocumentPath,
		&version.ReviewCommitSHA,
		&reviewerAgentID,
		&addressedInCommitSHA,
		&addressedAt,
		&version.CreatedAt,
		&version.UpdatedAt,
	)
	if err != nil {
		return version, err
	}
	if reviewerAgentID.Valid {
		version.ReviewerAgentID = &reviewerAgentID.String
	}
	if addressedInCommitSHA.Valid {
		version.AddressedInCommitSHA = &addressedInCommitSHA.String
	}
	if addressedAt.Valid {
		version.AddressedAt = &addressedAt.Time
	}
	return version, nil
}

func scanProjectIssueReviewNotification(
	scanner interface{ Scan(...any) error },
) (ProjectIssueReviewNotification, error) {
	var notification ProjectIssueReviewNotification
	var payload []byte
	err := scanner.Scan(
		&notification.ID,
		&notification.OrgID,
		&notification.IssueID,
		&notification.NotificationType,
		&notification.TargetAgentID,
		&notification.ReviewCommitSHA,
		&notification.AddressedInCommitSHA,
		&payload,
		&notification.CreatedAt,
	)
	if err != nil {
		return notification, err
	}
	if len(payload) == 0 {
		notification.Payload = json.RawMessage(`{}`)
	} else {
		notification.Payload = json.RawMessage(payload)
	}
	return notification, nil
}

func scanProjectIssueParticipant(scanner interface{ Scan(...any) error }) (ProjectIssueParticipant, error) {
	var participant ProjectIssueParticipant
	var removedAt sql.NullTime

	err := scanner.Scan(
		&participant.ID,
		&participant.OrgID,
		&participant.IssueID,
		&participant.AgentID,
		&participant.Role,
		&participant.JoinedAt,
		&removedAt,
	)
	if err != nil {
		return participant, err
	}
	if removedAt.Valid {
		participant.RemovedAt = &removedAt.Time
	}
	return participant, nil
}

func scanProjectIssueComment(scanner interface{ Scan(...any) error }) (ProjectIssueComment, error) {
	var comment ProjectIssueComment
	err := scanner.Scan(
		&comment.ID,
		&comment.OrgID,
		&comment.IssueID,
		&comment.AuthorAgentID,
		&comment.Body,
		&comment.CreatedAt,
		&comment.UpdatedAt,
	)
	if err != nil {
		return comment, err
	}
	return comment, nil
}
