package taskplan

import (
	"encoding/json"
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
