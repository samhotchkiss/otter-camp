package taskcheckpoint

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

const (
	RecoveryFileWriteMetadataKey                                  = "recovery_file_write_checkpoint"
	recoveryFileWriteCheckpointVersion                            = 1
	RecoveryFileWriteBlockerClassDurableCheckpoint                = "durable_recovery_checkpoint"
	RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint = "repeated_non_substantive_recovery_checkpoint"
)

type RecoveryFileWriteCheckpoint struct {
	Version               int      `json:"version"`
	TargetPath            string   `json:"target_path"`
	ArtifactPath          string   `json:"artifact_path,omitempty"`
	BlockerClass          string   `json:"blocker_class,omitempty"`
	FailureReason         string   `json:"failure_reason,omitempty"`
	PriorFailureReasons   []string `json:"prior_failure_reasons,omitempty"`
	HistoryStartMessageID string   `json:"history_start_message_id,omitempty"`
	HaltTurnID            string   `json:"halt_turn_id,omitempty"`
	UpdatedAt             string   `json:"updated_at,omitempty"`
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
	return NormalizeRecoveryFileWriteCheckpoint(checkpoint), true
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
	checkpoint = NormalizeRecoveryFileWriteCheckpoint(checkpoint)
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

func NormalizeRecoveryFileWriteCheckpoint(checkpoint RecoveryFileWriteCheckpoint) RecoveryFileWriteCheckpoint {
	if checkpoint.Version <= 0 {
		checkpoint.Version = recoveryFileWriteCheckpointVersion
	}
	checkpoint.TargetPath = strings.TrimSpace(checkpoint.TargetPath)
	checkpoint.ArtifactPath = strings.TrimSpace(checkpoint.ArtifactPath)
	checkpoint.BlockerClass = strings.TrimSpace(checkpoint.BlockerClass)
	checkpoint.FailureReason = strings.TrimSpace(checkpoint.FailureReason)
	checkpoint.PriorFailureReasons = normalizeRecoveryFailureReasons(checkpoint.PriorFailureReasons)
	checkpoint.HistoryStartMessageID = strings.TrimSpace(checkpoint.HistoryStartMessageID)
	checkpoint.HaltTurnID = strings.TrimSpace(checkpoint.HaltTurnID)
	checkpoint.UpdatedAt = strings.TrimSpace(checkpoint.UpdatedAt)
	if checkpoint.BlockerClass == "" {
		checkpoint.BlockerClass = RecoveryFileWriteBlockerClass(&checkpoint)
	}
	return checkpoint
}

func RecoveryFileWriteBlockerClass(checkpoint *RecoveryFileWriteCheckpoint) string {
	if checkpoint == nil {
		return ""
	}
	switch strings.TrimSpace(checkpoint.BlockerClass) {
	case RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint:
		return RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint
	case RecoveryFileWriteBlockerClassDurableCheckpoint:
		return RecoveryFileWriteBlockerClassDurableCheckpoint
	}
	if RecoveryFileWriteFailureIsRepeatedDraftReject(checkpoint.FailureReason) {
		return RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint
	}
	if strings.TrimSpace(checkpoint.TargetPath) != "" ||
		strings.TrimSpace(checkpoint.ArtifactPath) != "" ||
		strings.TrimSpace(checkpoint.FailureReason) != "" ||
		strings.TrimSpace(checkpoint.HistoryStartMessageID) != "" ||
		strings.TrimSpace(checkpoint.HaltTurnID) != "" ||
		len(normalizeRecoveryFailureReasons(checkpoint.PriorFailureReasons)) > 0 {
		return RecoveryFileWriteBlockerClassDurableCheckpoint
	}
	return ""
}

func RecoveryFileWriteFailureHistory(checkpoint *RecoveryFileWriteCheckpoint) []string {
	if checkpoint == nil {
		return nil
	}
	history := append([]string(nil), normalizeRecoveryFailureReasons(checkpoint.PriorFailureReasons)...)
	current := strings.TrimSpace(checkpoint.FailureReason)
	if current == "" {
		return history
	}
	for _, existing := range history {
		if existing == current {
			return history
		}
	}
	return append(history, current)
}

func normalizeRecoveryFailureReasons(reasons []string) []string {
	if len(reasons) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(reasons))
	normalized := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		trimmed := strings.TrimSpace(reason)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func RecoveryFileWriteFailureRejectsDraft(reason string) bool {
	lower := strings.ToLower(strings.TrimSpace(reason))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "instead of the file body") ||
		strings.Contains(lower, "repeated non-substantive recovery drafts") ||
		strings.Contains(lower, "repeated intent-only recovery drafts")
}

func RecoveryFileWriteFailureIsRepeatedTargetDrift(reason string) bool {
	lower := strings.ToLower(strings.TrimSpace(reason))
	return strings.Contains(lower, "repeated recovery target drift")
}

func RecoveryFileWriteFailureIsIntentOnly(reason string) bool {
	lower := strings.ToLower(strings.TrimSpace(reason))
	return strings.Contains(lower, "intent to write the deliverable") ||
		strings.Contains(lower, "repeated intent-only recovery drafts")
}

func RecoveryFileWriteFailureIsRepeatedDraftReject(reason string) bool {
	lower := strings.ToLower(strings.TrimSpace(reason))
	return strings.Contains(lower, "repeated non-substantive recovery drafts") ||
		strings.Contains(lower, "repeated intent-only recovery drafts")
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
	if failure := strings.TrimSpace(checkpoint.FailureReason); failure != "" {
		lines = append(lines, "- Last write failure: "+failure)
		lines = append(lines, "- Resolve that failure before retrying the final file.write.")
		if history := RecoveryFileWriteFailureHistory(checkpoint); len(history) > 1 {
			lines = append(lines, "- Prior recovery failure history: "+strings.Join(history[:len(history)-1], " | "))
		}
		if RecoveryFileWriteFailureRejectsDraft(failure) {
			lines = append(lines,
				"- The prior recovery halt already rejected a non-substantive draft; do not reuse rejected placeholder narration from the target or artifact as the next draft.",
				"- The next attempt must begin with the substantive file body for the target path, not progress narration or a description of intent to write it.",
			)
			if RecoveryFileWriteFailureIsRepeatedDraftReject(failure) {
				lines = append(lines, "- Repeated non-substantive recovery drafts are a hardened blocker state; do not expect a generic retry loop to make progress.")
			}
		}
		if RecoveryFileWriteFailureIsRepeatedTargetDrift(failure) {
			lines = append(lines, "- The checkpoint already rejected recovery target drift. Do not switch to a different file path unless a new authoritative validation failure explicitly names it.")
		}
	}
	lines = append(lines, "- If the target file already exists, continue from that durable output instead of restarting the same failed write.")
	return lines
}
