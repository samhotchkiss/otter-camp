package projectfailure

import (
	"encoding/json"
	"strings"
	"time"
)

const settingsKey = "automatic_failure"

type FailureHistoryEntry struct {
	ProjectID                string     `json:"project_id,omitempty"`
	RetryAttemptCount        int        `json:"retry_attempt_count,omitempty"`
	FailureCategory          string     `json:"failure_category,omitempty"`
	FailureClass             string     `json:"failure_class,omitempty"`
	FailurePhase             string     `json:"failure_phase,omitempty"`
	LastCheckpoint           string     `json:"last_checkpoint,omitempty"`
	LastSuccessfulCheckpoint string     `json:"last_successful_checkpoint,omitempty"`
	FailureReason            string     `json:"failure_reason,omitempty"`
	SetupPersisted           bool       `json:"setup_persisted,omitempty"`
	RecordedAt               *time.Time `json:"recorded_at,omitempty"`
}

type State struct {
	Action                   string
	Source                   string
	FailureCategory          string
	FailureClass             string
	FailurePhase             string
	LastCheckpoint           string
	LastSuccessfulCheckpoint string
	FailureReason            string
	SetupPersisted           bool
	RetryBudget              int
	RetryAttemptCount        int
	FailureHistory           []FailureHistoryEntry
	RecordedAt               *time.Time
}

func Parse(settings json.RawMessage) State {
	state := State{}
	payload := parseSettings(settings)
	raw, ok := payload[settingsKey]
	if !ok {
		return state
	}

	var decoded struct {
		Action                   string                `json:"action"`
		Source                   string                `json:"source"`
		FailureCategory          string                `json:"failure_category"`
		FailureClass             string                `json:"failure_class"`
		FailurePhase             string                `json:"failure_phase"`
		LastCheckpoint           string                `json:"last_checkpoint"`
		LastSuccessfulCheckpoint string                `json:"last_successful_checkpoint"`
		FailureReason            string                `json:"failure_reason"`
		SetupPersisted           bool                  `json:"setup_persisted"`
		RetryBudget              int                   `json:"retry_budget"`
		RetryAttemptCount        int                   `json:"retry_attempt_count"`
		FailureHistory           []FailureHistoryEntry `json:"failure_history"`
		RecordedAt               *time.Time            `json:"recorded_at"`
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
	state.LastSuccessfulCheckpoint = strings.TrimSpace(decoded.LastSuccessfulCheckpoint)
	state.FailureReason = strings.TrimSpace(decoded.FailureReason)
	state.SetupPersisted = decoded.SetupPersisted
	if decoded.RetryBudget > 0 {
		state.RetryBudget = decoded.RetryBudget
	}
	if decoded.RetryAttemptCount > 0 {
		state.RetryAttemptCount = decoded.RetryAttemptCount
	}
	state.FailureHistory = normalizeFailureHistory(decoded.FailureHistory)
	state.RecordedAt = decoded.RecordedAt
	return state
}

func Apply(settings json.RawMessage, state State) (json.RawMessage, error) {
	payload := parseSettings(settings)
	payload[settingsKey] = mustMarshalJSON(map[string]any{
		"action":                     strings.TrimSpace(state.Action),
		"source":                     strings.TrimSpace(state.Source),
		"failure_category":           strings.TrimSpace(state.FailureCategory),
		"failure_class":              strings.TrimSpace(state.FailureClass),
		"failure_phase":              strings.TrimSpace(state.FailurePhase),
		"last_checkpoint":            strings.TrimSpace(state.LastCheckpoint),
		"last_successful_checkpoint": strings.TrimSpace(state.LastSuccessfulCheckpoint),
		"failure_reason":             strings.TrimSpace(state.FailureReason),
		"setup_persisted":            state.SetupPersisted,
		"retry_budget":               state.RetryBudget,
		"retry_attempt_count":        state.RetryAttemptCount,
		"failure_history":            normalizeFailureHistory(state.FailureHistory),
		"recorded_at":                state.RecordedAt,
	})
	return marshalSettings(payload)
}

func (s State) JSON() json.RawMessage {
	encoded, err := json.Marshal(map[string]any{
		"action":                     strings.TrimSpace(s.Action),
		"source":                     strings.TrimSpace(s.Source),
		"failure_category":           strings.TrimSpace(s.FailureCategory),
		"failure_class":              strings.TrimSpace(s.FailureClass),
		"failure_phase":              strings.TrimSpace(s.FailurePhase),
		"last_checkpoint":            strings.TrimSpace(s.LastCheckpoint),
		"last_successful_checkpoint": strings.TrimSpace(s.LastSuccessfulCheckpoint),
		"failure_reason":             strings.TrimSpace(s.FailureReason),
		"setup_persisted":            s.SetupPersisted,
		"retry_budget":               s.RetryBudget,
		"retry_attempt_count":        s.RetryAttemptCount,
		"failure_history":            normalizeFailureHistory(s.FailureHistory),
		"recorded_at":                s.RecordedAt,
	})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(encoded)
}

func normalizeFailureHistory(entries []FailureHistoryEntry) []FailureHistoryEntry {
	out := make([]FailureHistoryEntry, 0, len(entries))
	for _, entry := range entries {
		normalized := FailureHistoryEntry{
			ProjectID:                strings.TrimSpace(entry.ProjectID),
			RetryAttemptCount:        entry.RetryAttemptCount,
			FailureCategory:          strings.TrimSpace(entry.FailureCategory),
			FailureClass:             strings.TrimSpace(entry.FailureClass),
			FailurePhase:             strings.TrimSpace(entry.FailurePhase),
			LastCheckpoint:           strings.TrimSpace(entry.LastCheckpoint),
			LastSuccessfulCheckpoint: strings.TrimSpace(entry.LastSuccessfulCheckpoint),
			FailureReason:            strings.TrimSpace(entry.FailureReason),
			SetupPersisted:           entry.SetupPersisted,
			RecordedAt:               entry.RecordedAt,
		}
		if normalized.ProjectID == "" &&
			normalized.FailureCategory == "" &&
			normalized.FailureClass == "" &&
			normalized.FailurePhase == "" &&
			normalized.LastCheckpoint == "" &&
			normalized.LastSuccessfulCheckpoint == "" &&
			normalized.FailureReason == "" &&
			normalized.RetryAttemptCount == 0 &&
			!normalized.SetupPersisted &&
			normalized.RecordedAt == nil {
			continue
		}
		out = append(out, normalized)
	}
	return out
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
