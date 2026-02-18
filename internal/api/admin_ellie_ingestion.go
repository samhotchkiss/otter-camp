package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type AdminEllieIngestionHandler struct {
	Store *store.EllieIngestionStore
}

func (h *AdminEllieIngestionHandler) GetCoverage(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil {
		http.Error(w, `{"error":"service unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	orgID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if orgID == "" {
		http.Error(w, `{"error":"missing workspace"}`, http.StatusUnauthorized)
		return
	}

	days := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			days = v
		}
	}

	rows, summary, err := h.Store.ListCoverageByDay(r.Context(), orgID, days)
	if err != nil {
		http.Error(w, `{"error":"failed to load coverage"}`, http.StatusInternalServerError)
		return
	}

	resp := map[string]any{
		"summary": summary,
		"days":    rows,
	}
	_ = json.NewEncoder(w).Encode(resp)
}
