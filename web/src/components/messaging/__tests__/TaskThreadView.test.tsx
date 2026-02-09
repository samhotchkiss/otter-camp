import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import TaskThreadView from "../TaskThreadView";

describe("TaskThreadView", () => {
  it("renders agent initials in avatar fallback", () => {
    render(
      <TaskThreadView
        messages={[
          {
            id: "msg-1",
            senderId: "agent-1",
            senderName: "Jeff G",
            senderType: "agent",
            content: "Status update",
            createdAt: "2026-02-08T00:00:00.000Z",
          },
        ]}
      />,
    );

    expect(screen.getByLabelText("Agent avatar")).toHaveTextContent("JG");
  });
});
