import { describe, expect, it } from "vitest";
import { getActivityDescription } from "./activityFormat";

describe("getActivityDescription", () => {
  it("builds useful git.push text from metadata", () => {
    const description = getActivityDescription({
      type: "git.push",
      metadata: {
        branch: "main",
        commit_message: "Fix feed fallback wiring",
      },
    });

    expect(description).toContain("main");
    expect(description).toContain("Fix feed fallback wiring");
  });

  it("ignores summary echoes that only repeat git push type", () => {
    const description = getActivityDescription({
      type: "git.push",
      actorName: "Sam",
      summary: "Sam: git.push",
      metadata: {
        branch: "main",
        commit_message: "Fix feed fallback wiring",
      },
    });

    expect(description).toContain("main");
    expect(description).toContain("Fix feed fallback wiring");
    expect(description).not.toBe("git.push");
  });

  it("ignores placeholder-prefixed git push summary echoes", () => {
    const description = getActivityDescription({
      type: "git.push",
      actorName: "System",
      summary: "Unknown: git.push",
      metadata: {
        branch: "main",
        commit_message: "Fix project activity formatting",
      },
    });

    expect(description).toContain("main");
    expect(description).toContain("Fix project activity formatting");
  });
});
