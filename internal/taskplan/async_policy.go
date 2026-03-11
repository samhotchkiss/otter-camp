package taskplan

const (
	AsyncDecisionProceed          = "proceed"
	AsyncDecisionProceedAndFlag   = "proceed_and_flag"
	AsyncDecisionPrepareForReview = "prepare_for_review"
	AsyncDecisionHardStop         = "hard_stop"
)

var (
	asyncProceedAndFlagSignals = []string{
		"reasonable assumption",
		"assume for now",
		"placeholder",
		"draft",
		"low risk",
		"low-risk",
		"minor wording",
		"minor copy",
		"copy tweak",
		"style preference",
		"subjective preference",
		"confirm later",
		"review later",
		"flag for review",
		"follow up later",
		"follow-up later",
		"defer review",
		"can refine later",
		"non-blocking",
		"not blocking",
	}
	asyncPrepareForReviewSignals = []string{
		"pause for review",
		"pause for human review",
		"pause for approval",
		"await review",
		"await approval",
		"review checkpoint",
		"approval checkpoint",
		"sign off",
		"sign-off",
		"signoff",
		"prepare for review",
		"before finalizing",
		"before finalising",
		"before implementation",
		"before merge",
		"before shipping",
		"before launch",
		"human review before continuing",
		"human approval before continuing",
	}
	asyncHardStopSignals = []string{
		"irreversible",
		"cannot undo",
		"can't undo",
		"permanent",
		"delete production",
		"drop table",
		"drop database",
		"migrate production",
		"production migration",
		"production data",
		"billing",
		"pricing",
		"invoice",
		"charge customers",
		"payment",
		"legal",
		"contract",
		"privacy",
		"security",
		"credentials",
		"press release",
		"brand rename",
		"rename the company",
		"public launch announcement",
		"invalidates downstream",
		"invalidate downstream",
		"highly ambiguous",
		"ambiguous requirement",
	}
)

type AsyncDecision struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

func (d AsyncDecision) NeedsReviewArtifact() bool {
	return d.Outcome == AsyncDecisionProceedAndFlag ||
		d.Outcome == AsyncDecisionPrepareForReview ||
		d.Outcome == AsyncDecisionHardStop
}

func (d AsyncDecision) PausesTask() bool {
	return d.Outcome == AsyncDecisionPrepareForReview || d.Outcome == AsyncDecisionHardStop
}

func AssessAsyncDecision(title string, description *string) AsyncDecision {
	text := normalizeText(title, description)
	if text == "" {
		return AsyncDecision{
			Outcome: AsyncDecisionProceed,
			Reason:  "task can continue without waiting on human input",
		}
	}

	switch {
	case containsAny(text, asyncHardStopSignals):
		return AsyncDecision{
			Outcome: AsyncDecisionHardStop,
			Reason:  "decision is high-impact, irreversible, or too ambiguous to guess safely",
		}
	case containsAny(text, asyncPrepareForReviewSignals):
		return AsyncDecision{
			Outcome: AsyncDecisionPrepareForReview,
			Reason:  "task explicitly calls for a human review checkpoint before continuing",
		}
	case containsAny(text, asyncProceedAndFlagSignals):
		return AsyncDecision{
			Outcome: AsyncDecisionProceedAndFlag,
			Reason:  "a low-risk assumption can keep async progress moving and be reviewed later",
		}
	default:
		return AsyncDecision{
			Outcome: AsyncDecisionProceed,
			Reason:  "task can continue without waiting on human input",
		}
	}
}
