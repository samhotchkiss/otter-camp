import { describe, expect, it } from "vitest";
import {
  AGENT_PROFILES,
  ROLE_CATEGORIES,
  START_FROM_SCRATCH_PROFILE,
  filterAgentProfiles,
} from "./index";

describe("agent profile data", () => {
  it("ships the full 15-profile starter set", () => {
    expect(AGENT_PROFILES.length).toBe(15);
    const ids = AGENT_PROFILES.map((profile) => profile.id);
    expect(ids).toContain("sloane");
    expect(ids).toContain("rowan");
  });

  it("has unique profile ids", () => {
    const ids = AGENT_PROFILES.map((profile) => profile.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  it("includes expected role categories", () => {
    expect(ROLE_CATEGORIES).toContain("Engineering");
    expect(ROLE_CATEGORIES).toContain("Research");
    expect(ROLE_CATEGORIES).toContain("Operations");
  });

  it("filters profiles by role and keyword", () => {
    const roleFiltered = filterAgentProfiles(AGENT_PROFILES, { roleCategory: "Research", query: "" });
    expect(roleFiltered.every((profile) => profile.roleCategory === "Research")).toBe(true);

    const keywordFiltered = filterAgentProfiles(AGENT_PROFILES, { roleCategory: "all", query: "witty" });
    expect(keywordFiltered.some((profile) => profile.name === "Kit")).toBe(true);
  });

  it("exposes start-from-scratch profile metadata", () => {
    expect(START_FROM_SCRATCH_PROFILE.id).toBe("start-from-scratch");
    expect(START_FROM_SCRATCH_PROFILE.isStarter).toBe(false);
  });
});
