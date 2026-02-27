//go:build integration

package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDegradedModeBannerShowsRecoveryGuidance(t *testing.T) {
	t.Parallel()

	model := NewModel(DefaultState())
	model = pressRealtimeMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model = pressRealtimeMsg(model, ConnectionStateMsg{State: ConnectionDisconnected, Degraded: true})

	view := model.View()
	if !strings.Contains(view, "DEGRADED MODE") {
		t.Fatalf("view missing degraded banner: %q", view)
	}
	// EX-100: message changed to be context-aware; disconnected shows "Reconnecting".
	if !strings.Contains(view, "Reconnecting") && !strings.Contains(view, "stale") {
		t.Fatalf("view missing degraded actionable message: %q", view)
	}
	for _, marker := range []string{"Main state:", "Chat state:", "Sidebar state:"} {
		if strings.Contains(view, marker) {
			t.Fatalf("view contains debug panel state marker %q: %q", marker, view)
		}
	}

	model = pressRealtimeMsg(model, ConnectionStateMsg{State: ConnectionConnected, Degraded: false})
	if strings.Contains(model.View(), "DEGRADED MODE") {
		t.Fatalf("degraded banner should clear after recovery: %q", model.View())
	}
}

func TestRealtimeClientExtendedReplayStability(t *testing.T) {
	t.Parallel()

	server := newScriptedSSEServer(t, []scriptedResponse{
		{frames: synthFrames(1, 80)},
		{frames: synthFrames(81, 160)},
		{frames: synthFrames(161, 240)},
	})
	defer server.Close()

	reducer := NewEventReducer(nil)
	applied := 0
	replaySyncedSignals := 0

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	client := &RealtimeClient{
		Connector: HTTPSSEConnector{URL: server.URL()},
		Snapshots: SnapshotFetcherFunc(func(context.Context, []string) error { return nil }),
		Reducer:   reducer,
		Backoff:   []time.Duration{10 * time.Millisecond},
		OnReplaySynced: func() {
			replaySyncedSignals++
		},
		OnEvent: func(event EventEnvelope, wasApplied bool) {
			if wasApplied {
				applied++
			}
			if event.Seq == 240 {
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

	if got := reducer.LastSeq(); got != 240 {
		t.Fatalf("LastSeq() = %d, want 240", got)
	}
	if applied != 240 {
		t.Fatalf("applied events = %d, want 240", applied)
	}
	if replaySyncedSignals != 1 {
		t.Fatalf("replay synced signals = %d, want 1", replaySyncedSignals)
	}
}

func TestTUIPerformanceBudgets(t *testing.T) {
	t.Parallel()

	clock := &integrationStepClock{
		now:  time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC),
		step: 40 * time.Millisecond,
	}
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		Clock:                       clock.Now,
		MemorySteadyStateBoundBytes: 512 * 1024 * 1024,
		DisableMemorySampler:        true,
	})

	model = pressRealtimeMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model = pressRealtimeMsg(model, ConnectionStateMsg{State: ConnectionConnected})
	model = pressRealtimeMsg(model, tea.KeyMsg{Type: tea.KeyTab})

	deltaPayload := map[string]string{"message_id": "msg-perf", "role": "assistant", "delta": "ok"}
	rawPayload, _ := json.Marshal(deltaPayload)
	model = pressRealtimeMsg(model, ChatEnvelopeMsg{Envelope: EventEnvelope{
		Seq:        1,
		EventID:    "evt-perf-1",
		EventType:  "chat.message.delta",
		OccurredAt: clock.Peek().Add(-80 * time.Millisecond),
		OrgID:      "org-1",
		Payload:    rawPayload,
	}})
	model = pressRealtimeMsg(model, memorySampleMsg{})

	metrics := model.PerformanceMetrics()
	if metrics.InitialInteractivePaint <= 0 || metrics.InitialInteractivePaint > 1200*time.Millisecond {
		t.Fatalf("initial paint latency out of bounds: %v", metrics.InitialInteractivePaint)
	}
	if metrics.KeypressToVisible <= 0 || metrics.KeypressToVisible > keypressLatencyBudget {
		t.Fatalf("keypress latency out of bounds: %v", metrics.KeypressToVisible)
	}
	if metrics.SSEDeltaRenderLatency <= 0 || metrics.SSEDeltaRenderLatency > sseRenderLatencyBudget {
		t.Fatalf("sse render latency out of bounds: %v", metrics.SSEDeltaRenderLatency)
	}
	if metrics.PeakMemoryBytes == 0 {
		t.Fatal("peak memory sample should be captured")
	}
	if failures := model.QualityGateFailures(); len(failures) > 0 {
		t.Fatalf("quality gate failures: %v", failures)
	}
}

func synthFrames(start, end int64) []string {
	frames := make([]string, 0, end-start+1)
	for seq := start; seq <= end; seq++ {
		frames = append(frames, encodeEnvelopeFrame(seq, fmt.Sprintf("evt-%d", seq), "chat.message.delta", map[string]any{"message_id": "msg", "role": "assistant", "delta": "x"}))
	}
	return frames
}

type integrationStepClock struct {
	now  time.Time
	step time.Duration
}

func (c *integrationStepClock) Now() time.Time {
	value := c.now
	c.now = c.now.Add(c.step)
	return value
}

func (c *integrationStepClock) Peek() time.Time {
	return c.now
}
