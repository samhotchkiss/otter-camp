import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import MessageHistory from "../MessageHistory";
import type { Agent, DMMessage } from "../types";

const agent: Agent = {
  id: "agent-1",
  name: "Agent One",
  status: "online",
  role: "Helper",
};

describe("MessageHistory", () => {
  it("renders an empty state when there are no messages", () => {
    render(<MessageHistory messages={[]} currentUserId="user-1" agent={agent} />);
    expect(
      screen.getByText(/Start a conversation with Agent One/i),
    ).toBeInTheDocument();
  });

  it("renders message content", () => {
    const messages: DMMessage[] = [
      {
        id: "m1",
        threadId: "dm_agent-1",
        senderId: "user-1",
        senderName: "You",
        senderType: "user",
        content: "Hello there",
        createdAt: "2024-01-01T00:00:00.000Z",
      },
      {
        id: "m2",
        threadId: "dm_agent-1",
        senderId: "agent-1",
        senderName: "Agent One",
        senderType: "agent",
        content: "Hi!",
        createdAt: "2024-01-01T00:01:00.000Z",
      },
    ];

    render(
      <MessageHistory messages={messages} currentUserId="user-1" agent={agent} />,
    );

    expect(screen.getByText("Hello there")).toBeInTheDocument();
    expect(screen.getByText("Hi!")).toBeInTheDocument();
  });

  it("invokes onLoadMore when clicking load earlier", async () => {
    const user = userEvent.setup();
    const onLoadMore = vi.fn();

    render(
      <MessageHistory
        messages={[]}
        currentUserId="user-1"
        agent={agent}
        hasMore
        onLoadMore={onLoadMore}
      />,
    );

    await user.click(screen.getByRole("button", { name: /Load earlier/i }));
    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });
});

