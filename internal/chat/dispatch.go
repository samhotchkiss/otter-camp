package chat

import (
	"encoding/json"
	"strings"
	"time"
)

const agentTurnDispatchMetadataKey = "agent_turn_dispatch"
const summarizationBackoffMetadataKey = "summarization_backoff"

type SummarizationBackoffState struct {
	FailureCount  int    `json:"failure_count"`
	LastError     string `json:"last_error,omitempty"`
	LastFailureAt string `json:"last_failure_at,omitempty"`
	NextAllowedAt string `json:"next_allowed_at,omitempty"`
}

func AgentTurnDispatchCancelled(metadata json.RawMessage) bool {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return false
	}

	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return false
	}
	dispatch, _ := payload[agentTurnDispatchMetadataKey].(map[string]any)
	if dispatch == nil {
		return false
	}
	cancelledAt, _ := dispatch["cancelled_at"].(string)
	return strings.TrimSpace(cancelledAt) != ""
}

func MergeAgentTurnDispatchCancelledMetadata(metadata json.RawMessage, reason string, at time.Time) (json.RawMessage, error) {
	payload := map[string]any{}
	if len(metadata) != 0 && json.Valid(metadata) {
		if err := json.Unmarshal(metadata, &payload); err != nil {
			return nil, err
		}
	}

	dispatch, _ := payload[agentTurnDispatchMetadataKey].(map[string]any)
	if dispatch == nil {
		dispatch = map[string]any{}
	}
	dispatch["cancelled_at"] = at.UTC().Format(time.RFC3339Nano)
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		dispatch["cancel_reason"] = trimmed
	}
	payload[agentTurnDispatchMetadataKey] = dispatch

	merged, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(merged), nil
}

func ParseSummarizationBackoff(metadata json.RawMessage) (SummarizationBackoffState, bool) {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return SummarizationBackoffState{}, false
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return SummarizationBackoffState{}, false
	}
	raw, ok := payload[summarizationBackoffMetadataKey]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return SummarizationBackoffState{}, false
	}
	var state SummarizationBackoffState
	if err := json.Unmarshal(raw, &state); err != nil {
		return SummarizationBackoffState{}, false
	}
	if state.FailureCount <= 0 && strings.TrimSpace(state.NextAllowedAt) == "" {
		return SummarizationBackoffState{}, false
	}
	return state, true
}

func MergeSummarizationBackoffMetadata(metadata json.RawMessage, state SummarizationBackoffState) (json.RawMessage, error) {
	payload := map[string]any{}
	if len(metadata) != 0 && json.Valid(metadata) {
		if err := json.Unmarshal(metadata, &payload); err != nil {
			return nil, err
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload[summarizationBackoffMetadataKey] = state
	merged, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(merged), nil
}

func ClearSummarizationBackoffMetadata(metadata json.RawMessage) (json.RawMessage, error) {
	payload := map[string]any{}
	if len(metadata) != 0 && json.Valid(metadata) {
		if err := json.Unmarshal(metadata, &payload); err != nil {
			return nil, err
		}
	}
	delete(payload, summarizationBackoffMetadataKey)
	merged, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(merged), nil
}
