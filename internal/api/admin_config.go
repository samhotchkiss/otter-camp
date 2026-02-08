package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	defaultAdminConfigHistoryLimit = 20
	maxAdminConfigHistoryLimit     = 100
)

type AdminConfigHandler struct {
	DB              *sql.DB
	OpenClawHandler openClawConnectionStatus
	EventStore      *store.ConnectionEventStore
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

type adminConfigPatchRequest struct {
	Confirm bool            `json:"confirm"`
	DryRun  bool            `json:"dry_run"`
	Patch   json.RawMessage `json:"patch"`
}

var allowedAdminConfigPatchKeys = map[string]struct{}{
	"agents":       {},
	"heartbeat":    {},
	"channels":     {},
	"models":       {},
	"gateway":      {},
	"proxy":        {},
	"proxy_url":    {},
	"proxyUrl":     {},
	"port":         {},
	"cron_jobs":    {},
	"cronJobs":     {},
	"agent_config": {},
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

func (h *AdminConfigHandler) Patch(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(middleware.WorkspaceFromContext(r.Context())) == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	var req adminConfigPatchRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}
	if !req.Confirm {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "confirm must be true"})
		return
	}

	patchPayload, err := validateAdminConfigPatchPayload(req.Patch)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if req.DryRun {
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"dry_run": true,
			"message": "config patch validated",
			"patch":   json.RawMessage(patchPayload),
		})
		return
	}

	dispatcher := &AdminConnectionsHandler{
		DB:              h.DB,
		OpenClawHandler: h.OpenClawHandler,
		EventStore:      h.EventStore,
	}
	dispatcher.dispatchAdminCommand(
		w,
		r,
		adminCommandActionConfigPatch,
		adminCommandDispatchInput{
			ConfigPatch: patchPayload,
			Confirm:     true,
			DryRun:      false,
		},
	)
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

func validateAdminConfigPatchPayload(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("patch is required")
	}

	var patch map[string]json.RawMessage
	if err := json.Unmarshal(raw, &patch); err != nil {
		return nil, fmt.Errorf("patch must be a JSON object")
	}
	if len(patch) == 0 {
		return nil, fmt.Errorf("patch must include at least one key")
	}

	unsupported := make([]string, 0)
	for key, value := range patch {
		name := strings.TrimSpace(key)
		if name == "" {
			return nil, fmt.Errorf("patch contains an empty key")
		}
		if _, ok := allowedAdminConfigPatchKeys[name]; !ok {
			unsupported = append(unsupported, name)
		}
		if len(bytes.TrimSpace(value)) == 0 {
			return nil, fmt.Errorf("patch field %q must not be empty", name)
		}
	}
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return nil, fmt.Errorf("unsupported patch keys: %s", strings.Join(unsupported, ", "))
	}

	var normalized map[string]interface{}
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("patch must be valid JSON")
	}
	return canonicalizeOpenClawConfigData(normalized)
}
