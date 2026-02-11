package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

const (
	maxActivityEventsBodySize    = 2 << 20 // 2MB
	maxActivityEventsBatch       = 500
	activityListDefaultLimit     = 50
	activityListMaxLimit         = 200
	elephantIngestMaxDeadLetters = 500
)

var activityStatusValues = map[string]struct{}{
	"started":   {},
	"completed": {},
	"failed":    {},
	"timeout":   {},
}

var completionPushStatusValues = map[string]struct{}{
	"succeeded": {},
	"failed":    {},
	"unknown":   {},
}

var activityUUIDRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var activityCommitSHARegex = regexp.MustCompile(`^[a-fA-F0-9]{7,64}$`)

type AgentActivityHandler struct {
	DB    *sql.DB
	Store *store.AgentActivityEventStore
	Hub   *ws.Hub

	storeOnce sync.Once
	storeErr  error
}

type ingestAgentActivityEventsRequest struct {
	OrgID  string                      `json:"org_id"`
	Events []ingestAgentActivityRecord `json:"events"`
}

type ingestAgentActivityScope struct {
	ProjectID   string `json:"project_id,omitempty"`
	IssueID     string `json:"issue_id,omitempty"`
	IssueNumber *int   `json:"issue_number,omitempty"`
	ThreadID    string `json:"thread_id,omitempty"`
}

type ingestAgentActivityRecord struct {
	ID           string                    `json:"id"`
	AgentID      string                    `json:"agent_id"`
	SessionKey   string                    `json:"session_key,omitempty"`
	Trigger      string                    `json:"trigger"`
	Channel      string                    `json:"channel,omitempty"`
	Summary      string                    `json:"summary"`
	Detail       string                    `json:"detail,omitempty"`
	Scope        *ingestAgentActivityScope `json:"scope,omitempty"`
	TokensUsed   int                       `json:"tokens_used"`
	ModelUsed    string                    `json:"model_used,omitempty"`
	CommitSHA    string                    `json:"commit_sha,omitempty"`
	CommitBranch string                    `json:"commit_branch,omitempty"`
	CommitRemote string                    `json:"commit_remote,omitempty"`
	PushStatus   string                    `json:"push_status,omitempty"`
	DurationMs   int64                     `json:"duration_ms"`
	Status       string                    `json:"status"`
	StartedAt    time.Time                 `json:"started_at"`
	CompletedAt  *time.Time                `json:"completed_at,omitempty"`
}

type ingestAgentActivityEventsResponse struct {
	OK       bool      `json:"ok"`
	Inserted int       `json:"inserted"`
	At       time.Time `json:"at"`
}

type listAgentActivityResponse struct {
	Items      []store.AgentActivityEvent `json:"items"`
	Total      int                        `json:"total"`
	NextBefore string                     `json:"next_before,omitempty"`
}

type elephantCompletionMemoryDraft struct {
	EventID      string `json:"event_id"`
	Summary      string `json:"summary"`
	Detail       string `json:"detail,omitempty"`
	IssueID      string `json:"issue_id,omitempty"`
	IssueNumber  int    `json:"issue_number,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	CommitBranch string `json:"commit_branch,omitempty"`
	CommitRemote string `json:"commit_remote,omitempty"`
	PushStatus   string `json:"push_status,omitempty"`
	SessionKey   string `json:"session_key,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
}

type elephantIngestDeadLetter struct {
	Draft         elephantCompletionMemoryDraft `json:"draft"`
	Attempts      int                           `json:"attempts"`
	LastError     string                        `json:"last_error,omitempty"`
	LastAttemptAt string                        `json:"last_attempt_at,omitempty"`
	NextAttemptAt string                        `json:"next_attempt_at,omitempty"`
}

type elephantIngestStats struct {
	LastRunAt          string `json:"last_run_at,omitempty"`
	LastRunProcessed   int    `json:"last_run_processed"`
	LastRunInserted    int    `json:"last_run_inserted"`
	LastRunDuplicates  int    `json:"last_run_duplicates"`
	LastRunFailed      int    `json:"last_run_failed"`
	TotalProcessed     int    `json:"total_processed"`
	TotalInserted      int    `json:"total_inserted"`
	TotalDuplicates    int    `json:"total_duplicates"`
	TotalFailed        int    `json:"total_failed"`
	DeadLetterCount    int    `json:"dead_letter_count"`
	LastFailureMessage string `json:"last_failure_message,omitempty"`
}

func (h *AgentActivityHandler) IngestEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	if status, err := requireOpenClawSyncAuth(r); err != nil {
		sendJSON(w, status, errorResponse{Error: err.Error()})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxActivityEventsBodySize+1))
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "failed to read body"})
		return
	}
	defer r.Body.Close()
	if len(body) > maxActivityEventsBodySize {
		sendJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "payload too large"})
		return
	}

	var req ingestAgentActivityEventsRequest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	req.OrgID = strings.TrimSpace(req.OrgID)
	if req.OrgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
		return
	}
	if !activityUUIDRegex.MatchString(req.OrgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id must be a UUID"})
		return
	}
	if len(req.Events) == 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "events are required"})
		return
	}
	if len(req.Events) > maxActivityEventsBatch {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "too many events"})
		return
	}

	createInputs := make([]store.CreateAgentActivityEventInput, 0, len(req.Events))
	for idx, event := range req.Events {
		input, err := normalizeActivityIngestRecord(event)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("event %d: %s", idx, err.Error())})
			return
		}
		createInputs = append(createInputs, input)
	}

	activityStore, err := h.resolveStore()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database unavailable"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, req.OrgID)
	if err := activityStore.CreateEvents(ctx, createInputs); err != nil {
		if isActivityWorkspaceScopeError(err) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to persist activity events"})
		return
	}
	if err := h.persistCompletionMetadataActivities(ctx, req.OrgID, createInputs); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to persist completion metadata"})
		return
	}
	if err := h.persistElephantCompletionMemories(ctx, req.OrgID, createInputs); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to persist elephant memory ingestion"})
		return
	}

	h.broadcastRealtimeActivityEvents(req.OrgID, createInputs, time.Now().UTC())

	sendJSON(w, http.StatusOK, ingestAgentActivityEventsResponse{
		OK:       true,
		Inserted: len(createInputs),
		At:       time.Now().UTC(),
	})
}

type activityEventBroadcastEnvelope struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

func (h *AgentActivityHandler) broadcastRealtimeActivityEvents(orgID string, events []store.CreateAgentActivityEventInput, createdAt time.Time) {
	if h == nil || h.Hub == nil {
		return
	}

	for _, event := range events {
		payload := map[string]interface{}{
			"id":            event.ID,
			"org_id":        orgID,
			"agent_id":      event.AgentID,
			"session_key":   event.SessionKey,
			"trigger":       event.Trigger,
			"channel":       event.Channel,
			"summary":       event.Summary,
			"detail":        event.Detail,
			"project_id":    event.ProjectID,
			"issue_id":      event.IssueID,
			"issue_number":  event.IssueNumber,
			"thread_id":     event.ThreadID,
			"tokens_used":   event.TokensUsed,
			"model_used":    event.ModelUsed,
			"commit_sha":    event.CommitSHA,
			"commit_branch": event.CommitBranch,
			"commit_remote": event.CommitRemote,
			"push_status":   event.PushStatus,
			"duration_ms":   event.DurationMs,
			"status":        event.Status,
			"started_at":    event.StartedAt.UTC(),
			"created_at":    createdAt,
		}
		if event.CompletedAt != nil {
			payload["completed_at"] = event.CompletedAt.UTC()
		}

		wire := activityEventBroadcastEnvelope{
			Type: "ActivityEventReceived",
			Data: map[string]interface{}{
				"event": payload,
			},
		}
		data, err := json.Marshal(wire)
		if err != nil {
			continue
		}
		h.Hub.Broadcast(orgID, data)
	}
}

func (h *AgentActivityHandler) persistCompletionMetadataActivities(
	ctx context.Context,
	orgID string,
	events []store.CreateAgentActivityEventInput,
) error {
	if h == nil || h.DB == nil {
		return nil
	}
	conn, err := store.WithWorkspace(ctx, h.DB)
	if err != nil {
		return err
	}
	defer conn.Close()

	query := `
		WITH updated AS (
			UPDATE activity_log
			SET metadata = $2::jsonb,
			    created_at = NOW()
			WHERE org_id = $1
			  AND action = 'git.push'
			  AND metadata->>'completion_event_id' = $3
			RETURNING id
		)
		INSERT INTO activity_log (org_id, action, metadata)
		SELECT $1, 'git.push', $2::jsonb
		WHERE NOT EXISTS (SELECT 1 FROM updated)
	`

	for _, event := range events {
		if strings.TrimSpace(event.CommitSHA) == "" {
			continue
		}
		pushStatus := strings.TrimSpace(event.PushStatus)
		if pushStatus == "" {
			pushStatus = "unknown"
		}
		metadata := map[string]any{
			"completion_event_id": event.ID,
			"source":              "agent_activity_completion",
			"commit_sha":          strings.TrimSpace(event.CommitSHA),
			"branch":              strings.TrimSpace(event.CommitBranch),
			"remote":              strings.TrimSpace(event.CommitRemote),
			"push_status":         pushStatus,
			"summary":             strings.TrimSpace(event.Summary),
			"session_key":         strings.TrimSpace(event.SessionKey),
		}
		if strings.TrimSpace(event.ProjectID) != "" {
			metadata["project_id"] = strings.TrimSpace(event.ProjectID)
		}
		if strings.TrimSpace(event.IssueID) != "" {
			metadata["issue_id"] = strings.TrimSpace(event.IssueID)
		}
		if event.IssueNumber > 0 {
			metadata["issue_number"] = event.IssueNumber
		}

		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, query, orgID, metadataJSON, event.ID); err != nil {
			return err
		}
	}
	return nil
}

func (h *AgentActivityHandler) persistElephantCompletionMemories(
	ctx context.Context,
	orgID string,
	events []store.CreateAgentActivityEventInput,
) error {
	if h == nil || h.DB == nil {
		return nil
	}
	workspaceID := strings.TrimSpace(orgID)
	if workspaceID == "" {
		return nil
	}

	conn, err := store.WithWorkspace(ctx, h.DB)
	if err != nil {
		return err
	}
	defer conn.Close()

	elephantAgentID, err := resolveElephantAgentIDForWorkspace(ctx, conn, workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	now := time.Now().UTC()
	deadLetterKey := elephantDeadLettersSyncMetadataKey(workspaceID)
	statsKey := elephantStatsSyncMetadataKey(workspaceID)

	deadLetters, err := loadElephantIngestDeadLetters(ctx, conn, deadLetterKey)
	if err != nil {
		return err
	}
	stats, err := loadElephantIngestStats(ctx, conn, statsKey)
	if err != nil {
		return err
	}
	if stats == nil {
		stats = &elephantIngestStats{}
	}
	stats.LastRunProcessed = 0
	stats.LastRunInserted = 0
	stats.LastRunDuplicates = 0
	stats.LastRunFailed = 0

	pendingByEventID := make(map[string]elephantIngestDeadLetter, len(deadLetters))
	for _, deadLetter := range deadLetters {
		eventID := strings.TrimSpace(deadLetter.Draft.EventID)
		if eventID == "" {
			continue
		}
		pendingByEventID[eventID] = deadLetter
	}
	for _, draft := range buildElephantCompletionMemoryDrafts(events) {
		pendingByEventID[draft.EventID] = elephantIngestDeadLetter{Draft: draft}
	}

	lastFailureMessage := ""
	for eventID, deadLetter := range pendingByEventID {
		nextAttemptAt := parseOptionalRFC3339FromValue(deadLetter.NextAttemptAt)
		if !nextAttemptAt.IsZero() && nextAttemptAt.After(now) {
			continue
		}
		stats.LastRunProcessed++
		stats.TotalProcessed++

		duplicate, ingestErr := h.ingestElephantCompletionDraft(ctx, elephantAgentID, deadLetter.Draft)
		if ingestErr == nil {
			if duplicate {
				stats.LastRunDuplicates++
				stats.TotalDuplicates++
			} else {
				stats.LastRunInserted++
				stats.TotalInserted++
			}
			delete(pendingByEventID, eventID)
			continue
		}

		deadLetter.Attempts++
		deadLetter.LastError = strings.TrimSpace(ingestErr.Error())
		deadLetter.LastAttemptAt = now.Format(time.RFC3339)
		deadLetter.NextAttemptAt = now.Add(elephantIngestRetryBackoff(deadLetter.Attempts)).Format(time.RFC3339)
		pendingByEventID[eventID] = deadLetter

		stats.LastRunFailed++
		stats.TotalFailed++
		lastFailureMessage = deadLetter.LastError
	}

	stats.LastRunAt = now.Format(time.RFC3339)
	stats.DeadLetterCount = len(pendingByEventID)
	stats.LastFailureMessage = lastFailureMessage

	remainingDeadLetters := trimElephantIngestDeadLetters(pendingByEventID, elephantIngestMaxDeadLetters)
	if err := upsertSyncMetadataValue(ctx, conn, deadLetterKey, remainingDeadLetters, now); err != nil {
		return err
	}
	if err := upsertSyncMetadataValue(ctx, conn, statsKey, stats, now); err != nil {
		return err
	}

	return nil
}

func (h *AgentActivityHandler) ingestElephantCompletionDraft(
	ctx context.Context,
	elephantAgentID string,
	draft elephantCompletionMemoryDraft,
) (bool, error) {
	if h == nil || h.DB == nil {
		return false, nil
	}

	kind := store.MemoryKindFact
	if strings.EqualFold(strings.TrimSpace(draft.PushStatus), "failed") {
		kind = store.MemoryKindLesson
	}

	title := elephantCompletionTitle(draft, kind)
	content := elephantCompletionContent(draft)
	if strings.TrimSpace(content) == "" {
		content = title
	}

	metadata := map[string]any{
		"ingestion_source": "agent_activity_completion",
		"completion_event": draft.EventID,
		"commit_sha":       strings.TrimSpace(draft.CommitSHA),
		"push_status":      strings.TrimSpace(strings.ToLower(draft.PushStatus)),
		"issue_number":     draft.IssueNumber,
		"issue_id":         strings.TrimSpace(draft.IssueID),
		"project_id":       strings.TrimSpace(draft.ProjectID),
		"session_key":      strings.TrimSpace(draft.SessionKey),
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return false, err
	}

	occurredAt := parseOptionalRFC3339FromValue(draft.StartedAt)
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	sourceIssue := strings.TrimSpace(draft.IssueID)
	if sourceIssue == "" && draft.IssueNumber > 0 {
		sourceIssue = strconv.Itoa(draft.IssueNumber)
	}
	sourceProject := strings.TrimSpace(draft.ProjectID)
	sourceSession := strings.TrimSpace(draft.SessionKey)

	memoryStore := store.NewMemoryStore(h.DB)
	entry, createErr := memoryStore.Create(ctx, store.CreateMemoryEntryInput{
		AgentID:       strings.TrimSpace(elephantAgentID),
		Kind:          kind,
		Title:         title,
		Content:       content,
		Metadata:      metadataJSON,
		Importance:    3,
		Confidence:    0.8,
		Sensitivity:   store.MemorySensitivityInternal,
		OccurredAt:    occurredAt,
		SourceSession: optionalStringPtr(sourceSession),
		SourceProject: optionalStringPtr(sourceProject),
		SourceIssue:   optionalStringPtr(sourceIssue),
	})
	if createErr != nil {
		if errors.Is(createErr, store.ErrDuplicateMemory) {
			return true, nil
		}
		return false, createErr
	}

	eventsStore := store.NewMemoryEventsStore(h.DB)
	eventPayload, err := json.Marshal(map[string]any{
		"memory_id":         entry.ID,
		"agent_id":          elephantAgentID,
		"source_event_id":   draft.EventID,
		"source_session":    sourceSession,
		"source_project_id": sourceProject,
		"source_issue":      sourceIssue,
		"kind":              kind,
		"title":             title,
	})
	if err == nil {
		_, _ = eventsStore.Publish(ctx, store.PublishMemoryEventInput{
			EventType: store.MemoryEventTypeMemoryCreated,
			Payload:   eventPayload,
		})
	}

	return false, nil
}

func buildElephantCompletionMemoryDrafts(events []store.CreateAgentActivityEventInput) []elephantCompletionMemoryDraft {
	drafts := make([]elephantCompletionMemoryDraft, 0, len(events))
	for _, event := range events {
		trigger := strings.TrimSpace(strings.ToLower(event.Trigger))
		commitSHA := strings.TrimSpace(event.CommitSHA)
		if trigger != "task.completion" && commitSHA == "" {
			continue
		}
		eventID := strings.TrimSpace(event.ID)
		if eventID == "" {
			continue
		}
		startedAt := ""
		if !event.StartedAt.IsZero() {
			startedAt = event.StartedAt.UTC().Format(time.RFC3339)
		}
		drafts = append(drafts, elephantCompletionMemoryDraft{
			EventID:      eventID,
			Summary:      strings.TrimSpace(event.Summary),
			Detail:       strings.TrimSpace(event.Detail),
			IssueID:      strings.TrimSpace(event.IssueID),
			IssueNumber:  event.IssueNumber,
			ProjectID:    strings.TrimSpace(event.ProjectID),
			CommitSHA:    commitSHA,
			CommitBranch: strings.TrimSpace(event.CommitBranch),
			CommitRemote: strings.TrimSpace(event.CommitRemote),
			PushStatus:   strings.TrimSpace(strings.ToLower(event.PushStatus)),
			SessionKey:   strings.TrimSpace(event.SessionKey),
			StartedAt:    startedAt,
		})
	}
	return drafts
}

func resolveElephantAgentIDForWorkspace(ctx context.Context, conn *sql.Conn, orgID string) (string, error) {
	var agentID string
	err := conn.QueryRowContext(
		ctx,
		`SELECT id
		   FROM agents
		  WHERE org_id = $1
		    AND slug = 'elephant'
		    AND status != 'retired'
		  ORDER BY created_at ASC
		  LIMIT 1`,
		orgID,
	).Scan(&agentID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(agentID), nil
}

func elephantCompletionTitle(draft elephantCompletionMemoryDraft, kind string) string {
	issueLabel := "project completion"
	if draft.IssueNumber > 0 {
		issueLabel = fmt.Sprintf("issue #%d completion", draft.IssueNumber)
	} else if strings.TrimSpace(draft.IssueID) != "" {
		issueLabel = "issue completion"
	}
	if kind == store.MemoryKindLesson {
		return fmt.Sprintf("Push failure captured for %s", issueLabel)
	}
	return fmt.Sprintf("Completion captured for %s", issueLabel)
}

func elephantCompletionContent(draft elephantCompletionMemoryDraft) string {
	segments := make([]string, 0, 6)
	summary := strings.TrimSpace(draft.Summary)
	if summary != "" {
		segments = append(segments, summary)
	}
	detail := strings.TrimSpace(draft.Detail)
	if detail != "" && !strings.EqualFold(detail, summary) {
		segments = append(segments, detail)
	}
	if sha := strings.TrimSpace(draft.CommitSHA); sha != "" {
		segments = append(segments, fmt.Sprintf("commit %s", sha))
	}
	if status := strings.TrimSpace(strings.ToLower(draft.PushStatus)); status != "" {
		segments = append(segments, fmt.Sprintf("push %s", status))
	}
	if branch := strings.TrimSpace(draft.CommitBranch); branch != "" {
		segments = append(segments, fmt.Sprintf("branch %s", branch))
	}
	if remote := strings.TrimSpace(draft.CommitRemote); remote != "" {
		segments = append(segments, fmt.Sprintf("remote %s", remote))
	}
	return strings.Join(segments, " | ")
}

func loadElephantIngestDeadLetters(
	ctx context.Context,
	conn *sql.Conn,
	key string,
) ([]elephantIngestDeadLetter, error) {
	var raw string
	err := conn.QueryRowContext(ctx, `SELECT value FROM sync_metadata WHERE key = $1`, key).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	out := make([]elephantIngestDeadLetter, 0)
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, nil
	}
	return out, nil
}

func loadElephantIngestStats(
	ctx context.Context,
	conn *sql.Conn,
	key string,
) (*elephantIngestStats, error) {
	var raw string
	err := conn.QueryRowContext(ctx, `SELECT value FROM sync_metadata WHERE key = $1`, key).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	stats := &elephantIngestStats{}
	if err := json.Unmarshal([]byte(trimmed), stats); err != nil {
		return nil, nil
	}
	return stats, nil
}

func upsertSyncMetadataValue(
	ctx context.Context,
	conn *sql.Conn,
	key string,
	value any,
	now time.Time,
) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(
		ctx,
		`INSERT INTO sync_metadata (key, value, updated_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (key) DO UPDATE
		 SET value = EXCLUDED.value,
		     updated_at = EXCLUDED.updated_at`,
		key,
		string(payload),
		now,
	)
	return err
}

func trimElephantIngestDeadLetters(
	byEventID map[string]elephantIngestDeadLetter,
	limit int,
) []elephantIngestDeadLetter {
	if limit <= 0 {
		limit = elephantIngestMaxDeadLetters
	}
	items := make([]elephantIngestDeadLetter, 0, len(byEventID))
	for _, value := range byEventID {
		eventID := strings.TrimSpace(value.Draft.EventID)
		if eventID == "" {
			continue
		}
		items = append(items, value)
	}
	sort.SliceStable(items, func(i, j int) bool {
		leftNext := parseOptionalRFC3339FromValue(items[i].NextAttemptAt)
		rightNext := parseOptionalRFC3339FromValue(items[j].NextAttemptAt)
		if !leftNext.Equal(rightNext) {
			return leftNext.After(rightNext)
		}
		if items[i].Attempts != items[j].Attempts {
			return items[i].Attempts > items[j].Attempts
		}
		return items[i].Draft.EventID < items[j].Draft.EventID
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func elephantIngestRetryBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return time.Minute
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<(attempt-1)) * time.Minute
}

func parseOptionalRFC3339FromValue(raw string) time.Time {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func elephantDeadLettersSyncMetadataKey(orgID string) string {
	return "elephant_memory_ingest_dead_letters:" + strings.TrimSpace(orgID)
}

func elephantStatsSyncMetadataKey(orgID string) string {
	return "elephant_memory_ingest_stats:" + strings.TrimSpace(orgID)
}

func normalizeActivityIngestRecord(event ingestAgentActivityRecord) (store.CreateAgentActivityEventInput, error) {
	id := strings.TrimSpace(event.ID)
	if id == "" {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("id is required")
	}
	agentID := strings.TrimSpace(event.AgentID)
	if agentID == "" {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("agent_id is required")
	}
	trigger := strings.TrimSpace(event.Trigger)
	if trigger == "" {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("trigger is required")
	}
	summary := strings.TrimSpace(event.Summary)
	if summary == "" {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("summary is required")
	}
	if event.StartedAt.IsZero() {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("started_at is required")
	}
	status := strings.TrimSpace(event.Status)
	if status == "" {
		status = "completed"
	}
	if _, ok := activityStatusValues[status]; !ok {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("status is invalid")
	}
	if event.TokensUsed < 0 {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("tokens_used must be >= 0")
	}
	if event.DurationMs < 0 {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("duration_ms must be >= 0")
	}
	commitSHA := strings.TrimSpace(event.CommitSHA)
	if commitSHA != "" {
		if !activityCommitSHARegex.MatchString(commitSHA) {
			return store.CreateAgentActivityEventInput{}, fmt.Errorf("commit_sha must be a hex git SHA")
		}
	}
	pushStatus := strings.ToLower(strings.TrimSpace(event.PushStatus))
	if commitSHA != "" && pushStatus == "" {
		pushStatus = "unknown"
	}
	if pushStatus != "" {
		if _, ok := completionPushStatusValues[pushStatus]; !ok {
			return store.CreateAgentActivityEventInput{}, fmt.Errorf("push_status is invalid")
		}
	}

	input := store.CreateAgentActivityEventInput{
		ID:           id,
		AgentID:      agentID,
		SessionKey:   strings.TrimSpace(event.SessionKey),
		Trigger:      trigger,
		Channel:      strings.TrimSpace(event.Channel),
		Summary:      summary,
		Detail:       strings.TrimSpace(event.Detail),
		TokensUsed:   event.TokensUsed,
		ModelUsed:    strings.TrimSpace(event.ModelUsed),
		CommitSHA:    commitSHA,
		CommitBranch: strings.TrimSpace(event.CommitBranch),
		CommitRemote: strings.TrimSpace(event.CommitRemote),
		PushStatus:   pushStatus,
		DurationMs:   event.DurationMs,
		Status:       status,
		StartedAt:    event.StartedAt.UTC(),
		CompletedAt:  event.CompletedAt,
	}

	if event.Scope == nil {
		return input, nil
	}

	projectID := strings.TrimSpace(event.Scope.ProjectID)
	if projectID != "" && !activityUUIDRegex.MatchString(projectID) {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("scope.project_id must be a UUID")
	}
	issueID := strings.TrimSpace(event.Scope.IssueID)
	if issueID != "" && !activityUUIDRegex.MatchString(issueID) {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("scope.issue_id must be a UUID")
	}
	if event.Scope.IssueNumber != nil && *event.Scope.IssueNumber < 0 {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("scope.issue_number must be >= 0")
	}
	input.ProjectID = projectID
	input.IssueID = issueID
	if event.Scope.IssueNumber != nil {
		input.IssueNumber = *event.Scope.IssueNumber
	}
	input.ThreadID = strings.TrimSpace(event.Scope.ThreadID)

	return input, nil
}

func (h *AgentActivityHandler) ListByAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
		return
	}
	agentID := strings.TrimSpace(chi.URLParam(r, "id"))
	if agentID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "agent id is required"})
		return
	}
	if !uuidRegex.MatchString(agentID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "agent id must be a UUID"})
		return
	}
	opts, err := parseAgentActivityListOptions(r.URL.Query())
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	activityStore, err := h.resolveStore()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database unavailable"})
		return
	}
	items, err := h.listTimelineEventsByAgent(r.Context(), activityStore, agentID, opts)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list agent activity"})
		return
	}
	resp := buildListAgentActivityResponse(items, opts.Limit)
	sendJSON(w, http.StatusOK, resp)
}

func (h *AgentActivityHandler) ListRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
		return
	}
	opts, err := parseAgentActivityListOptions(r.URL.Query())
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	opts.AgentID = strings.TrimSpace(r.URL.Query().Get("agent_id"))

	activityStore, err := h.resolveStore()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database unavailable"})
		return
	}
	items, err := activityStore.ListRecent(r.Context(), opts)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list recent activity"})
		return
	}
	resp := buildListAgentActivityResponse(items, opts.Limit)
	sendJSON(w, http.StatusOK, resp)
}

func parseAgentActivityListOptions(values url.Values) (store.ListAgentActivityOptions, error) {
	opts := store.ListAgentActivityOptions{
		Limit: activityListDefaultLimit,
	}

	limitRaw := strings.TrimSpace(values.Get("limit"))
	if limitRaw != "" {
		limit, err := strconv.Atoi(limitRaw)
		if err != nil || limit <= 0 {
			return opts, fmt.Errorf("limit must be a positive integer")
		}
		if limit > activityListMaxLimit {
			limit = activityListMaxLimit
		}
		opts.Limit = limit
	}

	beforeRaw := strings.TrimSpace(values.Get("before"))
	if beforeRaw != "" {
		before, err := time.Parse(time.RFC3339, beforeRaw)
		if err != nil {
			return opts, fmt.Errorf("before must be RFC3339")
		}
		opts.Before = &before
	}

	opts.Trigger = strings.TrimSpace(values.Get("trigger"))
	opts.Channel = strings.TrimSpace(values.Get("channel"))
	opts.Status = strings.TrimSpace(values.Get("status"))
	projectID := strings.TrimSpace(values.Get("project_id"))
	if projectID != "" && !activityUUIDRegex.MatchString(projectID) {
		return opts, fmt.Errorf("project_id must be a UUID")
	}
	opts.ProjectID = projectID
	return opts, nil
}

func (h *AgentActivityHandler) listTimelineEventsByAgent(
	ctx context.Context,
	activityStore *store.AgentActivityEventStore,
	agentID string,
	opts store.ListAgentActivityOptions,
) ([]store.AgentActivityEvent, error) {
	items, err := activityStore.ListByAgent(ctx, agentID, opts)
	if err != nil {
		return nil, err
	}
	if len(items) > 0 {
		return items, nil
	}

	candidateIDs := fallbackTimelineAgentIDs(agentID)
	if len(candidateIDs) == 0 {
		return items, nil
	}

	merged := make(map[string]store.AgentActivityEvent)
	for _, event := range items {
		merged[event.ID] = event
	}

	for _, candidateID := range candidateIDs {
		candidateItems, listErr := activityStore.ListByAgent(ctx, candidateID, opts)
		if listErr != nil {
			return nil, listErr
		}
		for _, event := range candidateItems {
			merged[event.ID] = event
		}
	}

	out := make([]store.AgentActivityEvent, 0, len(merged))
	for _, event := range merged {
		out = append(out, event)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	if len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

func fallbackTimelineAgentIDs(agentID string) []string {
	trimmed := strings.TrimSpace(agentID)
	if trimmed == "" {
		return nil
	}

	candidates := []string{}
	add := func(value string) {
		v := strings.TrimSpace(value)
		if v == "" {
			return
		}
		for _, existing := range candidates {
			if strings.EqualFold(existing, v) {
				return
			}
		}
		candidates = append(candidates, v)
	}

	add(strings.ToLower(trimmed))
	for id, name := range agentNames {
		switch {
		case strings.EqualFold(id, trimmed):
			add(name)
			add(strings.ToLower(name))
		case strings.EqualFold(name, trimmed):
			add(id)
		}
	}

	return candidates
}

func buildListAgentActivityResponse(items []store.AgentActivityEvent, limit int) listAgentActivityResponse {
	resp := listAgentActivityResponse{
		Items: items,
		Total: len(items),
	}
	if len(items) == 0 {
		return resp
	}
	if limit <= 0 {
		limit = activityListDefaultLimit
	}
	if len(items) >= limit {
		last := items[len(items)-1].StartedAt.UTC()
		if !last.IsZero() {
			resp.NextBefore = last.Format(time.RFC3339)
		}
	}
	return resp
}

func (h *AgentActivityHandler) resolveStore() (*store.AgentActivityEventStore, error) {
	h.storeOnce.Do(func() {
		if h.Store != nil {
			return
		}
		db := h.DB
		if db == nil {
			db, h.storeErr = store.DB()
			if h.storeErr != nil {
				return
			}
		}
		h.Store = store.NewAgentActivityEventStore(db)
	})
	if h.storeErr != nil {
		return nil, h.storeErr
	}
	if h.Store == nil {
		return nil, fmt.Errorf("activity store unavailable")
	}
	return h.Store, nil
}

func isActivityWorkspaceScopeError(err error) bool {
	return errors.Is(err, store.ErrNoWorkspace) || errors.Is(err, store.ErrInvalidWorkspace)
}
