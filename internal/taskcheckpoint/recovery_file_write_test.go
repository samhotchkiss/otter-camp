package taskcheckpoint

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestParseRecoveryFileWriteCheckpoint(t *testing.T) {
	messageID := uuid.New()
	turnID := uuid.New()
	metadata := json.RawMessage(`{"recovery_file_write_checkpoint":{"version":1,"target_path":"docs/content-strategy.md","artifact_path":".ottercamp/recovery/docs/content-strategy.md","history_start_message_id":"` + messageID.String() + `","halt_turn_id":"` + turnID.String() + `"}}`)

	checkpoint, ok := ParseRecoveryFileWriteCheckpoint(metadata)
	if !ok {
		t.Fatal("expected checkpoint metadata to parse")
	}
	if checkpoint.TargetPath != "docs/content-strategy.md" {
		t.Fatalf("target_path = %q, want docs/content-strategy.md", checkpoint.TargetPath)
	}
	if checkpoint.ArtifactPath != ".ottercamp/recovery/docs/content-strategy.md" {
		t.Fatalf("artifact_path = %q, want recovery artifact path", checkpoint.ArtifactPath)
	}
	if checkpoint.FailureReason != "" {
		t.Fatalf("failure_reason = %q, want empty default", checkpoint.FailureReason)
	}
	if checkpoint.HistoryStartMessageID != messageID.String() {
		t.Fatalf("history_start_message_id = %q, want %q", checkpoint.HistoryStartMessageID, messageID.String())
	}
	if checkpoint.HaltTurnID != turnID.String() {
		t.Fatalf("halt_turn_id = %q, want %q", checkpoint.HaltTurnID, turnID.String())
	}
}

func TestMergeAndClearRecoveryFileWriteCheckpoint(t *testing.T) {
	checkpoint := RecoveryFileWriteCheckpoint{
		TargetPath:   "docs/content-strategy.md",
		ArtifactPath: ".ottercamp/recovery/docs/content-strategy.md",
	}
	merged, err := MergeRecoveryFileWriteCheckpoint(json.RawMessage(`{"foo":"bar"}`), checkpoint)
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	if _, ok := ParseRecoveryFileWriteCheckpoint(merged); !ok {
		t.Fatal("expected merged checkpoint metadata to parse")
	}

	cleared, err := ClearRecoveryFileWriteCheckpoint(merged)
	if err != nil {
		t.Fatalf("ClearRecoveryFileWriteCheckpoint: %v", err)
	}
	if _, ok := ParseRecoveryFileWriteCheckpoint(cleared); ok {
		t.Fatal("expected cleared metadata to remove checkpoint")
	}

	var payload map[string]any
	if err := json.Unmarshal(cleared, &payload); err != nil {
		t.Fatalf("Unmarshal cleared metadata: %v", err)
	}
	if got := strings.TrimSpace(payload["foo"].(string)); got != "bar" {
		t.Fatalf("foo = %q, want bar", got)
	}
}

func TestRecoveryFileWritePromptStrategyLines(t *testing.T) {
	lines := RecoveryFileWritePromptStrategyLines(&RecoveryFileWriteCheckpoint{
		TargetPath:    "docs/content-strategy.md",
		ArtifactPath:  ".ottercamp/recovery/docs/content-strategy.md",
		FailureReason: "recovered file.write reported success but docs/content-strategy.md was not found on disk",
	})
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "Recovery artifact: .ottercamp/recovery/docs/content-strategy.md") {
		t.Fatalf("prompt lines missing artifact guidance:\n%s", text)
	}
	if !strings.Contains(text, "Target file: docs/content-strategy.md") {
		t.Fatalf("prompt lines missing target guidance:\n%s", text)
	}
	if !strings.Contains(text, "Last write failure: recovered file.write reported success but docs/content-strategy.md was not found on disk") {
		t.Fatalf("prompt lines missing failure guidance:\n%s", text)
	}
}
