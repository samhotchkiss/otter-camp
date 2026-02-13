package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	importer "github.com/samhotchkiss/otter-camp/internal/import"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/scheduler"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type JobsHandler struct {
	Store *store.AgentJobStore
	DB    *sql.DB
}

type jobsListResponse struct {
	Items []jobPayload `json:"items"`
	Total int          `json:"total"`
}

type jobRunsResponse struct {
	Items []jobRunPayload `json:"items"`
	Total int             `json:"total"`
}

type jobPayload struct {
	ID                  string  `json:"id"`
	OrgID               string  `json:"org_id"`
	AgentID             string  `json:"agent_id"`
	Name                string  `json:"name"`
	Description         *string `json:"description,omitempty"`
	ScheduleKind        string  `json:"schedule_kind"`
	CronExpr            *string `json:"cron_expr,omitempty"`
	IntervalMS          *int64  `json:"interval_ms,omitempty"`
	RunAt               *string `json:"run_at,omitempty"`
	Timezone            string  `json:"timezone"`
	PayloadKind         string  `json:"payload_kind"`
	PayloadText         string  `json:"payload_text"`
	RoomID              *string `json:"room_id,omitempty"`
	Enabled             bool    `json:"enabled"`
	Status              string  `json:"status"`
	LastRunAt           *string `json:"last_run_at,omitempty"`
	LastRunStatus       *string `json:"last_run_status,omitempty"`
	LastRunError        *string `json:"last_run_error,omitempty"`
	NextRunAt           *string `json:"next_run_at,omitempty"`
	RunCount            int     `json:"run_count"`
	ErrorCount          int     `json:"error_count"`
	MaxFailures         int     `json:"max_failures"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	CreatedBy           *string `json:"created_by,omitempty"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
}

type jobRunPayload struct {
	ID          string  `json:"id"`
	JobID       string  `json:"job_id"`
	OrgID       string  `json:"org_id"`
	Status      string  `json:"status"`
	StartedAt   string  `json:"started_at"`
	CompletedAt *string `json:"completed_at,omitempty"`
	DurationMS  *int    `json:"duration_ms,omitempty"`
	Error       *string `json:"error,omitempty"`
	PayloadText string  `json:"payload_text"`
	MessageID   *string `json:"message_id,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

type createJobRequest struct {
	AgentID      string  `json:"agent_id"`
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	ScheduleKind string  `json:"schedule_kind"`
	CronExpr     *string `json:"cron_expr"`
	IntervalMS   *int64  `json:"interval_ms"`
	RunAt        *string `json:"run_at"`
	Timezone     *string `json:"timezone"`
	PayloadKind  string  `json:"payload_kind"`
	PayloadText  string  `json:"payload_text"`
	RoomID       *string `json:"room_id"`
	Enabled      *bool   `json:"enabled"`
	MaxFailures  *int    `json:"max_failures"`
	SessionKey   *string `json:"session_key"`
}

type patchJobRequest struct {
	Name         *string `json:"name"`
	Description  *string `json:"description"`
	ScheduleKind *string `json:"schedule_kind"`
	CronExpr     *string `json:"cron_expr"`
	IntervalMS   *int64  `json:"interval_ms"`
	RunAt        *string `json:"run_at"`
	Timezone     *string `json:"timezone"`
	PayloadKind  *string `json:"payload_kind"`
	PayloadText  *string `json:"payload_text"`
	RoomID       *string `json:"room_id"`
	Enabled      *bool   `json:"enabled"`
	MaxFailures  *int    `json:"max_failures"`
}

func (h *JobsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}
	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
		return
	}

	var req createJobRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	scopedAgentID, err := h.resolveScopedAgentID(r, req.SessionKey)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if scopedAgentID != nil && !strings.EqualFold(strings.TrimSpace(req.AgentID), *scopedAgentID) {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "agents may only create jobs for themselves"})
		return
	}

	runAt, err := parseOptionalRFC3339(req.RunAt, "run_at")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid run_at"})
		return
	}

	spec, err := scheduler.NormalizeScheduleSpec(
		req.ScheduleKind,
		req.CronExpr,
		req.IntervalMS,
		runAt,
		derefString(req.Timezone, "UTC"),
	)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	now := time.Now().UTC()
	nextRunAt, err := scheduler.ComputeNextRun(spec, now, nil)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	createdBy := middleware.UserFromContext(r.Context())
	var createdByPtr *string
	if strings.TrimSpace(createdBy) != "" {
		createdByPtr = &createdBy
	}

	job, err := h.Store.Create(r.Context(), store.CreateAgentJobInput{
		AgentID:      strings.TrimSpace(req.AgentID),
		Name:         strings.TrimSpace(req.Name),
		Description:  req.Description,
		ScheduleKind: req.ScheduleKind,
		CronExpr:     req.CronExpr,
		IntervalMS:   req.IntervalMS,
		RunAt:        runAt,
		Timezone:     req.Timezone,
		PayloadKind:  req.PayloadKind,
		PayloadText:  req.PayloadText,
		RoomID:       req.RoomID,
		Enabled:      req.Enabled,
		NextRunAt:    nextRunAt,
		MaxFailures:  req.MaxFailures,
		CreatedBy:    createdByPtr,
	})
	if err != nil {
		handleJobsStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusCreated, toJobPayload(*job))
}

func (h *JobsHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	scopedAgentID, err := h.resolveScopedAgentID(r, nil)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	filter := store.AgentJobFilter{}
	if scopedAgentID != nil {
		filter.AgentID = scopedAgentID
	} else if raw := strings.TrimSpace(r.URL.Query().Get("agent_id")); raw != "" {
		filter.AgentID = &raw
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		filter.Status = &raw
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("enabled")); raw != "" {
		enabled, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid enabled filter"})
			return
		}
		filter.Enabled = &enabled
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, parseErr := strconv.Atoi(raw)
		if parseErr != nil || limit <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
			return
		}
		filter.Limit = limit
	}

	items, err := h.Store.List(r.Context(), filter)
	if err != nil {
		handleJobsStoreError(w, err)
		return
	}

	payload := make([]jobPayload, 0, len(items))
	for _, item := range items {
		payload = append(payload, toJobPayload(item))
	}
	sendJSON(w, http.StatusOK, jobsListResponse{Items: payload, Total: len(payload)})
}

func (h *JobsHandler) Get(w http.ResponseWriter, r *http.Request) {
	job, scopedAgentID, ok := h.getAuthorizedJob(w, r)
	if !ok {
		return
	}
	if scopedAgentID != nil && job.AgentID != *scopedAgentID {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}
	sendJSON(w, http.StatusOK, toJobPayload(*job))
}

func (h *JobsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	job, scopedAgentID, ok := h.getAuthorizedJob(w, r)
	if !ok {
		return
	}
	if scopedAgentID != nil && job.AgentID != *scopedAgentID {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}

	var req patchJobRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}
	if req.Name == nil &&
		req.Description == nil &&
		req.ScheduleKind == nil &&
		req.CronExpr == nil &&
		req.IntervalMS == nil &&
		req.RunAt == nil &&
		req.Timezone == nil &&
		req.PayloadKind == nil &&
		req.PayloadText == nil &&
		req.RoomID == nil &&
		req.Enabled == nil &&
		req.MaxFailures == nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "at least one field is required"})
		return
	}

	runAt, err := parseOptionalRFC3339(req.RunAt, "run_at")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid run_at"})
		return
	}

	updated, err := h.Store.Update(r.Context(), job.ID, store.UpdateAgentJobInput{
		Name:         req.Name,
		Description:  req.Description,
		ScheduleKind: req.ScheduleKind,
		CronExpr:     req.CronExpr,
		IntervalMS:   req.IntervalMS,
		RunAt:        runAt,
		Timezone:     req.Timezone,
		PayloadKind:  req.PayloadKind,
		PayloadText:  req.PayloadText,
		RoomID:       req.RoomID,
		Enabled:      req.Enabled,
		MaxFailures:  req.MaxFailures,
	})
	if err != nil {
		handleJobsStoreError(w, err)
		return
	}

	if updated.Enabled && updated.Status == store.AgentJobStatusActive {
		spec, specErr := scheduler.NormalizeScheduleSpec(
			updated.ScheduleKind,
			updated.CronExpr,
			updated.IntervalMS,
			updated.RunAt,
			updated.Timezone,
		)
		if specErr == nil {
			nextRunAt, computeErr := scheduler.ComputeNextRun(spec, time.Now().UTC(), nil)
			if computeErr == nil {
				if refreshed, refreshErr := h.Store.Update(r.Context(), updated.ID, store.UpdateAgentJobInput{
					NextRunAt: nextRunAt,
				}); refreshErr == nil {
					updated = refreshed
				}
			}
		}
	}

	sendJSON(w, http.StatusOK, toJobPayload(*updated))
}

func (h *JobsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	job, scopedAgentID, ok := h.getAuthorizedJob(w, r)
	if !ok {
		return
	}
	if scopedAgentID != nil && job.AgentID != *scopedAgentID {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}

	if err := h.Store.Delete(r.Context(), job.ID); err != nil {
		handleJobsStoreError(w, err)
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": job.ID})
}

func (h *JobsHandler) RunNow(w http.ResponseWriter, r *http.Request) {
	job, scopedAgentID, ok := h.getAuthorizedJob(w, r)
	if !ok {
		return
	}
	if scopedAgentID != nil && job.AgentID != *scopedAgentID {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}

	now := time.Now().UTC()
	updated, err := h.Store.Update(r.Context(), job.ID, store.UpdateAgentJobInput{
		Status:    jobsStrPtr(store.AgentJobStatusActive),
		NextRunAt: &now,
	})
	if err != nil {
		handleJobsStoreError(w, err)
		return
	}
	sendJSON(w, http.StatusOK, toJobPayload(*updated))
}

func (h *JobsHandler) ListRuns(w http.ResponseWriter, r *http.Request) {
	job, scopedAgentID, ok := h.getAuthorizedJob(w, r)
	if !ok {
		return
	}
	if scopedAgentID != nil && job.AgentID != *scopedAgentID {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}

	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
			return
		}
		limit = parsed
	}

	runs, err := h.Store.ListRuns(r.Context(), job.ID, limit)
	if err != nil {
		handleJobsStoreError(w, err)
		return
	}

	payload := make([]jobRunPayload, 0, len(runs))
	for _, run := range runs {
		payload = append(payload, toJobRunPayload(run))
	}
	sendJSON(w, http.StatusOK, jobRunsResponse{Items: payload, Total: len(payload)})
}

func (h *JobsHandler) Pause(w http.ResponseWriter, r *http.Request) {
	job, scopedAgentID, ok := h.getAuthorizedJob(w, r)
	if !ok {
		return
	}
	if scopedAgentID != nil && job.AgentID != *scopedAgentID {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}

	updated, err := h.Store.Update(r.Context(), job.ID, store.UpdateAgentJobInput{
		Status: jobsStrPtr(store.AgentJobStatusPaused),
	})
	if err != nil {
		handleJobsStoreError(w, err)
		return
	}
	sendJSON(w, http.StatusOK, toJobPayload(*updated))
}

func (h *JobsHandler) Resume(w http.ResponseWriter, r *http.Request) {
	job, scopedAgentID, ok := h.getAuthorizedJob(w, r)
	if !ok {
		return
	}
	if scopedAgentID != nil && job.AgentID != *scopedAgentID {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}
	if job.ScheduleKind == store.AgentJobScheduleOnce && job.Status == store.AgentJobStatusCompleted {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "cannot resume completed one-shot job"})
		return
	}

	spec, err := scheduler.NormalizeScheduleSpec(
		job.ScheduleKind,
		job.CronExpr,
		job.IntervalMS,
		job.RunAt,
		job.Timezone,
	)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	nextRunAt, err := scheduler.ComputeNextRun(spec, time.Now().UTC(), nil)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	updated, err := h.Store.Update(r.Context(), job.ID, store.UpdateAgentJobInput{
		Status:    jobsStrPtr(store.AgentJobStatusActive),
		NextRunAt: nextRunAt,
	})
	if err != nil {
		handleJobsStoreError(w, err)
		return
	}
	sendJSON(w, http.StatusOK, toJobPayload(*updated))
}

func (h *JobsHandler) ImportOpenClawCron(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
		return
	}

	result, err := importer.NewOpenClawCronJobImporter(h.DB).ImportFromSyncMetadata(r.Context(), workspaceID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to import openclaw cron jobs"})
		return
	}
	sendJSON(w, http.StatusOK, result)
}

func (h *JobsHandler) getAuthorizedJob(w http.ResponseWriter, r *http.Request) (*store.AgentJob, *string, bool) {
	if h.Store == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return nil, nil, false
	}

	scopedAgentID, err := h.resolveScopedAgentID(r, nil)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return nil, nil, false
	}

	jobID := strings.TrimSpace(chi.URLParam(r, "id"))
	if jobID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "job id is required"})
		return nil, nil, false
	}
	job, err := h.Store.GetByID(r.Context(), jobID)
	if err != nil {
		handleJobsStoreError(w, err)
		return nil, nil, false
	}
	return job, scopedAgentID, true
}

func (h *JobsHandler) resolveScopedAgentID(r *http.Request, bodySessionKey *string) (*string, error) {
	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		return nil, errors.New("org_id is required")
	}

	sessionKey := strings.TrimSpace(r.URL.Query().Get("session_key"))
	if sessionKey == "" && bodySessionKey != nil {
		sessionKey = strings.TrimSpace(*bodySessionKey)
	}
	if sessionKey == "" {
		return nil, nil
	}

	agentID, err := resolveSessionAgentID(r.Context(), h.DB, workspaceID, sessionKey)
	if err != nil {
		return nil, err
	}
	return &agentID, nil
}

func resolveSessionAgentID(ctx context.Context, db *sql.DB, orgID, sessionKey string) (string, error) {
	if canonicalID, ok := ExtractChameleonSessionAgentID(sessionKey); ok {
		var exists bool
		err := db.QueryRowContext(
			ctx,
			`SELECT EXISTS (
				SELECT 1
				FROM agents
				WHERE org_id = $1 AND id = $2
			)`,
			orgID,
			canonicalID,
		).Scan(&exists)
		if err != nil {
			return "", err
		}
		if !exists {
			return "", errors.New("session agent not found")
		}
		return canonicalID, nil
	}

	identity := strings.TrimSpace(ExtractSessionAgentIdentity(sessionKey))
	if identity == "" {
		return "", errors.New("session_key is invalid")
	}

	var agentID string
	err := db.QueryRowContext(
		ctx,
		`SELECT id
		 FROM agents
		 WHERE org_id = $1
		   AND (id::text = $2 OR LOWER(slug) = LOWER($2))
		 LIMIT 1`,
		orgID,
		identity,
	).Scan(&agentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.New("session agent not found")
		}
		return "", err
	}
	return agentID, nil
}

func toJobPayload(job store.AgentJob) jobPayload {
	return jobPayload{
		ID:                  job.ID,
		OrgID:               job.OrgID,
		AgentID:             job.AgentID,
		Name:                job.Name,
		Description:         job.Description,
		ScheduleKind:        job.ScheduleKind,
		CronExpr:            job.CronExpr,
		IntervalMS:          job.IntervalMS,
		RunAt:               formatOptionalTime(job.RunAt),
		Timezone:            job.Timezone,
		PayloadKind:         job.PayloadKind,
		PayloadText:         job.PayloadText,
		RoomID:              job.RoomID,
		Enabled:             job.Enabled,
		Status:              job.Status,
		LastRunAt:           formatOptionalTime(job.LastRunAt),
		LastRunStatus:       job.LastRunStatus,
		LastRunError:        job.LastRunError,
		NextRunAt:           formatOptionalTime(job.NextRunAt),
		RunCount:            job.RunCount,
		ErrorCount:          job.ErrorCount,
		MaxFailures:         job.MaxFailures,
		ConsecutiveFailures: job.ConsecutiveFailures,
		CreatedBy:           job.CreatedBy,
		CreatedAt:           job.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:           job.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toJobRunPayload(run store.AgentJobRun) jobRunPayload {
	return jobRunPayload{
		ID:          run.ID,
		JobID:       run.JobID,
		OrgID:       run.OrgID,
		Status:      run.Status,
		StartedAt:   run.StartedAt.UTC().Format(time.RFC3339),
		CompletedAt: formatOptionalTime(run.CompletedAt),
		DurationMS:  run.DurationMS,
		Error:       run.Error,
		PayloadText: run.PayloadText,
		MessageID:   run.MessageID,
		CreatedAt:   run.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func formatOptionalTime(ts *time.Time) *string {
	if ts == nil {
		return nil
	}
	value := ts.UTC().Format(time.RFC3339)
	return &value
}

func handleJobsStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
	case errors.Is(err, store.ErrValidation):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	case errors.Is(err, store.ErrConflict):
		sendJSON(w, http.StatusConflict, errorResponse{Error: "conflict"})
	default:
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	}
}

func jobsStrPtr(value string) *string {
	return &value
}

func derefString(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}
