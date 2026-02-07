import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import AgentCard, { type AgentCardData, formatLastActive } from "../AgentCard";

describe("formatLastActive", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns Never for empty or invalid values", () => {
    expect(formatLastActive(undefined)).toBe("Never");
    expect(formatLastActive(null)).toBe("Never");
    expect(formatLastActive("invalid")).toBe("Never");
    expect(formatLastActive("0")).toBe("Never");
    expect(formatLastActive(0)).toBe("Never");
  });

  it("formats relative time for recent activity", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-01T00:00:00Z"));

    expect(formatLastActive("2023-12-31T23:58:00Z")).toBe("2m ago");
    expect(formatLastActive("2023-12-31T23:59:30Z")).toBe("Just now");
  });

  it("falls back to '?' initials when agent name is missing", () => {
    const brokenAgent = {
      id: "agent-1",
      status: "online",
    } as unknown as AgentCardData;

    render(<AgentCard agent={brokenAgent} />);

    expect(screen.getByText("?")).toBeInTheDocument();
  });
});
