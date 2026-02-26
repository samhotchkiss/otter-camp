//go:build integration

package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestRealtimeSyntheticSoakNoPanic(t *testing.T) {
	if os.Getenv("OTTERCAMP_TUI_RUN_SOAK") != "1" {
		t.Skip("set OTTERCAMP_TUI_RUN_SOAK=1 to execute soak run")
	}

	duration := 60 * time.Minute
	if raw := os.Getenv("OTTERCAMP_TUI_SOAK_DURATION"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatalf("invalid OTTERCAMP_TUI_SOAK_DURATION: %v", err)
		}
		duration = parsed
	}

	model := NewModelWithRuntime(DefaultState(), RuntimeHints{DisableMemorySampler: true})
	model = pressRealtimeMsg(model, ConnectionStateMsg{State: ConnectionConnected})

	started := time.Now()
	seq := int64(1)
	for time.Since(started) < duration {
		payload, _ := json.Marshal(map[string]string{"message_id": "soak-msg", "role": "assistant", "delta": "tick"})
		model = pressRealtimeMsg(model, ChatEnvelopeMsg{Envelope: EventEnvelope{
			Seq:        seq,
			EventID:    fmt.Sprintf("soak-%d", seq),
			EventType:  "chat.message.delta",
			OccurredAt: time.Now().UTC(),
			OrgID:      "org-1",
			Payload:    payload,
		}})
		seq++
		if seq%1000 == 0 {
			model = pressRealtimeMsg(model, memorySampleMsg{})
		}
	}

	if got := model.PerformanceMetrics().PeakMemoryBytes; got == 0 {
		t.Fatal("expected non-zero memory sample during soak")
	}
}
