import { describe, expect, it } from "vitest";
import {
  canTransitionReviewState,
  transitionReviewState,
  reviewStateLabel,
} from "./reviewStateMachine";

describe("reviewStateMachine", () => {
  it("allows only valid transitions", () => {
    expect(canTransitionReviewState("draft", "ready_for_review")).toBe(true);
    expect(canTransitionReviewState("draft", "approved")).toBe(false);
    expect(canTransitionReviewState("ready_for_review", "needs_changes")).toBe(true);
    expect(canTransitionReviewState("ready_for_review", "approved")).toBe(true);
    expect(canTransitionReviewState("needs_changes", "ready_for_review")).toBe(true);
    expect(canTransitionReviewState("approved", "draft")).toBe(false);
  });

  it("throws for invalid transitions", () => {
    expect(() => transitionReviewState("draft", "approved")).toThrow(
      "invalid transition"
    );
  });

  it("maps display labels", () => {
    expect(reviewStateLabel("draft")).toBe("Draft");
    expect(reviewStateLabel("ready_for_review")).toBe("Ready for Review");
    expect(reviewStateLabel("needs_changes")).toBe("Needs Changes");
    expect(reviewStateLabel("approved")).toBe("Approved");
  });
});
