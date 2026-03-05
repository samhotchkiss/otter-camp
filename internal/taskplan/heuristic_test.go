package taskplan

import (
	"encoding/json"
	"reflect"
	"testing"
)

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
}
