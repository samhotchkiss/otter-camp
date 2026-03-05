package taskplan

import "testing"

func TestResolveReviewPolicyTaskOverrideBeatsProjectPolicy(t *testing.T) {
	projectSettings := ApplyReviewPolicy(nil, ReviewPolicy{
		Mode:       PolicyHumanReviewRequired,
		Guardrails: []string{"Require sign-off"},
	})
	taskMetadata := ApplyReviewPolicy(nil, ReviewPolicy{
		Mode:       PolicyDelegatedAuthority,
		Guardrails: []string{"Keep brand-safe", "Avoid unsupported claims"},
	})

	policy := ResolveReviewPolicy(projectSettings, taskMetadata)
	if policy.Mode != PolicyDelegatedAuthority {
		t.Fatalf("Mode = %q, want %q", policy.Mode, PolicyDelegatedAuthority)
	}
	if policy.Source != "task" {
		t.Fatalf("Source = %q, want task", policy.Source)
	}
	if len(policy.Guardrails) != 2 {
		t.Fatalf("guardrails len = %d, want 2", len(policy.Guardrails))
	}
}

func TestResolveReviewPolicyFallsBackToProjectSettings(t *testing.T) {
	projectSettings := ApplyReviewPolicy(nil, ReviewPolicy{
		Mode:           PolicyHumanReviewPreferred,
		SummaryCadence: "twice-weekly",
	})

	policy := ResolveReviewPolicy(projectSettings, nil)
	if policy.Mode != PolicyHumanReviewPreferred {
		t.Fatalf("Mode = %q, want %q", policy.Mode, PolicyHumanReviewPreferred)
	}
	if policy.Source != "project" {
		t.Fatalf("Source = %q, want project", policy.Source)
	}
	if policy.SummaryCadence != "twice-weekly" {
		t.Fatalf("SummaryCadence = %q, want twice-weekly", policy.SummaryCadence)
	}
}
