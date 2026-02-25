package tui

import (
	"encoding/json"
	"errors"
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
