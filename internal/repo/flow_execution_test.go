package repo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFlowExecutionDefaults(t *testing.T) {
	t.Parallel()

	if got := defaultVisitNumber(0); got != 1 {
		t.Fatalf("defaultVisitNumber(0) = %d, want 1", got)
	}
	if got := defaultVisitNumber(3); got != 3 {
		t.Fatalf("defaultVisitNumber(3) = %d, want 3", got)
	}

	if got := normalizeFlowNodeExecutionStatus("   "); got != "active" {
		t.Fatalf("normalizeFlowNodeExecutionStatus(blank) = %q, want %q", got, "active")
	}
	if got := normalizeFlowNodeExecutionRuntimeSubstate("", nil); got == nil || *got != "waiting_for_turn" {
		t.Fatalf("normalizeFlowNodeExecutionRuntimeSubstate(active,nil) = %v, want waiting_for_turn", got)
	}
	if got := normalizeFlowNodeExecutionRuntimeSubstate("completed", nil); got != nil {
		t.Fatalf("normalizeFlowNodeExecutionRuntimeSubstate(completed,nil) = %v, want nil", got)
	}
	if got := defaultSubtaskStatus(""); got != "pending" {
		t.Fatalf("defaultSubtaskStatus(blank) = %q, want %q", got, "pending")
	}
}

func TestProjectTaskDependencyMigrationConstraintsDefined(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "migrations", "0045_project_task_dependency.sql")
	sqlBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}

	sql := string(sqlBytes)
	if !strings.Contains(sql, "source_type = depends_on_type") {
		t.Fatalf("migration missing same-level check constraint: %s", path)
	}
	if !strings.Contains(sql, "source_id <> depends_on_id") {
		t.Fatalf("migration missing self-dependency check constraint: %s", path)
	}
}

func TestProjectSubtaskNextSequenceNumberUsesMaxPlusOne(t *testing.T) {
	t.Parallel()

	path := "flow_execution.go"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}

	if !strings.Contains(string(src), "COALESCE(MAX(sequence_number), 0) + 1") {
		t.Fatalf("NextSequenceNumber query must use MAX(sequence_number)+1")
	}
}

func TestFlowExecutionRecoveryCheckpointMetadataRoundTrip(t *testing.T) {
	t.Parallel()

	runID := uuid.New()
	failedTurnID := uuid.New()
	updatedAt := time.Date(2026, 3, 24, 10, 0, 0, 0, time.UTC)

	raw := FlowExecutionMetadataWithRecoveryCheckpoint(
		FlowExecutionMetadataWithEntryHeadSHA(
			FlowExecutionMetadataWithLiveOwner(json.RawMessage(`{}`), FlowExecutionLiveOwner{RunID: &runID}),
			"entry123",
		),
		&FlowExecutionRecoveryCheckpoint{
			CheckpointType: "stranded_execution",
			LastCommitSHA:  "abc123",
			BranchHeadSHA:  "def456",
			FailedTurnID:   &failedTurnID,
			ResumeAction:   "start_new_turn",
			TargetPath:     "docs/plan.md",
			ArtifactRef:    ".ottercamp/recovery/docs/plan.md",
			FailureClass:   "product_runtime",
			FailureSummary: "active execution lost live task turn",
			UpdatedAt:      &updatedAt,
		},
	)

	checkpoint, ok := FlowExecutionRecoveryCheckpointFromMetadata(raw)
	if !ok || checkpoint == nil {
		t.Fatal("expected recovery checkpoint in metadata")
	}
	if checkpoint.CheckpointType != "stranded_execution" {
		t.Fatalf("checkpoint_type = %q, want stranded_execution", checkpoint.CheckpointType)
	}
	if checkpoint.FailedTurnID == nil || *checkpoint.FailedTurnID != failedTurnID {
		t.Fatalf("failed_turn_id = %v, want %s", checkpoint.FailedTurnID, failedTurnID)
	}
	if checkpoint.ResumeAction != "start_new_turn" {
		t.Fatalf("resume_action = %q, want start_new_turn", checkpoint.ResumeAction)
	}
	if checkpoint.BranchHeadSHA != "def456" {
		t.Fatalf("branch_head_sha = %q, want def456", checkpoint.BranchHeadSHA)
	}
	if checkpoint.TargetPath != "docs/plan.md" {
		t.Fatalf("target_path = %q, want docs/plan.md", checkpoint.TargetPath)
	}
	if checkpoint.ArtifactRef != ".ottercamp/recovery/docs/plan.md" {
		t.Fatalf("artifact_ref = %q, want .ottercamp/recovery/docs/plan.md", checkpoint.ArtifactRef)
	}
	if checkpoint.FailureClass != "product_runtime" {
		t.Fatalf("failure_class = %q, want product_runtime", checkpoint.FailureClass)
	}
	liveOwner := FlowExecutionLiveOwnerFromMetadata(raw)
	if liveOwner.RunID == nil || *liveOwner.RunID != runID {
		t.Fatalf("live run id = %v, want %s", liveOwner.RunID, runID)
	}
	if got := FlowExecutionEntryHeadSHAFromMetadata(raw); got != "entry123" {
		t.Fatalf("entry head sha = %q, want entry123", got)
	}
}
