package middleware

import (
	"net/http"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/api"
)

func PrefixEnforcement() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := strings.TrimSpace(r.URL.Path)
			if path == "" {
				path = "/"
			}

			if path == "/" {
				http.Redirect(w, r, "/v1/", http.StatusPermanentRedirect)
				return
			}

			if strings.HasPrefix(path, "/v1/") || path == "/v1" || allowedPrefixBypass(path) {
				next.ServeHTTP(w, r)
				return
			}

			api.NewResponder(r.Context()).Error(
				w,
				http.StatusNotFound,
				api.ErrCodeNotFound,
				"This API uses the /v1/ prefix. See docs.",
			)
		})
	}
}

func allowedPrefixBypass(path string) bool {
	if strings.HasPrefix(path, "/test/") {
		return true
	}

	switch path {
	case "/health", "/health/live", "/health/ready", "/ready", "/metrics":
		return true
	default:
		return false
	}
}
