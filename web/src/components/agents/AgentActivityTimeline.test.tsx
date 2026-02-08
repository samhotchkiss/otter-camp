import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { AgentActivityEvent } from "../../hooks/useAgentActivity";
import AgentActivityTimeline from "./AgentActivityTimeline";

const sampleEvents: AgentActivityEvent[] = [
  {
    id: "evt-1",
    orgId: "org-1",
    agentId: "main",
    sessionKey: "agent:main:main",
    trigger: "chat.slack",
    channel: "slack",
    summary: "Slack response",
    tokensUsed: 12,
    durationMs: 500,
    status: "completed",
    startedAt: new Date("2026-02-08T20:00:00.000Z"),
    createdAt: new Date("2026-02-08T20:00:00.000Z"),
  },
];

describe("AgentActivityTimeline", () => {
  it("renders loading/empty/error states", () => {
    const { rerender } = render(<AgentActivityTimeline events={[]} isLoading />);
    expect(screen.getByText("Loading activity...")).toBeInTheDocument();

    rerender(<AgentActivityTimeline events={[]} error="Failed to fetch" />);
    expect(screen.getByText("Failed to fetch")).toBeInTheDocument();

    rerender(<AgentActivityTimeline events={[]} />);
    expect(screen.getByText("No activity yet.")).toBeInTheDocument();
  });

  it("renders timeline events and load-more action", () => {
    const onLoadMore = vi.fn();
    render(
      <AgentActivityTimeline
        events={sampleEvents}
        hasMore
        onLoadMore={onLoadMore}
      />,
    );

    expect(screen.getByText("Slack response")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });
});
