package taskorchestration

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
)

func TestApplyMergesParentIntegrationMetadata(t *testing.T) {
	childA := uuid.New()
	childB := uuid.New()
	metadata := taskdecomp.ApplyMetadata(json.RawMessage(`{}`), taskdecomp.Plan{
		RequiresDecomposition: true,
		PrimaryDeliverable:    "Ship the launch",
		Deliverables:          []string{"Ship the launch", "Landing page", "Billing flow"},
	}, "Ship the launch end to end.", []uuid.UUID{childA, childB})

	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	updated, err := Apply(metadata, Update{
		ChildVerifications: []ChildVerification{
			NewChildVerification(childA, "Verified the landing page output against the brief.", now),
			NewChildVerification(childB, "Verified the billing flow handoff.", now),
		},
		IntegrationCheck:  NewIntegrationCheck("passed", "Ran the launch smoke test across both child deliverables.", now),
		OutcomeAssessment: NewOutcomeAssessment(true, "The launch bundle now satisfies the parent outcome.", now),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	state, ok := Parse(updated)
	if !ok {
		t.Fatal("Parse ok = false, want true")
	}
	if len(state.ChildVerifications) != 2 {
		t.Fatalf("child_verifications len = %d, want 2", len(state.ChildVerifications))
	}
	if state.IntegrationCheck == nil || state.IntegrationCheck.Status != "passed" {
		t.Fatalf("integration_check = %+v, want passed", state.IntegrationCheck)
	}
	if state.OutcomeAssessment == nil || !state.OutcomeAssessment.Satisfied {
		t.Fatalf("outcome_assessment = %+v, want satisfied", state.OutcomeAssessment)
	}
}

func TestValidateCompletionRejectsMissingParentChecks(t *testing.T) {
	parentID := uuid.New()
	childID := uuid.New()
	parent := repo.ProjectTask{
		ID:       parentID,
		Metadata: taskdecomp.ApplyMetadata(json.RawMessage(`{}`), taskdecomp.Plan{RequiresDecomposition: true, PrimaryDeliverable: "Primary", Deliverables: []string{"Primary", "Child"}}, "source", []uuid.UUID{childID}),
	}
	child := repo.ProjectTask{ID: childID, TaskNumber: 2, Title: "Billing child", WorkStatus: "done"}

	err := ValidateCompletion(parent, []repo.ProjectTask{child})
	if !errors.Is(err, ErrParentCompletionRequirements) {
		t.Fatalf("ValidateCompletion err = %v, want ErrParentCompletionRequirements", err)
	}
	if !strings.Contains(err.Error(), "verify child outputs") {
		t.Fatalf("error = %v, want missing child verification detail", err)
	}
	if !strings.Contains(err.Error(), "integration or end-to-end") {
		t.Fatalf("error = %v, want integration detail", err)
	}
}

func TestValidateCompletionAllowsVerifiedIntegratedParent(t *testing.T) {
	parentID := uuid.New()
	childID := uuid.New()
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	parent := repo.ProjectTask{
		ID:         parentID,
		TaskNumber: 1,
		Title:      "Launch parent",
		Metadata:   taskdecomp.ApplyMetadata(json.RawMessage(`{}`), taskdecomp.Plan{RequiresDecomposition: true, PrimaryDeliverable: "Primary", Deliverables: []string{"Primary", "Child"}}, "source", []uuid.UUID{childID}),
	}
	metadata, err := Apply(parent.Metadata, Update{
		ChildVerifications: []ChildVerification{NewChildVerification(childID, "Verified the child output.", now)},
		IntegrationCheck:   NewIntegrationCheck("passed", "Ran the integration smoke test.", now),
		OutcomeAssessment:  NewOutcomeAssessment(true, "The parent outcome is satisfied.", now),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	parent.Metadata = metadata

	child := repo.ProjectTask{ID: childID, TaskNumber: 2, Title: "Billing child", WorkStatus: "done"}
	if err := ValidateCompletion(parent, []repo.ProjectTask{child}); err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
}
