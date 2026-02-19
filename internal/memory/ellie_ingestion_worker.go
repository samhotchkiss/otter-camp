package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"regexp"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

const (
	defaultEllieIngestionInterval           = 5 * time.Minute
	defaultEllieIngestionBridgeRetry        = 10 * time.Second
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
	ListRoomsForIngestionByOrg(ctx context.Context, orgID string, limit int) ([]store.EllieRoomIngestionCandidate, error)
	GetRoomCursor(ctx context.Context, orgID, roomID string) (*store.EllieRoomCursor, error)
	ListRoomMessagesSince(ctx context.Context, orgID, roomID string, afterCreatedAt *time.Time, afterMessageID *string, limit int) ([]store.EllieIngestionMessage, error)
	InsertExtractedMemory(ctx context.Context, input store.CreateEllieExtractedMemoryInput) (bool, error)
	CreateWindowRun(ctx context.Context, input store.CreateEllieIngestionWindowRunInput) error
	UpsertRoomCursor(ctx context.Context, input store.UpsertEllieRoomCursorInput) error
}

type EllieIngestionRunResult struct {
	ProcessedMessages         int
	WindowsProcessed          int
	RoomsProcessed            int
	InsertedMemories          int
	InsertedLLMMemories       int
	InsertedHeuristicMemories int
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

type EllieIngestionPauseChecker interface {
	ShouldPause(ctx context.Context, orgID string) (bool, error)
}

type EllieIngestionWorkerConfig struct {
	OrgID                string
	Interval             time.Duration
	BridgeRetryInterval  time.Duration
	BatchSize            int
	MaxPerRoom           int
	BackfillMaxPerRoom   int
	BackfillWindowSize   int
	BackfillWindowStride int
	WindowGap            time.Duration
	Mode                 EllieIngestionMode
	LLMExtractor         EllieIngestionLLMExtractor
	PauseChecker         EllieIngestionPauseChecker
}

type EllieIngestionWorker struct {
	Store                EllieIngestionStore
	OrgID                string
	Interval             time.Duration
	BridgeRetryInterval  time.Duration
	BatchSize            int
	MaxPerRoom           int
	BackfillMaxPerRoom   int
	BackfillWindowSize   int
	BackfillWindowStride int
	WindowGap            time.Duration
	Mode                 EllieIngestionMode
	LLMExtractor         EllieIngestionLLMExtractor
	PauseChecker         EllieIngestionPauseChecker
	Logf                 func(format string, args ...any)
}

func NewEllieIngestionWorker(store EllieIngestionStore, cfg EllieIngestionWorkerConfig) *EllieIngestionWorker {
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultEllieIngestionInterval
	}
	bridgeRetryInterval := cfg.BridgeRetryInterval
	if bridgeRetryInterval <= 0 {
		bridgeRetryInterval = defaultEllieIngestionBridgeRetry
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
		Store:                store,
		OrgID:                strings.TrimSpace(cfg.OrgID),
		Interval:             interval,
		BridgeRetryInterval:  bridgeRetryInterval,
		BatchSize:            batchSize,
		MaxPerRoom:           maxPerRoom,
		BackfillMaxPerRoom:   backfillMaxPerRoom,
		BackfillWindowSize:   backfillWindowSize,
		BackfillWindowStride: backfillWindowStride,
		WindowGap:            windowGap,
		Mode:                 mode,
		LLMExtractor:         cfg.LLMExtractor,
		PauseChecker:         cfg.PauseChecker,
		Logf:                 log.Printf,
	}
}

func (w *EllieIngestionWorker) SetMode(mode EllieIngestionMode) {
	if w == nil {
		return
	}
	w.Mode = normalizeEllieIngestionMode(mode)
}

func (w *EllieIngestionWorker) SetOrgID(orgID string) {
	if w == nil {
		return
	}
	w.OrgID = strings.TrimSpace(orgID)
}

func (w *EllieIngestionWorker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if w.shouldPause(ctx) {
			if err := sleepWithContext(ctx, w.Interval); err != nil {
				return
			}
			continue
		}
		result, err := w.RunOnce(ctx)
		if err != nil {
			if w.Logf != nil {
				w.Logf("ellie ingestion worker run failed: %v", err)
			}
			sleepDuration := w.Interval
			if errors.Is(err, ws.ErrOpenClawNotConnected) {
				sleepDuration = w.BridgeRetryInterval
			}
			if err := sleepWithContext(ctx, sleepDuration); err != nil {
				return
			}
			continue
		}
		if result.ProcessedMessages > 0 {
			continue
		}
		if err := sleepWithContext(ctx, w.Interval); err != nil {
			return
		}
	}
}

func (w *EllieIngestionWorker) shouldPause(ctx context.Context) bool {
	if w == nil || w.PauseChecker == nil {
		return false
	}
	orgID := strings.TrimSpace(w.OrgID)
	if orgID == "" {
		return false
	}
	pause, err := w.PauseChecker.ShouldPause(ctx, orgID)
	if err != nil {
		if w.Logf != nil {
			w.Logf("ellie ingestion pause check failed org=%s: %v", orgID, err)
		}
		return false
	}
	return pause
}

func (w *EllieIngestionWorker) RunOnce(ctx context.Context) (EllieIngestionRunResult, error) {
	if w == nil {
		return EllieIngestionRunResult{}, fmt.Errorf("ellie ingestion worker is nil")
	}
	if w.Store == nil {
		return EllieIngestionRunResult{}, fmt.Errorf("ellie ingestion store is required")
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

	var rooms []store.EllieRoomIngestionCandidate
	var err error
	if strings.TrimSpace(w.OrgID) != "" {
		rooms, err = w.Store.ListRoomsForIngestionByOrg(ctx, w.OrgID, w.BatchSize)
	} else {
		rooms, err = w.Store.ListRoomsForIngestion(ctx, w.BatchSize)
	}
	if err != nil {
		return EllieIngestionRunResult{}, fmt.Errorf("list rooms for ellie ingestion: %w", err)
	}

	result := EllieIngestionRunResult{}
	var (
		runSawRetriableFailure bool
		runSawAnyFailure       bool
		runLastErr             error
	)
	for _, room := range rooms {
		cursor, err := w.Store.GetRoomCursor(ctx, room.OrgID, room.RoomID)
		if err != nil {
			return result, fmt.Errorf("load ellie room cursor %s/%s: %w", room.OrgID, room.RoomID, err)
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
			return result, fmt.Errorf("list room messages for ellie ingestion %s/%s: %w", room.OrgID, room.RoomID, err)
		}
		if len(messages) == 0 {
			continue
		}

		result.RoomsProcessed++
		var lastSuccessful store.EllieIngestionMessage
		var hasLastSuccessful bool
		var sawAnyLLMFailure bool
		var sawRetriableLLMFailure bool
		var lastLLMErr error

		var windows [][]store.EllieIngestionMessage
		if mode == EllieIngestionModeBackfill && w.BackfillWindowSize > 0 {
			windows = groupEllieIngestionMessagesByCount(messages, w.BackfillWindowSize, w.BackfillWindowStride)
		} else {
			windows = groupEllieIngestionMessagesByWindow(messages, w.WindowGap)
		}
		for _, window := range windows {
			subWindows := [][]store.EllieIngestionMessage{window}
			if w.LLMExtractor != nil {
				if budgeter, ok := w.LLMExtractor.(EllieIngestionLLMBudgeter); ok {
					maxPromptChars, maxMessageChars := budgeter.PromptBudget()
					subWindows = splitEllieIngestionWindowByPromptBudget(room.OrgID, room.RoomID, window, maxPromptChars, maxMessageChars)
				}
			}

			for _, subWindow := range subWindows {
				if len(subWindow) == 0 {
					continue
				}

				// Record run metrics for observability and for the ingestion coverage dashboard.
				runStarted := time.Now()
				windowStartAt := subWindow[0].CreatedAt
				windowEndAt := subWindow[len(subWindow)-1].CreatedAt
				firstMessageID := strings.TrimSpace(subWindow[0].ID)
				lastMessageID := strings.TrimSpace(subWindow[len(subWindow)-1].ID)
				messageCount := len(subWindow)
				tokenCount := 0
				for _, msg := range subWindow {
					if msg.TokenCount > 0 {
						tokenCount += msg.TokenCount
					}
				}

				if w.LLMExtractor != nil {
					llmCandidates, llmResult, llmAttempts, err := w.extractLLMMemoryCandidatesWithRetry(ctx, room, subWindow)
					if err != nil {
						if w.Logf != nil {
							w.Logf("ellie ingestion llm extraction failed room=%s: %v", room.RoomID, err)
						}

						_ = w.Store.CreateWindowRun(ctx, store.CreateEllieIngestionWindowRunInput{
							OrgID:          room.OrgID,
							RoomID:         room.RoomID,
							WindowStartAt:  windowStartAt,
							WindowEndAt:    windowEndAt,
							FirstMessageID: &firstMessageID,
							LastMessageID:  &lastMessageID,
							MessageCount:   messageCount,
							TokenCount:     tokenCount,
							LLMUsed:        true,
							LLMModel:       llmResult.Model,
							LLMTraceID:     llmResult.TraceID,
							LLMAttempts:    llmAttempts,
							OK:             false,
							Error:          err.Error(),
							DurationMS:     int(time.Since(runStarted).Milliseconds()),
						})

						// Do not advance cursors past failing windows. We'll retry on the next run.
						if isRetriableEllieIngestionLLMError(err) {
							sawRetriableLLMFailure = true
						}
						sawAnyLLMFailure = true
						lastLLMErr = err
						break
					}

					result.WindowsProcessed++
					result.ProcessedMessages += len(subWindow)

					insertedTotal := 0
					insertedMemories := 0
					insertedProjects := 0
					insertedIssues := 0
					for _, candidate := range llmCandidates {
						inserted, err := w.Store.InsertExtractedMemory(ctx, candidate)
						if err != nil {
							return result, fmt.Errorf("insert llm extracted memory for room %s: %w", room.RoomID, err)
						}
						if !inserted {
							continue
						}
						insertedTotal++
						result.InsertedMemories++
						result.InsertedLLMMemories++

						metaType := ""
						if raw := candidate.Metadata; len(raw) > 0 {
							var meta map[string]any
							_ = json.Unmarshal(raw, &meta)
							if v, ok := meta["type"].(string); ok {
								metaType = strings.TrimSpace(strings.ToLower(v))
							}
						}
						switch metaType {
						case "project":
							insertedProjects++
						case "issue":
							insertedIssues++
						default:
							insertedMemories++
						}

						if w.Logf != nil {
							w.Logf("ellie ingestion extracted llm memory kind=%s room=%s", candidate.Kind, room.RoomID)
						}
					}

					_ = w.Store.CreateWindowRun(ctx, store.CreateEllieIngestionWindowRunInput{
						OrgID:            room.OrgID,
						RoomID:           room.RoomID,
						WindowStartAt:    windowStartAt,
						WindowEndAt:      windowEndAt,
						FirstMessageID:   &firstMessageID,
						LastMessageID:    &lastMessageID,
						MessageCount:     messageCount,
						TokenCount:       tokenCount,
						LLMUsed:          true,
						LLMModel:         llmResult.Model,
						LLMTraceID:       llmResult.TraceID,
						LLMAttempts:      llmAttempts,
						OK:               true,
						DurationMS:       int(time.Since(runStarted).Milliseconds()),
						InsertedTotal:    insertedTotal,
						InsertedMemories: insertedMemories,
						InsertedProjects: insertedProjects,
						InsertedIssues:   insertedIssues,
					})

					// Success: we can safely advance to the end of this subwindow.
					lastSuccessful = subWindow[len(subWindow)-1]
					hasLastSuccessful = true
					continue
				}

				// No LLM extractor configured; heuristic mode.
				result.WindowsProcessed++
				result.ProcessedMessages += len(subWindow)

				candidate, ok := deriveEllieMemoryCandidateFromWindow(subWindow)
				if !ok {
					_ = w.Store.CreateWindowRun(ctx, store.CreateEllieIngestionWindowRunInput{
						OrgID:          room.OrgID,
						RoomID:         room.RoomID,
						WindowStartAt:  windowStartAt,
						WindowEndAt:    windowEndAt,
						FirstMessageID: &firstMessageID,
						LastMessageID:  &lastMessageID,
						MessageCount:   messageCount,
						TokenCount:     tokenCount,
						LLMUsed:        false,
						OK:             true,
						DurationMS:     int(time.Since(runStarted).Milliseconds()),
					})
					lastSuccessful = subWindow[len(subWindow)-1]
					hasLastSuccessful = true
					continue
				}

				inserted, err := w.Store.InsertExtractedMemory(ctx, candidate)
				if err != nil {
					return result, fmt.Errorf("insert ellie extracted memory for room %s: %w", room.RoomID, err)
				}
				if inserted {
					result.InsertedMemories++
					result.InsertedHeuristicMemories++
				}
				if inserted && w.Logf != nil {
					w.Logf("ellie ingestion extracted memory kind=%s room=%s", candidate.Kind, room.RoomID)
				}

				_ = w.Store.CreateWindowRun(ctx, store.CreateEllieIngestionWindowRunInput{
					OrgID:            room.OrgID,
					RoomID:           room.RoomID,
					WindowStartAt:    windowStartAt,
					WindowEndAt:      windowEndAt,
					FirstMessageID:   &firstMessageID,
					LastMessageID:    &lastMessageID,
					MessageCount:     messageCount,
					TokenCount:       tokenCount,
					LLMUsed:          false,
					OK:               true,
					DurationMS:       int(time.Since(runStarted).Milliseconds()),
					InsertedTotal:    boolToInt(inserted),
					InsertedMemories: boolToInt(inserted),
				})

				lastSuccessful = subWindow[len(subWindow)-1]
				hasLastSuccessful = true
			}

			// If an LLM failure occurred for this room, stop processing without advancing the
			// cursor past the last successful subwindow.
			if w.LLMExtractor != nil && sawAnyLLMFailure {
				break
			}
		}

		// Only advance the cursor to the last successfully processed message. If the LLM
		// failed mid-batch, we intentionally leave the cursor behind so the worker retries
		// on the next run.
		if hasLastSuccessful {
			if err := w.Store.UpsertRoomCursor(ctx, store.UpsertEllieRoomCursorInput{
				OrgID:                room.OrgID,
				RoomID:               room.RoomID,
				LastMessageID:        lastSuccessful.ID,
				LastMessageCreatedAt: lastSuccessful.CreatedAt,
			}); err != nil {
				return result, fmt.Errorf("upsert ellie room cursor %s/%s: %w", room.OrgID, room.RoomID, err)
			}
		}

		// Do not abort the entire run on a single-room failure. Continue other rooms so one
		// flaky bridge/session does not starve ingestion coverage for the whole org.
		if sawRetriableLLMFailure {
			runSawRetriableFailure = true
			runSawAnyFailure = true
			if lastLLMErr != nil {
				runLastErr = lastLLMErr
			} else {
				runLastErr = ws.ErrOpenClawNotConnected
			}
			continue
		}
		if sawAnyLLMFailure {
			runSawAnyFailure = true
			if lastLLMErr != nil {
				runLastErr = lastLLMErr
			} else {
				runLastErr = fmt.Errorf("ellie ingestion llm extraction failed")
			}
			continue
		}
	}

	// Only surface a run-level error when nothing at all was processed; this preserves
	// fast retry behavior while still allowing partial progress when some rooms succeed.
	if result.ProcessedMessages == 0 && runSawAnyFailure {
		if runSawRetriableFailure {
			return result, ws.ErrOpenClawNotConnected
		}
		if runLastErr != nil {
			return result, runLastErr
		}
		return result, fmt.Errorf("ellie ingestion llm extraction failed")
	}

	return result, nil
}

func (w *EllieIngestionWorker) extractLLMMemoryCandidates(
	ctx context.Context,
	room store.EllieRoomIngestionCandidate,
	window []store.EllieIngestionMessage,
) ([]store.CreateEllieExtractedMemoryInput, EllieIngestionLLMExtractionResult, error) {
	if w == nil || w.LLMExtractor == nil || len(window) == 0 {
		return nil, EllieIngestionLLMExtractionResult{}, nil
	}
	result, err := w.LLMExtractor.Extract(ctx, EllieIngestionLLMExtractionInput{
		OrgID:    room.OrgID,
		RoomID:   room.RoomID,
		Messages: window,
	})
	if err != nil {
		return nil, EllieIngestionLLMExtractionResult{}, err
	}

	candidates := make([]store.CreateEllieExtractedMemoryInput, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		normalized, ok := normalizeEllieLLMExtractedCandidate(room, window, result, candidate)
		if !ok {
			continue
		}
		candidates = append(candidates, normalized)
	}
	return candidates, result, nil
}

func (w *EllieIngestionWorker) extractLLMMemoryCandidatesWithRetry(
	ctx context.Context,
	room store.EllieRoomIngestionCandidate,
	window []store.EllieIngestionMessage,
) ([]store.CreateEllieExtractedMemoryInput, EllieIngestionLLMExtractionResult, int, error) {
	if w == nil || w.LLMExtractor == nil {
		return nil, EllieIngestionLLMExtractionResult{}, 0, nil
	}

	const maxAttempts = 3
	var lastErr error
	var lastResult EllieIngestionLLMExtractionResult

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		candidates, result, err := w.extractLLMMemoryCandidates(ctx, room, window)
		if err == nil {
			return candidates, result, attempt, nil
		}
		lastErr = err
		lastResult = result
		if !isRetriableEllieIngestionLLMError(err) || attempt == maxAttempts {
			break
		}

		// Exponential backoff with jitter so we play nicely with transient bridge failures.
		backoff := time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond
		if backoff > 8*time.Second {
			backoff = 8 * time.Second
		}
		jitter := time.Duration(rand.Intn(250)) * time.Millisecond
		if err := sleepWithContext(ctx, backoff+jitter); err != nil {
			return nil, lastResult, attempt, err
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("llm extraction failed")
	}
	return nil, lastResult, maxAttempts, lastErr
}

func isRetriableEllieIngestionLLMError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ws.ErrOpenClawNotConnected) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	// Conservatively retry gateway-size errors and transient websocket close codes.
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "websocket") &&
		(strings.Contains(msg, "1006") || strings.Contains(msg, "1001") || strings.Contains(msg, "1012") || strings.Contains(msg, "timeout")) {
		return true
	}
	if strings.Contains(msg, "openclaw bridge call failed") {
		return true
	}
	if strings.Contains(msg, "econnrefused") || strings.Contains(msg, "connection refused") {
		return true
	}
	if strings.Contains(msg, "unexpected server response: 502") || strings.Contains(msg, "bad gateway") {
		return true
	}
	return false
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
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
	scoringWindow := normalizeEllieIngestionWindowForScoring(window)
	if len(scoringWindow) == 0 {
		scoringWindow = window
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
	if ellieIngestionHasSensitiveLeak(title) || ellieIngestionHasSensitiveLeak(content) {
		// Never persist secrets/PII (even redacted markers). This keeps the DB safe even if
		// upstream extraction produces unsafe candidates.
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
		"source_message_ids": ellieWindowMessageIDs(scoringWindow),
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
		// Prefer "flattened" metadata for downstream retrieval and auditing.
		for k, v := range candidate.Metadata {
			if strings.TrimSpace(k) == "" || v == nil {
				continue
			}
			metadata[k] = v
		}
	}

	sourceConversationID := normalizeEllieOptionalID(candidate.SourceConversationID)
	if sourceConversationID == nil {
		sourceConversationID = firstEllieConversationID(window)
	}

	// Stage 2-lite: score and filter candidates so hosted and local run the same
	// "high recall -> deterministic keep" pipeline.
	score, decision := ellieIngestionStage2Decision(kind, title, content, metadata, scoringWindow)
	metadata["accept_score"] = score
	metadata["accept_decision"] = decision
	// Keep review-scored candidates to match local high-recall behavior.
	// Only hard rejects are discarded here.
	if decision == "reject" {
		return store.CreateEllieExtractedMemoryInput{}, false
	}

	// Stage 2-lite: prevent "topic ideas / queued output / system logs" from becoming
	// autobiographical durable memories unless there is user-authored evidence in the cited sources.
	if metaType, _ := metadata["type"].(string); strings.TrimSpace(metaType) == "" {
		originHint, _ := metadata["origin_hint"].(string)
		switch strings.TrimSpace(strings.ToLower(originHint)) {
		case "queued_task", "system_artifact", "log_output":
			if !ellieIngestionCandidateHasUserEvidence(metadata, scoringWindow) {
				return store.CreateEllieExtractedMemoryInput{}, false
			}
		}
	}

	// Map pipeline-style sensitivity to the DB enum (normal|sensitive).
	sensitivity := "normal"
	if raw, ok := metadata["sensitivity"]; ok {
		if s, ok := raw.(string); ok {
			switch strings.TrimSpace(strings.ToLower(s)) {
			case "high", "medium", "sensitive":
				sensitivity = "sensitive"
			}
		}
	}
	if flags, ok := metadata["pii_flags"]; ok {
		if list, ok := flags.([]any); ok {
			for _, v := range list {
				if strings.TrimSpace(strings.ToLower(fmt.Sprint(v))) == "medical" {
					sensitivity = "sensitive"
					break
				}
			}
		}
	}

	metadataRaw, _ := json.Marshal(metadata)

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
		Sensitivity:          sensitivity,
	}, true
}

func normalizeEllieIngestionWindowForScoring(window []store.EllieIngestionMessage) []store.EllieIngestionMessage {
	if len(window) == 0 {
		return nil
	}
	out := make([]store.EllieIngestionMessage, 0, len(window))
	for _, msg := range window {
		if normalized, ok := normalizeEllieIngestionPromptMessage(msg); ok {
			out = append(out, normalized)
		}
	}
	return out
}

func ellieIngestionHasSensitiveLeak(text string) bool {
	lower := strings.ToLower(text)
	// Token-like patterns (keep conservative).
	if strings.Contains(lower, "ghp_") || strings.Contains(lower, "sk-") || strings.Contains(lower, "xoxb-") || strings.Contains(lower, "xoxp-") {
		return true
	}
	if strings.Contains(lower, "api key") || strings.Contains(lower, "password") || strings.Contains(lower, "pairing code") || strings.Contains(lower, "token:") {
		return true
	}
	return false
}

func ellieIngestionCandidateHasUserEvidence(metadata map[string]any, window []store.EllieIngestionMessage) bool {
	rawIDs, ok := metadata["source_message_ids"]
	if !ok || rawIDs == nil {
		return false
	}
	sourceIDs := map[string]struct{}{}
	switch v := rawIDs.(type) {
	case []string:
		for _, id := range v {
			sourceIDs[strings.TrimSpace(id)] = struct{}{}
		}
	case []any:
		for _, item := range v {
			id := strings.TrimSpace(fmt.Sprint(item))
			if id != "" {
				sourceIDs[id] = struct{}{}
			}
		}
	}
	if len(sourceIDs) == 0 {
		return false
	}
	for _, msg := range window {
		if strings.TrimSpace(strings.ToLower(msg.SenderType)) != "user" {
			continue
		}
		if _, ok := sourceIDs[strings.TrimSpace(msg.ID)]; ok {
			return true
		}
	}
	return false
}

func ellieIngestionStage2Decision(kind, title, content string, metadata map[string]any, window []store.EllieIngestionMessage) (int, string) {
	// Context entries (projects/issues) are accepted as long as they are non-empty and safe.
	if metaType, _ := metadata["type"].(string); strings.TrimSpace(metaType) != "" {
		return 80, "accept"
	}

	score := 50

	// Evidence quality.
	score += ellieIngestionStage2EvidenceScore(metadata, window)

	// Durability.
	score += ellieIngestionStage2DurabilityScore(title, content)

	// Atomicity/specificity.
	score += ellieIngestionStage2AtomicityScore(title, content)

	// Sensitivity penalty (conservative).
	if strings.TrimSpace(strings.ToLower(fmt.Sprint(metadata["sensitivity"]))) == "high" {
		score -= 20
	}
	if flags, ok := metadata["pii_flags"]; ok {
		if list, ok := flags.([]any); ok {
			for _, v := range list {
				if strings.TrimSpace(strings.ToLower(fmt.Sprint(v))) == "medical" {
					score -= 20
					break
				}
			}
		}
	}

	// Thresholds (match Stage2 spec defaults).
	switch {
	case score >= 65:
		return score, "accept"
	case score >= 45:
		return score, "review"
	default:
		return score, "reject"
	}
}

func ellieIngestionStage2EvidenceScore(metadata map[string]any, window []store.EllieIngestionMessage) int {
	sourceIDs := map[string]struct{}{}
	switch v := metadata["source_message_ids"].(type) {
	case []string:
		for _, id := range v {
			id = strings.TrimSpace(id)
			if id != "" {
				sourceIDs[id] = struct{}{}
			}
		}
	case []any:
		for _, item := range v {
			id := strings.TrimSpace(fmt.Sprint(item))
			if id != "" {
				sourceIDs[id] = struct{}{}
			}
		}
	}
	if len(sourceIDs) == 0 {
		return 0
	}

	total := 0
	user := 0
	artifact := 0
	for _, msg := range window {
		if _, ok := sourceIDs[strings.TrimSpace(msg.ID)]; !ok {
			continue
		}
		total++
		if strings.TrimSpace(strings.ToLower(msg.SenderType)) == "user" {
			user++
		}
		if ellieIngestionIsArtifactMessage(msg) {
			artifact++
		}
	}
	if total == 0 {
		return 0
	}

	score := 0
	ratio := float64(user) / float64(total)
	switch {
	case ratio >= 0.5:
		score += 15
	case ratio >= 0.2:
		score += 8
	case ratio == 0:
		score -= 10
	}

	artifactRatio := float64(artifact) / float64(total)
	switch {
	case artifactRatio > 0.5:
		score -= 25
	case artifactRatio > 0.2:
		score -= 10
	}
	return score
}

func ellieIngestionIsArtifactMessage(msg store.EllieIngestionMessage) bool {
	body := strings.TrimSpace(msg.Body)
	lower := strings.ToLower(body)
	senderType := strings.TrimSpace(strings.ToLower(msg.SenderType))
	if strings.HasPrefix(body, "[Queued") || strings.Contains(body, "Queued #") {
		return true
	}
	if senderType == "system" {
		return true
	}
	if strings.Contains(lower, "heartbeat") || strings.Contains(lower, "no_reply") {
		return true
	}
	// Slack bridge can wrap human messages as "System: [ts] Slack message ...".
	// Treat these as artifacts only when the sender itself is system.
	if strings.HasPrefix(body, "System:") && senderType == "system" {
		return true
	}
	if strings.HasPrefix(body, "Tool ") && strings.Contains(lower, "result:") && senderType == "system" {
		return true
	}
	return false
}

func ellieIngestionStage2DurabilityScore(title, content string) int {
	text := strings.ToLower(strings.TrimSpace(title + " " + content))
	score := 0

	// Durable patterns.
	if regexp.MustCompile(`\b(prefers?|doesn'?t like|always|never|from now on)\b`).MatchString(text) {
		score += 15
	} else if regexp.MustCompile(`\b(migrat|architect|infrastructure|deploy|configur|set up|install|uses? .+ for)\b`).MatchString(text) {
		score += 15
	} else if regexp.MustCompile(`\b(family|wife|husband|partner|son|daughter|lives? in|based in|works? at|role is)\b`).MatchString(text) {
		score += 15
	}

	// Ephemeral patterns.
	if regexp.MustCompile(`\b(today|right now|this week|this morning|this afternoon)\b`).MatchString(text) &&
		!regexp.MustCompile(`\b(always|never|rule|from now on|every)\b`).MatchString(text) {
		score -= 15
	}
	if regexp.MustCompile(`\b(done and documented|waiting on|in progress|currently running|status:|working on it)\b`).MatchString(text) {
		score -= 15
	}
	if regexp.MustCompile(`\b(exec failed|stack trace|error:|exception:|enoent|econnrefused|timeout)\b`).MatchString(text) {
		score -= 15
	}

	// Draft/brainstorm.
	if regexp.MustCompile(`\b(topic idea|blog post idea|draft|outline|brainstorm|could write about|potential topic)\b`).MatchString(text) {
		score -= 25
	}

	if score < -25 {
		return -25
	}
	if score > 25 {
		return 25
	}
	return score
}

func ellieIngestionStage2AtomicityScore(title, content string) int {
	text := strings.TrimSpace(title + " " + content)
	score := 0
	sentences := regexp.MustCompile(`[.!?]`).Split(text, -1)
	meaningful := 0
	for _, s := range sentences {
		if len(strings.TrimSpace(s)) > 5 {
			meaningful++
		}
	}
	if meaningful <= 2 {
		score += 10
	} else if meaningful > 5 {
		score -= 15
	}
	if regexp.MustCompile(`[A-Z][a-z]+`).MatchString(title) {
		score += 5
	}
	if regexp.MustCompile(`^(Work style|Communication style|General|Preferences|Notes|Misc)`).MatchString(title) {
		score -= 5
	}
	if score < -20 {
		return -20
	}
	if score > 15 {
		return 15
	}
	return score
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

func splitEllieIngestionWindowByPromptBudget(
	orgID string,
	roomID string,
	window []store.EllieIngestionMessage,
	maxPromptChars int,
	maxMessageChars int,
) [][]store.EllieIngestionMessage {
	if len(window) == 0 {
		return [][]store.EllieIngestionMessage{}
	}
	if maxPromptChars <= 0 {
		return [][]store.EllieIngestionMessage{window}
	}
	if maxMessageChars <= 0 {
		maxMessageChars = defaultEllieIngestionOpenClawMaxMessageChars
	}

	// Measure the fixed prompt overhead (no messages).
	baseLen := len(buildEllieIngestionOpenClawPrompt(EllieIngestionLLMExtractionInput{
		OrgID:    orgID,
		RoomID:   roomID,
		Messages: nil,
	}, 0, maxMessageChars))
	if baseLen >= maxPromptChars {
		// Misconfigured budget; fall back to single-message windows.
		return groupEllieIngestionMessagesByCount(window, 1, 1)
	}

	chunks := make([][]store.EllieIngestionMessage, 0, 4)
	i := 0
	for i < len(window) {
		j := i
		for j < len(window) {
			candidate := window[i : j+1]
			promptLen := len(buildEllieIngestionOpenClawPrompt(EllieIngestionLLMExtractionInput{
				OrgID:    orgID,
				RoomID:   roomID,
				Messages: candidate,
			}, 0, maxMessageChars))
			if promptLen <= maxPromptChars {
				j++
				continue
			}
			break
		}

		if j == i {
			// Single message still doesn't fit; the prompt builder has a final safeguard
			// to shrink the message further, but we still need forward progress.
			j = i + 1
		}

		out := make([]store.EllieIngestionMessage, j-i)
		copy(out, window[i:j])
		chunks = append(chunks, out)
		i = j
	}
	if len(chunks) == 0 {
		return [][]store.EllieIngestionMessage{window}
	}
	return chunks
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
