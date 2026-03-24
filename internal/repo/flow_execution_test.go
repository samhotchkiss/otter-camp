package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
