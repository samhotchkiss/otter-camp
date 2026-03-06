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

func TestCompletionReportRejectsStrategyWithoutOperationalSections(t *testing.T) {
	description := "Define the product strategy, positioning tradeoffs, and the roadmap sequence for the analytics platform."
	plan := Analyze("Positioning tradeoffs for analytics expansion", &description)
	metadata := ApplyMetadata(json.RawMessage(`{}`), plan)

	metadata, _, _, err := ApplyProcessUpdate(metadata, ProcessUpdate{
		Artifacts: []ArtifactEvidence{
			{
				Slug:     "strategy-brief",
				Summary:  "Strategy direction for the analytics expansion.",
				Sections: []string{"goal", "target segments", "core capabilities"},
			},
			{
				Slug:     "tradeoff-matrix",
				Summary:  "Decision criteria and tradeoffs are documented.",
				Sections: []string{"options", "tradeoffs", "decision"},
			},
			{
				Slug:     "decision-log",
				Summary:  "The accountable owner and rationale are recorded.",
				Sections: []string{"decision", "rationale", "owner"},
			},
			{
				Slug:     "success-narrative",
				Summary:  "Outcome narrative for the selected direction.",
				Sections: []string{"milestones", "risks"},
			},
		},
		HasArtifactChanges: true,
	})
	if err != nil {
		t.Fatalf("ApplyProcessUpdate: %v", err)
	}

	_, err = CompletionReport(metadata)
	if !errors.Is(err, ErrPlanningArtifactContractIncomplete) {
		t.Fatalf("CompletionReport err = %v, want ErrPlanningArtifactContractIncomplete", err)
	}
	if err == nil || !strings.Contains(err.Error(), "not serving") || !strings.Contains(err.Error(), "key metrics") || !strings.Contains(err.Error(), "defensibility") {
		t.Fatalf("CompletionReport error = %v, want missing strategy operating sections", err)
	}
}

func TestCompletionReportRejectsSpecWithoutNonGoalsMetricsAndPhasing(t *testing.T) {
	description := "Write the PRD, requirements, implementation plan, and acceptance criteria for the billing migration."
	plan := Analyze("PRD for billing migration", &description)
	metadata := ApplyMetadata(json.RawMessage(`{}`), plan)

	metadata, _, _, err := ApplyProcessUpdate(metadata, ProcessUpdate{
		Artifacts: []ArtifactEvidence{
			{
				Slug:     "prd",
				Summary:  "Billing migration scope and requirements.",
				Sections: []string{"goals", "scope", "constraints"},
			},
			{
				Slug:     "implementation-plan",
				Summary:  "Delivery sequencing and ownership.",
				Sections: []string{"milestones", "owners", "rollout"},
			},
			{
				Slug:     "acceptance-criteria",
				Summary:  "Acceptance scenarios and verification checks.",
				Sections: []string{"scenarios", "edge cases", "verification"},
			},
			{
				Slug:     "dependency-log",
				Summary:  "Dependencies and mitigations.",
				Sections: []string{"dependencies", "risks", "mitigations"},
			},
		},
		HasArtifactChanges: true,
	})
	if err != nil {
		t.Fatalf("ApplyProcessUpdate: %v", err)
	}

	_, err = CompletionReport(metadata)
	if !errors.Is(err, ErrPlanningArtifactContractIncomplete) {
		t.Fatalf("CompletionReport err = %v, want ErrPlanningArtifactContractIncomplete", err)
	}
	if err == nil || !strings.Contains(err.Error(), "non-goals") || !strings.Contains(err.Error(), "success metrics") || !strings.Contains(err.Error(), "open questions") || !strings.Contains(err.Error(), "phasing") {
		t.Fatalf("CompletionReport error = %v, want missing spec operating sections", err)
	}
}

func TestCompletionReportRejectsMetricsWithoutInputHealthAndCadence(t *testing.T) {
	description := "Define the north-star metric, input metrics, health metrics, dashboard spec, and weekly review cadence for activation."
	plan := Analyze("Metric tree and instrumentation plan", &description)
	metadata := ApplyMetadata(json.RawMessage(`{}`), plan)

	metadata, _, _, err := ApplyProcessUpdate(metadata, ProcessUpdate{
		Artifacts: []ArtifactEvidence{
			{
				Slug:     "metric-tree",
				Summary:  "Metric framework for activation.",
				Sections: []string{"north star", "counter metrics"},
			},
			{
				Slug:     "instrumentation-plan",
				Summary:  "Telemetry events and ownership.",
				Sections: []string{"events", "owners", "qa"},
			},
			{
				Slug:     "dashboard-spec",
				Summary:  "Views and alerts for the activation dashboard.",
				Sections: []string{"views", "slices", "alerts"},
			},
			{
				Slug:     "review-cadence",
				Summary:  "Weekly operating review schedule.",
				Sections: []string{"schedule", "owners", "thresholds"},
			},
		},
		HasArtifactChanges: true,
	})
	if err != nil {
		t.Fatalf("ApplyProcessUpdate: %v", err)
	}

	_, err = CompletionReport(metadata)
	if !errors.Is(err, ErrPlanningArtifactContractIncomplete) {
		t.Fatalf("CompletionReport err = %v, want ErrPlanningArtifactContractIncomplete", err)
	}
	if err == nil || !strings.Contains(err.Error(), "input metrics") || !strings.Contains(err.Error(), "health metrics") {
		t.Fatalf("CompletionReport error = %v, want missing metrics framework sections", err)
	}
}

func TestCompletionReportRejectsGTMWithoutBeachheadICPAndExpansionPlan(t *testing.T) {
	description := "Create the GTM launch plan with beachhead segment, ICP, positioning, messaging, channel strategy, launch timeline, success metrics, and expansion plan."
	plan := Analyze("Go-to-market launch plan", &description)
	metadata := ApplyMetadata(json.RawMessage(`{}`), plan)

	metadata, _, _, err := ApplyProcessUpdate(metadata, ProcessUpdate{
		Artifacts: []ArtifactEvidence{
			{
				Slug:     "launch-brief",
				Summary:  "Launch scope and success model.",
				Sections: []string{"launch scope", "success metrics"},
			},
			{
				Slug:     "audience-messaging",
				Summary:  "Positioning and proof for the launch.",
				Sections: []string{"positioning", "messaging", "proof"},
			},
			{
				Slug:     "channel-plan",
				Summary:  "Channel strategy and timing.",
				Sections: []string{"channel strategy", "owners", "launch timeline"},
			},
			{
				Slug:     "launch-checklist",
				Summary:  "Readiness and approvals for launch.",
				Sections: []string{"readiness", "approvals", "contingency"},
			},
		},
		HasArtifactChanges: true,
	})
	if err != nil {
		t.Fatalf("ApplyProcessUpdate: %v", err)
	}

	_, err = CompletionReport(metadata)
	if !errors.Is(err, ErrPlanningArtifactContractIncomplete) {
		t.Fatalf("CompletionReport err = %v, want ErrPlanningArtifactContractIncomplete", err)
	}
	if err == nil || !strings.Contains(err.Error(), "beachhead segment") || !strings.Contains(err.Error(), "icp") || !strings.Contains(err.Error(), "expansion plan") {
		t.Fatalf("CompletionReport error = %v, want missing GTM operating sections", err)
	}
}

func TestCompletionReportRejectsIncompleteRiskReadinessArtifacts(t *testing.T) {
	description := "Build the pre-mortem, risk register, mitigation plan, and readiness checklist for the regulated rollout."
	plan := Analyze("Launch readiness pre-mortem", &description)
	metadata := ApplyMetadata(json.RawMessage(`{}`), plan)

	metadata, _, _, err := ApplyProcessUpdate(metadata, ProcessUpdate{
		Artifacts: []ArtifactEvidence{
			{
				Slug:     "risk-register",
				Summary:  "Material launch risks are captured.",
				Sections: []string{"major risks", "impact"},
			},
			{
				Slug:     "premortem",
				Summary:  "Likely failure modes are documented.",
				Sections: []string{"failure modes", "triggers", "responses"},
			},
			{
				Slug:     "mitigation-plan",
				Summary:  "Mitigations are listed.",
				Sections: []string{"mitigations", "dates"},
			},
			{
				Slug:     "readiness-checklist",
				Summary:  "Readiness blockers and rollback are recorded.",
				Sections: []string{"blockers", "rollback"},
			},
		},
		HasArtifactChanges: true,
	})
	if err != nil {
		t.Fatalf("ApplyProcessUpdate: %v", err)
	}

	_, err = CompletionReport(metadata)
	if !errors.Is(err, ErrPlanningArtifactContractIncomplete) {
		t.Fatalf("CompletionReport err = %v, want ErrPlanningArtifactContractIncomplete", err)
	}
	if err == nil || !strings.Contains(err.Error(), "severity") || !strings.Contains(err.Error(), "owners") || !strings.Contains(err.Error(), "go/no-go checklist") {
		t.Fatalf("CompletionReport error = %v, want missing risk readiness sections", err)
	}
}

func TestCompletionReportRequiresHypothesesForThinContextStrategy(t *testing.T) {
	description := "Define the product strategy and positioning tradeoffs for this greenfield analytics platform with no data yet."
	plan := Analyze("Strategy for greenfield analytics platform", &description)
	metadata := ApplyMetadata(json.RawMessage(`{}`), plan)

	metadata, _, report, err := ApplyProcessUpdate(metadata, ProcessUpdate{
		Artifacts: []ArtifactEvidence{
			{
				Slug:     "strategy-brief",
				Summary:  "Draft strategy for the greenfield analytics platform.",
				Sections: []string{"goal", "target segments", "not serving", "core capabilities"},
			},
			{
				Slug:     "tradeoff-matrix",
				Summary:  "Tradeoffs between focus options.",
				Sections: []string{"options", "tradeoffs", "decision"},
			},
			{
				Slug:     "decision-log",
				Summary:  "Decision record for the draft direction.",
				Sections: []string{"decision", "rationale", "owner"},
			},
			{
				Slug:     "success-narrative",
				Summary:  "Success narrative for the first phase.",
				Sections: []string{"key metrics", "defensibility", "milestones", "risks"},
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
	if err == nil || !strings.Contains(err.Error(), "hypotheses") || !strings.Contains(err.Error(), "open questions") {
		t.Fatalf("CompletionReport error = %v, want thin-context hypothesis requirements", err)
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
				Sections: []string{"goals", "scope"},
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
