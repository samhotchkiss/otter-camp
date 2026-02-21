package importer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
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
	if !openClawImportUUIDRegex.MatchString(orgID) {
		return OpenClawProjectDiscoveryResult{}, fmt.Errorf("invalid org_id")
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
		if taxonomyErr := ensureOpenClawProjectTaxonomyNode(ctx, tx, orgID, candidate.Name); taxonomyErr != nil {
			return OpenClawProjectDiscoveryResult{}, taxonomyErr
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
	description := BuildOpenClawProjectDescription(candidate)

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

	state, workStatus := inferOpenClawIssueLifecycle(project.Status, issue)
	body := buildOpenClawIssueDescription(project, issue)

	var (
		existingID         string
		existingState      string
		existingWorkStatus string
		existingBody       sql.NullString
	)
	queryErr := tx.QueryRowContext(
		ctx,
		`SELECT id, state, work_status, body
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
	).Scan(&existingID, &existingState, &existingWorkStatus, &existingBody)
	if queryErr != nil {
		if errors.Is(queryErr, sql.ErrNoRows) {
			var lockedProjectID string
			if err := tx.QueryRowContext(
				ctx,
				`SELECT id
				   FROM projects
				  WHERE id = $1
				  FOR UPDATE`,
				projectID,
			).Scan(&lockedProjectID); err != nil {
				return false, false, fmt.Errorf("lock project issue sequence for project %s: %w", projectID, err)
			}

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
						org_id, project_id, issue_number, title, body, state, origin, work_status, closed_at
					) VALUES (
						$1, $2, $3, $4, $5, $6, 'local', $7, $8
					)`,
				orgID,
				projectID,
				nextIssueNumber,
				title,
				nullableOpenClawText(body),
				state,
				workStatus,
				closedAt,
			); err != nil {
				return false, false, fmt.Errorf("insert discovered issue %s: %w", title, err)
			}
			return true, false, nil
		}
		return false, false, fmt.Errorf("lookup discovered issue %s: %w", title, queryErr)
	}

	currentBody := strings.TrimSpace(existingBody.String)
	if strings.EqualFold(strings.TrimSpace(existingState), state) &&
		strings.EqualFold(strings.TrimSpace(existingWorkStatus), workStatus) &&
		currentBody == body {
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
		        work_status = $3,
		        body = $4,
		        closed_at = $5
		  WHERE id = $1`,
		existingID,
		state,
		workStatus,
		nullableOpenClawText(body),
		closedAt,
	); err != nil {
		return false, false, fmt.Errorf("update discovered issue %s: %w", title, err)
	}

	return false, true, nil
}

func inferOpenClawIssueLifecycle(projectStatus string, issue OpenClawIssueCandidate) (state string, workStatus string) {
	rawStatus := strings.TrimSpace(issue.Status)
	workStatus = normalizeOpenClawIssueStatus(issue.Status, issue.Title)
	if rawStatus == "" && strings.EqualFold(strings.TrimSpace(projectStatus), "completed") && !isOpenClawTerminalIssueStatus(workStatus) {
		workStatus = "done"
	}
	if hasOpenClawCompletionSignal(issue.Title) && !isOpenClawTerminalIssueStatus(workStatus) {
		workStatus = "done"
	}
	if isOpenClawTerminalIssueStatus(workStatus) {
		return "closed", workStatus
	}
	return "open", workStatus
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
	return BuildOpenClawProjectDescription(candidate)
}

// BuildOpenClawProjectDescription generates a concise, human-readable description
// for projects inferred from OpenClaw migration signals.
func BuildOpenClawProjectDescription(candidate OpenClawProjectCandidate) string {
	parts := []string{
		buildOpenClawProjectIntro(candidate),
	}
	if candidate.LastDiscussedAt != nil && !candidate.LastDiscussedAt.IsZero() {
		parts = append(parts, "Last discussed on "+candidate.LastDiscussedAt.UTC().Format("2006-01-02")+".")
	}
	if evidence := buildOpenClawEvidenceSummary(candidate.Signals); evidence != "" {
		parts = append(parts, evidence)
	}
	parts = append(parts, "Inferred status: "+normalizeOpenClawProjectStatus(candidate.Status)+".")
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
	if status := strings.TrimSpace(issue.Status); status != "" {
		parts = append(parts, "Status: "+status+".")
	}
	if !issue.OccurredAt.IsZero() {
		parts = append(parts, "Seen at: "+issue.OccurredAt.UTC().Format(time.RFC3339)+".")
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func buildOpenClawProjectIntro(candidate OpenClawProjectCandidate) string {
	issueTitles := collectOpenClawIssueTitles(candidate.Issues, 3)
	if len(issueTitles) > 0 {
		return "Imported from OpenClaw activity. Initial focus: " + strings.Join(issueTitles, "; ") + "."
	}

	repo := strings.TrimSpace(candidate.RepoPath)
	if repo != "" {
		repoName := strings.TrimSpace(strings.TrimSuffix(filepath.Base(repo), ".git"))
		if repoName != "" && repoName != "." && repoName != string(filepath.Separator) {
			return "Imported from OpenClaw activity for repository " + repoName + "."
		}
	}

	if name := strings.TrimSpace(candidate.Name); name != "" {
		return "Imported from OpenClaw activity for " + name + "."
	}
	return "Imported from OpenClaw activity."
}

func collectOpenClawIssueTitles(issues []OpenClawIssueCandidate, limit int) []string {
	if limit <= 0 || len(issues) == 0 {
		return nil
	}
	out := make([]string, 0, limit)
	seen := map[string]struct{}{}
	for _, issue := range issues {
		title := strings.TrimSpace(issue.Title)
		title = strings.TrimRight(title, ".")
		if title == "" {
			continue
		}
		if len(title) > 96 {
			title = strings.TrimSpace(title[:96]) + "..."
		}
		norm := strings.ToLower(title)
		if _, exists := seen[norm]; exists {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, title)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func buildOpenClawEvidenceSummary(signals []string) string {
	if len(signals) == 0 {
		return ""
	}

	counts := map[string]int{}
	for _, signal := range signals {
		normalized := "other"
		if idx := strings.Index(signal, ":"); idx > 0 {
			normalized = strings.TrimSpace(strings.ToLower(signal[:idx]))
		} else {
			trimmed := strings.TrimSpace(strings.ToLower(signal))
			if trimmed != "" {
				normalized = trimmed
			}
		}
		switch normalized {
		case "workspace", "session", "memory":
			// keep known buckets
		default:
			normalized = "other"
		}
		counts[normalized]++
	}

	if len(counts) == 0 {
		return ""
	}

	ordered := []string{"workspace", "session", "memory"}
	fragments := make([]string, 0, len(counts))
	for _, bucket := range ordered {
		if count := counts[bucket]; count > 0 {
			fragments = append(fragments, fmt.Sprintf("%s (%d)", bucket, count))
			delete(counts, bucket)
		}
	}
	if len(counts) > 0 {
		remaining := make([]string, 0, len(counts))
		for bucket := range counts {
			remaining = append(remaining, bucket)
		}
		sort.Strings(remaining)
		for _, bucket := range remaining {
			fragments = append(fragments, fmt.Sprintf("%s (%d)", bucket, counts[bucket]))
		}
	}

	return "Evidence: " + strings.Join(fragments, ", ") + "."
}

func nullableOpenClawText(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func ensureOpenClawProjectTaxonomyNode(ctx context.Context, tx *sql.Tx, orgID, projectName string) error {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return nil
	}

	projectsRootID, projectsRootDepth, err := ensureOpenClawProjectsTaxonomyRoot(ctx, tx, orgID)
	if err != nil {
		return err
	}

	projectSlug := normalizeOpenClawProjectTaxonomySlug(projectName)
	if projectSlug == "" {
		return nil
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO ellie_taxonomy_nodes (org_id, parent_id, slug, display_name, description, depth)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (org_id, parent_id, slug) DO UPDATE
		 SET display_name = EXCLUDED.display_name`,
		orgID,
		projectsRootID,
		projectSlug,
		projectName,
		"Imported from project discovery.",
		projectsRootDepth+1,
	); err != nil {
		return fmt.Errorf("upsert project taxonomy node for %q: %w", projectName, err)
	}

	return nil
}

func ensureOpenClawProjectsTaxonomyRoot(ctx context.Context, tx *sql.Tx, orgID string) (string, int, error) {
	var (
		rootID    string
		rootDepth int
	)
	err := tx.QueryRowContext(
		ctx,
		`SELECT id::text, depth
		   FROM ellie_taxonomy_nodes
		  WHERE org_id = $1
		    AND parent_id IS NULL
		    AND slug = 'projects'
		  ORDER BY created_at ASC, id ASC
		  LIMIT 1`,
		orgID,
	).Scan(&rootID, &rootDepth)
	if err == nil {
		return rootID, rootDepth, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", 0, fmt.Errorf("lookup projects taxonomy root: %w", err)
	}

	insertErr := tx.QueryRowContext(
		ctx,
		`INSERT INTO ellie_taxonomy_nodes (org_id, parent_id, slug, display_name, description, depth)
		 VALUES ($1, NULL, 'projects', 'Projects', 'Project-specific operational and product memory.', 0)
		 RETURNING id::text, depth`,
		orgID,
	).Scan(&rootID, &rootDepth)
	if insertErr == nil {
		return rootID, rootDepth, nil
	}
	if !isOpenClawTaxonomyConflict(insertErr) {
		return "", 0, fmt.Errorf("insert projects taxonomy root: %w", insertErr)
	}

	if err := tx.QueryRowContext(
		ctx,
		`SELECT id::text, depth
		   FROM ellie_taxonomy_nodes
		  WHERE org_id = $1
		    AND parent_id IS NULL
		    AND slug = 'projects'
		  ORDER BY created_at ASC, id ASC
		  LIMIT 1`,
		orgID,
	).Scan(&rootID, &rootDepth); err != nil {
		return "", 0, fmt.Errorf("reload projects taxonomy root after conflict: %w", err)
	}
	return rootID, rootDepth, nil
}

func normalizeOpenClawProjectTaxonomySlug(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return ""
	}

	var builder strings.Builder
	lastWasSeparator := false
	for _, char := range normalized {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
			lastWasSeparator = false
			continue
		}
		if !lastWasSeparator {
			builder.WriteByte('-')
			lastWasSeparator = true
		}
	}

	return strings.Trim(builder.String(), "-")
}

func isOpenClawTaxonomyConflict(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	return pqErr.Code == "23505"
}
