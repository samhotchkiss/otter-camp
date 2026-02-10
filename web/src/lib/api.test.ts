import { describe, expect, it } from "vitest";

import { API_URL } from "./api";

describe("API_URL", () => {
  it("falls back to window.location.origin when VITE_API_URL is not configured", () => {
    expect(API_URL).toBe(window.location.origin);
  });
});
