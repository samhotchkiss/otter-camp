import { describe, expect, it } from "vitest";
import { formatProjectTaskSummary } from "./projectTaskSummary";

describe("formatProjectTaskSummary", () => {
  it("returns no-task copy when total is zero", () => {
    expect(formatProjectTaskSummary(0, 0)).toBe("No issues yet");
  });

  it("returns numeric summary when issues exist", () => {
    expect(formatProjectTaskSummary(3, 7)).toBe("3/7 issues");
  });
});
