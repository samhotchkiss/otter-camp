package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const (
	defaultAdminConfigHistoryLimit = 20
	maxAdminConfigHistoryLimit     = 100
)

type AdminConfigHandler struct {
	DB *sql.DB
}

type adminConfigSnapshotResponse struct {
	Hash       string          `json:"hash"`
	Source     string          `json:"source,omitempty"`
	Path       string          `json:"path,omitempty"`
	CapturedAt time.Time       `json:"captured_at"`
	UpdatedAt  *time.Time      `json:"updated_at,omitempty"`
	Data       json.RawMessage `json:"data"`
}

type adminConfigCurrentResponse struct {
	Snapshot *adminConfigSnapshotResponse `json:"snapshot,omitempty"`
}

type adminConfigHistoryResponse struct {
	Entries []adminConfigSnapshotResponse `json:"entries"`
	Total   int                           `json:"total"`
}

func (h *AdminConfigHandler) GetCurrent(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(middleware.WorkspaceFromContext(r.Context())) == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	var (
		snapshot  *openClawConfigSnapshotRecord
		updatedAt time.Time
	)
	if h.DB != nil {
		value, ts, found, err := h.loadSnapshotFromDB(r)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load admin config snapshot"})
			return
		}
		if found {
			snapshot = value
			updatedAt = ts
		}
	}
	if snapshot == nil && memoryConfig != nil {
		cloned := *memoryConfig
		cloned.Data = append(json.RawMessage(nil), memoryConfig.Data...)
		snapshot = &cloned
		updatedAt = snapshot.CapturedAt
	}

	response := adminConfigCurrentResponse{}
	if snapshot != nil {
		response.Snapshot = &adminConfigSnapshotResponse{
			Hash:       snapshot.Hash,
			Source:     snapshot.Source,
			Path:       snapshot.Path,
			CapturedAt: snapshot.CapturedAt,
			UpdatedAt:  &updatedAt,
			Data:       append(json.RawMessage(nil), snapshot.Data...),
		}
	}
	sendJSON(w, http.StatusOK, response)
}

func (h *AdminConfigHandler) ListHistory(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(middleware.WorkspaceFromContext(r.Context())) == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	limit := defaultAdminConfigHistoryLimit
	rawLimit := strings.TrimSpace(r.URL.Query().Get("limit"))
	if rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be a positive integer"})
			return
		}
		limit = parsed
	}
	if limit > maxAdminConfigHistoryLimit {
		limit = maxAdminConfigHistoryLimit
	}

	history := make([]openClawConfigSnapshotRecord, 0)
	if h.DB != nil {
		dbHistory, err := loadOpenClawConfigHistory(r.Context(), h.DB)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load admin config history"})
			return
		}
		history = dbHistory
	}
	if len(history) == 0 && len(memoryConfigHistory) > 0 {
		history = append(history, memoryConfigHistory...)
	}

	sort.SliceStable(history, func(i, j int) bool {
		return history[i].CapturedAt.After(history[j].CapturedAt)
	})

	total := len(history)
	if total > limit {
		history = history[:limit]
	}

	entries := make([]adminConfigSnapshotResponse, 0, len(history))
	for _, item := range history {
		entries = append(entries, adminConfigSnapshotResponse{
			Hash:       item.Hash,
			Source:     item.Source,
			Path:       item.Path,
			CapturedAt: item.CapturedAt,
			Data:       append(json.RawMessage(nil), item.Data...),
		})
	}

	sendJSON(w, http.StatusOK, adminConfigHistoryResponse{
		Entries: entries,
		Total:   total,
	})
}

func (h *AdminConfigHandler) loadSnapshotFromDB(r *http.Request) (
	*openClawConfigSnapshotRecord,
	time.Time,
	bool,
	error,
) {
	var value string
	var updatedAt time.Time
	err := h.DB.QueryRowContext(
		r.Context(),
		`SELECT value, updated_at FROM sync_metadata WHERE key = $1`,
		syncMetadataOpenClawConfigSnapshotKey,
	).Scan(&value, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}
	if strings.TrimSpace(value) == "" {
		return nil, time.Time{}, false, nil
	}

	var snapshot openClawConfigSnapshotRecord
	if err := json.Unmarshal([]byte(value), &snapshot); err != nil {
		return nil, time.Time{}, false, err
	}
	return &snapshot, updatedAt.UTC(), true, nil
}
