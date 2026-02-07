import { describe, expect, it } from "vitest";

import { otterFacts } from "./otterFacts";

describe("otterFacts", () => {
  it("contains at least 100 facts", () => {
    expect(otterFacts.length).toBeGreaterThanOrEqual(100);
  });

  it("does not contain duplicates", () => {
    const unique = new Set(otterFacts);
    expect(unique.size).toBe(otterFacts.length);
  });

  it("uses trimmed, non-empty sentences", () => {
    otterFacts.forEach((fact) => {
      expect(fact).toBe(fact.trim());
      expect(fact.length).toBeGreaterThan(10);
      expect(fact.endsWith(".")).toBe(true);
    });
  });
});
