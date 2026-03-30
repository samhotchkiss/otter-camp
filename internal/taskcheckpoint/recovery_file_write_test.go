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
	if checkpoint.BlockerClass != RecoveryFileWriteBlockerClassDurableCheckpoint {
		t.Fatalf("blocker_class = %q, want %q", checkpoint.BlockerClass, RecoveryFileWriteBlockerClassDurableCheckpoint)
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

func TestRecoveryFileWritePromptStrategyLinesHardensRejectedDraftResume(t *testing.T) {
	lines := RecoveryFileWritePromptStrategyLines(&RecoveryFileWriteCheckpoint{
		TargetPath:          "docs/content-strategy.md",
		ArtifactPath:        ".ottercamp/recovery/docs/content-strategy.md",
		FailureReason:       "repeated intent-only recovery drafts for docs/content-strategy.md across explicit resume attempts; latest assistant draft for docs/content-strategy.md described intent to write the deliverable instead of the file body",
		PriorFailureReasons: []string{"assistant draft for docs/content-strategy.md described intent to write the deliverable instead of the file body"},
	})
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "rejected a non-substantive draft") {
		t.Fatalf("prompt lines missing rejected-draft hardening:\n%s", text)
	}
	if !strings.Contains(text, "must begin with the substantive file body") {
		t.Fatalf("prompt lines missing substantive-file-body instruction:\n%s", text)
	}
	if !strings.Contains(text, "hardened blocker state") {
		t.Fatalf("prompt lines missing repeated-draft blocker guidance:\n%s", text)
	}
	if !strings.Contains(text, "Prior recovery failure history") {
		t.Fatalf("prompt lines missing prior failure history:\n%s", text)
	}
}

func TestRecoveryFileWritePromptStrategyLinesHardensEmptyMutationResume(t *testing.T) {
	lines := RecoveryFileWritePromptStrategyLines(&RecoveryFileWriteCheckpoint{
		TargetPath:    "docs/content-strategy.md",
		ArtifactPath:  ".ottercamp/recovery/docs/content-strategy.md",
		FailureReason: "repeated recovery file.write without content for docs/content-strategy.md across explicit resume attempts; latest retry again omitted the full file body",
	})
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "without a file body") {
		t.Fatalf("prompt lines missing empty file.write hardening:\n%s", text)
	}

	lines = RecoveryFileWritePromptStrategyLines(&RecoveryFileWriteCheckpoint{
		TargetPath:    "docs/content-strategy.md",
		ArtifactPath:  ".ottercamp/recovery/docs/content-strategy.md",
		FailureReason: "repeated recovery cli.execute without command for docs/content-strategy.md across explicit resume attempts; latest retry again omitted cli.execute.command",
	})
	text = strings.Join(lines, "\n")
	if !strings.Contains(text, "missing `command`") {
		t.Fatalf("prompt lines missing empty cli.execute hardening:\n%s", text)
	}
}

func TestRecoveryFileWriteFailureHistoryDeduplicatesCurrentFailure(t *testing.T) {
	checkpoint := NormalizeRecoveryFileWriteCheckpoint(RecoveryFileWriteCheckpoint{
		TargetPath:          "docs/content-strategy.md",
		ArtifactPath:        ".ottercamp/recovery/docs/content-strategy.md",
		FailureReason:       "repeated intent-only recovery drafts for docs/content-strategy.md across explicit resume attempts; latest assistant draft for docs/content-strategy.md described intent to write the deliverable instead of the file body",
		PriorFailureReasons: []string{"assistant draft for docs/content-strategy.md described intent to write the deliverable instead of the file body", "assistant draft for docs/content-strategy.md described intent to write the deliverable instead of the file body"},
	})
	if checkpoint.BlockerClass != RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint {
		t.Fatalf("blocker_class = %q, want %q", checkpoint.BlockerClass, RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint)
	}
	history := RecoveryFileWriteFailureHistory(&checkpoint)
	if len(history) != 2 {
		t.Fatalf("failure history len = %d, want 2 history=%v", len(history), history)
	}
	if history[0] != "assistant draft for docs/content-strategy.md described intent to write the deliverable instead of the file body" {
		t.Fatalf("failure history[0] = %q, want prior failure", history[0])
	}
	if history[1] != checkpoint.FailureReason {
		t.Fatalf("failure history[1] = %q, want current failure", history[1])
	}
}

func TestRecoveryFileWriteFailureIsMissingContentRecognizesCorrectionReason(t *testing.T) {
	reason := "file.write for templates/template-08-replace.html was emitted without `content`; the next retry must provide the full file body instead of another empty file.write call"
	if !RecoveryFileWriteFailureIsMissingContent(reason) {
		t.Fatalf("RecoveryFileWriteFailureIsMissingContent(%q) = false, want true", reason)
	}
}

func TestRecoveryFileWriteFailureIsMissingCommandRecognizesCorrectionReason(t *testing.T) {
	reason := "cli.execute for templates/template-08-replace.html was emitted without `command`; the next retry must provide a concrete cli.execute.command string or a populated file.write call"
	if !RecoveryFileWriteFailureIsMissingCommand(reason) {
		t.Fatalf("RecoveryFileWriteFailureIsMissingCommand(%q) = false, want true", reason)
	}
}
