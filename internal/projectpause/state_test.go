package projectpause

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestParseDefaultsForNilEmptyNullAndInvalidSettings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		settings json.RawMessage
	}{
		{name: "nil"},
		{name: "empty bytes", settings: json.RawMessage{}},
		{name: "empty object", settings: json.RawMessage(`{}`)},
		{name: "null", settings: json.RawMessage(`null`)},
		{name: "invalid top level", settings: json.RawMessage(`{`)},
		{name: "invalid pause payload", settings: json.RawMessage(`{"pause":"broken"}`)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			state := Parse(tc.settings)
			if state.IsPaused {
				t.Fatal("IsPaused = true, want false")
			}
			if state.Reason != "" {
				t.Fatalf("Reason = %q, want empty", state.Reason)
			}
			assertJSONEqual(t, state.Metadata, json.RawMessage(`{}`))
			if state.PausedAt != nil {
				t.Fatalf("PausedAt = %v, want nil", state.PausedAt)
			}
			if state.PausedByType != "" {
				t.Fatalf("PausedByType = %q, want empty", state.PausedByType)
			}
			if state.PausedByID != nil {
				t.Fatalf("PausedByID = %v, want nil", *state.PausedByID)
			}
		})
	}
}

func TestParseValidSettings(t *testing.T) {
	t.Parallel()

	actorID := uuid.New()
	pausedAt := time.Date(2026, time.March, 5, 12, 34, 56, 0, time.UTC)
	settings := json.RawMessage(`{
		"pause": {
			"is_paused": true,
			"reason": "  maintenance window  ",
			"metadata": {"source": "operator", "ticket": "OPS-42"},
			"paused_at": "` + pausedAt.Format(time.RFC3339Nano) + `",
			"paused_by_type": "  user  ",
			"paused_by_id": "` + actorID.String() + `"
		}
	}`)

	state := Parse(settings)
	if !state.IsPaused {
		t.Fatal("IsPaused = false, want true")
	}
	if state.Reason != "maintenance window" {
		t.Fatalf("Reason = %q, want maintenance window", state.Reason)
	}
	assertJSONEqual(t, state.Metadata, json.RawMessage(`{"source":"operator","ticket":"OPS-42"}`))
	if state.PausedAt == nil || !state.PausedAt.Equal(pausedAt) {
		t.Fatalf("PausedAt = %v, want %s", state.PausedAt, pausedAt.Format(time.RFC3339Nano))
	}
	if state.PausedByType != "user" {
		t.Fatalf("PausedByType = %q, want user", state.PausedByType)
	}
	if state.PausedByID == nil || *state.PausedByID != actorID {
		t.Fatalf("PausedByID = %v, want %s", state.PausedByID, actorID)
	}
}

func TestApplyPauseRoundTrip(t *testing.T) {
	t.Parallel()

	actorID := uuid.New()
	now := time.Date(2026, time.March, 5, 8, 15, 0, 0, time.FixedZone("MST", -7*60*60))

	settings, err := ApplyPause(
		json.RawMessage(`{"existing":{"enabled":true}}`),
		"  maintenance window  ",
		json.RawMessage(`{"source":"operator","ticket":"OPS-42"}`),
		now,
		"  user  ",
		actorID,
	)
	if err != nil {
		t.Fatalf("ApplyPause: %v", err)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(settings, &payload); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if _, ok := payload["existing"]; !ok {
		t.Fatalf("settings = %s, want existing key preserved", string(settings))
	}

	state := Parse(settings)
	if !state.IsPaused {
		t.Fatal("IsPaused = false, want true")
	}
	if state.Reason != "maintenance window" {
		t.Fatalf("Reason = %q, want maintenance window", state.Reason)
	}
	assertJSONEqual(t, state.Metadata, json.RawMessage(`{"source":"operator","ticket":"OPS-42"}`))
	if state.PausedAt == nil || !state.PausedAt.Equal(now.UTC()) {
		t.Fatalf("PausedAt = %v, want %s", state.PausedAt, now.UTC().Format(time.RFC3339Nano))
	}
	if state.PausedByType != "user" {
		t.Fatalf("PausedByType = %q, want user", state.PausedByType)
	}
	if state.PausedByID == nil || *state.PausedByID != actorID {
		t.Fatalf("PausedByID = %v, want %s", state.PausedByID, actorID)
	}
}

func TestApplyPauseRoundTripHandlesEmptyReasonAndNilMetadata(t *testing.T) {
	t.Parallel()

	settings, err := ApplyPause(nil, "   ", nil, time.Date(2026, time.March, 5, 17, 0, 0, 0, time.UTC), "", uuid.Nil)
	if err != nil {
		t.Fatalf("ApplyPause: %v", err)
	}

	state := Parse(settings)
	if !state.IsPaused {
		t.Fatal("IsPaused = false, want true")
	}
	if state.Reason != "" {
		t.Fatalf("Reason = %q, want empty", state.Reason)
	}
	assertJSONEqual(t, state.Metadata, json.RawMessage(`{}`))
	if state.PausedByType != "" {
		t.Fatalf("PausedByType = %q, want empty", state.PausedByType)
	}
	if state.PausedByID != nil {
		t.Fatalf("PausedByID = %v, want nil", *state.PausedByID)
	}
}

func TestClearPauseIsIdempotent(t *testing.T) {
	t.Parallel()

	pausedSettings, err := ApplyPause(
		json.RawMessage(`{"existing":{"enabled":true}}`),
		"maintenance window",
		json.RawMessage(`{"source":"operator"}`),
		time.Date(2026, time.March, 5, 17, 0, 0, 0, time.UTC),
		"user",
		uuid.New(),
	)
	if err != nil {
		t.Fatalf("ApplyPause: %v", err)
	}

	cleared, err := ClearPause(pausedSettings)
	if err != nil {
		t.Fatalf("ClearPause first call: %v", err)
	}
	assertPauseCleared(t, cleared)

	clearedAgain, err := ClearPause(cleared)
	if err != nil {
		t.Fatalf("ClearPause second call: %v", err)
	}
	assertPauseCleared(t, clearedAgain)
	assertJSONEqual(t, clearedAgain, cleared)
}

func TestNewErrorWrapsErrProjectPaused(t *testing.T) {
	t.Parallel()

	err := NewError("  maintenance window  ")
	if !errors.Is(err, ErrProjectPaused) {
		t.Fatalf("errors.Is(err, ErrProjectPaused) = false, want true")
	}
	if err.Error() != "project is paused: maintenance window" {
		t.Fatalf("Error() = %q, want project is paused: maintenance window", err.Error())
	}

	blank := NewError("   ")
	if !errors.Is(blank, ErrProjectPaused) {
		t.Fatalf("errors.Is(blank, ErrProjectPaused) = false, want true")
	}
	if blank.Error() != ErrProjectPaused.Error() {
		t.Fatalf("blank Error() = %q, want %q", blank.Error(), ErrProjectPaused.Error())
	}
}

func assertPauseCleared(t *testing.T, settings json.RawMessage) {
	t.Helper()

	state := Parse(settings)
	if state.IsPaused {
		t.Fatal("IsPaused = true, want false")
	}
	if state.Reason != "" {
		t.Fatalf("Reason = %q, want empty", state.Reason)
	}
	assertJSONEqual(t, state.Metadata, json.RawMessage(`{}`))

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(settings, &payload); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if _, ok := payload["pause"]; ok {
		t.Fatalf("settings = %s, want pause key removed", string(settings))
	}
	if _, ok := payload["existing"]; !ok {
		t.Fatalf("settings = %s, want existing key preserved", string(settings))
	}
}

func assertJSONEqual(t *testing.T, got, want json.RawMessage) {
	t.Helper()

	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("unmarshal got JSON %q: %v", string(got), err)
	}
	var wantValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("unmarshal want JSON %q: %v", string(want), err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON = %s, want %s", string(got), string(want))
	}
}
