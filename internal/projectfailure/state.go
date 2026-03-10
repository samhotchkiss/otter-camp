package projectfailure

import (
	"encoding/json"
	"strings"
	"time"
)

const settingsKey = "automatic_failure"

type State struct {
	Action          string
	Source          string
	FailureCategory string
	FailureClass    string
	FailurePhase    string
	LastCheckpoint  string
	FailureReason   string
	RecordedAt      *time.Time
}

func Parse(settings json.RawMessage) State {
	state := State{}
	payload := parseSettings(settings)
	raw, ok := payload[settingsKey]
	if !ok {
		return state
	}

	var decoded struct {
		Action          string     `json:"action"`
		Source          string     `json:"source"`
		FailureCategory string     `json:"failure_category"`
		FailureClass    string     `json:"failure_class"`
		FailurePhase    string     `json:"failure_phase"`
		LastCheckpoint  string     `json:"last_checkpoint"`
		FailureReason   string     `json:"failure_reason"`
		RecordedAt      *time.Time `json:"recorded_at"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return state
	}

	state.Action = strings.TrimSpace(decoded.Action)
	state.Source = strings.TrimSpace(decoded.Source)
	state.FailureCategory = strings.TrimSpace(decoded.FailureCategory)
	state.FailureClass = strings.TrimSpace(decoded.FailureClass)
	state.FailurePhase = strings.TrimSpace(decoded.FailurePhase)
	state.LastCheckpoint = strings.TrimSpace(decoded.LastCheckpoint)
	state.FailureReason = strings.TrimSpace(decoded.FailureReason)
	state.RecordedAt = decoded.RecordedAt
	return state
}

func Apply(settings json.RawMessage, state State) (json.RawMessage, error) {
	payload := parseSettings(settings)
	payload[settingsKey] = mustMarshalJSON(map[string]any{
		"action":           strings.TrimSpace(state.Action),
		"source":           strings.TrimSpace(state.Source),
		"failure_category": strings.TrimSpace(state.FailureCategory),
		"failure_class":    strings.TrimSpace(state.FailureClass),
		"failure_phase":    strings.TrimSpace(state.FailurePhase),
		"last_checkpoint":  strings.TrimSpace(state.LastCheckpoint),
		"failure_reason":   strings.TrimSpace(state.FailureReason),
		"recorded_at":      state.RecordedAt,
	})
	return marshalSettings(payload)
}

func (s State) JSON() json.RawMessage {
	encoded, err := json.Marshal(map[string]any{
		"action":           strings.TrimSpace(s.Action),
		"source":           strings.TrimSpace(s.Source),
		"failure_category": strings.TrimSpace(s.FailureCategory),
		"failure_class":    strings.TrimSpace(s.FailureClass),
		"failure_phase":    strings.TrimSpace(s.FailurePhase),
		"last_checkpoint":  strings.TrimSpace(s.LastCheckpoint),
		"failure_reason":   strings.TrimSpace(s.FailureReason),
		"recorded_at":      s.RecordedAt,
	})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(encoded)
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

func mustMarshalJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(encoded)
}
