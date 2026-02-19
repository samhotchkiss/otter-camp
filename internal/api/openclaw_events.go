package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

type OpenClawEventsHandler struct {
	DB *sql.DB
}

type openClawEventEnvelope struct {
	Event              string                 `json:"event"`
	OrgID              string                 `json:"org_id"`
	SessionKey         string                 `json:"session_key"`
	CompactionDetected bool                   `json:"compaction_detected"`
	Data               map[string]interface{} `json:"data"`
}

type openClawEventIngestResponse struct {
	OK      bool `json:"ok"`
	Updated int  `json:"updated"`
}

func (h *OpenClawEventsHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	db := h.DB
	if db == nil {
		if dbConn, err := store.DB(); err == nil {
			db = dbConn
		}
	}
	if db == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database unavailable"})
		return
	}

	if status, err := requireOpenClawSyncAuth(r.Context(), db, r); err != nil {
		sendJSON(w, status, errorResponse{Error: err.Error()})
		return
	}

	var envelope openClawEventEnvelope
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	eventName := strings.ToLower(strings.TrimSpace(envelope.Event))
	if eventName == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "event is required"})
		return
	}

	isCompactionEvent := strings.Contains(eventName, "compaction")
	if !isCompactionEvent && !envelope.CompactionDetected {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "unsupported event"})
		return
	}

	orgID := strings.TrimSpace(envelope.OrgID)
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
		return
	}
	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	sessionKey := strings.TrimSpace(envelope.SessionKey)
	if sessionKey == "" && envelope.Data != nil {
		if raw, ok := envelope.Data["session_key"].(string); ok {
			sessionKey = strings.TrimSpace(raw)
		}
	}
	if sessionKey == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "session_key is required"})
		return
	}

	result, err := db.ExecContext(
		r.Context(),
		`UPDATE dm_injection_state
		 SET compaction_detected = TRUE,
		     updated_at = NOW()
		 WHERE org_id = $1 AND session_key = $2`,
		orgID,
		sessionKey,
	)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update injection state"})
		return
	}

	updatedRows := 0
	if rowsAffected, rowsErr := result.RowsAffected(); rowsErr == nil && rowsAffected > 0 {
		updatedRows = int(rowsAffected)
	}

	sendJSON(w, http.StatusOK, openClawEventIngestResponse{
		OK:      true,
		Updated: updatedRows,
	})
}
