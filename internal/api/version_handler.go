package api

import (
	"net/http"
	"runtime"
	"strings"
)

type BuildInfo struct {
	Version     string
	Commit      string
	BuiltAt     string
	RepoVersion string
}

type VersionHandler struct {
	info BuildInfo
}

func NewVersionHandler(info BuildInfo) VersionHandler {
	return VersionHandler{info: info}
}

func (h VersionHandler) Get(w http.ResponseWriter, r *http.Request) {
	responder := NewResponder(r.Context())

	version := strings.TrimSpace(h.info.Version)
	if version == "" {
		version = "dev"
	}

	commit := strings.TrimSpace(h.info.Commit)
	if commit == "" {
		commit = "unknown"
	}

	builtAt := strings.TrimSpace(h.info.BuiltAt)
	if builtAt == "" {
		builtAt = "unknown"
	}

	responder.JSON(w, http.StatusOK, map[string]string{
		"version":      version,
		"commit":       commit,
		"built_at":     builtAt,
		"go_version":   runtime.Version(),
		"repo_version": strings.TrimSpace(h.info.RepoVersion),
	})
}
