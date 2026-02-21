package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const (
	IssueFlowBlockerEscalationProjectManager = "project_manager"
	IssueFlowBlockerEscalationHuman          = "human"

	IssueFlowBlockerStatusOpen      = "open"
	IssueFlowBlockerStatusResolved  = "resolved"
	IssueFlowBlockerStatusCancelled = "cancelled"
)

type ProjectIssueFlowBlocker struct {
	ID                            string     `json:"id"`
	OrgID                         string     `json:"org_id"`
	IssueID                       string     `json:"issue_id"`
	ProjectID                     string     `json:"project_id"`
	RaisedByAgentID               *string    `json:"raised_by_agent_id,omitempty"`
	AssignedProjectManagerAgentID *string    `json:"assigned_project_manager_agent_id,omitempty"`
	EscalationLevel               string     `json:"escalation_level"`
	Status                        string     `json:"status"`
	Summary                       string     `json:"summary"`
	Detail                        *string    `json:"detail,omitempty"`
	ResolutionNote                *string    `json:"resolution_note,omitempty"`
	RaisedAt                      time.Time  `json:"raised_at"`
	EscalatedToHumanAt            *time.Time `json:"escalated_to_human_at,omitempty"`
	ResolvedAt                    *time.Time `json:"resolved_at,omitempty"`
	CreatedAt                     time.Time  `json:"created_at"`
	UpdatedAt                     time.Time  `json:"updated_at"`
}

type CreateProjectIssueFlowBlockerInput struct {
	IssueID                       string
	Summary                       string
	Detail                        *string
	RaisedByAgentID               *string
	AssignedProjectManagerAgentID *string
}

type ProjectIssueFlowBlockerStore struct {
	db *sql.DB
}

func NewProjectIssueFlowBlockerStore(db *sql.DB) *ProjectIssueFlowBlockerStore {
	return &ProjectIssueFlowBlockerStore{db: db}
}

func normalizeIssueFlowBlockerEscalationLevel(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case IssueFlowBlockerEscalationHuman:
		return IssueFlowBlockerEscalationHuman
	default:
		return IssueFlowBlockerEscalationProjectManager
	}
}

func normalizeIssueFlowBlockerStatus(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case IssueFlowBlockerStatusResolved:
		return IssueFlowBlockerStatusResolved
	case IssueFlowBlockerStatusCancelled:
		return IssueFlowBlockerStatusCancelled
	default:
		return IssueFlowBlockerStatusOpen
	}
}

func (s *ProjectIssueFlowBlockerStore) GetByID(ctx context.Context, blockerID string) (*ProjectIssueFlowBlocker, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	blockerID = strings.TrimSpace(blockerID)
	if !uuidRegex.MatchString(blockerID) {
		return nil, fmt.Errorf("%w: invalid blocker_id", ErrValidation)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	record, err := scanProjectIssueFlowBlocker(conn.QueryRowContext(
		ctx,
		`SELECT id, org_id, issue_id, project_id, raised_by_agent_id, assigned_project_manager_agent_id,
				escalation_level, status, summary, detail, resolution_note, raised_at,
				escalated_to_human_at, resolved_at, created_at, updated_at
			FROM project_issue_flow_blockers
			WHERE id = $1`,
		blockerID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load issue flow blocker: %w", err)
	}
	if record.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	return &record, nil
}

func (s *ProjectIssueFlowBlockerStore) GetOpenByIssue(ctx context.Context, issueID string) (*ProjectIssueFlowBlocker, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("%w: invalid issue_id", ErrValidation)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	record, err := scanProjectIssueFlowBlocker(conn.QueryRowContext(
		ctx,
		`SELECT id, org_id, issue_id, project_id, raised_by_agent_id, assigned_project_manager_agent_id,
				escalation_level, status, summary, detail, resolution_note, raised_at,
				escalated_to_human_at, resolved_at, created_at, updated_at
			FROM project_issue_flow_blockers
			WHERE issue_id = $1 AND status = 'open'
			ORDER BY raised_at DESC
			LIMIT 1`,
		issueID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load open issue flow blocker: %w", err)
	}
	if record.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	return &record, nil
}

func (s *ProjectIssueFlowBlockerStore) Create(ctx context.Context, input CreateProjectIssueFlowBlockerInput) (*ProjectIssueFlowBlocker, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID := strings.TrimSpace(input.IssueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("%w: invalid issue_id", ErrValidation)
	}

	summary := strings.TrimSpace(input.Summary)
	if summary == "" {
		return nil, fmt.Errorf("%w: summary is required", ErrValidation)
	}
	detail := normalizeOptionalIssueText(input.Detail)

	raisedBy, err := normalizeOptionalIssueAgentID(input.RaisedByAgentID)
	if err != nil {
		return nil, err
	}
	assignedPM, err := normalizeOptionalIssueAgentID(input.AssignedProjectManagerAgentID)
	if err != nil {
		return nil, err
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var projectID string
	var orgID string
	if err := tx.QueryRowContext(
		ctx,
		`SELECT project_id, org_id
			FROM project_issues
			WHERE id = $1
			FOR UPDATE`,
		issueID,
	).Scan(&projectID, &orgID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load issue for blocker: %w", err)
	}
	if orgID != workspaceID {
		return nil, ErrForbidden
	}

	var hasOpen bool
	if err := tx.QueryRowContext(
		ctx,
		`SELECT EXISTS(
			SELECT 1
			FROM project_issue_flow_blockers
			WHERE issue_id = $1 AND status = 'open'
		)`,
		issueID,
	).Scan(&hasOpen); err != nil {
		return nil, fmt.Errorf("failed to check existing blockers: %w", err)
	}
	if hasOpen {
		return nil, fmt.Errorf("%w: issue already has an open blocker", ErrConflict)
	}

	if raisedBy != nil {
		if err := ensureAgentVisible(ctx, tx, *raisedBy); err != nil {
			return nil, err
		}
	}
	if assignedPM != nil {
		if err := ensureAgentVisible(ctx, tx, *assignedPM); err != nil {
			return nil, err
		}
	}

	record, err := scanProjectIssueFlowBlocker(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issue_flow_blockers (
			org_id, issue_id, project_id, raised_by_agent_id, assigned_project_manager_agent_id,
			escalation_level, status, summary, detail
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, org_id, issue_id, project_id, raised_by_agent_id, assigned_project_manager_agent_id,
			escalation_level, status, summary, detail, resolution_note, raised_at,
			escalated_to_human_at, resolved_at, created_at, updated_at`,
		workspaceID,
		issueID,
		projectID,
		nullableString(raisedBy),
		nullableString(assignedPM),
		IssueFlowBlockerEscalationProjectManager,
		IssueFlowBlockerStatusOpen,
		summary,
		nullableString(detail),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create issue flow blocker: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *ProjectIssueFlowBlockerStore) EscalateToHuman(ctx context.Context, blockerID string) (*ProjectIssueFlowBlocker, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	blockerID = strings.TrimSpace(blockerID)
	if !uuidRegex.MatchString(blockerID) {
		return nil, fmt.Errorf("%w: invalid blocker_id", ErrValidation)
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	current, err := scanProjectIssueFlowBlocker(tx.QueryRowContext(
		ctx,
		`SELECT id, org_id, issue_id, project_id, raised_by_agent_id, assigned_project_manager_agent_id,
				escalation_level, status, summary, detail, resolution_note, raised_at,
				escalated_to_human_at, resolved_at, created_at, updated_at
			FROM project_issue_flow_blockers
			WHERE id = $1
			FOR UPDATE`,
		blockerID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load issue flow blocker: %w", err)
	}
	if current.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	if normalizeIssueFlowBlockerStatus(current.Status) != IssueFlowBlockerStatusOpen {
		return nil, fmt.Errorf("%w: blocker is not open", ErrConflict)
	}

	record, err := scanProjectIssueFlowBlocker(tx.QueryRowContext(
		ctx,
		`UPDATE project_issue_flow_blockers
			SET escalation_level = $2,
				escalated_to_human_at = COALESCE(escalated_to_human_at, NOW()),
				updated_at = NOW()
			WHERE id = $1
			RETURNING id, org_id, issue_id, project_id, raised_by_agent_id, assigned_project_manager_agent_id,
				escalation_level, status, summary, detail, resolution_note, raised_at,
				escalated_to_human_at, resolved_at, created_at, updated_at`,
		blockerID,
		IssueFlowBlockerEscalationHuman,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to escalate issue flow blocker: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *ProjectIssueFlowBlockerStore) Resolve(ctx context.Context, blockerID string, resolutionNote *string) (*ProjectIssueFlowBlocker, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	blockerID = strings.TrimSpace(blockerID)
	if !uuidRegex.MatchString(blockerID) {
		return nil, fmt.Errorf("%w: invalid blocker_id", ErrValidation)
	}

	note := normalizeOptionalIssueText(resolutionNote)

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	current, err := scanProjectIssueFlowBlocker(tx.QueryRowContext(
		ctx,
		`SELECT id, org_id, issue_id, project_id, raised_by_agent_id, assigned_project_manager_agent_id,
				escalation_level, status, summary, detail, resolution_note, raised_at,
				escalated_to_human_at, resolved_at, created_at, updated_at
			FROM project_issue_flow_blockers
			WHERE id = $1
			FOR UPDATE`,
		blockerID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load issue flow blocker: %w", err)
	}
	if current.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	if normalizeIssueFlowBlockerStatus(current.Status) != IssueFlowBlockerStatusOpen {
		return nil, fmt.Errorf("%w: blocker is not open", ErrConflict)
	}

	record, err := scanProjectIssueFlowBlocker(tx.QueryRowContext(
		ctx,
		`UPDATE project_issue_flow_blockers
			SET status = $2,
				resolution_note = $3,
				resolved_at = COALESCE(resolved_at, NOW()),
				updated_at = NOW()
			WHERE id = $1
			RETURNING id, org_id, issue_id, project_id, raised_by_agent_id, assigned_project_manager_agent_id,
				escalation_level, status, summary, detail, resolution_note, raised_at,
				escalated_to_human_at, resolved_at, created_at, updated_at`,
		blockerID,
		IssueFlowBlockerStatusResolved,
		nullableString(note),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve issue flow blocker: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &record, nil
}

func scanProjectIssueFlowBlocker(scanner interface{ Scan(...any) error }) (ProjectIssueFlowBlocker, error) {
	var record ProjectIssueFlowBlocker
	var raisedBy sql.NullString
	var assignedPM sql.NullString
	var detail sql.NullString
	var resolutionNote sql.NullString
	var escalatedAt sql.NullTime
	var resolvedAt sql.NullTime

	if err := scanner.Scan(
		&record.ID,
		&record.OrgID,
		&record.IssueID,
		&record.ProjectID,
		&raisedBy,
		&assignedPM,
		&record.EscalationLevel,
		&record.Status,
		&record.Summary,
		&detail,
		&resolutionNote,
		&record.RaisedAt,
		&escalatedAt,
		&resolvedAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return record, err
	}

	if raisedBy.Valid {
		record.RaisedByAgentID = &raisedBy.String
	}
	if assignedPM.Valid {
		record.AssignedProjectManagerAgentID = &assignedPM.String
	}
	if detail.Valid {
		record.Detail = &detail.String
	}
	if resolutionNote.Valid {
		record.ResolutionNote = &resolutionNote.String
	}
	if escalatedAt.Valid {
		record.EscalatedToHumanAt = &escalatedAt.Time
	}
	if resolvedAt.Valid {
		record.ResolvedAt = &resolvedAt.Time
	}

	record.EscalationLevel = normalizeIssueFlowBlockerEscalationLevel(record.EscalationLevel)
	record.Status = normalizeIssueFlowBlockerStatus(record.Status)
	return record, nil
}
