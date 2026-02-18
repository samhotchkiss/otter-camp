package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	defaultEllieIngestionInterval           = 5 * time.Minute
	defaultEllieIngestionBatchSize          = 100
	defaultEllieIngestionMaxPerRoom         = 200
	defaultEllieIngestionBackfillMaxPerRoom = 250
	ellieIngestionWindowGap                 = 15 * time.Minute
)

var ellieOperationalContextPattern = regexp.MustCompile(`\b(api|build|code|config|database|deploy|feature|migration|pipeline|release|schema|test)\b`)

type EllieIngestionMode string

const (
	EllieIngestionModeNormal   EllieIngestionMode = "normal"
	EllieIngestionModeBackfill EllieIngestionMode = "backfill"
)

type EllieIngestionStore interface {
	ListRoomsForIngestion(ctx context.Context, limit int) ([]store.EllieRoomIngestionCandidate, error)
	GetRoomCursor(ctx context.Context, orgID, roomID string) (*store.EllieRoomCursor, error)
	ListRoomMessagesSince(ctx context.Context, orgID, roomID string, afterCreatedAt *time.Time, afterMessageID *string, limit int) ([]store.EllieIngestionMessage, error)
	InsertExtractedMemory(ctx context.Context, input store.CreateEllieExtractedMemoryInput) (bool, error)
	UpsertRoomCursor(ctx context.Context, input store.UpsertEllieRoomCursorInput) error
}

type EllieIngestionLLMExtractionInput struct {
	OrgID    string
	RoomID   string
	Messages []store.EllieIngestionMessage
}

type EllieIngestionLLMCandidate struct {
	Kind                 string
	Title                string
	Content              string
	Importance           int
	Confidence           float64
	SourceConversationID *string
	Metadata             map[string]any
}

type EllieIngestionLLMExtractionResult struct {
	Model      string
	TraceID    string
	Candidates []EllieIngestionLLMCandidate
}

type EllieIngestionLLMExtractor interface {
	Extract(ctx context.Context, input EllieIngestionLLMExtractionInput) (EllieIngestionLLMExtractionResult, error)
}

type EllieIngestionWorkerConfig struct {
	Interval           time.Duration
	BatchSize          int
	MaxPerRoom         int
	BackfillMaxPerRoom int
	BackfillWindowSize int
	BackfillWindowStride int
	WindowGap          time.Duration
	Mode               EllieIngestionMode
	LLMExtractor       EllieIngestionLLMExtractor
}

type EllieIngestionWorker struct {
	Store              EllieIngestionStore
	Interval           time.Duration
	BatchSize          int
	MaxPerRoom         int
	BackfillMaxPerRoom int
	BackfillWindowSize int
	BackfillWindowStride int
	WindowGap          time.Duration
	Mode               EllieIngestionMode
	LLMExtractor       EllieIngestionLLMExtractor
	Logf               func(format string, args ...any)
}

func NewEllieIngestionWorker(store EllieIngestionStore, cfg EllieIngestionWorkerConfig) *EllieIngestionWorker {
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultEllieIngestionInterval
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = defaultEllieIngestionBatchSize
	}
	maxPerRoom := cfg.MaxPerRoom
	if maxPerRoom <= 0 {
		maxPerRoom = defaultEllieIngestionMaxPerRoom
	}
	backfillMaxPerRoom := cfg.BackfillMaxPerRoom
	if backfillMaxPerRoom <= 0 {
		backfillMaxPerRoom = defaultEllieIngestionBackfillMaxPerRoom
	}
	windowGap := cfg.WindowGap
	if windowGap <= 0 {
		windowGap = ellieIngestionWindowGap
	}
	backfillWindowSize := cfg.BackfillWindowSize
	if backfillWindowSize <= 0 {
		backfillWindowSize = 0
	}
	backfillWindowStride := cfg.BackfillWindowStride
	if backfillWindowStride <= 0 {
		backfillWindowStride = backfillWindowSize
	}
	mode := normalizeEllieIngestionMode(cfg.Mode)
	return &EllieIngestionWorker{
		Store:              store,
		Interval:           interval,
		BatchSize:          batchSize,
		MaxPerRoom:         maxPerRoom,
		BackfillMaxPerRoom: backfillMaxPerRoom,
		BackfillWindowSize: backfillWindowSize,
		BackfillWindowStride: backfillWindowStride,
		WindowGap:          windowGap,
		Mode:               mode,
		LLMExtractor:       cfg.LLMExtractor,
		Logf:               log.Printf,
	}
}

func (w *EllieIngestionWorker) SetMode(mode EllieIngestionMode) {
	if w == nil {
		return
	}
	w.Mode = normalizeEllieIngestionMode(mode)
}

func (w *EllieIngestionWorker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		processed, err := w.RunOnce(ctx)
		if err != nil {
			if w.Logf != nil {
				w.Logf("ellie ingestion worker run failed: %v", err)
			}
			if err := sleepWithContext(ctx, w.Interval); err != nil {
				return
			}
			continue
		}
		if processed > 0 {
			continue
		}
		if err := sleepWithContext(ctx, w.Interval); err != nil {
			return
		}
	}
}

func (w *EllieIngestionWorker) RunOnce(ctx context.Context) (int, error) {
	if w == nil {
		return 0, fmt.Errorf("ellie ingestion worker is nil")
	}
	if w.Store == nil {
		return 0, fmt.Errorf("ellie ingestion store is required")
	}
	if w.BatchSize <= 0 {
		w.BatchSize = defaultEllieIngestionBatchSize
	}
	if w.MaxPerRoom <= 0 {
		w.MaxPerRoom = defaultEllieIngestionMaxPerRoom
	}
	if w.BackfillMaxPerRoom <= 0 {
		w.BackfillMaxPerRoom = defaultEllieIngestionBackfillMaxPerRoom
	}
	mode := normalizeEllieIngestionMode(w.Mode)
	maxPerRoom := w.MaxPerRoom
	if mode == EllieIngestionModeBackfill {
		maxPerRoom = w.BackfillMaxPerRoom
	}

	rooms, err := w.Store.ListRoomsForIngestion(ctx, w.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("list rooms for ellie ingestion: %w", err)
	}

	processed := 0
	for _, room := range rooms {
		cursor, err := w.Store.GetRoomCursor(ctx, room.OrgID, room.RoomID)
		if err != nil {
			return processed, fmt.Errorf("load ellie room cursor %s/%s: %w", room.OrgID, room.RoomID, err)
		}

		var (
			afterCreatedAt *time.Time
			afterMessageID *string
		)
		if cursor != nil && cursor.LastMessageID != "" && !cursor.LastMessageCreatedAt.IsZero() {
			afterCreatedAt = &cursor.LastMessageCreatedAt
			afterMessageID = &cursor.LastMessageID
		} else if mode == EllieIngestionModeBackfill {
			epoch := time.Unix(0, 0).UTC()
			afterCreatedAt = &epoch
		}

		messages, err := w.Store.ListRoomMessagesSince(ctx, room.OrgID, room.RoomID, afterCreatedAt, afterMessageID, maxPerRoom)
		if err != nil {
			return processed, fmt.Errorf("list room messages for ellie ingestion %s/%s: %w", room.OrgID, room.RoomID, err)
		}
		if len(messages) == 0 {
			continue
		}

		var windows [][]store.EllieIngestionMessage
		if mode == EllieIngestionModeBackfill && w.BackfillWindowSize > 0 {
			windows = groupEllieIngestionMessagesByCount(messages, w.BackfillWindowSize, w.BackfillWindowStride)
		} else {
			windows = groupEllieIngestionMessagesByWindow(messages, w.WindowGap)
		}
		for _, window := range windows {
			processed += len(window)
			if w.LLMExtractor != nil {
				llmCandidates, err := w.extractLLMMemoryCandidates(ctx, room, window)
				if err != nil {
					if w.Logf != nil {
						w.Logf("ellie ingestion llm extraction failed room=%s: %v (falling back to heuristics)", room.RoomID, err)
					}
				} else if len(llmCandidates) > 0 {
					for _, candidate := range llmCandidates {
						inserted, err := w.Store.InsertExtractedMemory(ctx, candidate)
						if err != nil {
							return processed, fmt.Errorf("insert llm extracted memory for room %s: %w", room.RoomID, err)
						}
						if inserted && w.Logf != nil {
							w.Logf("ellie ingestion extracted llm memory kind=%s room=%s", candidate.Kind, room.RoomID)
						}
					}
					continue
				}
			}

			candidate, ok := deriveEllieMemoryCandidateFromWindow(window)
			if !ok {
				continue
			}

			inserted, err := w.Store.InsertExtractedMemory(ctx, candidate)
			if err != nil {
				return processed, fmt.Errorf("insert ellie extracted memory for room %s: %w", room.RoomID, err)
			}
			if inserted && w.Logf != nil {
				w.Logf("ellie ingestion extracted memory kind=%s room=%s", candidate.Kind, room.RoomID)
			}
		}

		last := messages[len(messages)-1]
		if err := w.Store.UpsertRoomCursor(ctx, store.UpsertEllieRoomCursorInput{
			OrgID:                room.OrgID,
			RoomID:               room.RoomID,
			LastMessageID:        last.ID,
			LastMessageCreatedAt: last.CreatedAt,
		}); err != nil {
			return processed, fmt.Errorf("upsert ellie room cursor %s/%s: %w", room.OrgID, room.RoomID, err)
		}
	}

	return processed, nil
}

func (w *EllieIngestionWorker) extractLLMMemoryCandidates(
	ctx context.Context,
	room store.EllieRoomIngestionCandidate,
	window []store.EllieIngestionMessage,
) ([]store.CreateEllieExtractedMemoryInput, error) {
	if w == nil || w.LLMExtractor == nil || len(window) == 0 {
		return nil, nil
	}
	result, err := w.LLMExtractor.Extract(ctx, EllieIngestionLLMExtractionInput{
		OrgID:    room.OrgID,
		RoomID:   room.RoomID,
		Messages: window,
	})
	if err != nil {
		return nil, err
	}

	candidates := make([]store.CreateEllieExtractedMemoryInput, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		normalized, ok := normalizeEllieLLMExtractedCandidate(room, window, result, candidate)
		if !ok {
			continue
		}
		candidates = append(candidates, normalized)
	}
	return candidates, nil
}

func normalizeEllieIngestionMode(mode EllieIngestionMode) EllieIngestionMode {
	switch strings.TrimSpace(strings.ToLower(string(mode))) {
	case string(EllieIngestionModeBackfill):
		return EllieIngestionModeBackfill
	default:
		return EllieIngestionModeNormal
	}
}

func deriveEllieMemoryCandidate(message store.EllieIngestionMessage) (store.CreateEllieExtractedMemoryInput, bool) {
	return deriveEllieMemoryCandidateFromWindow([]store.EllieIngestionMessage{message})
}

func deriveEllieMemoryCandidateFromWindow(messages []store.EllieIngestionMessage) (store.CreateEllieExtractedMemoryInput, bool) {
	if len(messages) == 0 {
		return store.CreateEllieExtractedMemoryInput{}, false
	}

	body := strings.TrimSpace(joinEllieWindowBodies(messages))
	if isEllieLowSignalMessage(body) {
		return store.CreateEllieExtractedMemoryInput{}, false
	}
	lowerBody := strings.ToLower(body)

	kind := "context"
	title := "Context observed in room"
	importance := 3
	confidence := 0.7

	switch {
	case strings.Contains(lowerBody, "we decided") ||
		(strings.Contains(lowerBody, "decided to") && hasEllieOperationalContext(lowerBody)) ||
		strings.Contains(lowerBody, "decision:") ||
		strings.Contains(lowerBody, "we will use") ||
		strings.Contains(lowerBody, "let's go with"):
		kind = "technical_decision"
		title = "Technical decision captured"
		importance = 4
		confidence = 0.9
	case strings.Contains(lowerBody, "we prefer") ||
		strings.Contains(lowerBody, "prefer to use") ||
		strings.Contains(lowerBody, "preference:"):
		kind = "preference"
		title = "Preference captured"
		importance = 4
		confidence = 0.9
	case (strings.Contains(lowerBody, "avoid") || strings.Contains(lowerBody, "do not") || strings.Contains(lowerBody, "don't")) &&
		hasEllieOperationalContext(lowerBody):
		kind = "anti_pattern"
		title = "Anti-pattern captured"
		importance = 4
		confidence = 0.85
	case strings.Contains(lowerBody, "lesson learned") || strings.Contains(lowerBody, "we learned"):
		kind = "lesson"
		title = "Lesson captured"
		importance = 4
		confidence = 0.85
	case strings.Contains(lowerBody, "fact:") || strings.Contains(lowerBody, "confirmed that"):
		kind = "fact"
		title = "Fact captured"
		importance = 3
		confidence = 0.75
	}

	metadataRaw, _ := json.Marshal(map[string]any{
		"source_table":       "chat_messages",
		"source_message_ids": ellieWindowMessageIDs(messages),
		"source_room_id":     messages[0].RoomID,
		"extraction_method":  "heuristic_windowed",
	})

	content := body
	if len([]rune(content)) > 400 {
		content = string([]rune(content)[:400])
	}

	return store.CreateEllieExtractedMemoryInput{
		OrgID:                messages[0].OrgID,
		Kind:                 kind,
		Title:                title,
		Content:              content,
		Metadata:             metadataRaw,
		Importance:           importance,
		Confidence:           confidence,
		SourceConversationID: firstEllieConversationID(messages),
		OccurredAt:           messages[len(messages)-1].CreatedAt,
	}, true
}

func normalizeEllieLLMExtractedCandidate(
	room store.EllieRoomIngestionCandidate,
	window []store.EllieIngestionMessage,
	result EllieIngestionLLMExtractionResult,
	candidate EllieIngestionLLMCandidate,
) (store.CreateEllieExtractedMemoryInput, bool) {
	if len(window) == 0 {
		return store.CreateEllieExtractedMemoryInput{}, false
	}

	kind := strings.ToLower(strings.TrimSpace(candidate.Kind))
	switch kind {
	case "technical_decision", "process_decision", "preference", "fact", "lesson", "pattern", "anti_pattern", "correction", "process_outcome", "context":
	default:
		kind = "context"
	}

	title := strings.TrimSpace(candidate.Title)
	if title == "" {
		title = "LLM extracted memory"
	}
	content := strings.TrimSpace(candidate.Content)
	if content == "" {
		return store.CreateEllieExtractedMemoryInput{}, false
	}
	if len([]rune(content)) > 400 {
		content = string([]rune(content)[:400])
	}

	importance := candidate.Importance
	if importance <= 0 {
		importance = 3
	}
	if importance > 5 {
		importance = 5
	}

	confidence := candidate.Confidence
	if math.IsNaN(confidence) || confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	metadata := map[string]any{
		"source_table":       "chat_messages",
		"source_message_ids": ellieWindowMessageIDs(window),
		"source_room_id":     room.RoomID,
		"extraction_method":  "llm_windowed",
	}
	if model := strings.TrimSpace(result.Model); model != "" {
		metadata["extraction_model"] = model
	}
	if traceID := strings.TrimSpace(result.TraceID); traceID != "" {
		metadata["extraction_trace_id"] = traceID
	}
	if candidate.Metadata != nil && len(candidate.Metadata) > 0 {
		metadata["llm_metadata"] = candidate.Metadata
	}
	metadataRaw, _ := json.Marshal(metadata)

	sourceConversationID := normalizeEllieOptionalID(candidate.SourceConversationID)
	if sourceConversationID == nil {
		sourceConversationID = firstEllieConversationID(window)
	}

	return store.CreateEllieExtractedMemoryInput{
		OrgID:                room.OrgID,
		Kind:                 kind,
		Title:                title,
		Content:              content,
		Metadata:             metadataRaw,
		Importance:           importance,
		Confidence:           confidence,
		SourceConversationID: sourceConversationID,
		OccurredAt:           window[len(window)-1].CreatedAt,
	}, true
}

func groupEllieIngestionMessagesByWindow(messages []store.EllieIngestionMessage, gap time.Duration) [][]store.EllieIngestionMessage {
	if len(messages) == 0 {
		return [][]store.EllieIngestionMessage{}
	}
	if gap <= 0 {
		gap = ellieIngestionWindowGap
	}

	windows := make([][]store.EllieIngestionMessage, 0, len(messages))
	current := make([]store.EllieIngestionMessage, 0, len(messages))
	for _, message := range messages {
		if len(current) == 0 {
			current = append(current, message)
			continue
		}
		last := current[len(current)-1]
		if message.CreatedAt.Sub(last.CreatedAt) > gap {
			windows = append(windows, current)
			current = []store.EllieIngestionMessage{message}
			continue
		}
		current = append(current, message)
	}
	if len(current) > 0 {
		windows = append(windows, current)
	}
	return windows
}

func groupEllieIngestionMessagesByCount(
	messages []store.EllieIngestionMessage,
	windowSize int,
	stride int,
) [][]store.EllieIngestionMessage {
	if len(messages) == 0 {
		return [][]store.EllieIngestionMessage{}
	}
	if windowSize <= 0 {
		return [][]store.EllieIngestionMessage{messages}
	}
	if stride <= 0 {
		stride = windowSize
	}
	if stride > windowSize {
		stride = windowSize
	}

	windows := make([][]store.EllieIngestionMessage, 0, (len(messages)+stride-1)/stride)
	for start := 0; start < len(messages); start += stride {
		end := start + windowSize
		if end > len(messages) {
			end = len(messages)
		}
		windows = append(windows, messages[start:end])
		if end >= len(messages) {
			break
		}
	}
	return windows
}

func joinEllieWindowBodies(messages []store.EllieIngestionMessage) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		body := strings.TrimSpace(message.Body)
		if body == "" {
			continue
		}
		parts = append(parts, body)
	}
	return strings.Join(parts, "\n")
}

func ellieWindowMessageIDs(messages []store.EllieIngestionMessage) []string {
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		id := strings.TrimSpace(message.ID)
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func firstEllieConversationID(messages []store.EllieIngestionMessage) *string {
	for _, message := range messages {
		if message.ConversationID == nil {
			continue
		}
		trimmed := strings.TrimSpace(*message.ConversationID)
		if trimmed == "" {
			continue
		}
		return &trimmed
	}
	return nil
}

func normalizeEllieOptionalID(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func hasEllieOperationalContext(body string) bool {
	return ellieOperationalContextPattern.MatchString(body)
}

func isEllieLowSignalMessage(body string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(body))
	if trimmed == "" {
		return true
	}
	if len([]rune(trimmed)) < 16 {
		return true
	}
	lowSignal := []string{
		"thanks",
		"thank you",
		"ok",
		"okay",
		"sounds good",
		"great",
		"cool",
	}
	for _, token := range lowSignal {
		if trimmed == token {
			return true
		}
	}
	return false
}
