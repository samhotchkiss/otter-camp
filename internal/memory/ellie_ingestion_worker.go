package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	defaultEllieIngestionInterval   = 5 * time.Minute
	defaultEllieIngestionBatchSize  = 100
	defaultEllieIngestionMaxPerRoom = 200
)

type EllieIngestionStore interface {
	ListRoomsForIngestion(ctx context.Context, limit int) ([]store.EllieRoomIngestionCandidate, error)
	GetRoomCursor(ctx context.Context, orgID, roomID string) (*store.EllieRoomCursor, error)
	ListRoomMessagesSince(ctx context.Context, orgID, roomID string, afterCreatedAt *time.Time, afterMessageID *string, limit int) ([]store.EllieIngestionMessage, error)
	InsertExtractedMemory(ctx context.Context, input store.CreateEllieExtractedMemoryInput) (bool, error)
	UpsertRoomCursor(ctx context.Context, input store.UpsertEllieRoomCursorInput) error
}

type EllieIngestionWorkerConfig struct {
	Interval   time.Duration
	BatchSize  int
	MaxPerRoom int
}

type EllieIngestionWorker struct {
	Store      EllieIngestionStore
	Interval   time.Duration
	BatchSize  int
	MaxPerRoom int
	Logf       func(format string, args ...any)
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
	return &EllieIngestionWorker{
		Store:      store,
		Interval:   interval,
		BatchSize:  batchSize,
		MaxPerRoom: maxPerRoom,
		Logf:       log.Printf,
	}
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
		}

		messages, err := w.Store.ListRoomMessagesSince(ctx, room.OrgID, room.RoomID, afterCreatedAt, afterMessageID, w.MaxPerRoom)
		if err != nil {
			return processed, fmt.Errorf("list room messages for ellie ingestion %s/%s: %w", room.OrgID, room.RoomID, err)
		}
		if len(messages) == 0 {
			continue
		}

		for _, message := range messages {
			candidate, ok := deriveEllieMemoryCandidate(message)
			if !ok {
				processed += 1
				continue
			}
			inserted, err := w.Store.InsertExtractedMemory(ctx, candidate)
			if err != nil {
				return processed, fmt.Errorf("insert ellie extracted memory for message %s: %w", message.ID, err)
			}
			if inserted && w.Logf != nil {
				w.Logf("ellie ingestion extracted memory kind=%s room=%s message=%s", candidate.Kind, message.RoomID, message.ID)
			}
			processed += 1
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

func deriveEllieMemoryCandidate(message store.EllieIngestionMessage) (store.CreateEllieExtractedMemoryInput, bool) {
	body := strings.TrimSpace(message.Body)
	if isEllieLowSignalMessage(body) {
		return store.CreateEllieExtractedMemoryInput{}, false
	}
	lowerBody := strings.ToLower(body)

	kind := "context"
	title := "Context observed in room"
	importance := 3
	confidence := 0.7

	switch {
	case strings.Contains(lowerBody, "decide") || strings.Contains(lowerBody, "decision") || strings.Contains(lowerBody, "we will"):
		kind = "technical_decision"
		title = "Technical decision captured"
		importance = 4
		confidence = 0.9
	case strings.Contains(lowerBody, "prefer") || strings.Contains(lowerBody, "preference"):
		kind = "preference"
		title = "Preference captured"
		importance = 4
		confidence = 0.9
	case strings.Contains(lowerBody, "avoid") || strings.Contains(lowerBody, "do not") || strings.Contains(lowerBody, "don't"):
		kind = "anti_pattern"
		title = "Anti-pattern captured"
		importance = 4
		confidence = 0.85
	case strings.Contains(lowerBody, "lesson") || strings.Contains(lowerBody, "learned"):
		kind = "lesson"
		title = "Lesson captured"
		importance = 4
		confidence = 0.85
	case strings.Contains(lowerBody, "fact") || strings.Contains(lowerBody, "is "):
		kind = "fact"
		title = "Fact captured"
		importance = 3
		confidence = 0.75
	}

	metadataRaw, _ := json.Marshal(map[string]any{
		"source_table":      "chat_messages",
		"source_message_id": message.ID,
		"source_room_id":    message.RoomID,
		"extraction_method": "heuristic",
	})

	content := body
	if len([]rune(content)) > 400 {
		content = string([]rune(content)[:400])
	}

	return store.CreateEllieExtractedMemoryInput{
		OrgID:                message.OrgID,
		Kind:                 kind,
		Title:                title,
		Content:              content,
		Metadata:             metadataRaw,
		Importance:           importance,
		Confidence:           confidence,
		SourceConversationID: message.ConversationID,
		OccurredAt:           message.CreatedAt,
	}, true
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
