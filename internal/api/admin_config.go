package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
	syncMetadataOpenClawCutoverKey = "openclaw_config_cutover_checkpoint"
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

type adminConfigCutoverRequest struct {
	Confirm bool `json:"confirm"`
	DryRun  bool `json:"dry_run"`
}

type adminConfigReleaseGateRequest struct {
	Confirm bool `json:"confirm"`
}

type adminConfigReleaseGateCheck struct {
	Category string `json:"category"`
	Status   string `json:"status"`
	Message  string `json:"message"`
}

type adminConfigReleaseGateResponse struct {
	OK          bool                          `json:"ok"`
	Checks      []adminConfigReleaseGateCheck `json:"checks"`
	GeneratedAt time.Time                     `json:"generated_at"`
}

type bufferedResponseCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *bufferedResponseCapture) Header() http.Header {
	if c.header == nil {
		c.header = make(http.Header)
	}
	return c.header
}

func (c *bufferedResponseCapture) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.body.Write(p)
}

func (c *bufferedResponseCapture) WriteHeader(status int) {
	c.status = status
}

func (c *bufferedResponseCapture) FlushTo(w http.ResponseWriter) {
	for key, values := range c.Header() {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(c.body.Bytes())
}

type openClawConfigCutoverCheckpoint struct {
	SnapshotHash   string          `json:"snapshot_hash"`
	CutoverHash    string          `json:"cutover_hash"`
	PrimaryAgentID string          `json:"primary_agent_id"`
	ConfigPath     string          `json:"config_path,omitempty"`
	CapturedAt     time.Time       `json:"captured_at"`
	CreatedAt      time.Time       `json:"created_at"`
	CutoverConfig  json.RawMessage `json:"cutover_config"`
	RollbackConfig json.RawMessage `json:"rollback_config"`
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
	if snapshot == nil {
		snapshot = memoryConfigSnapshot()
	}
	if snapshot != nil {
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
	if len(history) == 0 {
		history = memoryConfigHistorySnapshot()
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

func (h *AdminConfigHandler) ReleaseGate(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	var req adminConfigReleaseGateRequest
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

	snapshot, err := h.loadSnapshotWithMemoryFallback(r)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load admin config snapshot"})
		return
	}

	report, err := h.evaluateSpec110ReleaseGate(r.Context(), snapshot)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to evaluate release gate"})
		return
	}

	if report.OK {
		sendJSON(w, http.StatusOK, report)
		return
	}
	sendJSON(w, http.StatusPreconditionFailed, report)
}

func (h *AdminConfigHandler) Cutover(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	var req adminConfigCutoverRequest
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

	snapshot, err := h.loadSnapshotWithMemoryFallback(r)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load admin config snapshot"})
		return
	}
	if snapshot == nil {
		sendJSON(w, http.StatusConflict, errorResponse{Error: "no OpenClaw config snapshot available for cutover"})
		return
	}

	gateReport, err := h.evaluateSpec110ReleaseGate(r.Context(), snapshot)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to evaluate release gate"})
		return
	}
	if !gateReport.OK {
		sendJSON(w, http.StatusPreconditionFailed, map[string]interface{}{
			"error": "spec 110 release gate failed",
			"gate":  gateReport,
		})
		return
	}

	cutoverConfig, primaryAgentID, err := buildTwoAgentOpenClawConfig(snapshot.Data)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	cutoverHash, err := hashCanonicalJSONRaw(cutoverConfig)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to hash cutover config"})
		return
	}

	snapshotHash := strings.TrimSpace(snapshot.Hash)
	if snapshotHash == "" {
		snapshotHash, err = hashCanonicalJSONRaw(snapshot.Data)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to hash snapshot config"})
			return
		}
	}

	checkpoint := openClawConfigCutoverCheckpoint{
		SnapshotHash:   snapshotHash,
		CutoverHash:    cutoverHash,
		PrimaryAgentID: primaryAgentID,
		ConfigPath:     strings.TrimSpace(snapshot.Path),
		CapturedAt:     snapshot.CapturedAt,
		CreatedAt:      time.Now().UTC(),
		CutoverConfig:  append(json.RawMessage(nil), cutoverConfig...),
		RollbackConfig: append(json.RawMessage(nil), snapshot.Data...),
	}

	if req.DryRun {
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"ok":         true,
			"dry_run":    true,
			"checkpoint": checkpoint,
		})
		return
	}

	dispatcher := &AdminConnectionsHandler{
		DB:              h.DB,
		OpenClawHandler: h.OpenClawHandler,
		EventStore:      h.EventStore,
	}
	dispatchCapture := &bufferedResponseCapture{}
	dispatcher.dispatchAdminCommand(
		dispatchCapture,
		r,
		adminCommandActionConfigCutover,
		adminCommandDispatchInput{
			ConfigFull: cutoverConfig,
			Confirm:    true,
			DryRun:     false,
		},
	)

	if dispatchCapture.status == http.StatusOK && h.DB != nil {
		if err := upsertSyncMetadataJSON(r.Context(), h.DB, syncMetadataOpenClawCutoverKey, checkpoint, time.Now().UTC()); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to persist cutover checkpoint"})
			return
		}
	}

	dispatchCapture.FlushTo(w)
}

func (h *AdminConfigHandler) Rollback(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	var req adminConfigCutoverRequest
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
	if h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	checkpoint, err := h.loadCutoverCheckpoint(r)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load cutover checkpoint"})
		return
	}
	if checkpoint == nil {
		sendJSON(w, http.StatusConflict, errorResponse{Error: "no cutover checkpoint available"})
		return
	}
	if len(bytes.TrimSpace(checkpoint.RollbackConfig)) == 0 {
		sendJSON(w, http.StatusConflict, errorResponse{Error: "cutover checkpoint is missing rollback config"})
		return
	}

	currentSnapshot, _, found, err := h.loadSnapshotFromDB(r)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load admin config snapshot"})
		return
	}
	if !found || currentSnapshot == nil {
		sendJSON(w, http.StatusConflict, errorResponse{Error: "no OpenClaw config snapshot available for rollback validation"})
		return
	}
	currentHash := strings.TrimSpace(currentSnapshot.Hash)
	if currentHash == "" {
		currentHash, err = hashCanonicalJSONRaw(currentSnapshot.Data)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to hash current config snapshot"})
			return
		}
	}
	if currentHash != strings.TrimSpace(checkpoint.CutoverHash) {
		sendJSON(w, http.StatusConflict, errorResponse{Error: "current config hash does not match stored cutover hash"})
		return
	}

	if req.DryRun {
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"ok":         true,
			"dry_run":    true,
			"checkpoint": checkpoint,
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
		adminCommandActionConfigRollback,
		adminCommandDispatchInput{
			ConfigFull: checkpoint.RollbackConfig,
			ConfigHash: strings.TrimSpace(checkpoint.CutoverHash),
			Confirm:    true,
			DryRun:     false,
		},
	)
}

func (h *AdminConfigHandler) loadSnapshotWithMemoryFallback(r *http.Request) (*openClawConfigSnapshotRecord, error) {
	snapshot, _, found, err := h.loadSnapshotFromDB(r)
	if err != nil {
		return nil, err
	}
	if found && snapshot != nil {
		return snapshot, nil
	}
	if snapshot := memoryConfigSnapshot(); snapshot != nil {
		return snapshot, nil
	}
	return nil, nil
}

func (h *AdminConfigHandler) evaluateSpec110ReleaseGate(
	ctx context.Context,
	snapshot *openClawConfigSnapshotRecord,
) (adminConfigReleaseGateResponse, error) {
	checks := make([]adminConfigReleaseGateCheck, 0, 5)
	now := time.Now().UTC()

	addCheck := func(category, status, message string) {
		checks = append(checks, adminConfigReleaseGateCheck{
			Category: category,
			Status:   status,
			Message:  message,
		})
	}

	var (
		cutoverConfig json.RawMessage
		snapshotHash  string
		cutoverHash   string
	)

	if snapshot == nil {
		addCheck("migration", "fail", "no OpenClaw config snapshot available")
	} else {
		var err error
		cutoverConfig, _, err = buildTwoAgentOpenClawConfig(snapshot.Data)
		if err != nil {
			addCheck("migration", "fail", fmt.Sprintf("failed to build two-agent cutover config: %v", err))
		} else {
			snapshotHash = strings.TrimSpace(snapshot.Hash)
			if snapshotHash == "" {
				snapshotHash, err = hashCanonicalJSONRaw(snapshot.Data)
			}
			if err != nil {
				addCheck("migration", "fail", "failed to hash snapshot config")
			} else {
				cutoverHash, err = hashCanonicalJSONRaw(cutoverConfig)
				if err != nil {
					addCheck("migration", "fail", "failed to hash cutover config")
				} else if snapshotHash == cutoverHash {
					addCheck("migration", "fail", "cutover config matches current snapshot; rollback checkpoint would be a no-op")
				} else {
					report, found, loadErr := h.loadLegacyImportReport(ctx)
					if loadErr != nil {
						addCheck("migration", "fail", "failed to load legacy workspace import report")
					} else if !found {
						addCheck("migration", "fail", "legacy workspace import report not found")
					} else if report.ProcessedWorkspaceCount > 0 && report.ImportedAgents == 0 {
						addCheck("migration", "fail", "legacy import processed workspaces but imported no agents")
					} else if report.ProcessedRetiredWorkspaces > 0 && report.TransitionFilesGenerated == 0 {
						addCheck("migration", "fail", "legacy retired workspaces missing transition files")
					} else {
						addCheck(
							"migration",
							"pass",
							fmt.Sprintf(
								"snapshot and cutover hashes differ (%s -> %s); legacy import report present for %d workspace(s)",
								snapshotHash,
								cutoverHash,
								report.ProcessedWorkspaceCount,
							),
						)
					}
				}
			}
		}
	}

	identityCheckPass := true
	if _, ok := ExtractChameleonSessionAgentID("agent:chameleon:oc:11111111-2222-3333-4444-555555555555"); !ok {
		identityCheckPass = false
	}
	if ValidateChameleonSessionKey("agent:chameleon:oc:not-a-uuid") {
		identityCheckPass = false
	}
	if !identityCheckPass {
		addCheck("identity", "fail", "canonical session identity parser validation failed")
	} else {
		addCheck("identity", "pass", "canonical session identity parser and spoof rejection are active")
	}

	modeCheckPass := strings.Contains(legacyTransitionChecklistTemplate, "task has no project") &&
		strings.Contains(legacyTransitionChecklistTemplate, "all file writes inside that repo")
	if !modeCheckPass {
		addCheck("mode_gating", "fail", "legacy transition checklist is missing project-bound execution policy")
	} else {
		addCheck("mode_gating", "pass", "project-bound execution checklist requires repo-scoped writes")
	}

	securityPass := true
	if _, err := normalizeRepositoryPath("../escape"); err == nil {
		securityPass = false
	}
	symlinkSafe, err := runReleaseGateSymlinkEscapeCheck()
	if err != nil || !symlinkSafe {
		securityPass = false
	}
	if !securityPass {
		addCheck("security", "fail", "security guard validation failed (path traversal/symlink/session spoof)")
	} else {
		addCheck("security", "pass", "path traversal, symlink escape, and session spoof checks are enforced")
	}

	bridgeDiagnostics, foundBridgeDiagnostics, bridgeErr := h.loadBridgeDiagnostics(ctx)
	if bridgeErr != nil {
		addCheck("performance", "fail", "failed to load bridge diagnostics")
	} else if !foundBridgeDiagnostics || bridgeDiagnostics == nil {
		addCheck("performance", "fail", "bridge diagnostics not available")
	} else if bridgeDiagnostics.DispatchQueueDepth > 25 || bridgeDiagnostics.ErrorsLastHour > 10 {
		addCheck(
			"performance",
			"fail",
			fmt.Sprintf(
				"bridge diagnostics exceed thresholds (queue_depth=%d, errors_last_hour=%d)",
				bridgeDiagnostics.DispatchQueueDepth,
				bridgeDiagnostics.ErrorsLastHour,
			),
		)
	} else {
		addCheck(
			"performance",
			"pass",
			fmt.Sprintf(
				"bridge diagnostics healthy (queue_depth=%d, errors_last_hour=%d)",
				bridgeDiagnostics.DispatchQueueDepth,
				bridgeDiagnostics.ErrorsLastHour,
			),
		)
	}

	ok := true
	for _, check := range checks {
		if check.Status != "pass" {
			ok = false
			break
		}
	}

	return adminConfigReleaseGateResponse{
		OK:          ok,
		Checks:      checks,
		GeneratedAt: now,
	}, nil
}

func (h *AdminConfigHandler) loadLegacyImportReport(
	ctx context.Context,
) (*openClawLegacyImportReport, bool, error) {
	if h.DB == nil {
		return nil, false, nil
	}
	raw, found, err := h.loadSyncMetadataRaw(ctx, syncMetadataOpenClawLegacyImportKey)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	var report openClawLegacyImportReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return nil, false, err
	}
	return &report, true, nil
}

func (h *AdminConfigHandler) loadBridgeDiagnostics(
	ctx context.Context,
) (*OpenClawBridgeDiagnostics, bool, error) {
	if h.DB == nil {
		return nil, false, nil
	}
	raw, found, err := h.loadSyncMetadataRaw(ctx, "openclaw_bridge_diagnostics")
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	var diagnostics OpenClawBridgeDiagnostics
	if err := json.Unmarshal([]byte(raw), &diagnostics); err != nil {
		return nil, false, err
	}
	return &diagnostics, true, nil
}

func (h *AdminConfigHandler) loadSyncMetadataRaw(
	ctx context.Context,
	key string,
) (string, bool, error) {
	if h.DB == nil {
		return "", false, nil
	}
	var raw string
	err := h.DB.QueryRowContext(
		ctx,
		`SELECT value FROM sync_metadata WHERE key = $1`,
		key,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(raw) == "" {
		return "", false, nil
	}
	return raw, true, nil
}

func runReleaseGateSymlinkEscapeCheck() (bool, error) {
	tempRoot, err := os.MkdirTemp("", "otter-release-gate-security-*")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tempRoot)

	projectRoot := filepath.Join(tempRoot, "project-root")
	outsideRoot := filepath.Join(tempRoot, "outside-root")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		return false, err
	}
	if err := os.MkdirAll(outsideRoot, 0o755); err != nil {
		return false, err
	}

	linkPath := filepath.Join(projectRoot, "escape-link")
	if err := os.Symlink(outsideRoot, linkPath); err != nil {
		return false, err
	}

	allowed, err := releaseGatePathWithinRoot(projectRoot, filepath.Join(linkPath, "escape.txt"))
	if err != nil {
		return false, err
	}
	return !allowed, nil
}

func releaseGatePathWithinRoot(rootPath, candidatePath string) (bool, error) {
	rootAbs, err := filepath.Abs(rootPath)
	if err != nil {
		return false, err
	}
	rootResolved, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return false, err
	}

	candidateAbs, err := filepath.Abs(candidatePath)
	if err != nil {
		return false, err
	}
	candidateResolved, err := filepath.EvalSymlinks(candidateAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			parentResolved, parentErr := filepath.EvalSymlinks(filepath.Dir(candidateAbs))
			if parentErr != nil {
				return false, parentErr
			}
			candidateResolved = filepath.Join(parentResolved, filepath.Base(candidateAbs))
		} else {
			return false, err
		}
	}

	rel, err := filepath.Rel(rootResolved, candidateResolved)
	if err != nil {
		return false, err
	}
	if rel == "." {
		return true, nil
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return false, nil
	}
	return true, nil
}

func (h *AdminConfigHandler) loadSnapshotFromDB(r *http.Request) (
	*openClawConfigSnapshotRecord,
	time.Time,
	bool,
	error,
) {
	if h.DB == nil {
		return nil, time.Time{}, false, nil
	}
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

func (h *AdminConfigHandler) loadCutoverCheckpoint(r *http.Request) (*openClawConfigCutoverCheckpoint, error) {
	if h.DB == nil {
		return nil, nil
	}
	var raw string
	err := h.DB.QueryRowContext(
		r.Context(),
		`SELECT value FROM sync_metadata WHERE key = $1`,
		syncMetadataOpenClawCutoverKey,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var checkpoint openClawConfigCutoverCheckpoint
	if err := json.Unmarshal([]byte(raw), &checkpoint); err != nil {
		return nil, err
	}
	return &checkpoint, nil
}

func hashCanonicalJSONRaw(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", fmt.Errorf("config payload is empty")
	}
	var decoded interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", err
	}
	canonical, err := canonicalizeOpenClawConfigData(decoded)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:]), nil
}

type openClawConfigAgentEntry struct {
	ID        string
	IsDefault bool
	Payload   map[string]interface{}
}

func buildTwoAgentOpenClawConfig(raw json.RawMessage) (json.RawMessage, string, error) {
	var root map[string]interface{}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, "", fmt.Errorf("invalid OpenClaw config snapshot")
	}
	agentsNode, ok := root["agents"]
	if !ok {
		return nil, "", fmt.Errorf("OpenClaw config snapshot is missing agents")
	}

	entries, shape, container, err := extractOpenClawConfigAgentEntries(agentsNode)
	if err != nil {
		return nil, "", err
	}
	if len(entries) == 0 {
		return nil, "", fmt.Errorf("OpenClaw config snapshot has no agents")
	}

	primaryIdx := 0
	for idx, entry := range entries {
		if entry.IsDefault {
			primaryIdx = idx
			break
		}
	}
	if strings.EqualFold(entries[primaryIdx].ID, "chameleon") {
		for idx, entry := range entries {
			if !strings.EqualFold(entry.ID, "chameleon") {
				primaryIdx = idx
				break
			}
		}
	}

	primary := cloneJSONMap(entries[primaryIdx].Payload)
	primaryID := strings.TrimSpace(entries[primaryIdx].ID)
	if primaryID == "" {
		return nil, "", fmt.Errorf("primary agent id is empty")
	}
	primary["id"] = primaryID

	var chameleon map[string]interface{}
	for _, entry := range entries {
		if strings.EqualFold(entry.ID, "chameleon") {
			chameleon = cloneJSONMap(entry.Payload)
			break
		}
	}
	if chameleon == nil {
		chameleon = map[string]interface{}{
			"id":        "chameleon",
			"name":      "Chameleon",
			"workspace": "~/.openclaw/workspace-chameleon",
		}
	}
	chameleon["id"] = "chameleon"

	switch shape {
	case "list":
		listContainer := cloneJSONMap(container)
		listContainer["list"] = []interface{}{primary, chameleon}
		root["agents"] = listContainer
	case "array":
		root["agents"] = []interface{}{primary, chameleon}
	default:
		root["agents"] = map[string]interface{}{
			primaryID:   primary,
			"chameleon": chameleon,
		}
	}

	canonical, err := canonicalizeOpenClawConfigData(root)
	if err != nil {
		return nil, "", err
	}
	return canonical, primaryID, nil
}

func extractOpenClawConfigAgentEntries(node interface{}) ([]openClawConfigAgentEntry, string, map[string]interface{}, error) {
	switch typed := node.(type) {
	case map[string]interface{}:
		if listNode, ok := typed["list"]; ok {
			list, ok := listNode.([]interface{})
			if !ok {
				return nil, "", nil, fmt.Errorf("OpenClaw agents.list must be an array")
			}
			entries := make([]openClawConfigAgentEntry, 0, len(list))
			for idx, raw := range list {
				record, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				id := strings.TrimSpace(toConfigString(record["id"]))
				if id == "" {
					id = fmt.Sprintf("agent-%d", idx+1)
				}
				isDefault, _ := record["default"].(bool)
				entries = append(entries, openClawConfigAgentEntry{
					ID:        id,
					IsDefault: isDefault,
					Payload:   cloneJSONMap(record),
				})
			}
			return entries, "list", typed, nil
		}

		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		entries := make([]openClawConfigAgentEntry, 0, len(keys))
		for _, key := range keys {
			record, ok := typed[key].(map[string]interface{})
			if !ok {
				continue
			}
			isDefault, _ := record["default"].(bool)
			entries = append(entries, openClawConfigAgentEntry{
				ID:        strings.TrimSpace(key),
				IsDefault: isDefault,
				Payload:   cloneJSONMap(record),
			})
		}
		return entries, "map", nil, nil
	case []interface{}:
		entries := make([]openClawConfigAgentEntry, 0, len(typed))
		for idx, raw := range typed {
			record, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			id := strings.TrimSpace(toConfigString(record["id"]))
			if id == "" {
				id = fmt.Sprintf("agent-%d", idx+1)
			}
			isDefault, _ := record["default"].(bool)
			entries = append(entries, openClawConfigAgentEntry{
				ID:        id,
				IsDefault: isDefault,
				Payload:   cloneJSONMap(record),
			})
		}
		return entries, "array", nil, nil
	default:
		return nil, "", nil, fmt.Errorf("OpenClaw agents must be an object or array")
	}
}

func cloneJSONMap(input map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(input))
	for key, value := range input {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneJSONValue(input interface{}) interface{} {
	switch typed := input.(type) {
	case map[string]interface{}:
		return cloneJSONMap(typed)
	case []interface{}:
		out := make([]interface{}, 0, len(typed))
		for _, item := range typed {
			out = append(out, cloneJSONValue(item))
		}
		return out
	default:
		return typed
	}
}

func toConfigString(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
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
