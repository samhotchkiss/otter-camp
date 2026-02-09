package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

const (
	defaultEmissionBufferSize = 100
	maxEmissionBatchSize      = 100
	maxEmissionRecentLimit    = 200
	maxEmissionDetailLength   = 5000
)

var emissionIDSequence uint64
var emissionEventJSONMarshal = json.Marshal

type Emission struct {
	ID         string            `json:"id"`
	OrgID      string            `json:"org_id,omitempty"`
	SourceType string            `json:"source_type"`
	SourceID   string            `json:"source_id"`
	Scope      *EmissionScope    `json:"scope,omitempty"`
	Kind       string            `json:"kind"`
	Summary    string            `json:"summary"`
	Detail     *string           `json:"detail,omitempty"`
	Progress   *EmissionProgress `json:"progress,omitempty"`
	Timestamp  time.Time         `json:"timestamp"`
}

type EmissionScope struct {
	ProjectID   *string `json:"project_id,omitempty"`
	IssueID     *string `json:"issue_id,omitempty"`
	IssueNumber *int64  `json:"issue_number,omitempty"`
}

type EmissionProgress struct {
	Current int     `json:"current"`
	Total   int     `json:"total"`
	Unit    *string `json:"unit,omitempty"`
}

type EmissionFilter struct {
	OrgID     string
	ProjectID string
	IssueID   string
	SourceID  string
}

type EmissionBuffer struct {
	mu        sync.RWMutex
	emissions []Emission
	maxSize   int
	bySource  map[string]Emission
}

func NewEmissionBuffer(maxSize int) *EmissionBuffer {
	if maxSize <= 0 {
		maxSize = defaultEmissionBufferSize
	}
	return &EmissionBuffer{
		emissions: make([]Emission, 0, maxSize),
		maxSize:   maxSize,
		bySource:  make(map[string]Emission, maxSize),
	}
}

func (b *EmissionBuffer) Push(emission Emission) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.emissions = append(b.emissions, emission)
	if b.maxSize > 0 && len(b.emissions) > b.maxSize {
		excess := len(b.emissions) - b.maxSize
		b.emissions = append([]Emission(nil), b.emissions[excess:]...)
	}
	if sourceID := strings.TrimSpace(emission.SourceID); sourceID != "" {
		b.bySource[sourceKey(emission.OrgID, sourceID)] = emission
	}
}

func (b *EmissionBuffer) Recent(limit int, filter EmissionFilter) []Emission {
	if limit <= 0 {
		limit = 25
	}

	projectID := strings.TrimSpace(filter.ProjectID)
	issueID := strings.TrimSpace(filter.IssueID)
	sourceID := strings.TrimSpace(filter.SourceID)
	orgID := strings.TrimSpace(filter.OrgID)

	b.mu.RLock()
	defer b.mu.RUnlock()

	out := make([]Emission, 0, min(limit, len(b.emissions)))
	for i := len(b.emissions) - 1; i >= 0 && len(out) < limit; i-- {
		emission := b.emissions[i]
		if orgID != "" && strings.TrimSpace(emission.OrgID) != orgID {
			continue
		}
		if sourceID != "" && strings.TrimSpace(emission.SourceID) != sourceID {
			continue
		}
		if projectID != "" {
			if emission.Scope == nil || emission.Scope.ProjectID == nil || strings.TrimSpace(*emission.Scope.ProjectID) != projectID {
				continue
			}
		}
		if issueID != "" {
			if emission.Scope == nil || emission.Scope.IssueID == nil || strings.TrimSpace(*emission.Scope.IssueID) != issueID {
				continue
			}
		}
		out = append(out, emission)
	}
	return out
}

func (b *EmissionBuffer) LatestBySource(orgID string, sourceID string) *Emission {
	orgID = strings.TrimSpace(orgID)
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	latest, ok := b.bySource[sourceKey(orgID, sourceID)]
	if !ok {
		return nil
	}
	copy := latest
	return &copy
}

func sourceKey(orgID string, sourceID string) string {
	return strings.TrimSpace(orgID) + ":" + strings.TrimSpace(sourceID)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type EmissionsHandler struct {
	Buffer *EmissionBuffer
	Hub    emissionBroadcaster
}

type emissionBroadcaster interface {
	Broadcast(orgID string, payload []byte)
	BroadcastTopic(orgID string, topic string, payload []byte)
}

type emissionIngestRequest struct {
	Emissions []Emission `json:"emissions"`
}

type emissionListResponse struct {
	Items []Emission `json:"items"`
	Total int        `json:"total"`
}

func (h *EmissionsHandler) Recent(w http.ResponseWriter, r *http.Request) {
	if h.Buffer == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "emission buffer not available"})
		return
	}
	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	limit := 25
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be a positive integer"})
			return
		}
		limit = parsed
	}
	if limit > maxEmissionRecentLimit {
		limit = maxEmissionRecentLimit
	}

	records := h.Buffer.Recent(limit, EmissionFilter{
		OrgID:     workspaceID,
		ProjectID: r.URL.Query().Get("project_id"),
		IssueID:   r.URL.Query().Get("issue_id"),
		SourceID:  r.URL.Query().Get("source_id"),
	})
	sendJSON(w, http.StatusOK, emissionListResponse{Items: records, Total: len(records)})
}

func (h *EmissionsHandler) Ingest(w http.ResponseWriter, r *http.Request) {
	if h.Buffer == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "emission buffer not available"})
		return
	}
	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	var req emissionIngestRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}
	if len(req.Emissions) == 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "emissions are required"})
		return
	}
	if len(req.Emissions) > maxEmissionBatchSize {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "too many emissions"})
		return
	}

	normalized := make([]Emission, 0, len(req.Emissions))
	for _, candidate := range req.Emissions {
		record, err := normalizeEmission(candidate)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		normalized = append(normalized, record)
	}

	for _, emission := range normalized {
		emission.OrgID = workspaceID
		h.Buffer.Push(emission)
		h.broadcastEmission(workspaceID, emission)
	}

	sendJSON(w, http.StatusAccepted, map[string]any{
		"ok":          true,
		"org_id":      workspaceID,
		"ingested":    len(normalized),
		"ingested_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func normalizeEmission(input Emission) (Emission, error) {
	input.ID = strings.TrimSpace(input.ID)
	if input.ID == "" {
		input.ID = newEmissionID()
	}

	input.SourceType = strings.TrimSpace(strings.ToLower(input.SourceType))
	switch input.SourceType {
	case "agent", "bridge", "codex", "cron", "system":
	default:
		return Emission{}, fmt.Errorf("invalid source_type")
	}

	input.SourceID = strings.TrimSpace(input.SourceID)
	if input.SourceID == "" {
		return Emission{}, fmt.Errorf("source_id is required")
	}

	input.Kind = strings.TrimSpace(strings.ToLower(input.Kind))
	switch input.Kind {
	case "status", "progress", "log", "milestone", "error":
	default:
		return Emission{}, fmt.Errorf("invalid kind")
	}

	input.Summary = strings.TrimSpace(input.Summary)
	if input.Summary == "" {
		return Emission{}, fmt.Errorf("summary is required")
	}
	if len(input.Summary) > 200 {
		return Emission{}, fmt.Errorf("summary exceeds 200 characters")
	}

	if input.Scope != nil {
		if input.Scope.ProjectID != nil {
			projectID := strings.TrimSpace(*input.Scope.ProjectID)
			if projectID == "" {
				input.Scope.ProjectID = nil
			} else {
				input.Scope.ProjectID = &projectID
			}
		}
		if input.Scope.IssueID != nil {
			issueID := strings.TrimSpace(*input.Scope.IssueID)
			if issueID == "" {
				input.Scope.IssueID = nil
			} else {
				input.Scope.IssueID = &issueID
			}
		}
		if input.Scope.ProjectID == nil && input.Scope.IssueID == nil && input.Scope.IssueNumber == nil {
			input.Scope = nil
		}
	}

	if input.Detail != nil {
		detail := strings.TrimSpace(*input.Detail)
		if detail == "" {
			input.Detail = nil
		} else {
			if len(detail) > maxEmissionDetailLength {
				return Emission{}, fmt.Errorf("detail exceeds %d characters", maxEmissionDetailLength)
			}
			input.Detail = &detail
		}
	}

	if input.Progress != nil {
		if input.Progress.Total <= 0 || input.Progress.Current < 0 || input.Progress.Current > input.Progress.Total {
			return Emission{}, fmt.Errorf("invalid progress")
		}
		if input.Progress.Unit != nil {
			unit := strings.TrimSpace(*input.Progress.Unit)
			if unit == "" {
				input.Progress.Unit = nil
			} else {
				input.Progress.Unit = &unit
			}
		}
	}

	if input.Timestamp.IsZero() {
		input.Timestamp = time.Now().UTC()
	} else {
		input.Timestamp = input.Timestamp.UTC()
	}

	return input, nil
}

func newEmissionID() string {
	seq := atomic.AddUint64(&emissionIDSequence, 1)
	return fmt.Sprintf("em_%d_%x", time.Now().UTC().UnixNano(), seq)
}

type emissionReceivedEvent struct {
	Type     ws.MessageType `json:"type"`
	Emission Emission       `json:"emission"`
}

func (h *EmissionsHandler) broadcastEmission(orgID string, emission Emission) {
	broadcastEmissionEvent(h.Hub, orgID, emission)
}

func broadcastEmissionEvent(hub emissionBroadcaster, orgID string, emission Emission) {
	if hub == nil {
		return
	}
	payload, err := emissionEventJSONMarshal(emissionReceivedEvent{
		Type:     ws.MessageEmissionReceived,
		Emission: emission,
	})
	if err != nil {
		log.Printf(
			"warning: failed to marshal emission broadcast payload: org_id=%s source_type=%s source_id=%s err=%v",
			orgID,
			emission.SourceType,
			emission.SourceID,
			err,
		)
		return
	}
	hub.Broadcast(orgID, payload)

	if emission.Scope == nil {
		return
	}
	if emission.Scope.ProjectID != nil && strings.TrimSpace(*emission.Scope.ProjectID) != "" {
		hub.BroadcastTopic(orgID, "project:"+strings.TrimSpace(*emission.Scope.ProjectID), payload)
	}
	if emission.Scope.IssueID != nil && strings.TrimSpace(*emission.Scope.IssueID) != "" {
		hub.BroadcastTopic(orgID, "issue:"+strings.TrimSpace(*emission.Scope.IssueID), payload)
	}
}
