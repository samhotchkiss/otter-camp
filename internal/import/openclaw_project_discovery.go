package importer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type OpenClawProjectDiscoveryInput struct {
	OrgID         string
	ParsedEvents  []OpenClawSessionEvent
	ReferenceTime time.Time
}

type OpenClawProjectDiscoveryPersistInput struct {
	OrgID         string
	ImportInput   OpenClawProjectImportInput
	ReferenceTime time.Time
}

type OpenClawProjectDiscoveryResult struct {
	ProjectsCreated int
	ProjectsUpdated int
	IssuesCreated   int
	IssuesUpdated   int
	ProcessedItems  int
}

type OpenClawProjectDiscoveryRunner interface {
	Discover(ctx context.Context, input OpenClawProjectDiscoveryInput) (OpenClawProjectDiscoveryResult, error)
}

type noopOpenClawProjectDiscoveryRunner struct{}

func (noopOpenClawProjectDiscoveryRunner) Discover(
	_ context.Context,
	_ OpenClawProjectDiscoveryInput,
) (OpenClawProjectDiscoveryResult, error) {
	return OpenClawProjectDiscoveryResult{}, nil
}

type openClawDBProjectDiscoveryRunner struct {
	DB *sql.DB
}

func newOpenClawProjectDiscoveryRunner(db *sql.DB) OpenClawProjectDiscoveryRunner {
	if db == nil {
		return noopOpenClawProjectDiscoveryRunner{}
	}
	return openClawDBProjectDiscoveryRunner{DB: db}
}

func (r openClawDBProjectDiscoveryRunner) Discover(
	ctx context.Context,
	input OpenClawProjectDiscoveryInput,
) (OpenClawProjectDiscoveryResult, error) {
	return DiscoverOpenClawProjectsFromHistory(ctx, r.DB, input)
}

func DiscoverOpenClawProjectsFromHistory(
	ctx context.Context,
	db *sql.DB,
	input OpenClawProjectDiscoveryInput,
) (OpenClawProjectDiscoveryResult, error) {
	importInput := OpenClawProjectImportInput{
		Sessions: make([]OpenClawSessionSignal, 0, len(input.ParsedEvents)),
	}

	for _, event := range input.ParsedEvents {
		body := strings.TrimSpace(event.Body)
		if body == "" {
			continue
		}

		projectHint := extractInlineProjectHint(body)
		issueHints := extractInlineIssueHints(body)
		if projectHint == "" && len(issueHints) == 0 {
			continue
		}

		importInput.Sessions = append(importInput.Sessions, OpenClawSessionSignal{
			AgentID:     strings.TrimSpace(event.AgentSlug),
			Summary:     body,
			ProjectHint: projectHint,
			IssueHints:  issueHints,
			OccurredAt:  event.CreatedAt.UTC(),
		})
	}

	return DiscoverOpenClawProjects(ctx, db, OpenClawProjectDiscoveryPersistInput{
		OrgID:         input.OrgID,
		ImportInput:   importInput,
		ReferenceTime: input.ReferenceTime,
	})
}

func DiscoverOpenClawProjects(
	ctx context.Context,
	db *sql.DB,
	input OpenClawProjectDiscoveryPersistInput,
) (OpenClawProjectDiscoveryResult, error) {
	if db == nil {
		return OpenClawProjectDiscoveryResult{}, fmt.Errorf("database is required")
	}

	orgID := strings.TrimSpace(input.OrgID)
	if orgID == "" {
		return OpenClawProjectDiscoveryResult{}, fmt.Errorf("org_id is required")
	}

	referenceTime := input.ReferenceTime.UTC()
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}

	candidates := InferOpenClawProjectCandidatesAt(input.ImportInput, referenceTime)
	result := OpenClawProjectDiscoveryResult{ProcessedItems: len(candidates)}
	if len(candidates) == 0 {
		return result, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return OpenClawProjectDiscoveryResult{}, fmt.Errorf("begin openclaw project discovery transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, candidate := range candidates {
		projectID, created, updated, upsertErr := upsertOpenClawDiscoveredProject(ctx, tx, orgID, candidate)
		if upsertErr != nil {
			return OpenClawProjectDiscoveryResult{}, upsertErr
		}
		if created {
			result.ProjectsCreated++
		}
		if updated {
			result.ProjectsUpdated++
		}

		for _, issue := range candidate.Issues {
			createdIssue, updatedIssue, issueErr := upsertOpenClawDiscoveredIssue(
				ctx,
				tx,
				orgID,
				projectID,
				candidate,
				issue,
			)
			if issueErr != nil {
				return OpenClawProjectDiscoveryResult{}, issueErr
			}
			if createdIssue {
				result.IssuesCreated++
			}
			if updatedIssue {
				result.IssuesUpdated++
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return OpenClawProjectDiscoveryResult{}, fmt.Errorf("commit openclaw project discovery transaction: %w", err)
	}

	return result, nil
}

func upsertOpenClawDiscoveredProject(
	ctx context.Context,
	tx *sql.Tx,
	orgID string,
	candidate OpenClawProjectCandidate,
) (projectID string, created bool, updated bool, err error) {
	name := strings.TrimSpace(candidate.Name)
	if name == "" {
		return "", false, false, nil
	}

	status := normalizeOpenClawProjectStatus(candidate.Status)
	description := buildOpenClawProjectDescription(candidate)

	var (
		existingID          string
		existingStatus      string
		existingDescription sql.NullString
	)
	queryErr := tx.QueryRowContext(
		ctx,
		`SELECT id, status, description
		   FROM projects
		  WHERE org_id = $1
		    AND LOWER(name) = LOWER($2)
		  ORDER BY created_at ASC, id ASC
		  LIMIT 1`,
		orgID,
		name,
	).Scan(&existingID, &existingStatus, &existingDescription)
	if queryErr != nil {
		if errors.Is(queryErr, sql.ErrNoRows) {
			var insertedID string
			insertErr := tx.QueryRowContext(
				ctx,
				`INSERT INTO projects (org_id, name, description, status)
				 VALUES ($1, $2, $3, $4)
				 RETURNING id`,
				orgID,
				name,
				nullableOpenClawText(description),
				status,
			).Scan(&insertedID)
			if insertErr != nil {
				return "", false, false, fmt.Errorf("insert discovered project %s: %w", name, insertErr)
			}
			return strings.TrimSpace(insertedID), true, false, nil
		}
		return "", false, false, fmt.Errorf("lookup discovered project %s: %w", name, queryErr)
	}

	currentDescription := strings.TrimSpace(existingDescription.String)
	if strings.EqualFold(strings.TrimSpace(existingStatus), status) && currentDescription == description {
		return strings.TrimSpace(existingID), false, false, nil
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE projects
		    SET status = $2,
		        description = $3
		  WHERE id = $1`,
		existingID,
		status,
		nullableOpenClawText(description),
	); err != nil {
		return "", false, false, fmt.Errorf("update discovered project %s: %w", name, err)
	}

	return strings.TrimSpace(existingID), false, true, nil
}

func upsertOpenClawDiscoveredIssue(
	ctx context.Context,
	tx *sql.Tx,
	orgID string,
	projectID string,
	project OpenClawProjectCandidate,
	issue OpenClawIssueCandidate,
) (created bool, updated bool, err error) {
	title := strings.TrimSpace(issue.Title)
	if title == "" {
		return false, false, nil
	}

	state := inferOpenClawIssueState(project.Status, title)
	body := buildOpenClawIssueDescription(project, issue)

	var (
		existingID    string
		existingState string
		existingBody  sql.NullString
	)
	queryErr := tx.QueryRowContext(
		ctx,
		`SELECT id, state, body
		   FROM project_issues
		  WHERE org_id = $1
		    AND project_id = $2
		    AND origin = 'local'
		    AND LOWER(title) = LOWER($3)
		  ORDER BY issue_number ASC, id ASC
		  LIMIT 1`,
		orgID,
		projectID,
		title,
	).Scan(&existingID, &existingState, &existingBody)
	if queryErr != nil {
		if errors.Is(queryErr, sql.ErrNoRows) {
			var nextIssueNumber int64
			if err := tx.QueryRowContext(
				ctx,
				`SELECT COALESCE(MAX(issue_number), 0) + 1
				   FROM project_issues
				  WHERE project_id = $1`,
				projectID,
			).Scan(&nextIssueNumber); err != nil {
				return false, false, fmt.Errorf("allocate discovered issue number for project %s: %w", projectID, err)
			}

			var closedAt any
			if state == "closed" {
				closedAt = time.Now().UTC()
			}

			if _, err := tx.ExecContext(
				ctx,
				`INSERT INTO project_issues (
					org_id, project_id, issue_number, title, body, state, origin, closed_at
				) VALUES (
					$1, $2, $3, $4, $5, $6, 'local', $7
				)`,
				orgID,
				projectID,
				nextIssueNumber,
				title,
				nullableOpenClawText(body),
				state,
				closedAt,
			); err != nil {
				return false, false, fmt.Errorf("insert discovered issue %s: %w", title, err)
			}
			return true, false, nil
		}
		return false, false, fmt.Errorf("lookup discovered issue %s: %w", title, queryErr)
	}

	currentBody := strings.TrimSpace(existingBody.String)
	if strings.EqualFold(strings.TrimSpace(existingState), state) && currentBody == body {
		return false, false, nil
	}

	var closedAt any
	if state == "closed" {
		closedAt = time.Now().UTC()
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE project_issues
		    SET state = $2,
		        body = $3,
		        closed_at = $4
		  WHERE id = $1`,
		existingID,
		state,
		nullableOpenClawText(body),
		closedAt,
	); err != nil {
		return false, false, fmt.Errorf("update discovered issue %s: %w", title, err)
	}

	return false, true, nil
}

func inferOpenClawIssueState(projectStatus, title string) string {
	if strings.EqualFold(strings.TrimSpace(projectStatus), "completed") || hasOpenClawCompletionSignal(title) {
		return "closed"
	}
	return "open"
}

func normalizeOpenClawProjectStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "completed":
		return "completed"
	case "archived":
		return "archived"
	default:
		return "active"
	}
}

func buildOpenClawProjectDescription(candidate OpenClawProjectCandidate) string {
	parts := []string{"Imported from OpenClaw conversation history."}
	if candidate.LastDiscussedAt != nil && !candidate.LastDiscussedAt.IsZero() {
		parts = append(parts, "Last discussed: "+candidate.LastDiscussedAt.UTC().Format(time.RFC3339)+".")
	}
	if len(candidate.Signals) > 0 {
		parts = append(parts, "Signals: "+strings.Join(candidate.Signals, ", ")+".")
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func buildOpenClawIssueDescription(project OpenClawProjectCandidate, issue OpenClawIssueCandidate) string {
	parts := []string{"Imported from OpenClaw project discovery."}
	if strings.TrimSpace(project.Name) != "" {
		parts = append(parts, "Project: "+strings.TrimSpace(project.Name)+".")
	}
	if strings.TrimSpace(issue.Source) != "" {
		parts = append(parts, "Source: "+strings.TrimSpace(issue.Source)+".")
	}
	if !issue.OccurredAt.IsZero() {
		parts = append(parts, "Seen at: "+issue.OccurredAt.UTC().Format(time.RFC3339)+".")
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func nullableOpenClawText(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}
