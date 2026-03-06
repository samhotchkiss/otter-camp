package chat

import (
	"encoding/json"
	"strings"
	"time"
)

const agentTurnDispatchMetadataKey = "agent_turn_dispatch"

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
