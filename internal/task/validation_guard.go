package task

import "encoding/json"

const ValidationGuardMetadataKey = "agent_turn_validation_guard"

type ValidationGuardState struct {
	InitialMessageID   string `json:"initial_message_id"`
	Fingerprint        string `json:"fingerprint"`
	AttemptFingerprint string `json:"attempt_fingerprint,omitempty"`
	ToolName           string `json:"tool_name"`
	FailureClass       string `json:"failure_class"`
	FailureCode        string `json:"failure_code"`
	FailureReason      string `json:"failure_reason"`
	Count              int    `json:"count"`
	BlockThreshold     int    `json:"block_threshold"`
	Blocked            bool   `json:"blocked"`
	FirstSeenAt        string `json:"first_seen_at,omitempty"`
	LastSeenAt         string `json:"last_seen_at,omitempty"`
	LastTurnID         string `json:"last_turn_id,omitempty"`
}

func ParseValidationGuard(metadata json.RawMessage) (ValidationGuardState, bool) {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return ValidationGuardState{}, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return ValidationGuardState{}, false
	}
	raw, ok := payload[ValidationGuardMetadataKey]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return ValidationGuardState{}, false
	}
	var state ValidationGuardState
	if err := json.Unmarshal(raw, &state); err != nil {
		return ValidationGuardState{}, false
	}
	return state, state.Count > 0 || state.Blocked
}

func MergeValidationGuardMetadata(metadata json.RawMessage, state ValidationGuardState) (json.RawMessage, error) {
	payload := map[string]any{}
	if len(metadata) != 0 && json.Valid(metadata) {
		if err := json.Unmarshal(metadata, &payload); err != nil {
			return nil, err
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload[ValidationGuardMetadataKey] = state
	merged, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(merged), nil
}

func ClearValidationGuardMetadata(metadata json.RawMessage) (json.RawMessage, error) {
	payload := map[string]any{}
	if len(metadata) != 0 && json.Valid(metadata) {
		if err := json.Unmarshal(metadata, &payload); err != nil {
			return nil, err
		}
	}
	delete(payload, ValidationGuardMetadataKey)
	merged, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(merged), nil
}
