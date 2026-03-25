package turn

import (
	"encoding/json"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/projectfailure"
)

func TestShouldClearProjectBootstrapAutomaticFailure(t *testing.T) {
	t.Run("clears healthy active bootstrap", func(t *testing.T) {
		if !shouldClearProjectBootstrapAutomaticFailure(projectBootstrapState{
			Status:                   projectBootstrapStatusActive,
			CurrentPhase:             projectBootstrapCheckpointFirstWaveJobsClaimed,
			LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveJobsClaimed,
			AssignmentCount:          4,
			PlannedTaskCount:         8,
			PlannedFlowTemplateCount: 1,
			FirstWaveTaskCount:       1,
			FirstWaveExecutionCount:  1,
			FirstWaveJobCount:        1,
		}) {
			t.Fatal("healthy active bootstrap should clear bootstrap automatic failure")
		}
	})

	t.Run("does not clear active bootstrap with validation failure", func(t *testing.T) {
		if shouldClearProjectBootstrapAutomaticFailure(projectBootstrapState{
			Status:                 projectBootstrapStatusActive,
			ValidationStatus:       projectBootstrapValidationFailed,
			ValidationFailureClass: projectBootstrapFailureFirstWaveExecution,
			ValidationFailureReason: "no first-wave execution left draft or queued",
		}) {
			t.Fatal("active bootstrap validation failure should not clear bootstrap automatic failure")
		}
	})

	t.Run("clears completed bootstrap", func(t *testing.T) {
		if !shouldClearProjectBootstrapAutomaticFailure(projectBootstrapState{
			Status:      projectBootstrapStatusCompleted,
			CurrentPhase: projectBootstrapCheckpointFirstWaveJobsClaimed,
		}) {
			t.Fatal("completed bootstrap should clear bootstrap automatic failure")
		}
	})
}

func TestClearBootstrapAutomaticFailureState(t *testing.T) {
	settings, err := projectfailure.Apply(json.RawMessage(`{"project_bootstrap":{"status":"active"}}`), projectfailure.State{
		Action:                   projectFailureActionPause,
		Source:                   projectBootstrapSource,
		FailureCategory:          projectFailureCategoryBootstrap,
		FailureClass:             projectBootstrapFailureFirstWaveExecution,
		FailurePhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastCheckpoint:           projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		FailureReason:            "stale bootstrap failure",
		SetupPersisted:           true,
	})
	if err != nil {
		t.Fatalf("Apply projectfailure: %v", err)
	}

	cleared, changed := clearBootstrapAutomaticFailureState(settings)
	if !changed {
		t.Fatal("expected bootstrap automatic failure to be cleared")
	}
	if state := projectfailure.Parse(cleared); state.Action != "" || state.Source != "" {
		t.Fatalf("automatic failure state = %+v, want cleared", state)
	}
	projectState := projectBootstrapProjectStateFromSettings(cleared)
	if projectState.Status != projectBootstrapStatusActive {
		t.Fatalf("project bootstrap status = %q, want %q", projectState.Status, projectBootstrapStatusActive)
	}
}
