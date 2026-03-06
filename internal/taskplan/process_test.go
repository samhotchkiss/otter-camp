package taskplan

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCompletionReportRejectsMissingRequiredSections(t *testing.T) {
	description := "Run customer interviews, capture assumptions, and design a validation plan for this new product idea."
	plan := Analyze("Validate the onboarding problem before writing specs", &description)
	metadata := ApplyMetadata(json.RawMessage(`{}`), plan)

	metadata, _, report, err := ApplyProcessUpdate(metadata, ProcessUpdate{
		Artifacts: []ArtifactEvidence{
			{
				Slug:     "problem-brief",
				Summary:  "The onboarding drop-off is real.",
				Sections: []string{"problem"},
			},
		},
		HasArtifactChanges: true,
	})
	if err != nil {
		t.Fatalf("ApplyProcessUpdate: %v", err)
	}
	if report.ProcessStatus != ProcessStatusPending {
		t.Fatalf("ProcessStatus = %q, want %q", report.ProcessStatus, ProcessStatusPending)
	}

	_, err = CompletionReport(metadata)
	if !errors.Is(err, ErrPlanningArtifactContractIncomplete) {
		t.Fatalf("CompletionReport err = %v, want ErrPlanningArtifactContractIncomplete", err)
	}
	if err == nil || !strings.Contains(err.Error(), "target user") || !strings.Contains(err.Error(), "evidence gaps") {
		t.Fatalf("CompletionReport error = %v, want missing required sections", err)
	}
}

func TestApplyProcessUpdateRecordsOverrideForIncompleteContract(t *testing.T) {
	description := "Write the PRD, requirements, implementation plan, and acceptance criteria for the billing migration."
	plan := Analyze("PRD for billing migration", &description)
	metadata := ApplyMetadata(json.RawMessage(`{}`), plan)

	metadata, _, _, err := ApplyProcessUpdate(metadata, ProcessUpdate{
		Artifacts: []ArtifactEvidence{
			{
				Slug:     "prd",
				Summary:  "Billing migration scope and requirements.",
				Sections: []string{"objective", "scope"},
			},
		},
		HasArtifactChanges: true,
	})
	if err != nil {
		t.Fatalf("ApplyProcessUpdate artifacts: %v", err)
	}

	if _, err := CompletionReport(metadata); !errors.Is(err, ErrPlanningArtifactContractIncomplete) {
		t.Fatalf("CompletionReport before override err = %v, want ErrPlanningArtifactContractIncomplete", err)
	}

	recordedAt := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	overrideReason := "Customer migration dates are fixed; implementation will fill the remaining sections after dependency confirmation."
	metadata, parsed, report, err := ApplyProcessUpdate(metadata, ProcessUpdate{
		OverrideReason: &overrideReason,
		ActorType:      "agent",
		ActorID:        uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		RecordedAt:     recordedAt,
	})
	if err != nil {
		t.Fatalf("ApplyProcessUpdate override: %v", err)
	}
	if parsed.Override == nil {
		t.Fatal("Override = nil, want recorded override")
	}
	if parsed.Override.Reason != overrideReason {
		t.Fatalf("Override.Reason = %q, want %q", parsed.Override.Reason, overrideReason)
	}
	if parsed.Override.RecordedAt != recordedAt.Format(time.RFC3339) {
		t.Fatalf("Override.RecordedAt = %q, want %q", parsed.Override.RecordedAt, recordedAt.Format(time.RFC3339))
	}
	if report.ProcessStatus != ProcessStatusOverridden {
		t.Fatalf("ProcessStatus = %q, want %q", report.ProcessStatus, ProcessStatusOverridden)
	}
	if !report.CanComplete() {
		t.Fatal("CanComplete = false, want true with recorded override")
	}
	finalReport, err := CompletionReport(metadata)
	if err != nil {
		t.Fatalf("CompletionReport after override: %v", err)
	}
	if finalReport.ProcessStatus != ProcessStatusOverridden {
		t.Fatalf("final ProcessStatus = %q, want %q", finalReport.ProcessStatus, ProcessStatusOverridden)
	}
}

func TestEvaluateDiscoveryContractUsesModeSpecificValidationSections(t *testing.T) {
	tests := []struct {
		name         string
		title        string
		description  string
		wantMode     string
		wantSections []string
	}{
		{
			name:         "new product",
			title:        "Validate the onboarding concept",
			description:  "Run customer interviews, capture assumptions, and design low-cost validation for this new product idea before we commit scope.",
			wantMode:     DiscoveryModeNewProduct,
			wantSections: []string{"low-cost tests", "desirability signals", "decision framework"},
		},
		{
			name:         "existing product",
			title:        "Investigate checkout drop-off in the existing product",
			description:  "Review support tickets, current funnel instrumentation, and usage data for the existing product before defining experiments.",
			wantMode:     DiscoveryModeExistingProduct,
			wantSections: []string{"prior feedback", "instrumentation baseline", "decision framework"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := Analyze(tc.title, &tc.description)
			if plan.DiscoveryMode != tc.wantMode {
				t.Fatalf("DiscoveryMode = %q, want %q", plan.DiscoveryMode, tc.wantMode)
			}

			report := Evaluate(plan)
			var validation ArtifactContract
			found := false
			for _, contract := range report.ArtifactContract {
				if contract.Slug == "validation-plan" {
					validation = contract
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("validation-plan contract missing from %#v", report.ArtifactContract)
			}
			for _, wantSection := range tc.wantSections {
				if !containsString(validation.RequiredSections, wantSection) {
					t.Fatalf("validation required sections = %#v, want %q", validation.RequiredSections, wantSection)
				}
			}
		})
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}
