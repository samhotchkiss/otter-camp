package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const defaultLabelColor = "#6b7280"

// Label represents an org-scoped label that can be attached to projects/issues.
type Label struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
}

// LabelStore provides workspace-isolated label CRUD and assignment operations.
type LabelStore struct {
	db *sql.DB
}

const labelSelectColumns = `
	id,
	org_id,
	name,
	color,
	created_at
`

// NewLabelStore creates a new LabelStore.
func NewLabelStore(db *sql.DB) *LabelStore {
	return &LabelStore{db: db}
}

// List returns all labels in the current workspace.
func (s *LabelStore) List(ctx context.Context) ([]Label, error) {
	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT `+labelSelectColumns+`
		FROM labels
		WHERE org_id = $1
		ORDER BY lower(name), id
	`, middleware.WorkspaceFromContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to list labels: %w", err)
	}
	defer rows.Close()

	labels := make([]Label, 0)
	for rows.Next() {
		label, scanErr := scanLabel(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan label: %w", scanErr)
		}
		labels = append(labels, label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read labels: %w", err)
	}
	return labels, nil
}

// GetByID returns one label by ID in the current workspace.
func (s *LabelStore) GetByID(ctx context.Context, id string) (*Label, error) {
	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	label, err := scanLabel(conn.QueryRowContext(ctx, `
		SELECT `+labelSelectColumns+`
		FROM labels
		WHERE id = $1 AND org_id = $2
	`, strings.TrimSpace(id), middleware.WorkspaceFromContext(ctx)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get label by id: %w", err)
	}
	return &label, nil
}

// GetByName returns one label by exact name in the current workspace.
func (s *LabelStore) GetByName(ctx context.Context, name string) (*Label, error) {
	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	label, err := scanLabel(conn.QueryRowContext(ctx, `
		SELECT `+labelSelectColumns+`
		FROM labels
		WHERE org_id = $1 AND name = $2
	`, middleware.WorkspaceFromContext(ctx), strings.TrimSpace(name)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get label by name: %w", err)
	}
	return &label, nil
}

// Create inserts a new label in the current workspace.
func (s *LabelStore) Create(ctx context.Context, name, color string) (*Label, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	normalizedName := strings.TrimSpace(name)
	if normalizedName == "" {
		return nil, fmt.Errorf("label name is required")
	}
	normalizedColor := strings.TrimSpace(color)
	if normalizedColor == "" {
		normalizedColor = defaultLabelColor
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	label, err := scanLabel(conn.QueryRowContext(ctx, `
		INSERT INTO labels (org_id, name, color)
		VALUES ($1, $2, $3)
		RETURNING `+labelSelectColumns, workspaceID, normalizedName, normalizedColor))
	if err != nil {
		return nil, fmt.Errorf("failed to create label: %w", err)
	}
	return &label, nil
}

// Update patches name/color on an existing label.
func (s *LabelStore) Update(ctx context.Context, id string, name *string, color *string) (*Label, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	normalizedName, err := normalizeOptionalString(name)
	if err != nil {
		return nil, err
	}
	normalizedColor, err := normalizeOptionalString(color)
	if err != nil {
		return nil, err
	}
	if normalizedName == nil && normalizedColor == nil {
		return s.GetByID(ctx, id)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	label, err := scanLabel(conn.QueryRowContext(ctx, `
		UPDATE labels
		SET
			name = COALESCE($3, name),
			color = COALESCE($4, color)
		WHERE id = $1 AND org_id = $2
		RETURNING `+labelSelectColumns, strings.TrimSpace(id), workspaceID, normalizedName, normalizedColor))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to update label: %w", err)
	}
	return &label, nil
}

// Delete removes a label from the current workspace.
func (s *LabelStore) Delete(ctx context.Context, id string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	res, err := conn.ExecContext(ctx, `
		DELETE FROM labels
		WHERE id = $1 AND org_id = $2
	`, strings.TrimSpace(id), workspaceID)
	if err != nil {
		return fmt.Errorf("failed to delete label: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to inspect label delete result: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// EnsureByName returns an existing label or creates it if missing.
func (s *LabelStore) EnsureByName(ctx context.Context, name, defaultColor string) (*Label, error) {
	existing, err := s.GetByName(ctx, name)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	created, createErr := s.Create(ctx, name, defaultColor)
	if createErr == nil {
		return created, nil
	}

	var pqErr *pq.Error
	if errors.As(createErr, &pqErr) && string(pqErr.Code) == "23505" {
		return s.GetByName(ctx, name)
	}
	return nil, createErr
}

// ListForProject returns labels attached to one project.
func (s *LabelStore) ListForProject(ctx context.Context, projectID string) ([]Label, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT `+labelSelectColumns+`
		FROM labels l
		INNER JOIN project_labels pl ON pl.label_id = l.id
		INNER JOIN projects p ON p.id = pl.project_id
		WHERE p.id = $1
		  AND p.org_id = $2
		  AND l.org_id = $2
		ORDER BY lower(l.name), l.id
	`, strings.TrimSpace(projectID), workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list project labels: %w", err)
	}
	defer rows.Close()

	labels := make([]Label, 0)
	for rows.Next() {
		label, scanErr := scanLabel(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan project label: %w", scanErr)
		}
		labels = append(labels, label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read project labels: %w", err)
	}
	return labels, nil
}

// AddToProject attaches a label to one project.
func (s *LabelStore) AddToProject(ctx context.Context, projectID, labelID string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	res, err := conn.ExecContext(ctx, `
		INSERT INTO project_labels (project_id, label_id)
		SELECT p.id, l.id
		FROM projects p
		INNER JOIN labels l ON l.id = $2
		WHERE p.id = $1
		  AND p.org_id = $3
		  AND l.org_id = $3
		ON CONFLICT (project_id, label_id) DO NOTHING
	`, strings.TrimSpace(projectID), strings.TrimSpace(labelID), workspaceID)
	if err != nil {
		return fmt.Errorf("failed to add label to project: %w", err)
	}

	rows, err := res.RowsAffected()
	if err == nil && rows > 0 {
		return nil
	}

	exists, err := hasProjectLabel(ctx, conn, projectID, labelID, workspaceID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

// RemoveFromProject detaches a label from one project.
func (s *LabelStore) RemoveFromProject(ctx context.Context, projectID, labelID string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx, `
		DELETE FROM project_labels pl
		USING projects p
		WHERE pl.project_id = p.id
		  AND pl.project_id = $1
		  AND pl.label_id = $2
		  AND p.org_id = $3
	`, strings.TrimSpace(projectID), strings.TrimSpace(labelID), workspaceID)
	if err != nil {
		return fmt.Errorf("failed to remove label from project: %w", err)
	}
	return nil
}

// ListForIssue returns labels attached to one issue.
func (s *LabelStore) ListForIssue(ctx context.Context, issueID string) ([]Label, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT `+labelSelectColumns+`
		FROM labels l
		INNER JOIN issue_labels il ON il.label_id = l.id
		INNER JOIN project_issues i ON i.id = il.issue_id
		WHERE i.id = $1
		  AND i.org_id = $2
		  AND l.org_id = $2
		ORDER BY lower(l.name), l.id
	`, strings.TrimSpace(issueID), workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list issue labels: %w", err)
	}
	defer rows.Close()

	labels := make([]Label, 0)
	for rows.Next() {
		label, scanErr := scanLabel(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan issue label: %w", scanErr)
		}
		labels = append(labels, label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read issue labels: %w", err)
	}
	return labels, nil
}

// AddToIssue attaches a label to one issue.
func (s *LabelStore) AddToIssue(ctx context.Context, issueID, labelID string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	res, err := conn.ExecContext(ctx, `
		INSERT INTO issue_labels (issue_id, label_id)
		SELECT i.id, l.id
		FROM project_issues i
		INNER JOIN labels l ON l.id = $2
		WHERE i.id = $1
		  AND i.org_id = $3
		  AND l.org_id = $3
		ON CONFLICT (issue_id, label_id) DO NOTHING
	`, strings.TrimSpace(issueID), strings.TrimSpace(labelID), workspaceID)
	if err != nil {
		return fmt.Errorf("failed to add label to issue: %w", err)
	}

	rows, err := res.RowsAffected()
	if err == nil && rows > 0 {
		return nil
	}

	exists, err := hasIssueLabel(ctx, conn, issueID, labelID, workspaceID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

// RemoveFromIssue detaches a label from one issue.
func (s *LabelStore) RemoveFromIssue(ctx context.Context, issueID, labelID string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx, `
		DELETE FROM issue_labels il
		USING project_issues i
		WHERE il.issue_id = i.id
		  AND il.issue_id = $1
		  AND il.label_id = $2
		  AND i.org_id = $3
	`, strings.TrimSpace(issueID), strings.TrimSpace(labelID), workspaceID)
	if err != nil {
		return fmt.Errorf("failed to remove label from issue: %w", err)
	}
	return nil
}

// MapForProjects returns labels keyed by project ID for the given projects.
func (s *LabelStore) MapForProjects(ctx context.Context, projectIDs []string) (map[string][]Label, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	filtered := normalizeIDList(projectIDs)
	result := make(map[string][]Label, len(filtered))
	for _, id := range filtered {
		result[id] = []Label{}
	}
	if len(filtered) == 0 {
		return result, nil
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT pl.project_id, `+labelSelectColumns+`
		FROM project_labels pl
		INNER JOIN labels l ON l.id = pl.label_id
		INNER JOIN projects p ON p.id = pl.project_id
		WHERE p.org_id = $1
		  AND l.org_id = $1
		  AND pl.project_id = ANY($2::uuid[])
		ORDER BY pl.project_id, lower(l.name), l.id
	`, workspaceID, pq.Array(filtered))
	if err != nil {
		return nil, fmt.Errorf("failed to map labels for projects: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var projectID string
		label, scanErr := scanLabelWithPrefix(rows, &projectID)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan project label map row: %w", scanErr)
		}
		result[projectID] = append(result[projectID], label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read project label map rows: %w", err)
	}
	return result, nil
}

// MapForIssues returns labels keyed by issue ID for the given issues.
func (s *LabelStore) MapForIssues(ctx context.Context, issueIDs []string) (map[string][]Label, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	filtered := normalizeIDList(issueIDs)
	result := make(map[string][]Label, len(filtered))
	for _, id := range filtered {
		result[id] = []Label{}
	}
	if len(filtered) == 0 {
		return result, nil
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT il.issue_id, `+labelSelectColumns+`
		FROM issue_labels il
		INNER JOIN labels l ON l.id = il.label_id
		INNER JOIN project_issues i ON i.id = il.issue_id
		WHERE i.org_id = $1
		  AND l.org_id = $1
		  AND il.issue_id = ANY($2::uuid[])
		ORDER BY il.issue_id, lower(l.name), l.id
	`, workspaceID, pq.Array(filtered))
	if err != nil {
		return nil, fmt.Errorf("failed to map labels for issues: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var issueID string
		label, scanErr := scanLabelWithPrefix(rows, &issueID)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan issue label map row: %w", scanErr)
		}
		result[issueID] = append(result[issueID], label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read issue label map rows: %w", err)
	}
	return result, nil
}

func hasProjectLabel(ctx context.Context, conn *sql.Conn, projectID, labelID, workspaceID string) (bool, error) {
	var exists bool
	err := conn.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM project_labels pl
			INNER JOIN projects p ON p.id = pl.project_id
			INNER JOIN labels l ON l.id = pl.label_id
			WHERE pl.project_id = $1
			  AND pl.label_id = $2
			  AND p.org_id = $3
			  AND l.org_id = $3
		)
	`, strings.TrimSpace(projectID), strings.TrimSpace(labelID), workspaceID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check existing project label link: %w", err)
	}
	return exists, nil
}

func hasIssueLabel(ctx context.Context, conn *sql.Conn, issueID, labelID, workspaceID string) (bool, error) {
	var exists bool
	err := conn.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM issue_labels il
			INNER JOIN project_issues i ON i.id = il.issue_id
			INNER JOIN labels l ON l.id = il.label_id
			WHERE il.issue_id = $1
			  AND il.label_id = $2
			  AND i.org_id = $3
			  AND l.org_id = $3
		)
	`, strings.TrimSpace(issueID), strings.TrimSpace(labelID), workspaceID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check existing issue label link: %w", err)
	}
	return exists, nil
}

func scanLabel(scanner interface{ Scan(...any) error }) (Label, error) {
	var item Label
	err := scanner.Scan(
		&item.ID,
		&item.OrgID,
		&item.Name,
		&item.Color,
		&item.CreatedAt,
	)
	return item, err
}

func scanLabelWithPrefix(scanner interface{ Scan(...any) error }, idDest *string) (Label, error) {
	var item Label
	err := scanner.Scan(
		idDest,
		&item.ID,
		&item.OrgID,
		&item.Name,
		&item.Color,
		&item.CreatedAt,
	)
	return item, err
}

func normalizeOptionalString(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" {
		return nil, fmt.Errorf("value cannot be empty")
	}
	return &normalized, nil
}

func normalizeIDList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		normalized := strings.TrimSpace(raw)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}
