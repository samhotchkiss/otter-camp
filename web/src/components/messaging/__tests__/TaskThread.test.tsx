import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import TaskThread from "../TaskThread";

const wsState: { lastMessage: any } = { lastMessage: null };

vi.mock("../../../contexts/WebSocketContext", () => ({
  useWS: vi.fn(() => ({
    connected: true,
    lastMessage: wsState.lastMessage,
    sendMessage: vi.fn(),
  })),
}));

const mockFetch = vi.fn();
globalThis.fetch = mockFetch as any;

describe("TaskThread (messaging)", () => {
  beforeEach(() => {
    wsState.lastMessage = null;
    vi.clearAllMocks();
  });

  it("renders fetched messages and markdown formatting", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () =>
        Promise.resolve({
          messages: [
            {
              id: "msg-1",
              taskId: "task-1",
              senderId: "agent-1",
              senderName: "Scout",
              senderType: "agent",
              content: "Hello **world**",
              createdAt: "2026-02-04T12:00:00.000Z",
            },
          ],
        }),
    });

    render(<TaskThread taskId="task-1" apiEndpoint="/api/messages" />);

    expect(await screen.findByText("Scout")).toBeInTheDocument();
    const bold = screen.getByText("world");
    expect(bold.tagName).toBe("STRONG");
  });

  it("appends CommentAdded websocket messages for the same task", async () => {
    const { rerender } = render(
      <TaskThread
        taskId="task-1"
        initialMessages={[
          {
            id: "msg-1",
            taskId: "task-1",
            senderId: "agent-1",
            senderName: "Scout",
            senderType: "agent",
            content: "First message",
            createdAt: "2026-02-04T12:00:00.000Z",
          },
        ]}
      />,
    );

    expect(screen.getByText("First message")).toBeInTheDocument();

    wsState.lastMessage = {
      type: "CommentAdded",
      data: {
        taskId: "task-1",
        message: {
          id: "msg-2",
          taskId: "task-1",
          senderId: "agent-2",
          senderName: "Builder",
          senderType: "agent",
          content: "New message",
          createdAt: "2026-02-04T12:01:00.000Z",
        },
      },
    };

    rerender(
      <TaskThread
        taskId="task-1"
        initialMessages={[
          {
            id: "msg-1",
            taskId: "task-1",
            senderId: "agent-1",
            senderName: "Scout",
            senderType: "agent",
            content: "First message",
            createdAt: "2026-02-04T12:00:00.000Z",
          },
        ]}
      />,
    );

    expect(await screen.findByText("New message")).toBeInTheDocument();
  });

  it("ignores CommentAdded websocket messages for other tasks", async () => {
    const { rerender } = render(
      <TaskThread
        taskId="task-1"
        initialMessages={[
          {
            id: "msg-1",
            taskId: "task-1",
            senderId: "agent-1",
            senderName: "Scout",
            senderType: "agent",
            content: "First message",
            createdAt: "2026-02-04T12:00:00.000Z",
          },
        ]}
      />,
    );

    wsState.lastMessage = {
      type: "CommentAdded",
      data: {
        taskId: "task-2",
        message: {
          id: "msg-2",
          taskId: "task-2",
          senderId: "agent-2",
          senderName: "Builder",
          senderType: "agent",
          content: "Other task message",
          createdAt: "2026-02-04T12:01:00.000Z",
        },
      },
    };

    rerender(
      <TaskThread
        taskId="task-1"
        initialMessages={[
          {
            id: "msg-1",
            taskId: "task-1",
            senderId: "agent-1",
            senderName: "Scout",
            senderType: "agent",
            content: "First message",
            createdAt: "2026-02-04T12:00:00.000Z",
          },
        ]}
      />,
    );

    await waitFor(() => {
      expect(screen.queryByText("Other task message")).not.toBeInTheDocument();
    });
  });
});
