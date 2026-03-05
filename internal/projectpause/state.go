package projectpause

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const settingsKey = "pause"

var ErrProjectPaused = errors.New("project is paused")

type State struct {
	IsPaused     bool
	Reason       string
	Metadata     json.RawMessage
	PausedAt     *time.Time
	PausedByType string
	PausedByID   *uuid.UUID
}

type pausedError struct {
	reason string
}

func (e pausedError) Error() string {
	if strings.TrimSpace(e.reason) == "" {
		return ErrProjectPaused.Error()
	}
	return fmt.Sprintf("%s: %s", ErrProjectPaused.Error(), strings.TrimSpace(e.reason))
}

func (e pausedError) Is(target error) bool {
	return target == ErrProjectPaused
}

func NewError(reason string) error {
	return pausedError{reason: strings.TrimSpace(reason)}
}

func Parse(settings json.RawMessage) State {
	state := State{Metadata: json.RawMessage(`{}`)}
	payload := parseSettings(settings)
	rawPause, ok := payload[settingsKey]
	if !ok {
		return state
	}

	var pause struct {
		IsPaused     bool            `json:"is_paused"`
		Reason       string          `json:"reason"`
		Metadata     json.RawMessage `json:"metadata"`
		PausedAt     *time.Time      `json:"paused_at"`
		PausedByType string          `json:"paused_by_type"`
		PausedByID   *uuid.UUID      `json:"paused_by_id"`
	}
	if err := json.Unmarshal(rawPause, &pause); err != nil {
		return state
	}

	state.IsPaused = pause.IsPaused
	state.Reason = strings.TrimSpace(pause.Reason)
	state.Metadata = normalizeMetadata(pause.Metadata)
	state.PausedAt = pause.PausedAt
	state.PausedByType = strings.TrimSpace(pause.PausedByType)
	state.PausedByID = pause.PausedByID
	return state
}

func ApplyPause(settings json.RawMessage, reason string, metadata json.RawMessage, now time.Time, actorType string, actorID uuid.UUID) (json.RawMessage, error) {
	payload := parseSettings(settings)
	pausePayload := map[string]any{
		"is_paused": true,
		"reason":    strings.TrimSpace(reason),
		"metadata":  asJSONObject(metadata),
		"paused_at": now.UTC(),
	}
	if trimmed := strings.TrimSpace(actorType); trimmed != "" {
		pausePayload["paused_by_type"] = trimmed
	}
	if actorID != uuid.Nil {
		pausePayload["paused_by_id"] = actorID.String()
	}

	payload[settingsKey] = mustMarshalJSON(pausePayload)
	return marshalSettings(payload)
}

func ClearPause(settings json.RawMessage) (json.RawMessage, error) {
	payload := parseSettings(settings)
	delete(payload, settingsKey)
	return marshalSettings(payload)
}

func parseSettings(settings json.RawMessage) map[string]json.RawMessage {
	if len(settings) == 0 {
		return map[string]json.RawMessage{}
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(settings, &payload); err != nil || payload == nil {
		return map[string]json.RawMessage{}
	}
	return payload
}

func marshalSettings(payload map[string]json.RawMessage) (json.RawMessage, error) {
	if payload == nil {
		payload = map[string]json.RawMessage{}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(encoded) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(encoded), nil
}

func normalizeMetadata(metadata json.RawMessage) json.RawMessage {
	if len(metadata) == 0 || string(metadata) == "null" {
		return json.RawMessage(`{}`)
	}
	return metadata
}

func asJSONObject(metadata json.RawMessage) json.RawMessage {
	normalized := normalizeMetadata(metadata)
	var decoded map[string]any
	if err := json.Unmarshal(normalized, &decoded); err != nil || decoded == nil {
		return json.RawMessage(`{}`)
	}
	return mustMarshalJSON(decoded)
}

func mustMarshalJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(encoded)
}
