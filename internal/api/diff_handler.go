package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrDiffBranchNotFound = errors.New("diff branch not found")

const maxDiffBytes = 500 * 1024

type GitDiffStat struct {
	FilesChanged int `json:"files_changed"`
	Insertions   int `json:"insertions"`
	Deletions    int `json:"deletions"`
}

type GitDiff struct {
	BaseBranch string
	DiffStat   GitDiffStat
	DiffText   string
}

type GitService interface {
	Diff(ctx context.Context, projectID uuid.UUID, branchName, baseBranch string) (GitDiff, error)
}

type DiffHandler struct {
	pool       *pgxpool.Pool
	gitService GitService
}

type taskDiffRow struct {
	ID             uuid.UUID
	ProjectID      uuid.UUID
	OrganizationID uuid.UUID
	BranchName     string
	BaseBranch     string
}

func NewDiffHandler(pool *pgxpool.Pool, gitService GitService) DiffHandler {
	return DiffHandler{pool: pool, gitService: gitService}
}

func (h DiffHandler) GetTaskDiff(w http.ResponseWriter, r *http.Request) {
	responder := NewResponder(r.Context())
	if h.pool == nil {
		responder.Error(w, http.StatusNotImplemented, ErrCodeNotImplemented, "diff service is not configured")
		return
	}

	organizationID, ok := OrganizationIDFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, ErrCodeUnauthorized, "authentication required")
		return
	}

	taskID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, ErrCodeValidation, "invalid task id")
		return
	}

	taskRow, err := h.loadTaskDiffRow(r.Context(), taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		responder.Error(w, http.StatusNotFound, ErrCodeResourceNotFound, "task not found")
		return
	}
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, ErrCodeInternal, "failed to load task")
		return
	}

	if taskRow.OrganizationID != organizationID {
		responder.Error(w, http.StatusNotFound, ErrCodeResourceNotFound, "task not found")
		return
	}

	remoteConfigured, err := h.projectHasRemote(r.Context(), taskRow.ProjectID)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, ErrCodeInternal, "failed to load project remotes")
		return
	}
	if !remoteConfigured {
		responder.Error(w, http.StatusUnprocessableEntity, "no_remote", "Project has no git remote configured")
		return
	}

	if h.gitService == nil {
		responder.Error(w, http.StatusNotImplemented, ErrCodeNotImplemented, "git diff service is not configured")
		return
	}

	diff, err := h.gitService.Diff(r.Context(), taskRow.ProjectID, taskRow.BranchName, taskRow.BaseBranch)
	if errors.Is(err, ErrDiffBranchNotFound) {
		responder.Error(w, http.StatusNotFound, ErrCodeResourceNotFound, "branch not found")
		return
	}
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, ErrCodeInternal, "failed to generate diff")
		return
	}

	if len(diff.DiffText) > maxDiffBytes {
		diff.DiffText = diff.DiffText[:maxDiffBytes]
	}

	responder.JSON(w, http.StatusOK, map[string]any{
		"task_id":     taskRow.ID,
		"branch_name": taskRow.BranchName,
		"base_branch": diff.BaseBranch,
		"diff_stat":   diff.DiffStat,
		"diff_text":   diff.DiffText,
	})
}

func (h DiffHandler) loadTaskDiffRow(ctx context.Context, taskID uuid.UUID) (taskDiffRow, error) {
	row := h.pool.QueryRow(ctx, `
		SELECT
			t.id,
			t.project_id,
			p.organization_id,
			COALESCE(NULLIF(t.branch_name, ''), 'main') AS branch_name,
			'main' AS base_branch
		FROM project_task t
		JOIN project p ON p.id = t.project_id
		WHERE t.id = $1
	`, taskID)

	var task taskDiffRow
	err := row.Scan(&task.ID, &task.ProjectID, &task.OrganizationID, &task.BranchName, &task.BaseBranch)
	if isSchemaNotReadyDiffError(err) {
		return taskDiffRow{}, pgx.ErrNoRows
	}
	if err != nil {
		return taskDiffRow{}, err
	}
	return task, nil
}

func (h DiffHandler) projectHasRemote(ctx context.Context, projectID uuid.UUID) (bool, error) {
	row := h.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM project_remote
			WHERE project_id = $1
		)
	`, projectID)

	var exists bool
	err := row.Scan(&exists)
	if isSchemaNotReadyDiffError(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return exists, nil
}

func isSchemaNotReadyDiffError(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "42P01" || pgErr.Code == "42703"
}

func (g GitDiff) String() string {
	return fmt.Sprintf("%s:%d/%d/%d", g.BaseBranch, g.DiffStat.FilesChanged, g.DiffStat.Insertions, g.DiffStat.Deletions)
}
