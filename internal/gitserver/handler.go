// Package gitserver implements Smart HTTP git protocol endpoints.
package gitserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

var uuidRegex = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Handler handles git Smart HTTP requests.
type Handler struct {
	// RepoResolver maps (orgID, projectID) to local repo path.
	RepoResolver func(ctx context.Context, orgID, projectID string) (string, error)
}

// Routes returns a chi router for git endpoints.
// Mount at /git
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	r.Get("/{org}/{project}.git/info/refs", h.InfoRefs)
	r.Post("/{org}/{project}.git/git-upload-pack", h.UploadPack)

	return r
}

// InfoRefs handles GET /info/refs?service=git-upload-pack
func (h *Handler) InfoRefs(w http.ResponseWriter, r *http.Request) {
	org := chi.URLParam(r, "org")
	project := chi.URLParam(r, "project")
	service := r.URL.Query().Get("service")

	if !uuidRegex.MatchString(org) || !uuidRegex.MatchString(project) {
		http.Error(w, "Invalid org or project", http.StatusBadRequest)
		return
	}

	if service != "git-upload-pack" {
		http.Error(w, "Invalid service", http.StatusBadRequest)
		return
	}

	if authOrg := OrgIDFromContext(r.Context()); authOrg == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="OtterCamp Git"`)
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	} else if authOrg != org {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	repoPath, err := h.RepoResolver(r.Context(), org, project)
	if err != nil {
		handleRepoError(w, err)
		return
	}

	w.Header().Set("Content-Type", fmt.Sprintf("application/x-%s-advertisement", service))
	w.WriteHeader(http.StatusOK)

	pktLine := fmt.Sprintf("# service=%s\n", service)
	fmt.Fprintf(w, "%04x%s0000", len(pktLine)+4, pktLine)

	cmd := exec.Command("git", "upload-pack", "--stateless-rpc", "--advertise-refs", repoPath)
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		http.Error(w, "git upload-pack failed", http.StatusInternalServerError)
		return
	}
}

// UploadPack handles POST /git-upload-pack (clone/fetch)
func (h *Handler) UploadPack(w http.ResponseWriter, r *http.Request) {
	org := chi.URLParam(r, "org")
	project := chi.URLParam(r, "project")

	if !uuidRegex.MatchString(org) || !uuidRegex.MatchString(project) {
		http.Error(w, "Invalid org or project", http.StatusBadRequest)
		return
	}

	if authOrg := OrgIDFromContext(r.Context()); authOrg == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="OtterCamp Git"`)
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	} else if authOrg != org {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	repoPath, err := h.RepoResolver(r.Context(), org, project)
	if err != nil {
		handleRepoError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")

	cmd := exec.Command("git", "upload-pack", "--stateless-rpc", repoPath)
	cmd.Stdin = r.Body
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		http.Error(w, "git upload-pack failed", http.StatusInternalServerError)
		return
	}
}

func handleRepoError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrForbidden):
		http.Error(w, "Forbidden", http.StatusForbidden)
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "Repository not found", http.StatusNotFound)
	default:
		http.Error(w, "Repository error", http.StatusInternalServerError)
	}
}
