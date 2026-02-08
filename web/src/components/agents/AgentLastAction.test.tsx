import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import AgentLastAction from "./AgentLastAction";

describe("AgentLastAction", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders trigger badge, summary, and relative time", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-02-08T13:00:00.000Z"));

    render(
      <AgentLastAction
        summary="Responded to Sam in #leadership"
        trigger="chat.slack"
        status="completed"
        startedAt="2026-02-08T12:58:00.000Z"
      />,
    );

    expect(screen.getByText("Slack")).toBeInTheDocument();
    expect(screen.getByText("Responded to Sam in #leadership")).toBeInTheDocument();
    expect(screen.getByText("2m ago")).toBeInTheDocument();
  });

  it("shows fallback text when no last action exists", () => {
    render(<AgentLastAction />);
    expect(screen.getByText("No recent activity")).toBeInTheDocument();
  });
});
