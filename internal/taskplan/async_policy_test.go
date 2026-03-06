package taskplan

import "testing"

func TestAssessAsyncDecisionClassifiesRepresentativeScenariosEX248(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		title       string
		description string
		want        string
	}{
		{
			name:        "default proceed",
			title:       "Implement webhook retries",
			description: "Ship the queue retry fix and update the tests.",
			want:        AsyncDecisionProceed,
		},
		{
			name:        "proceed and flag",
			title:       "Draft placeholder homepage copy",
			description: "Use a reasonable assumption for the hero line, keep it low-risk, and confirm later.",
			want:        AsyncDecisionProceedAndFlag,
		},
		{
			name:        "prepare for review",
			title:       "Prepare launch direction",
			description: "Pause for review before finalizing the selected concept.",
			want:        AsyncDecisionPrepareForReview,
		},
		{
			name:        "hard stop",
			title:       "Choose production pricing migration",
			description: "This change is irreversible, touches billing, and should not be guessed.",
			want:        AsyncDecisionHardStop,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := AssessAsyncDecision(tc.title, &tc.description)
			if got.Outcome != tc.want {
				t.Fatalf("Outcome = %q, want %q", got.Outcome, tc.want)
			}
			if got.Reason == "" {
				t.Fatal("Reason = empty, want value")
			}
		})
	}
}

func TestAssessAsyncDecisionLowRiskClarificationDoesNotHardStopEX248(t *testing.T) {
	t.Parallel()

	description := "Pick a placeholder label, make a reasonable assumption, and flag it for review later."

	got := AssessAsyncDecision("Clarify minor CTA wording", &description)
	if got.Outcome == AsyncDecisionHardStop {
		t.Fatalf("Outcome = %q, want non-hard-stop", got.Outcome)
	}
}

func TestAssessAsyncDecisionHighRiskIrreversibleDoesHardStopEX248(t *testing.T) {
	t.Parallel()

	description := "Proceeding would delete production data and trigger an irreversible billing migration."

	got := AssessAsyncDecision("Resolve ambiguous production pricing decision", &description)
	if got.Outcome != AsyncDecisionHardStop {
		t.Fatalf("Outcome = %q, want %q", got.Outcome, AsyncDecisionHardStop)
	}
}
