export type ReviewWorkflowState =
  | "draft"
  | "ready_for_review"
  | "needs_changes"
  | "approved";

const ALLOWED_TRANSITIONS: Record<ReviewWorkflowState, ReadonlyArray<ReviewWorkflowState>> = {
  draft: ["ready_for_review"],
  ready_for_review: ["needs_changes", "approved"],
  needs_changes: ["ready_for_review"],
  approved: [],
};

export function canTransitionReviewState(
  from: ReviewWorkflowState,
  to: ReviewWorkflowState
): boolean {
  return ALLOWED_TRANSITIONS[from].includes(to);
}

export function transitionReviewState(
  from: ReviewWorkflowState,
  to: ReviewWorkflowState
): ReviewWorkflowState {
  if (!canTransitionReviewState(from, to)) {
    throw new Error(`invalid transition from ${from} to ${to}`);
  }
  return to;
}

export function reviewStateLabel(state: ReviewWorkflowState): string {
  switch (state) {
    case "draft":
      return "Draft";
    case "ready_for_review":
      return "Ready for Review";
    case "needs_changes":
      return "Needs Changes";
    case "approved":
      return "Approved";
    default:
      return state;
  }
}
