package repo

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	FlowExecutionMetadataLiveRunID          = "live_run_id"
	FlowExecutionMetadataLiveTurnID         = "live_turn_id"
	FlowExecutionMetadataRecoveryCheckpoint = "recovery_checkpoint"
	FlowExecutionMetadataReviewDecision     = "review_decision"
)

type FlowExecutionLiveOwner struct {
	RunID  *uuid.UUID
	TurnID *uuid.UUID
}

type FlowExecutionRecoveryCheckpoint struct {
	CheckpointType      string     `json:"checkpoint_type,omitempty"`
	LastCommitSHA       string     `json:"last_commit_sha,omitempty"`
	BranchHeadSHA       string     `json:"branch_head_sha,omitempty"`
	LastCompletedTurnID *uuid.UUID `json:"last_completed_turn_id,omitempty"`
	FailedTurnID        *uuid.UUID `json:"failed_turn_id,omitempty"`
	ResumeAction        string     `json:"resume_action,omitempty"`
	TargetPath          string     `json:"target_path,omitempty"`
	ArtifactRef         string     `json:"artifact_ref,omitempty"`
	FailureClass        string     `json:"failure_class,omitempty"`
	FailureSummary      string     `json:"failure_summary,omitempty"`
	UpdatedAt           *time.Time `json:"updated_at,omitempty"`
}

type FlowExecutionReviewDecision struct {
	Decision  string     `json:"decision,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	Findings  string     `json:"findings,omitempty"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
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

func FlowExecutionMetadataWithRecoveryCheckpoint(existing json.RawMessage, checkpoint *FlowExecutionRecoveryCheckpoint) json.RawMessage {
	payload := map[string]any{}
	if len(existing) > 0 && json.Valid(existing) {
		_ = json.Unmarshal(existing, &payload)
	}
	if checkpoint == nil {
		delete(payload, FlowExecutionMetadataRecoveryCheckpoint)
	} else {
		payload[FlowExecutionMetadataRecoveryCheckpoint] = checkpoint
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func FlowExecutionMetadataWithReviewDecision(existing json.RawMessage, decision *FlowExecutionReviewDecision) json.RawMessage {
	payload := map[string]any{}
	if len(existing) > 0 && json.Valid(existing) {
		_ = json.Unmarshal(existing, &payload)
	}
	if decision == nil {
		delete(payload, FlowExecutionMetadataReviewDecision)
	} else {
		payload[FlowExecutionMetadataReviewDecision] = decision
	}
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

func FlowExecutionRecoveryCheckpointFromMetadata(raw json.RawMessage) (*FlowExecutionRecoveryCheckpoint, bool) {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false
	}
	value, ok := payload[FlowExecutionMetadataRecoveryCheckpoint]
	if !ok || len(value) == 0 || string(value) == "null" {
		return nil, false
	}
	var checkpoint FlowExecutionRecoveryCheckpoint
	if err := json.Unmarshal(value, &checkpoint); err != nil {
		return nil, false
	}
	return &checkpoint, true
}

func FlowExecutionReviewDecisionFromMetadata(raw json.RawMessage) (*FlowExecutionReviewDecision, bool) {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false
	}
	value, ok := payload[FlowExecutionMetadataReviewDecision]
	if !ok || len(value) == 0 || string(value) == "null" {
		return nil, false
	}
	var decision FlowExecutionReviewDecision
	if err := json.Unmarshal(value, &decision); err != nil {
		return nil, false
	}
	return &decision, true
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
