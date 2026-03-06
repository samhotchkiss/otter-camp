package taskplan

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalyzeMapsPlaybooksToDeterministicFollowOnCandidates(t *testing.T) {
	tests := []struct {
		name               string
		plan               Plan
		wantStatus         string
		wantActionType     string
		wantWorkStatus     string
		wantTarget         string
		wantSourceArtifact string
		wantTitlePart      string
	}{
		{
			name:               "discovery creates draft spec follow-on",
			plan:               Analyze("Validate the onboarding problem", strPtr("Run customer interviews, document assumptions, and build a validation plan for this new product idea before we commit scope.")),
			wantStatus:         FollowOnStatusProposed,
			wantActionType:     FollowOnActionDraftTask,
			wantWorkStatus:     "draft",
			wantTarget:         PlaybookExecutionSpec,
			wantSourceArtifact: "validation-plan",
			wantTitlePart:      "PRD/spec",
		},
		{
			name: "strategy review work becomes review packet follow-on",
			plan: AnalyzeWithPolicy("Homepage design options", strPtr("Generate 10 homepage design options, compare them, shortlist the best two, and recommend a direction with tradeoffs."), ReviewPolicy{
				Mode: PolicyHumanReviewRequired,
			}),
			wantStatus:         FollowOnStatusProposed,
			wantActionType:     FollowOnActionReviewPacket,
			wantWorkStatus:     "",
			wantTarget:         PlaybookStrategy,
			wantSourceArtifact: "success-narrative",
			wantTitlePart:      "roadmap",
		},
		{
			name:               "strategy work can queue roadmap follow-on directly",
			plan:               Analyze("Positioning and roadmap for analytics expansion", strPtr("Define the product strategy, positioning tradeoffs, and the roadmap sequence for the analytics platform.")),
			wantStatus:         FollowOnStatusProposed,
			wantActionType:     FollowOnActionReadyTask,
			wantWorkStatus:     "queued",
			wantTarget:         PlaybookStrategy,
			wantSourceArtifact: "success-narrative",
			wantTitlePart:      "roadmap",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.plan.FollowOn.Status != tc.wantStatus {
				t.Fatalf("FollowOn.Status = %q, want %q", tc.plan.FollowOn.Status, tc.wantStatus)
			}
			if len(tc.plan.FollowOn.Candidates) == 0 {
				t.Fatalf("FollowOn.Candidates = %#v, want non-empty candidates", tc.plan.FollowOn.Candidates)
			}
			first := tc.plan.FollowOn.Candidates[0]
			if first.ActionType != tc.wantActionType {
				t.Fatalf("candidate.ActionType = %q, want %q", first.ActionType, tc.wantActionType)
			}
			if first.WorkStatus != tc.wantWorkStatus {
				t.Fatalf("candidate.WorkStatus = %q, want %q", first.WorkStatus, tc.wantWorkStatus)
			}
			if first.TargetPlaybook != tc.wantTarget {
				t.Fatalf("candidate.TargetPlaybook = %q, want %q", first.TargetPlaybook, tc.wantTarget)
			}
			if first.SourceArtifactSlug != tc.wantSourceArtifact {
				t.Fatalf("candidate.SourceArtifactSlug = %q, want %q", first.SourceArtifactSlug, tc.wantSourceArtifact)
			}
			if !containsSubstring(first.Title, tc.wantTitlePart) {
				t.Fatalf("candidate.Title = %q, want substring %q", first.Title, tc.wantTitlePart)
			}
		})
	}
}

func TestApplyProcessUpdatePersistsFollowOnStopReason(t *testing.T) {
	description := "Run customer interviews, document assumptions, and build a validation plan for this new product idea before we commit scope."
	plan := Analyze("Validate the onboarding problem", &description)
	metadata := ApplyMetadata(json.RawMessage(`{}`), plan)

	stopReason := "Validation is paused until the PM confirms which opportunity is worth turning into delivery work."
	updated, parsed, _, err := ApplyProcessUpdate(metadata, ProcessUpdate{
		FollowOnStopReason: &stopReason,
	})
	if err != nil {
		t.Fatalf("ApplyProcessUpdate: %v", err)
	}

	reparsed, ok := Parse(updated)
	if !ok {
		t.Fatal("Parse(updated) = false, want true")
	}
	for _, candidatePlan := range []Plan{parsed, reparsed} {
		if candidatePlan.FollowOnStopReason != stopReason {
			t.Fatalf("FollowOnStopReason = %q, want %q", candidatePlan.FollowOnStopReason, stopReason)
		}
		if candidatePlan.FollowOn.Status != FollowOnStatusStopped {
			t.Fatalf("FollowOn.Status = %q, want %q", candidatePlan.FollowOn.Status, FollowOnStatusStopped)
		}
		if candidatePlan.FollowOn.StopReason != stopReason {
			t.Fatalf("FollowOn.StopReason = %q, want %q", candidatePlan.FollowOn.StopReason, stopReason)
		}
		if len(candidatePlan.FollowOn.Candidates) == 0 {
			t.Fatalf("FollowOn.Candidates = %#v, want preserved candidates", candidatePlan.FollowOn.Candidates)
		}
	}
}

func containsSubstring(value, needle string) bool {
	return strings.Contains(value, needle)
}

func strPtr(value string) *string {
	return &value
}
