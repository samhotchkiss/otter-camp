package taskplan

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAnalyzeSelectsBaselinePlaybooks(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		description string
		playbook    string
	}{
		{
			name:        "discovery",
			title:       "Validate the onboarding problem before writing specs",
			description: "Run customer interviews, capture assumptions, and design a lightweight validation plan for this new product idea.",
			playbook:    PlaybookDiscovery,
		},
		{
			name:        "strategy",
			title:       "Positioning and roadmap for analytics expansion",
			description: "Define the product strategy, positioning tradeoffs, and the roadmap sequence for the analytics platform.",
			playbook:    PlaybookStrategy,
		},
		{
			name:        "execution spec",
			title:       "PRD for billing migration",
			description: "Write the PRD, requirements, implementation plan, and acceptance criteria for the billing migration.",
			playbook:    PlaybookExecutionSpec,
		},
		{
			name:        "backlog decomposition",
			title:       "Break the approved PRD into delivery work",
			description: "Decompose the scope into epics, user stories, sprint-ready tickets, and sequencing constraints.",
			playbook:    PlaybookBacklogDecomposition,
		},
		{
			name:        "risk readiness",
			title:       "Launch readiness pre-mortem",
			description: "Build the pre-mortem, risk register, mitigation plan, and readiness checklist for the regulated rollout.",
			playbook:    PlaybookRiskReadiness,
		},
		{
			name:        "metrics",
			title:       "Metric tree and instrumentation plan",
			description: "Define north-star metrics, KPI instrumentation, the dashboard spec, and weekly metric review cadence.",
			playbook:    PlaybookMetrics,
		},
		{
			name:        "gtm launch",
			title:       "Go-to-market launch plan",
			description: "Create the GTM launch plan, messaging brief, channel plan, and sales enablement checklist for release.",
			playbook:    PlaybookGTMLaunch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := Analyze(tc.title, &tc.description)
			if plan.Playbook != tc.playbook {
				t.Fatalf("Playbook = %q, want %q", plan.Playbook, tc.playbook)
			}
			if plan.WorkType == "" || plan.ProjectStage == "" || plan.EvidenceMaturity == "" || plan.RiskLevel == "" {
				t.Fatalf("planning context should be fully populated, got %#v", plan)
			}
			if len(plan.Artifacts) == 0 {
				t.Fatalf("Artifacts = %#v, want non-empty artifact plan", plan.Artifacts)
			}
			if len(plan.FollowOnSuggestions) == 0 {
				t.Fatalf("FollowOnSuggestions = %#v, want non-empty follow-on suggestions", plan.FollowOnSuggestions)
			}
		})
	}
}

func TestAnalyzeClassifiesSubjectiveMultiOptionRequestsAsReviewAndRefinement(t *testing.T) {
	description := "Generate 10 homepage design options, compare them, shortlist the best two, and recommend a direction with tradeoffs."

	plan := Analyze("Homepage design options", &description)
	if !plan.RequiresReviewAndRefinement() {
		t.Fatalf("RequiresReviewAndRefinement = false, want true")
	}
	if plan.DefaultTemplateSlug != ReviewRefinementTemplate {
		t.Fatalf("DefaultTemplateSlug = %q, want %q", plan.DefaultTemplateSlug, ReviewRefinementTemplate)
	}
	if len(plan.PlannedStages) != 3 {
		t.Fatalf("planned stages len = %d, want 3", len(plan.PlannedStages))
	}
	if got := plan.PlannedStages[0]; got != "generation" {
		t.Fatalf("planned stage[0] = %q, want generation", got)
	}
	if len(plan.ReviewPacket.Sections) < 4 {
		t.Fatalf("review packet sections len = %d, want >= 4", len(plan.ReviewPacket.Sections))
	}
	if plan.Playbook != PlaybookStrategy {
		t.Fatalf("Playbook = %q, want %q", plan.Playbook, PlaybookStrategy)
	}
}

func TestAnalyzeClassifiesVerifiableRequestsAsExecutionFirst(t *testing.T) {
	description := "Verify whether the webhook retry policy still uses exponential backoff and confirm the current maximum delay."

	plan := Analyze("Does the webhook retry policy still back off exponentially?", &description)
	if plan.RequiresReviewAndRefinement() {
		t.Fatalf("RequiresReviewAndRefinement = true, want false")
	}
	if plan.Mode != ModeExecutionFirst {
		t.Fatalf("Mode = %q, want %q", plan.Mode, ModeExecutionFirst)
	}
	if plan.Playbook != PlaybookExecutionSpec {
		t.Fatalf("Playbook = %q, want %q", plan.Playbook, PlaybookExecutionSpec)
	}
}

func TestAnalyzeWithDelegatedPolicyChoosesAutonomousInternalReview(t *testing.T) {
	description := "Generate 10 homepage design options, compare them, and recommend a direction that stays on-brand."

	plan := AnalyzeWithPolicy("Homepage design options", &description, ReviewPolicy{
		Mode:       PolicyDelegatedAuthority,
		Guardrails: []string{"Use OtterCamp voice", "Avoid unsupported claims"},
	})
	if plan.Mode != ModeAutonomousInternal {
		t.Fatalf("Mode = %q, want %q", plan.Mode, ModeAutonomousInternal)
	}
	if plan.DefaultTemplateSlug != InternalReviewTemplate {
		t.Fatalf("DefaultTemplateSlug = %q, want %q", plan.DefaultTemplateSlug, InternalReviewTemplate)
	}
	if plan.ReviewPolicyMode != PolicyDelegatedAuthority {
		t.Fatalf("ReviewPolicyMode = %q, want %q", plan.ReviewPolicyMode, PolicyDelegatedAuthority)
	}
	if !reflect.DeepEqual(plan.Guardrails, []string{"Use OtterCamp voice", "Avoid unsupported claims"}) {
		t.Fatalf("Guardrails = %#v, want delegated guardrails", plan.Guardrails)
	}
	if !reflect.DeepEqual(plan.PlannedStages, []string{"generation", "internal_review", "autonomous_delivery"}) {
		t.Fatalf("PlannedStages = %#v, want autonomous internal review stages", plan.PlannedStages)
	}
	if plan.Playbook != PlaybookStrategy {
		t.Fatalf("Playbook = %q, want %q", plan.Playbook, PlaybookStrategy)
	}
}

func TestAnalyzeWithPreferredPolicyAddsPeriodicReviewSummary(t *testing.T) {
	description := "Brainstorm 12 campaign concepts, compare them, and recommend which ones to keep shipping."

	plan := AnalyzeWithPolicy("Campaign concepts", &description, ReviewPolicy{
		Mode: PolicyHumanReviewPreferred,
	})
	if plan.Mode != ModeAutonomousInternal {
		t.Fatalf("Mode = %q, want %q", plan.Mode, ModeAutonomousInternal)
	}
	if plan.SummaryCadence != "weekly" {
		t.Fatalf("SummaryCadence = %q, want weekly", plan.SummaryCadence)
	}
	if !reflect.DeepEqual(plan.PlannedStages, []string{"generation", "internal_review", "periodic_review_summary"}) {
		t.Fatalf("PlannedStages = %#v, want periodic review summary stages", plan.PlannedStages)
	}
}

func TestAnalyzeAmbiguousInputsFallbackDeterministically(t *testing.T) {
	description := "Need a plan for this work."

	plan := Analyze("Plan next steps", &description)
	if plan.Playbook != PlaybookExecutionSpec {
		t.Fatalf("Playbook = %q, want %q", plan.Playbook, PlaybookExecutionSpec)
	}
	if plan.ProjectStage != StageDefinition {
		t.Fatalf("ProjectStage = %q, want %q", plan.ProjectStage, StageDefinition)
	}
	if plan.EvidenceMaturity != EvidenceDirectional {
		t.Fatalf("EvidenceMaturity = %q, want %q", plan.EvidenceMaturity, EvidenceDirectional)
	}
	if plan.RiskLevel != RiskLow {
		t.Fatalf("RiskLevel = %q, want %q", plan.RiskLevel, RiskLow)
	}
}

func TestApplyMetadataRoundTripsReviewPlanning(t *testing.T) {
	description := "Brainstorm 12 launch-week blog post ideas, compare them, and recommend a shortlist."

	plan := Analyze("Launch-week blog post ideas", &description)
	metadata := ApplyMetadata(json.RawMessage(`{"existing":true}`), plan)

	parsed, ok := Parse(metadata)
	if !ok {
		t.Fatal("Parse(metadata) = false, want true")
	}
	if parsed.Mode != ModeReviewAndRefinement {
		t.Fatalf("Mode = %q, want %q", parsed.Mode, ModeReviewAndRefinement)
	}
	if parsed.ReviewPacket.Summary == "" {
		t.Fatal("review packet summary = empty, want value")
	}
	if len(parsed.ReviewPacket.Sections) == 0 {
		t.Fatal("review packet sections = empty, want values")
	}
	if parsed.Playbook != PlaybookGTMLaunch {
		t.Fatalf("Playbook = %q, want %q", parsed.Playbook, PlaybookGTMLaunch)
	}
	if len(parsed.Artifacts) == 0 {
		t.Fatalf("Artifacts = %#v, want non-empty artifact list", parsed.Artifacts)
	}
	if len(parsed.FollowOnSuggestions) == 0 {
		t.Fatalf("FollowOnSuggestions = %#v, want non-empty follow-ons", parsed.FollowOnSuggestions)
	}
}
