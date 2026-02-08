import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { AgentActivityEvent } from "../../hooks/useAgentActivity";
import AgentActivityItem from "./AgentActivityItem";

const sampleEvent: AgentActivityEvent = {
  id: "evt-1",
  orgId: "org-1",
  agentId: "main",
  sessionKey: "agent:main:main",
  trigger: "chat.slack",
  channel: "slack",
  summary: "Responded to a Slack request",
  detail: "Detailed payload from Slack message",
  projectId: "project-1",
  issueId: "issue-1",
  issueNumber: 42,
  threadId: "thread-1",
  tokensUsed: 128,
  modelUsed: "opus-4-6",
  durationMs: 3200,
  status: "completed",
  startedAt: new Date("2026-02-08T20:00:00.000Z"),
  completedAt: new Date("2026-02-08T20:00:03.000Z"),
  createdAt: new Date("2026-02-08T20:00:00.000Z"),
};

describe("AgentActivityItem", () => {
  it("renders summary + metadata", () => {
    render(<AgentActivityItem event={sampleEvent} />);

    expect(screen.getByText("Responded to a Slack request")).toBeInTheDocument();
    expect(screen.getByText("opus-4-6")).toBeInTheDocument();
    expect(screen.getByText("128 tokens")).toBeInTheDocument();
    expect(screen.getByText("3s")).toBeInTheDocument();
  });

  it("toggles detail section", () => {
    render(<AgentActivityItem event={sampleEvent} />);

    expect(screen.queryByText("Detailed payload from Slack message")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Show details" }));

    expect(screen.getByText("Detailed payload from Slack message")).toBeInTheDocument();
    expect(screen.getByText("Project: project-1")).toBeInTheDocument();
    expect(screen.getByText("Issue #: 42")).toBeInTheDocument();
  });
});
