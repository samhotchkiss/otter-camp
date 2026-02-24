package server

import (
	"log/slog"
	"net/http"

	"github.com/samhotchkiss/otter-camp/internal/api"
)

func newTestResetHandler(logger *slog.Logger, resetter TestResetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if resetter == nil {
			api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "test reset unavailable")
			return
		}
		if err := resetter.Reset(r.Context()); err != nil {
			if logger != nil {
				logger.Error("test reset failed", "error", err)
			}
			api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "test reset failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
