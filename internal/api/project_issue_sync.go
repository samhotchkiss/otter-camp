package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type ProjectIssueSyncHandler struct {
	Projects      *store.ProjectStore
	ProjectRepos  *store.ProjectRepoStore
	Installations *store.GitHubInstallationStore
	SyncJobs      *store.GitHubSyncJobStore
	IssueStore    *store.ProjectIssueStore
}

type projectIssueImportResponse struct {
	ProjectID          string                         `json:"project_id"`
	RepositoryFullName string                         `json:"repository_full_name"`
	Job                projectIssueImportJobView      `json:"job"`
	Checkpoints        []projectIssueCheckpointStatus `json:"checkpoints"`
}

type projectIssueSyncStatusResponse struct {
	ProjectID          string                         `json:"project_id"`
	RepositoryFullName string                         `json:"repository_full_name"`
	Counts             store.ProjectIssueCounts       `json:"counts"`
	LastSyncedAt       *time.Time                     `json:"last_synced_at,omitempty"`
	SyncError          *string                        `json:"sync_error,omitempty"`
	Job                *projectIssueImportJobView     `json:"job,omitempty"`
	Checkpoints        []projectIssueCheckpointStatus `json:"checkpoints"`
}

type projectIssueImportJobView struct {
	ID             string     `json:"id"`
	Type           string     `json:"type"`
	Status         string     `json:"status"`
	AttemptCount   int        `json:"attempt_count"`
	MaxAttempts    int        `json:"max_attempts"`
	LastError      *string    `json:"last_error,omitempty"`
	LastErrorClass *string    `json:"last_error_class,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

type projectIssueCheckpointStatus struct {
	ID                 string    `json:"id"`
	RepositoryFullName string    `json:"repository_full_name"`
	Resource           string    `json:"resource"`
	Cursor             *string   `json:"cursor,omitempty"`
	LastSyncedAt       time.Time `json:"last_synced_at"`
}

func (h *ProjectIssueSyncHandler) ManualImport(w http.ResponseWriter, r *http.Request) {
	if h.Projects == nil || h.ProjectRepos == nil || h.Installations == nil || h.SyncJobs == nil || h.IssueStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}

	repoBinding, err := h.requireImportContext(r, projectID)
	if err != nil {
		handleProjectIssueSyncStoreError(w, err)
		return
	}

	payload, err := json.Marshal(map[string]any{
		"project_id":           projectID,
		"repository_full_name": repoBinding.RepositoryFullName,
		"requested_at":         time.Now().UTC().Format(time.RFC3339),
		"source":               "manual_import_api",
	})
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create import payload"})
		return
	}

	job, err := h.SyncJobs.Enqueue(r.Context(), store.EnqueueGitHubSyncJobInput{
		ProjectID:   &projectID,
		JobType:     store.GitHubSyncJobTypeIssueImport,
		Payload:     payload,
		MaxAttempts: 5,
	})
	if err != nil {
		handleProjectIssueSyncStoreError(w, err)
		return
	}

	checkpoints, err := h.IssueStore.ListSyncCheckpoints(r.Context(), projectID)
	if err != nil {
		handleProjectIssueSyncStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusAccepted, projectIssueImportResponse{
		ProjectID:          projectID,
		RepositoryFullName: repoBinding.RepositoryFullName,
		Job:                toProjectIssueImportJobView(*job),
		Checkpoints:        toProjectIssueCheckpointStatusList(checkpoints),
	})
}

func (h *ProjectIssueSyncHandler) Status(w http.ResponseWriter, r *http.Request) {
	if h.Projects == nil || h.ProjectRepos == nil || h.Installations == nil || h.SyncJobs == nil || h.IssueStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}

	repoBinding, err := h.requireImportContext(r, projectID)
	if err != nil {
		handleProjectIssueSyncStoreError(w, err)
		return
	}

	counts, err := h.IssueStore.GetProjectIssueCounts(r.Context(), projectID)
	if err != nil {
		handleProjectIssueSyncStoreError(w, err)
		return
	}
	checkpoints, err := h.IssueStore.ListSyncCheckpoints(r.Context(), projectID)
	if err != nil {
		handleProjectIssueSyncStoreError(w, err)
		return
	}
	job, err := h.SyncJobs.GetLatestByProjectAndType(r.Context(), projectID, store.GitHubSyncJobTypeIssueImport)
	if err != nil {
		handleProjectIssueSyncStoreError(w, err)
		return
	}

	var lastSyncedAt *time.Time
	if len(checkpoints) > 0 {
		last := checkpoints[0].LastSyncedAt.UTC()
		lastSyncedAt = &last
	}

	var syncError *string
	if job != nil && job.LastError != nil && *job.LastError != "" {
		syncError = job.LastError
	}

	var jobView *projectIssueImportJobView
	if job != nil {
		view := toProjectIssueImportJobView(*job)
		jobView = &view
	}

	sendJSON(w, http.StatusOK, projectIssueSyncStatusResponse{
		ProjectID:          projectID,
		RepositoryFullName: repoBinding.RepositoryFullName,
		Counts:             *counts,
		LastSyncedAt:       lastSyncedAt,
		SyncError:          syncError,
		Job:                jobView,
		Checkpoints:        toProjectIssueCheckpointStatusList(checkpoints),
	})
}

func (h *ProjectIssueSyncHandler) requireImportContext(
	r *http.Request,
	projectID string,
) (*store.ProjectRepoBinding, error) {
	if _, err := h.Projects.GetByID(r.Context(), projectID); err != nil {
		return nil, err
	}

	repoBinding, err := h.ProjectRepos.GetBinding(r.Context(), projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errManualIssueImportRepoNotMapped
		}
		return nil, err
	}
	if !repoBinding.Enabled {
		return nil, errManualIssueImportRepoDisabled
	}

	if _, err := h.Installations.GetByOrg(r.Context()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errManualIssueImportInstallationMissing
		}
		return nil, err
	}

	return repoBinding, nil
}

func toProjectIssueImportJobView(job store.GitHubSyncJob) projectIssueImportJobView {
	return projectIssueImportJobView{
		ID:             job.ID,
		Type:           job.JobType,
		Status:         job.Status,
		AttemptCount:   job.AttemptCount,
		MaxAttempts:    job.MaxAttempts,
		LastError:      job.LastError,
		LastErrorClass: job.LastErrorClass,
		CreatedAt:      job.CreatedAt.UTC(),
		UpdatedAt:      job.UpdatedAt.UTC(),
		CompletedAt:    job.CompletedAt,
	}
}

func toProjectIssueCheckpointStatusList(
	checkpoints []store.ProjectIssueSyncCheckpoint,
) []projectIssueCheckpointStatus {
	out := make([]projectIssueCheckpointStatus, 0, len(checkpoints))
	for _, checkpoint := range checkpoints {
		out = append(out, projectIssueCheckpointStatus{
			ID:                 checkpoint.ID,
			RepositoryFullName: checkpoint.RepositoryFullName,
			Resource:           checkpoint.Resource,
			Cursor:             checkpoint.Cursor,
			LastSyncedAt:       checkpoint.LastSyncedAt.UTC(),
		})
	}
	return out
}

var (
	errManualIssueImportRepoNotMapped       = errors.New("project repository not mapped")
	errManualIssueImportRepoDisabled        = errors.New("project repository mapping is disabled")
	errManualIssueImportInstallationMissing = errors.New("github installation not connected for this workspace")
)

func handleProjectIssueSyncStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
	case errors.Is(err, errManualIssueImportRepoNotMapped):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project repository not mapped"})
	case errors.Is(err, errManualIssueImportRepoDisabled):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project repository mapping is disabled"})
	case errors.Is(err, errManualIssueImportInstallationMissing):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "github installation not connected for this workspace"})
	default:
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	}
}
