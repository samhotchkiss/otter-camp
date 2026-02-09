package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type DeployConfigHandler struct {
	Store *store.DeployConfigStore
}

type deployConfigRequest struct {
	DeployMethod  string  `json:"deployMethod"`
	GitHubRepoURL *string `json:"githubRepoUrl"`
	GitHubBranch  *string `json:"githubBranch"`
	CLICommand    *string `json:"cliCommand"`
}

type deployConfigResponse struct {
	DeployMethod  string  `json:"deployMethod"`
	GitHubRepoURL *string `json:"githubRepoUrl"`
	GitHubBranch  string  `json:"githubBranch"`
	CLICommand    *string `json:"cliCommand"`
}

func (h *DeployConfigHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "deploy config store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	config, err := h.Store.GetByProject(r.Context(), projectID)
	if err != nil {
		sendJSON(w, deployConfigStoreErrorStatus(err), errorResponse{Error: deployConfigStoreErrorMessage(err)})
		return
	}

	sendJSON(w, http.StatusOK, deployConfigToResponse(config))
}

func (h *DeployConfigHandler) Put(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "deploy config store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	var req deployConfigRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON payload"})
		return
	}

	branch := ""
	if req.GitHubBranch != nil {
		branch = *req.GitHubBranch
	}

	config, err := h.Store.Upsert(r.Context(), store.UpsertDeployConfigInput{
		ProjectID:     projectID,
		DeployMethod:  req.DeployMethod,
		GitHubRepoURL: req.GitHubRepoURL,
		GitHubBranch:  branch,
		CLICommand:    req.CLICommand,
	})
	if err != nil {
		sendJSON(w, deployConfigStoreErrorStatus(err), errorResponse{Error: deployConfigStoreErrorMessage(err)})
		return
	}

	sendJSON(w, http.StatusOK, deployConfigToResponse(config))
}

func deployConfigToResponse(cfg *store.DeployConfig) deployConfigResponse {
	return deployConfigResponse{
		DeployMethod:  cfg.DeployMethod,
		GitHubRepoURL: cfg.GitHubRepoURL,
		GitHubBranch:  cfg.GitHubBranch,
		CLICommand:    cfg.CLICommand,
	}
}

func deployConfigStoreErrorStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		return http.StatusBadRequest
	case errors.Is(err, store.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case strings.Contains(err.Error(), "invalid"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func deployConfigStoreErrorMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		return "workspace is required"
	case errors.Is(err, store.ErrForbidden):
		return "forbidden"
	case errors.Is(err, store.ErrNotFound):
		return "project not found"
	default:
		return err.Error()
	}
}
