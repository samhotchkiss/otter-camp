package taskcheckpoint

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

const (
	RecoveryFileWriteMetadataKey       = "recovery_file_write_checkpoint"
	recoveryFileWriteCheckpointVersion = 1
)

type RecoveryFileWriteCheckpoint struct {
	Version               int    `json:"version"`
	TargetPath            string `json:"target_path"`
	ArtifactPath          string `json:"artifact_path,omitempty"`
	HistoryStartMessageID string `json:"history_start_message_id,omitempty"`
	HaltTurnID            string `json:"halt_turn_id,omitempty"`
	UpdatedAt             string `json:"updated_at,omitempty"`
}

func ParseRecoveryFileWriteCheckpoint(metadata json.RawMessage) (RecoveryFileWriteCheckpoint, bool) {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return RecoveryFileWriteCheckpoint{}, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return RecoveryFileWriteCheckpoint{}, false
	}
	raw, ok := payload[RecoveryFileWriteMetadataKey]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return RecoveryFileWriteCheckpoint{}, false
	}
	var checkpoint RecoveryFileWriteCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		return RecoveryFileWriteCheckpoint{}, false
	}
	if checkpoint.Version <= 0 {
		checkpoint.Version = recoveryFileWriteCheckpointVersion
	}
	return checkpoint, true
}

func MergeRecoveryFileWriteCheckpoint(metadata json.RawMessage, checkpoint RecoveryFileWriteCheckpoint) (json.RawMessage, error) {
	payload := map[string]any{}
	if len(metadata) != 0 && json.Valid(metadata) {
		if err := json.Unmarshal(metadata, &payload); err != nil {
			return nil, err
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if checkpoint.Version <= 0 {
		checkpoint.Version = recoveryFileWriteCheckpointVersion
	}
	payload[RecoveryFileWriteMetadataKey] = checkpoint
	merged, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(merged), nil
}

func ClearRecoveryFileWriteCheckpoint(metadata json.RawMessage) (json.RawMessage, error) {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return json.RawMessage(`{}`), nil
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		return json.RawMessage(`{}`), nil
	}
	delete(payload, RecoveryFileWriteMetadataKey)
	merged, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(merged), nil
}

func RecoveryFileWriteHistoryStartMessageID(checkpoint *RecoveryFileWriteCheckpoint) *uuid.UUID {
	if checkpoint == nil {
		return nil
	}
	id, err := uuid.Parse(strings.TrimSpace(checkpoint.HistoryStartMessageID))
	if err != nil || id == uuid.Nil {
		return nil
	}
	return &id
}

func RecoveryFileWritePromptStrategyLines(checkpoint *RecoveryFileWriteCheckpoint) []string {
	lines := []string{
		"Recovery Execution Strategy:",
		"- Treat the latest recovery halt as a checkpoint, not as permission to retry the same empty file.write cycle.",
		"- Only retry file mutation after concrete file content exists in a persisted artifact or in the real target file.",
	}
	if checkpoint == nil {
		return lines
	}
	if target := strings.TrimSpace(checkpoint.TargetPath); target != "" {
		lines = append(lines, "- Target file: "+target)
	}
	if artifact := strings.TrimSpace(checkpoint.ArtifactPath); artifact != "" {
		lines = append(lines, "- Recovery artifact: "+artifact)
		lines = append(lines, "- Read the persisted recovery artifact before retrying the final file.write.")
	} else {
		lines = append(lines, "- No persisted recovery artifact exists for the last halt; do not claim one exists on the next attempt.")
	}
	lines = append(lines, "- If the target file already exists, continue from that durable output instead of restarting the same failed write.")
	return lines
}
