package repo

import (
	"encoding/json"

	"github.com/google/uuid"
)

const (
	FlowExecutionMetadataLiveRunID  = "live_run_id"
	FlowExecutionMetadataLiveTurnID = "live_turn_id"
)

type FlowExecutionLiveOwner struct {
	RunID  *uuid.UUID
	TurnID *uuid.UUID
}

func FlowExecutionMetadataWithLiveOwner(existing json.RawMessage, owner FlowExecutionLiveOwner) json.RawMessage {
	payload := map[string]any{}
	if len(existing) > 0 && json.Valid(existing) {
		_ = json.Unmarshal(existing, &payload)
	}

	setOrDeleteUUID(payload, FlowExecutionMetadataLiveRunID, owner.RunID)
	setOrDeleteUUID(payload, FlowExecutionMetadataLiveTurnID, owner.TurnID)

	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func FlowExecutionLiveOwnerFromMetadata(raw json.RawMessage) FlowExecutionLiveOwner {
	if len(raw) == 0 || !json.Valid(raw) {
		return FlowExecutionLiveOwner{}
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return FlowExecutionLiveOwner{}
	}
	return FlowExecutionLiveOwner{
		RunID:  metadataUUIDPointer(payload, FlowExecutionMetadataLiveRunID),
		TurnID: metadataUUIDPointer(payload, FlowExecutionMetadataLiveTurnID),
	}
}

func metadataUUIDPointer(payload map[string]any, key string) *uuid.UUID {
	value, ok := payload[key]
	if !ok {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return nil
	}
	parsed, err := uuid.Parse(text)
	if err != nil {
		return nil
	}
	return &parsed
}

func setOrDeleteUUID(payload map[string]any, key string, value *uuid.UUID) {
	if value == nil || *value == uuid.Nil {
		delete(payload, key)
		return
	}
	payload[key] = value.String()
}
