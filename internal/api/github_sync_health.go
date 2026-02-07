package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/githubsync"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/syncmetrics"
)

type GitHubSyncHealthHandler struct {
	Store *store.GitHubSyncJobStore
}

type githubSyncHealthResponse struct {
	QueueDepth     []store.GitHubSyncQueueDepth     `json:"queue_depth"`
	StuckJobs      int                              `json:"stuck_jobs"`
	StuckThreshold string                           `json:"stuck_threshold"`
	Metrics        syncmetrics.Snapshot             `json:"metrics"`
	Poller         githubsync.RepoDriftPollSnapshot `json:"poller"`
	GeneratedAt    time.Time                        `json:"generated_at"`
}

func (h *GitHubSyncHealthHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	stuckThreshold := 15 * time.Minute
	if raw := strings.TrimSpace(r.URL.Query().Get("stuck_threshold")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "stuck_threshold must be a positive duration"})
			return
		}
		stuckThreshold = parsed
	}

	queueDepth, err := h.Store.QueueDepth(r.Context())
	if err != nil {
		handleGitHubSyncHealthStoreError(w, err)
		return
	}

	stuckJobs, err := h.Store.CountStuckJobs(r.Context(), stuckThreshold)
	if err != nil {
		handleGitHubSyncHealthStoreError(w, err)
		return
	}

	resp := githubSyncHealthResponse{
		QueueDepth:     queueDepth,
		StuckJobs:      stuckJobs,
		StuckThreshold: stuckThreshold.String(),
		Metrics:        syncmetrics.SnapshotNow(),
		Poller:         githubsync.CurrentRepoDriftPollSnapshot(),
		GeneratedAt:    time.Now().UTC(),
	}
	sendJSON(w, http.StatusOK, resp)
}

func handleGitHubSyncHealthStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	default:
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "github sync health query failed"})
	}
}
