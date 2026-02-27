package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ConnectionState string

const (
	ConnectionConnected    ConnectionState = "connected"
	ConnectionReconnecting ConnectionState = "reconnecting"
	ConnectionDisconnected ConnectionState = "disconnected"
)

var (
	ErrEnvelopeValidation = errors.New("invalid realtime envelope")
	ErrSequenceGap        = errors.New("realtime sequence gap")
)

type SequenceGapError struct {
	Expected int64
	Actual   int64
}

func (e SequenceGapError) Error() string {
	return fmt.Sprintf("%v: expected seq=%d got=%d", ErrSequenceGap, e.Expected, e.Actual)
}

func (e SequenceGapError) Is(target error) bool {
	return target == ErrSequenceGap
}

type EnvelopeValidationError struct {
	Field string
}

func (e EnvelopeValidationError) Error() string {
	return fmt.Sprintf("%v: missing %s", ErrEnvelopeValidation, e.Field)
}

func (e EnvelopeValidationError) Is(target error) bool {
	return target == ErrEnvelopeValidation
}

type EventEnvelope struct {
	Seq        int64           `json:"seq"`
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	OrgID      string          `json:"org_id"`
	Payload    json.RawMessage `json:"payload"`
}

func DecodeEventEnvelope(data []byte) (EventEnvelope, error) {
	var raw struct {
		Seq        *int64          `json:"seq"`
		EventID    string          `json:"event_id"`
		EventType  string          `json:"event_type"`
		OccurredAt string          `json:"occurred_at"`
		OrgID      string          `json:"org_id"`
		Payload    json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return EventEnvelope{}, fmt.Errorf("decode envelope: %w", err)
	}
	if raw.Seq == nil || *raw.Seq <= 0 {
		return EventEnvelope{}, EnvelopeValidationError{Field: "seq"}
	}
	if strings.TrimSpace(raw.EventID) == "" {
		return EventEnvelope{}, EnvelopeValidationError{Field: "event_id"}
	}
	if strings.TrimSpace(raw.EventType) == "" {
		return EventEnvelope{}, EnvelopeValidationError{Field: "event_type"}
	}
	if strings.TrimSpace(raw.OccurredAt) == "" {
		return EventEnvelope{}, EnvelopeValidationError{Field: "occurred_at"}
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, raw.OccurredAt)
	if err != nil {
		return EventEnvelope{}, fmt.Errorf("occurred_at: %w", err)
	}
	if strings.TrimSpace(raw.OrgID) == "" {
		return EventEnvelope{}, EnvelopeValidationError{Field: "org_id"}
	}
	if len(raw.Payload) == 0 {
		return EventEnvelope{}, EnvelopeValidationError{Field: "payload"}
	}

	return EventEnvelope{
		Seq:        *raw.Seq,
		EventID:    strings.TrimSpace(raw.EventID),
		EventType:  strings.TrimSpace(raw.EventType),
		OccurredAt: occurredAt.UTC(),
		OrgID:      strings.TrimSpace(raw.OrgID),
		Payload:    raw.Payload,
	}, nil
}

type EventReducer struct {
	mu              sync.Mutex
	lastSeq         int64
	seenEventIDs    map[string]struct{}
	knownEventTypes map[string]struct{}
	counts          map[string]int
	logger          *slog.Logger
}

func NewEventReducer(logger *slog.Logger) *EventReducer {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	known := map[string]struct{}{
		"chat.message.created":   {},
		"chat.message.chunk":     {},
		"chat.message.delta":     {},
		"chat.message.finalized": {},
		"chat.tool_call.status":  {},
		"chat.turn.started":      {},
		"chat.turn.completed":    {},
		"chat.turn.cancelled":    {},
		"task.status.changed":    {},
		"task.flow.advanced":     {},
	}
	return &EventReducer{
		seenEventIDs:    map[string]struct{}{},
		knownEventTypes: known,
		counts:          map[string]int{},
		logger:          logger,
	}
}

func (r *EventReducer) LastSeq() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastSeq
}

func (r *EventReducer) SetLastSeq(seq int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if seq < 0 {
		seq = 0
	}
	r.lastSeq = seq
}

func (r *EventReducer) AppliedCount(eventType string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[eventType]
}

func (r *EventReducer) Apply(event EventEnvelope) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if event.Seq <= r.lastSeq {
		return false, nil
	}
	if r.lastSeq > 0 {
		expected := r.lastSeq + 1
		if event.Seq != expected {
			return false, SequenceGapError{Expected: expected, Actual: event.Seq}
		}
	}

	if _, seen := r.seenEventIDs[event.EventID]; seen {
		r.lastSeq = event.Seq
		return false, nil
	}

	if _, known := r.knownEventTypes[event.EventType]; !known {
		r.seenEventIDs[event.EventID] = struct{}{}
		r.lastSeq = event.Seq
		r.logger.Debug("tui reducer ignored unknown event type", "event_type", event.EventType, "seq", event.Seq)
		return false, nil
	}

	r.seenEventIDs[event.EventID] = struct{}{}
	r.lastSeq = event.Seq
	r.counts[event.EventType]++
	return true, nil
}

type ConnectionStateMsg struct {
	State    ConnectionState
	Degraded bool
}

type SnapshotFetcher interface {
	FetchSnapshots(ctx context.Context, visibleViews []string) error
}

type SnapshotFetcherFunc func(ctx context.Context, visibleViews []string) error

func (f SnapshotFetcherFunc) FetchSnapshots(ctx context.Context, visibleViews []string) error {
	if f == nil {
		return nil
	}
	return f(ctx, visibleViews)
}

type SSEConnector interface {
	Connect(ctx context.Context, sinceSeq int64) (io.ReadCloser, error)
}

type HTTPSSEConnector struct {
	Client *http.Client
	URL    string
	APIKey string
	Scopes string
}

func (c HTTPSSEConnector) Connect(ctx context.Context, sinceSeq int64) (io.ReadCloser, error) {
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	parsed, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil {
		return nil, fmt.Errorf("parse stream url: %w", err)
	}
	query := parsed.Query()
	query.Set("last-event-id", strconv.FormatInt(sinceSeq, 10))
	if strings.TrimSpace(c.Scopes) != "" {
		query.Set("scopes", strings.TrimSpace(c.Scopes))
	}
	parsed.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build stream request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if key := strings.TrimSpace(c.APIKey); key != "" {
		req.Header.Set("X-API-Key", key)
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		defer res.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		return nil, fmt.Errorf("open stream: status=%d body=%q", res.StatusCode, strings.TrimSpace(string(body)))
	}
	return res.Body, nil
}

type RealtimeClient struct {
	Connector      SSEConnector
	Snapshots      SnapshotFetcher
	Reducer        *EventReducer
	VisibleViews   func() []string
	Logger         *slog.Logger
	Backoff        []time.Duration
	OnStateChange  func(state ConnectionState, degraded bool)
	OnEvent        func(event EventEnvelope, applied bool)
	OnReplaySynced func()

	degraded        bool
	replaySynced    bool
	replayUntilSeq  int64 // high_watermark from connected frame; 0 means fire OnReplaySynced immediately
}

func (c *RealtimeClient) Run(ctx context.Context) error {
	if c.Connector == nil {
		return fmt.Errorf("realtime connector is required")
	}
	if c.Reducer == nil {
		return fmt.Errorf("realtime reducer is required")
	}
	if c.Snapshots == nil {
		c.Snapshots = SnapshotFetcherFunc(func(context.Context, []string) error { return nil })
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	backoff := c.Backoff
	if len(backoff) == 0 {
		backoff = []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, time.Second}
	}

	attempt := 0
	c.emitState(ConnectionDisconnected)
	for {
		if err := ctx.Err(); err != nil {
			c.emitState(ConnectionDisconnected)
			return nil
		}

		c.emitState(ConnectionReconnecting)
		if err := c.refreshSnapshots(ctx); err != nil {
			return err
		}

		sinceSeq := c.Reducer.LastSeq()
		stream, err := c.Connector.Connect(ctx, sinceSeq)
		if err != nil {
			if sleepErr := sleepWithContext(ctx, backoff[minInt(attempt, len(backoff)-1)]); sleepErr != nil {
				c.emitState(ConnectionDisconnected)
				return nil
			}
			attempt++
			continue
		}
		attempt = 0
		replayComplete := false
		consumeErr := c.consumeStream(ctx, stream, &replayComplete)
		if consumeErr != nil && !errors.Is(consumeErr, io.EOF) && !errors.Is(consumeErr, context.Canceled) {
			c.Logger.Debug("tui realtime stream ended", "error", consumeErr)
		}
	}
}

func (c *RealtimeClient) consumeStream(ctx context.Context, stream io.ReadCloser, replayComplete *bool) error {
	defer stream.Close()
	reader := bufio.NewReader(stream)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame, err := readSSEClientFrame(reader)
		if err != nil {
			return err
		}
		if strings.TrimSpace(frame.Data) == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(frame.Event), "connected") {
			if err := c.handleConnectedFrame(ctx, frame.Data, replayComplete); err != nil {
				return err
			}
			continue
		}

		// The server sends a "connected" meta-frame on stream open (not a domain event envelope).
		// Handle it explicitly: if gap:true, mark degraded; otherwise mark connected and replay synced.
		// On a fresh client (lastSeq=0), seed the reducer to high_watermark so historical replay
		// frames do not flood the chat pane on startup.
		if frame.Event == "connected" {
			var connMeta struct {
				Gap           bool  `json:"gap"`
				HighWatermark int64 `json:"high_watermark"`
			}
			if jsonErr := json.Unmarshal([]byte(frame.Data), &connMeta); jsonErr == nil {
				if connMeta.Gap {
					if degradeErr := c.markDegradedAndRefresh(ctx, "replay_gap", nil); degradeErr != nil {
						return degradeErr
					}
				} else if c.Reducer.LastSeq() == 0 && connMeta.HighWatermark > 0 {
					c.Reducer.SetLastSeq(connMeta.HighWatermark)
				}
			} else {
				c.degraded = false
				c.emitState(ConnectionConnected)
				if !*replayComplete {
					*replayComplete = true
					if c.OnReplaySynced != nil && !c.replaySynced {
						c.replaySynced = true
						c.OnReplaySynced()
					}
				}
				continue
			}
			if !connMeta.Gap {
				c.degraded = false
				c.emitState(ConnectionConnected)
				if !*replayComplete {
					*replayComplete = true
					if c.OnReplaySynced != nil && !c.replaySynced {
						c.replaySynced = true
						c.OnReplaySynced()
					}
				}
			}
			continue
		}

		envelope, err := DecodeEventEnvelope([]byte(frame.Data))
		if err != nil {
			if degradeErr := c.markDegradedAndRefresh(ctx, "invalid_envelope", err); degradeErr != nil {
				return degradeErr
			}
			continue
		}

		applied, applyErr := c.Reducer.Apply(envelope)
		if applyErr != nil {
			if errors.Is(applyErr, ErrSequenceGap) {
				if degradeErr := c.handleGap(ctx, envelope, applyErr); degradeErr != nil {
					return degradeErr
				}
				applied, applyErr = c.Reducer.Apply(envelope)
				if applyErr != nil {
					continue
				}
			} else {
				continue
			}
		}

		if c.degraded {
			c.degraded = false
		}
		if !*replayComplete {
			// Fire OnReplaySynced when we've processed up to the high_watermark
			// reported in the connected frame (all replay events delivered).
			// Fall back to firing on first event for connections where the
			// connected frame did not include a high_watermark.
			if c.replayUntilSeq == 0 || envelope.Seq >= c.replayUntilSeq {
				c.replayUntilSeq = 0
				*replayComplete = true
				if c.OnReplaySynced != nil && !c.replaySynced {
					c.replaySynced = true
					c.OnReplaySynced()
				}
			}
		}
		c.emitState(ConnectionConnected)
		if c.OnEvent != nil {
			c.OnEvent(envelope, applied)
		}
	}
}

func (c *RealtimeClient) handleConnectedFrame(ctx context.Context, data string, replayComplete *bool) error {
	var meta struct {
		Gap           bool  `json:"gap"`
		HighWatermark int64 `json:"high_watermark"`
	}
	if err := json.Unmarshal([]byte(data), &meta); err != nil {
		if degradeErr := c.markDegradedAndRefresh(ctx, "invalid_connected_meta", err); degradeErr != nil {
			return degradeErr
		}
		return nil
	}
	if meta.Gap {
		if err := c.markDegradedAndRefresh(ctx, "connected_gap", ErrSequenceGap); err != nil {
			return err
		}
		return nil
	}

	c.degraded = false
	c.emitState(ConnectionConnected)

	// If we are already up-to-date (reconnect with no missed events), fire
	// OnReplaySynced immediately so the TUI enters live mode right away.
	// Otherwise, defer until we process the event at high_watermark so that
	// replayed chat.message.finalized events are handled by the history-loading
	// path (turnsSynced=false) rather than being silently dropped.
	if c.Reducer.LastSeq() >= meta.HighWatermark {
		if !*replayComplete {
			*replayComplete = true
			if c.OnReplaySynced != nil && !c.replaySynced {
				c.replaySynced = true
				c.OnReplaySynced()
			}
		}
	} else {
		c.replayUntilSeq = meta.HighWatermark
	}
	return nil
}

func (c *RealtimeClient) handleGap(ctx context.Context, event EventEnvelope, cause error) error {
	if err := c.markDegradedAndRefresh(ctx, "sequence_gap", cause); err != nil {
		return err
	}
	c.Reducer.SetLastSeq(event.Seq - 1)
	return nil
}

func (c *RealtimeClient) markDegradedAndRefresh(ctx context.Context, reason string, cause error) error {
	c.degraded = true
	c.emitState(ConnectionConnected)
	if c.Logger == nil {
		c.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	c.Logger.Debug("tui realtime stream degraded", "reason", reason, "error", cause)
	return c.refreshSnapshots(ctx)
}

func (c *RealtimeClient) refreshSnapshots(ctx context.Context) error {
	views := []string{"sidebar", "main", "chat"}
	if c.VisibleViews != nil {
		resolved := c.VisibleViews()
		if len(resolved) > 0 {
			views = resolved
		}
	}
	return c.Snapshots.FetchSnapshots(ctx, views)
}

func (c *RealtimeClient) emitState(state ConnectionState) {
	if c.OnStateChange != nil {
		c.OnStateChange(state, c.degraded)
	}
}

type sseFrame struct {
	Event string
	ID    string
	Data  string
}

func readSSEClientFrame(reader *bufio.Reader) (sseFrame, error) {
	frame := sseFrame{}
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			if errors.Is(readErr, io.EOF) && line == "" {
				return sseFrame{}, io.EOF
			}
			if line == "" {
				return sseFrame{}, readErr
			}
		}

		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, ":") {
			if readErr != nil {
				return frame, readErr
			}
			continue
		}
		if line == "" {
			if frame.Event != "" || frame.ID != "" || frame.Data != "" {
				return frame, nil
			}
			if readErr != nil {
				return frame, readErr
			}
			continue
		}

		if strings.HasPrefix(line, "event:") {
			frame.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "id:") {
			frame.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		} else if strings.HasPrefix(line, "data:") {
			chunk := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if frame.Data == "" {
				frame.Data = chunk
			} else {
				frame.Data += "\n" + chunk
			}
		}
		if readErr != nil {
			return frame, readErr
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
