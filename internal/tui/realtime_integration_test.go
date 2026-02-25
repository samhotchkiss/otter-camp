//go:build integration

package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestRealtimeClientReconnectsWithSinceSeqReplay(t *testing.T) {
	t.Parallel()

	server := newScriptedSSEServer(t, []scriptedResponse{
		{frames: []string{encodeEnvelopeFrame(1, "evt-1", "chat.message.delta", map[string]any{"delta": "a"}), encodeEnvelopeFrame(2, "evt-2", "chat.message.delta", map[string]any{"delta": "b"})}},
		{frames: []string{encodeEnvelopeFrame(3, "evt-3", "chat.message.delta", map[string]any{"delta": "c"})}},
	})
	defer server.Close()

	reducer := NewEventReducer(nil)
	var fetches int
	var applied []int64

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := &RealtimeClient{
		Connector: HTTPSSEConnector{URL: server.URL()},
		Snapshots: SnapshotFetcherFunc(func(context.Context, []string) error {
			fetches++
			return nil
		}),
		Reducer: reducer,
		Backoff: []time.Duration{10 * time.Millisecond},
		OnEvent: func(event EventEnvelope, appliedEvent bool) {
			if appliedEvent {
				applied = append(applied, event.Seq)
			}
			if event.Seq == 3 {
				cancel()
			}
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	if err := <-done; err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	since := server.SinceSeqs()
	if len(since) < 2 {
		t.Fatalf("connect attempts = %d, want at least 2", len(since))
	}
	if since[0] != 0 || since[1] != 2 {
		t.Fatalf("since_seq values = %v, want prefix [0 2]", since)
	}
	if got := reducer.LastSeq(); got != 3 {
		t.Fatalf("LastSeq() = %d, want 3", got)
	}
	if len(applied) != 3 {
		t.Fatalf("applied seqs = %v, want three applied events", applied)
	}
	if fetches < 2 {
		t.Fatalf("snapshot fetches = %d, want at least 2 (startup + reconnect)", fetches)
	}
}

func TestRealtimeClientGapRecoveryRefreshesAndRecovers(t *testing.T) {
	t.Parallel()

	server := newScriptedSSEServer(t, []scriptedResponse{
		{frames: []string{encodeEnvelopeFrame(1, "evt-1", "chat.message.delta", map[string]any{"delta": "a"})}},
		{frames: []string{encodeEnvelopeFrame(3, "evt-3", "chat.message.delta", map[string]any{"delta": "c"}), encodeEnvelopeFrame(4, "evt-4", "chat.message.delta", map[string]any{"delta": "d"})}},
	})
	defer server.Close()

	reducer := NewEventReducer(nil)
	var fetches int
	var states []ConnectionStateMsg
	var mu sync.Mutex

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := &RealtimeClient{
		Connector: HTTPSSEConnector{URL: server.URL()},
		Snapshots: SnapshotFetcherFunc(func(context.Context, []string) error {
			fetches++
			return nil
		}),
		Reducer: reducer,
		Backoff: []time.Duration{10 * time.Millisecond},
		OnStateChange: func(state ConnectionState, degraded bool) {
			mu.Lock()
			states = append(states, ConnectionStateMsg{State: state, Degraded: degraded})
			mu.Unlock()
		},
		OnEvent: func(event EventEnvelope, _ bool) {
			if event.Seq == 4 {
				cancel()
			}
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	if err := <-done; err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if got := reducer.LastSeq(); got != 4 {
		t.Fatalf("LastSeq() = %d, want 4 after gap recovery", got)
	}
	if fetches < 3 {
		t.Fatalf("snapshot fetches = %d, want at least 3 (startup + reconnect + gap refresh)", fetches)
	}

	mu.Lock()
	defer mu.Unlock()
	if !containsDegradedState(states, true) {
		t.Fatalf("states=%v, want degraded=true state", states)
	}
	if !containsDegradedState(states, false) {
		t.Fatalf("states=%v, want degraded=false state after recovery", states)
	}
}

func TestRealtimeConnectionStateReflectedInModelStatus(t *testing.T) {
	t.Parallel()

	server := newScriptedSSEServer(t, []scriptedResponse{
		{status: http.StatusServiceUnavailable},
		{frames: []string{encodeEnvelopeFrame(1, "evt-1", "chat.message.delta", map[string]any{"delta": "a"})}},
	})
	defer server.Close()

	model := NewModel(DefaultState())
	reducer := NewEventReducer(nil)
	views := make([]string, 0, 4)
	var mu sync.Mutex

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := &RealtimeClient{
		Connector: HTTPSSEConnector{URL: server.URL()},
		Snapshots: SnapshotFetcherFunc(func(context.Context, []string) error { return nil }),
		Reducer:   reducer,
		Backoff:   []time.Duration{10 * time.Millisecond},
		OnStateChange: func(state ConnectionState, degraded bool) {
			mu.Lock()
			model = pressMsg(model, ConnectionStateMsg{State: state, Degraded: degraded})
			views = append(views, model.View())
			mu.Unlock()
		},
		OnEvent: func(event EventEnvelope, _ bool) {
			if event.Seq == 1 {
				cancel()
			}
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	if err := <-done; err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(views, "\n")
	if !strings.Contains(joined, "Realtime=reconnecting") {
		t.Fatalf("status views missing reconnecting state: %q", joined)
	}
	if !strings.Contains(joined, "Realtime=connected") {
		t.Fatalf("status views missing connected state: %q", joined)
	}
}

type scriptedResponse struct {
	status int
	frames []string
}

type scriptedSSEServer struct {
	t       *testing.T
	server  *httptest.Server
	scripts []scriptedResponse

	mu      sync.Mutex
	calls   int
	sinceQs []int64
}

func newScriptedSSEServer(t *testing.T, scripts []scriptedResponse) *scriptedSSEServer {
	t.Helper()
	fixture := &scriptedSSEServer{t: t, scripts: scripts}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.handle))
	return fixture
}

func (s *scriptedSSEServer) handle(w http.ResponseWriter, r *http.Request) {
	s.t.Helper()
	sinceRaw := strings.TrimSpace(r.URL.Query().Get("since_seq"))
	since := int64(0)
	if sinceRaw != "" {
		parsed, err := strconv.ParseInt(sinceRaw, 10, 64)
		if err != nil {
			s.t.Fatalf("invalid since_seq %q: %v", sinceRaw, err)
		}
		since = parsed
	}

	s.mu.Lock()
	callIndex := s.calls
	s.calls++
	s.sinceQs = append(s.sinceQs, since)
	response := scriptedResponse{}
	if len(s.scripts) > 0 {
		response = s.scripts[minInt(callIndex, len(s.scripts)-1)]
	}
	s.mu.Unlock()

	status := response.status
	if status == 0 {
		status = http.StatusOK
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":"unavailable"}`)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, frame := range response.frames {
		_, _ = io.WriteString(w, frame)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (s *scriptedSSEServer) Close() {
	s.server.Close()
}

func (s *scriptedSSEServer) URL() string {
	return s.server.URL
}

func (s *scriptedSSEServer) SinceSeqs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, len(s.sinceQs))
	copy(out, s.sinceQs)
	return out
}

func encodeEnvelopeFrame(seq int64, eventID, eventType string, payload map[string]any) string {
	envelope := map[string]any{
		"seq":         seq,
		"event_id":    eventID,
		"event_type":  eventType,
		"occurred_at": time.Unix(seq, 0).UTC().Format(time.RFC3339Nano),
		"org_id":      "org-1",
		"payload":     payload,
	}
	encoded, _ := json.Marshal(envelope)
	return fmt.Sprintf("event: %s\nid: %d\ndata: %s\n\n", eventType, seq, string(encoded))
}

func containsDegradedState(states []ConnectionStateMsg, degraded bool) bool {
	for _, state := range states {
		if state.Degraded == degraded {
			return true
		}
	}
	return false
}

func pressMsg(model Model, msg tea.Msg) Model {
	updated, _ := model.Update(msg)
	next, ok := updated.(Model)
	if !ok {
		panic("unexpected model type")
	}
	return next
}
