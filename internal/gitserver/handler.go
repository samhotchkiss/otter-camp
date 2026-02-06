// Package gitserver implements Smart HTTP git protocol endpoints.
package gitserver

import (
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Handler handles git Smart HTTP requests.
type Handler struct {
	// RepoResolver maps (orgID, projectID) to local repo path.
	// Returns empty string if not found or not authorized.
	RepoResolver func(r *http.Request, orgID, projectID string) (string, error)
}

// Routes returns a chi router for git endpoints.
// Mount at /git
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	// Smart HTTP endpoints
	// GET  /git/{org}/{project}.git/info/refs?service=git-upload-pack
	// POST /git/{org}/{project}.git/git-upload-pack
	// GET  /git/{org}/{project}.git/info/refs?service=git-receive-pack
	// POST /git/{org}/{project}.git/git-receive-pack

	r.Get("/{org}/{project}.git/info/refs", h.InfoRefs)
	r.Post("/{org}/{project}.git/git-upload-pack", h.UploadPack)
	r.Post("/{org}/{project}.git/git-receive-pack", h.ReceivePack)

	return r
}

// InfoRefs handles GET /info/refs?service=git-upload-pack|git-receive-pack
func (h *Handler) InfoRefs(w http.ResponseWriter, r *http.Request) {
	org := chi.URLParam(r, "org")
	project := chi.URLParam(r, "project")
	service := r.URL.Query().Get("service")

	if service != "git-upload-pack" && service != "git-receive-pack" {
		http.Error(w, "Invalid service", http.StatusBadRequest)
		return
	}

	repoPath, err := h.RepoResolver(r, org, project)
	if err != nil {
		http.Error(w, "Repository not found", http.StatusNotFound)
		return
	}

	// TODO: Implement git --advertise-refs
	// cmd := exec.Command("git", service, "--stateless-rpc", "--advertise-refs", repoPath)
	// ...

	w.Header().Set("Content-Type", fmt.Sprintf("application/x-%s-advertisement", service))
	w.WriteHeader(http.StatusOK)

	// Write packet line header
	pktLine := fmt.Sprintf("# service=%s\n", service)
	fmt.Fprintf(w, "%04x%s0000", len(pktLine)+4, pktLine)

	// Execute git command
	cmd := exec.Command("git", service, "--stateless-rpc", "--advertise-refs", repoPath)
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	_ = cmd.Run()
}

// UploadPack handles POST /git-upload-pack (clone/fetch)
func (h *Handler) UploadPack(w http.ResponseWriter, r *http.Request) {
	org := chi.URLParam(r, "org")
	project := chi.URLParam(r, "project")

	repoPath, err := h.RepoResolver(r, org, project)
	if err != nil {
		http.Error(w, "Repository not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")

	cmd := exec.Command("git", "upload-pack", "--stateless-rpc", repoPath)
	cmd.Stdin = r.Body
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	_ = cmd.Run()
}

// ReceivePack handles POST /git-receive-pack (push)
func (h *Handler) ReceivePack(w http.ResponseWriter, r *http.Request) {
	org := chi.URLParam(r, "org")
	project := chi.URLParam(r, "project")

	repoPath, err := h.RepoResolver(r, org, project)
	if err != nil {
		http.Error(w, "Repository not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/x-git-receive-pack-result")

	cmd := exec.Command("git", "receive-pack", "--stateless-rpc", repoPath)
	cmd.Stdin = r.Body
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	_ = cmd.Run()

	// TODO: Post-receive hooks
	// - Log push to activity_log
	// - WebSocket notification
	// - Optional GitHub sync trigger
}

// Helper to clean and validate path components
func cleanPathComponent(s string) string {
	s = strings.TrimSpace(s)
	s = filepath.Clean(s)
	// Remove any path traversal attempts
	s = strings.ReplaceAll(s, "..", "")
	s = strings.ReplaceAll(s, "/", "")
	s = strings.ReplaceAll(s, "\\", "")
	return s
}
