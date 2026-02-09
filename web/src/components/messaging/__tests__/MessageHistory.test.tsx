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

  it("renders image attachments inline", () => {
    const messages: DMMessage[] = [
      {
        id: "m-image",
        threadId: "dm_agent-1",
        senderId: "agent-1",
        senderName: "Agent One",
        senderType: "agent",
        content: "See screenshot",
        attachments: [
          {
            id: "att-image-1",
            filename: "screenshot.png",
            size_bytes: 1024,
            mime_type: "image/png",
            url: "/uploads/screenshot.png",
            thumbnail_url: "/uploads/screenshot-thumb.png",
          },
        ],
        createdAt: "2024-01-01T00:01:00.000Z",
      },
    ];

    render(<MessageHistory messages={messages} currentUserId="user-1" agent={agent} />);

    expect(screen.getByAltText("screenshot.png")).toBeInTheDocument();
  });

  it("renders non-image attachments as download cards", () => {
    const messages: DMMessage[] = [
      {
        id: "m-file",
        threadId: "dm_agent-1",
        senderId: "agent-1",
        senderName: "Agent One",
        senderType: "agent",
        content: "",
        attachments: [
          {
            id: "att-file-1",
            filename: "report.pdf",
            size_bytes: 2048,
            mime_type: "application/pdf",
            url: "/uploads/report.pdf",
          },
        ],
        createdAt: "2024-01-01T00:01:00.000Z",
      },
    ];

    render(<MessageHistory messages={messages} currentUserId="user-1" agent={agent} />);

    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Download" })).toHaveAttribute(
      "href",
      "/uploads/report.pdf",
    );
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

  it("shows failed message state and retries", async () => {
    const user = userEvent.setup();
    const onRetryMessage = vi.fn();

    const messages: DMMessage[] = [
      {
        id: "failed-1",
        threadId: "dm_agent-1",
        senderId: "user-1",
        senderName: "You",
        senderType: "user",
        content: "Please run this",
        createdAt: "2024-01-01T00:00:00.000Z",
        failed: true,
      },
    ];

    render(
      <MessageHistory
        messages={messages}
        currentUserId="user-1"
        agent={agent}
        onRetryMessage={onRetryMessage}
      />,
    );

    expect(screen.getByText("Send failed")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Retry/i }));
    expect(onRetryMessage).toHaveBeenCalledTimes(1);
    expect(onRetryMessage).toHaveBeenCalledWith(messages[0]);
  });

  it("renders session reset divider entries", () => {
    const messages: DMMessage[] = [
      {
        id: "reset-1",
        threadId: "project:abc",
        senderId: "session-reset",
        senderName: "Session",
        senderType: "agent",
        content: "",
        createdAt: "2026-02-07T12:00:00.000Z",
        isSessionReset: true,
      },
    ];

    render(<MessageHistory messages={messages} currentUserId="user-1" agent={agent} />);

    expect(screen.getByTestId("project-chat-session-divider")).toBeInTheDocument();
    expect(screen.getByText(/New chat session started/i)).toBeInTheDocument();
  });

  it("resolves agent sender labels and initials at render time", () => {
    const messages: DMMessage[] = [
      {
        id: "m-agent-1",
        threadId: "dm_avatar-design",
        senderId: "agent:avatar-design",
        senderName: "avatar-design",
        senderType: "agent",
        content: "Working on your mockups now.",
        createdAt: "2026-02-08T00:00:00.000Z",
      },
    ];

    render(
      <MessageHistory
        messages={messages}
        currentUserId="user-1"
        agent={agent}
        agentNamesByID={new Map([["avatar-design", "Jeff G"]])}
      />,
    );

    expect(screen.getByText("Jeff G")).toBeInTheDocument();
    expect(screen.queryByText("avatar-design")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Jeff G")).toHaveTextContent("JG");
  });
});
