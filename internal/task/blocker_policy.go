package task

import (
	"encoding/json"
	"strings"
)

const (
	metadataKeyBlockerPolicy                 = "blocker_policy"
	blockerPolicyKeyAutoCreateResolutionTask = "auto_create_resolution_task"
)

func blockerPolicyAutoCreateResolutionTask(metadata json.RawMessage) bool {
	if len(metadata) == 0 {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return false
	}
	raw, ok := payload[metadataKeyBlockerPolicy]
	if !ok {
		return false
	}
	policy, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	enabled, _ := policy[blockerPolicyKeyAutoCreateResolutionTask].(bool)
	return enabled
}

func ApplyBlockerAutoResolutionTask(existing json.RawMessage, enabled bool) json.RawMessage {
	payload := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}

	policy, _ := payload[metadataKeyBlockerPolicy].(map[string]any)
	if policy == nil {
		policy = map[string]any{}
	}

	if enabled {
		policy[blockerPolicyKeyAutoCreateResolutionTask] = true
		payload[metadataKeyBlockerPolicy] = policy
	} else {
		delete(policy, blockerPolicyKeyAutoCreateResolutionTask)
		if len(policy) == 0 {
			delete(payload, metadataKeyBlockerPolicy)
		} else {
			payload[metadataKeyBlockerPolicy] = policy
		}
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizeJSON(existing)
	}
	return normalizeJSON(json.RawMessage(strings.TrimSpace(string(encoded))))
}
