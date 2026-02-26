package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDecodeEventEnvelopeValidation(t *testing.T) {
	valid := map[string]any{
		"seq":         1,
		"event_id":    "evt-1",
		"event_type":  "chat.message.delta",
		"occurred_at": time.Now().UTC().Format(time.RFC3339Nano),
		"org_id":      "org-1",
		"payload":     map[string]any{"message": "hello"},
	}

	tests := []struct {
		name       string
		mutate     func(map[string]any)
		wantErrIs  error
		wantErrStr string
	}{
		{
			name: "missing seq",
			mutate: func(v map[string]any) {
				delete(v, "seq")
			},
			wantErrIs:  ErrEnvelopeValidation,
			wantErrStr: "seq",
		},
		{
			name: "missing event_id",
			mutate: func(v map[string]any) {
				delete(v, "event_id")
			},
			wantErrIs:  ErrEnvelopeValidation,
			wantErrStr: "event_id",
		},
		{
			name: "missing event_type",
			mutate: func(v map[string]any) {
				delete(v, "event_type")
			},
			wantErrIs:  ErrEnvelopeValidation,
			wantErrStr: "event_type",
		},
		{
			name: "missing occurred_at",
			mutate: func(v map[string]any) {
				delete(v, "occurred_at")
			},
			wantErrIs:  ErrEnvelopeValidation,
			wantErrStr: "occurred_at",
		},
		{
			name: "missing org_id",
			mutate: func(v map[string]any) {
				delete(v, "org_id")
			},
			wantErrIs:  ErrEnvelopeValidation,
			wantErrStr: "org_id",
		},
		{
			name: "missing payload",
			mutate: func(v map[string]any) {
				delete(v, "payload")
			},
			wantErrIs:  ErrEnvelopeValidation,
			wantErrStr: "payload",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cloned := map[string]any{}
			for k, v := range valid {
				cloned[k] = v
			}
			tc.mutate(cloned)
			encoded, err := json.Marshal(cloned)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}

			_, err = DecodeEventEnvelope(encoded)
			if err == nil {
				t.Fatalf("DecodeEventEnvelope() error = nil, want error")
			}
			if !errors.Is(err, tc.wantErrIs) {
				t.Fatalf("DecodeEventEnvelope() error = %v, want errors.Is(..., %v)", err, tc.wantErrIs)
			}
			if tc.wantErrStr != "" && !strings.Contains(err.Error(), tc.wantErrStr) {
				t.Fatalf("DecodeEventEnvelope() error = %q, want substring %q", err.Error(), tc.wantErrStr)
			}
		})
	}
}

func TestEventReducerOrderingDedupeAndUnknownEvents(t *testing.T) {
	reducer := NewEventReducer(nil)

	event1 := EventEnvelope{Seq: 1, EventID: "evt-1", EventType: "chat.message.delta", OccurredAt: time.Now().UTC(), OrgID: "org-1", Payload: json.RawMessage(`{"delta":"a"}`)}
	applied, err := reducer.Apply(event1)
	if err != nil {
		t.Fatalf("Apply(event1) error: %v", err)
	}
	if !applied {
		t.Fatal("Apply(event1) applied = false, want true")
	}

	duplicate := EventEnvelope{Seq: 2, EventID: "evt-1", EventType: "chat.message.delta", OccurredAt: time.Now().UTC(), OrgID: "org-1", Payload: json.RawMessage(`{"delta":"b"}`)}
	applied, err = reducer.Apply(duplicate)
	if err != nil {
		t.Fatalf("Apply(duplicate) error: %v", err)
	}
	if applied {
		t.Fatal("Apply(duplicate) applied = true, want false")
	}
	if got := reducer.AppliedCount("chat.message.delta"); got != 1 {
		t.Fatalf("AppliedCount(chat.message.delta) = %d, want 1", got)
	}

	unknown := EventEnvelope{Seq: 3, EventID: "evt-3", EventType: "unknown.event", OccurredAt: time.Now().UTC(), OrgID: "org-1", Payload: json.RawMessage(`{"x":1}`)}
	applied, err = reducer.Apply(unknown)
	if err != nil {
		t.Fatalf("Apply(unknown) error: %v", err)
	}
	if applied {
		t.Fatal("Apply(unknown) applied = true, want false")
	}

	gap := EventEnvelope{Seq: 5, EventID: "evt-5", EventType: "chat.message.delta", OccurredAt: time.Now().UTC(), OrgID: "org-1", Payload: json.RawMessage(`{"delta":"z"}`)}
	_, err = reducer.Apply(gap)
	if err == nil {
		t.Fatal("Apply(gap) error = nil, want sequence gap")
	}
	if !errors.Is(err, ErrSequenceGap) {
		t.Fatalf("Apply(gap) error = %v, want ErrSequenceGap", err)
	}

	event4 := EventEnvelope{Seq: 4, EventID: "evt-4", EventType: "chat.message.delta", OccurredAt: time.Now().UTC(), OrgID: "org-1", Payload: json.RawMessage(`{"delta":"c"}`)}
	applied, err = reducer.Apply(event4)
	if err != nil {
		t.Fatalf("Apply(event4) error: %v", err)
	}
	if !applied {
		t.Fatal("Apply(event4) applied = false, want true")
	}
	if got := reducer.LastSeq(); got != 4 {
		t.Fatalf("LastSeq() = %d, want 4", got)
	}
	if got := reducer.AppliedCount("chat.message.delta"); got != 2 {
		t.Fatalf("AppliedCount(chat.message.delta) = %d, want 2", got)
	}
}

func TestRealtimeConnectedMetaFrameNoGap(t *testing.T) {
	t.Parallel()

	var stateUpdates []ConnectionStateMsg
	replaySyncedCount := 0
	snapshotRefreshCount := 0
	replayComplete := false

	client := &RealtimeClient{
		Reducer: NewEventReducer(nil),
		Snapshots: SnapshotFetcherFunc(func(context.Context, []string) error {
			snapshotRefreshCount++
			return nil
		}),
		OnStateChange: func(state ConnectionState, degraded bool) {
			stateUpdates = append(stateUpdates, ConnectionStateMsg{State: state, Degraded: degraded})
		},
		OnReplaySynced: func() {
			replaySyncedCount++
		},
	}

	stream := io.NopCloser(strings.NewReader("event: connected\ndata: {\"high_watermark\":5}\n\n"))
	err := client.consumeStream(context.Background(), stream, &replayComplete)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("consumeStream() error = %v, want io.EOF", err)
	}

	if snapshotRefreshCount != 0 {
		t.Fatalf("snapshot refreshes = %d, want 0", snapshotRefreshCount)
	}
	if len(stateUpdates) != 1 {
		t.Fatalf("state updates = %d, want 1", len(stateUpdates))
	}
	if stateUpdates[0].State != ConnectionConnected || stateUpdates[0].Degraded {
		t.Fatalf("state update = %+v, want connected/degraded=false", stateUpdates[0])
	}
	if replaySyncedCount != 1 {
		t.Fatalf("replay synced count = %d, want 1", replaySyncedCount)
	}
	if !replayComplete {
		t.Fatal("replayComplete = false, want true")
	}
	if client.degraded {
		t.Fatal("client.degraded = true, want false")
	}
}

func TestRealtimeConnectedMetaFrameWithGapDegrades(t *testing.T) {
	t.Parallel()

	var stateUpdates []ConnectionStateMsg
	replaySyncedCount := 0
	snapshotRefreshCount := 0
	replayComplete := false

	client := &RealtimeClient{
		Reducer: NewEventReducer(nil),
		Snapshots: SnapshotFetcherFunc(func(context.Context, []string) error {
			snapshotRefreshCount++
			return nil
		}),
		OnStateChange: func(state ConnectionState, degraded bool) {
			stateUpdates = append(stateUpdates, ConnectionStateMsg{State: state, Degraded: degraded})
		},
		OnReplaySynced: func() {
			replaySyncedCount++
		},
	}

	stream := io.NopCloser(strings.NewReader(fmt.Sprintf(
		"event: connected\ndata: {\"high_watermark\":10,\"gap\":true}\n\n%s",
		mustEncodeEnvelopeFrameForTest(t, 1, "evt-1", "chat.message.delta"),
	)))
	err := client.consumeStream(context.Background(), stream, &replayComplete)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("consumeStream() error = %v, want io.EOF", err)
	}

	if snapshotRefreshCount != 1 {
		t.Fatalf("snapshot refreshes = %d, want 1", snapshotRefreshCount)
	}
	if !containsRealtimeState(stateUpdates, true) {
		t.Fatalf("state updates = %+v, want at least one degraded state", stateUpdates)
	}
	if !containsRealtimeState(stateUpdates, false) {
		t.Fatalf("state updates = %+v, want degraded=false after first good event", stateUpdates)
	}
	if replaySyncedCount != 1 {
		t.Fatalf("replay synced count = %d, want 1", replaySyncedCount)
	}
	if !replayComplete {
		t.Fatal("replayComplete = false, want true")
	}
	if client.degraded {
		t.Fatal("client.degraded = true, want false")
	}
	if got := client.Reducer.LastSeq(); got != 1 {
		t.Fatalf("Reducer.LastSeq() = %d, want 1", got)
	}
}

func mustEncodeEnvelopeFrameForTest(t *testing.T, seq int64, eventID, eventType string) string {
	t.Helper()
	envelope := map[string]any{
		"seq":         seq,
		"event_id":    eventID,
		"event_type":  eventType,
		"occurred_at": time.Unix(seq, 0).UTC().Format(time.RFC3339Nano),
		"org_id":      "org-1",
		"payload":     map[string]any{"delta": "ok"},
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return fmt.Sprintf("event: %s\nid: %d\ndata: %s\n\n", eventType, seq, string(encoded))
}

func containsRealtimeState(states []ConnectionStateMsg, degraded bool) bool {
	for _, state := range states {
		if state.Degraded == degraded {
			return true
		}
	}
	return false
}
