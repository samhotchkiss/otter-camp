import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import ActivityTriggerBadge from "./ActivityTriggerBadge";

describe("ActivityTriggerBadge", () => {
  it("renders mapped trigger label and status", () => {
    render(
      <ActivityTriggerBadge
        trigger="chat.slack"
        channel="slack"
        status="completed"
      />,
    );

    expect(screen.getByText("Slack")).toBeInTheDocument();
    expect(screen.getByText("completed")).toBeInTheDocument();
  });

  it("renders fallback labels for unknown triggers", () => {
    render(<ActivityTriggerBadge trigger="custom.pipeline" status="timeout" />);

    expect(screen.getByText("custom.pipeline")).toBeInTheDocument();
    expect(screen.getByText("timeout")).toBeInTheDocument();
  });

  it("maps heartbeat trigger", () => {
    render(<ActivityTriggerBadge trigger="heartbeat" />);

    expect(screen.getByText("Heartbeat")).toBeInTheDocument();
  });
});
