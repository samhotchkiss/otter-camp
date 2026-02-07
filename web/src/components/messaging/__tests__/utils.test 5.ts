import { describe, expect, it } from "vitest";
import { getInitials } from "../utils";

describe("getInitials", () => {
  it("returns initials for valid names", () => {
    expect(getInitials("Stone Weaver")).toBe("SW");
  });

  it("handles empty and malformed values safely", () => {
    expect(getInitials(undefined)).toBe("?");
    expect(getInitials(null)).toBe("?");
    expect(getInitials("   ")).toBe("?");
    expect(getInitials({})).toBe("?");
  });
});
