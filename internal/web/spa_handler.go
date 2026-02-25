package web

import (
	"net/http"
	"strings"
)

func SPAFallbackHandler(static *StaticFileServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if static == nil {
			http.NotFound(w, r)
			return
		}
		static.ServeIndex(w, r)
	}
}

func ShouldServeSPAFallback(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}

	path := strings.TrimSpace(r.URL.Path)
	switch {
	case path == "", path == "/":
		return true
	case path == "/v1", strings.HasPrefix(path, "/v1/"):
		return false
	case path == "/metrics":
		return false
	case path == "/health", strings.HasPrefix(path, "/health/"), path == "/ready":
		return false
	case strings.HasPrefix(path, "/assets/"), path == "/favicon.ico":
		return false
	case path == "/test", strings.HasPrefix(path, "/test/"):
		return false
	default:
		return true
	}
}
