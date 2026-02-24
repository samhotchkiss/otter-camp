package middleware

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/api"
)

func RequestID(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := strings.TrimSpace(r.Header.Get(api.HeaderRequestID))
			if requestID == "" {
				requestID = uuid.NewString()
			}

			w.Header().Set(api.HeaderRequestID, requestID)
			ctx := api.WithRequestID(r.Context(), requestID)

			logger.Debug("http request", "request_id", requestID, "method", r.Method, "path", r.URL.Path)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
