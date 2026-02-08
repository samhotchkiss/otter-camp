import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Emission } from "../hooks/useEmissions";
import AgentWorkingIndicator from "./AgentWorkingIndicator";

const makeEmission = (
  timestamp: string,
  overrides: Partial<Emission> = {},
): Emission => ({
  id: "em-1",
  source_type: "agent",
  source_id: "agent-1",
  kind: "status",
  summary: "working",
  timestamp,
  ...overrides,
});

describe("AgentWorkingIndicator", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("shows active state when emission is recent", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-02-08T12:00:30Z"));

    render(
      <AgentWorkingIndicator
        latestEmission={makeEmission("2026-02-08T12:00:10Z")}
        activeWindowSeconds={30}
      />,
    );

    expect(screen.getByText(/Agent is working/i)).toBeInTheDocument();
  });

  it("shows idle state when no recent emission exists", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-02-08T12:01:30Z"));

    render(
      <AgentWorkingIndicator
        latestEmission={makeEmission("2026-02-08T12:00:00Z")}
        activeWindowSeconds={30}
      />,
    );

    expect(screen.getByText("Idle")).toBeInTheDocument();
  });

  it("is deterministic for empty inputs", () => {
    render(<AgentWorkingIndicator latestEmission={null} />);
    expect(screen.getByText("Idle")).toBeInTheDocument();
  });
});
