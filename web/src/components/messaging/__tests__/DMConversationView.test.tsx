import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import DMConversationView from "../DMConversationView";
import type { Agent } from "../types";

vi.mock("../../../contexts/WebSocketContext", () => ({
  useWS: vi.fn(() => ({
    connected: true,
    lastMessage: null,
    sendMessage: vi.fn(() => true),
  })),
}));

const mockFetch = vi.fn();
globalThis.fetch = mockFetch as unknown as typeof fetch;

describe("DMConversationView", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.localStorage.clear();
  });

  it("fetches and renders initial messages", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () =>
        Promise.resolve({
          messages: [
            {
              id: "m1",
              threadId: "dm_agent-1",
              senderId: "agent-1",
              senderName: "Agent One",
              senderType: "agent",
              content: "Hello from the agent",
              createdAt: new Date().toISOString(),
            },
          ],
          hasMore: false,
          totalCount: 1,
        }),
    });

    const agent: Agent = {
      id: "agent-1",
      name: "Agent One",
      status: "online",
      role: "Helper",
    };

    render(<DMConversationView agent={agent} />);

    await waitFor(() => {
      expect(screen.getByText("Hello from the agent")).toBeInTheDocument();
    });

    expect(mockFetch).toHaveBeenCalledTimes(1);
    const url = String(mockFetch.mock.calls[0]?.[0] ?? "");
    expect(url).toContain(`${window.location.origin}/api/messages?`);
    expect(url).toContain("/api/messages?");
    expect(url).toContain("thread_id=dm_agent-1");
    expect(screen.getByText(/1 message/i)).toBeInTheDocument();
  });

  it("passes org and bearer auth context on message fetch", async () => {
    window.localStorage.setItem("otter-camp-org-id", "org-123");
    window.localStorage.setItem("otter_camp_token", "token-123");

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () =>
        Promise.resolve({
          messages: [],
          hasMore: false,
          totalCount: 0,
        }),
      headers: {
        get: () => "application/json",
      },
    });

    const agent: Agent = {
      id: "agent-2",
      name: "Agent Two",
      status: "offline",
    };

    render(<DMConversationView agent={agent} />);

    await waitFor(() => {
      expect(screen.getByText(/Start a conversation with Agent Two/i)).toBeInTheDocument();
    });

    const call = mockFetch.mock.calls[0] as [string, RequestInit];
    expect(call[0]).toContain("org_id=org-123");
    expect(call[1].headers).toMatchObject({
      Authorization: "Bearer token-123",
    });
  });

  it("normalizes malformed message payloads without crashing", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () =>
        Promise.resolve({
          messages: [
            {
              id: "m-fallback",
              threadId: "dm_agent-1",
              senderId: "agent-1",
              content: "Hello with missing sender fields",
              createdAt: new Date().toISOString(),
            },
          ],
          hasMore: false,
          totalCount: 1,
        }),
    });

    const agent: Agent = {
      id: "agent-1",
      name: "Agent One",
      status: "online",
      role: "Helper",
    };

    render(<DMConversationView agent={agent} />);

    await waitFor(() => {
      expect(
        screen.getByText("Hello with missing sender fields"),
      ).toBeInTheDocument();
    });

    expect(screen.getByText("Agent")).toBeInTheDocument();
  });

  it("sends on Enter and keeps newline behavior for Shift+Enter", async () => {
    mockFetch
      .mockResolvedValueOnce({
        ok: true,
        json: () =>
          Promise.resolve({
            messages: [],
            hasMore: false,
            totalCount: 0,
          }),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: () =>
          Promise.resolve({
            message: {
              id: "m-sent-1",
              thread_id: "dm_agent-1",
              sender_id: "current-user",
              sender_name: "You",
              sender_type: "user",
              content: "Hello with Enter",
              created_at: new Date().toISOString(),
            },
            delivery: { attempted: true, delivered: true },
          }),
      });

    const agent: Agent = {
      id: "agent-1",
      name: "Agent One",
      status: "online",
      role: "Helper",
    };

    render(<DMConversationView agent={agent} />);
    await waitFor(() => {
      expect(screen.getByPlaceholderText("Message Agent One...")).toBeInTheDocument();
    });

    const input = screen.getByPlaceholderText("Message Agent One...");
    fireEvent.change(input, { target: { value: "Hello with Enter" } });

    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
    expect(mockFetch).toHaveBeenCalledTimes(1);

    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(2));

    const postCall = mockFetch.mock.calls[1] as [string, RequestInit];
    expect(postCall[1]?.method).toBe("POST");
    expect(String(postCall[1]?.body ?? "")).toContain("Hello with Enter");
    expect(screen.getByText("Delivered to bridge")).toBeInTheDocument();
  });

  it("uploads attachments and includes them in message send payload", async () => {
    window.localStorage.setItem("otter-camp-org-id", "org-123");
    window.localStorage.setItem("otter_camp_token", "token-123");

    mockFetch
      .mockResolvedValueOnce({
        ok: true,
        json: () =>
          Promise.resolve({
            messages: [],
            hasMore: false,
            totalCount: 0,
          }),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: () =>
          Promise.resolve({
            attachment: {
              id: "att-1",
              filename: "diagram.png",
              size_bytes: 1024,
              mime_type: "image/png",
              url: "/api/attachments/att-1",
            },
          }),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: () =>
          Promise.resolve({
            message: {
              id: "m-sent-att-1",
              thread_id: "dm_agent-1",
              sender_id: "current-user",
              sender_name: "You",
              sender_type: "user",
              content: "",
              attachments: [
                {
                  id: "att-1",
                  filename: "diagram.png",
                  size_bytes: 1024,
                  mime_type: "image/png",
                  url: "/api/attachments/att-1",
                },
              ],
              created_at: new Date().toISOString(),
            },
            delivery: { attempted: true, delivered: true },
          }),
      });

    const agent: Agent = {
      id: "agent-1",
      name: "Agent One",
      status: "online",
      role: "Helper",
    };

    render(<DMConversationView agent={agent} />);
    await waitFor(() => {
      expect(screen.getByPlaceholderText("Message Agent One...")).toBeInTheDocument();
    });

    const fileInput = screen.getByTestId("dm-attachment-input") as HTMLInputElement;
    const sendButton = screen.getByLabelText("Send message");
    const file = new File(["image-bytes"], "diagram.png", { type: "image/png" });

    fireEvent.change(fileInput, { target: { files: [file] } });

    await waitFor(() => {
      expect(screen.getByText("diagram.png")).toBeInTheDocument();
    });
    expect(sendButton).not.toBeDisabled();

    fireEvent.click(sendButton);

    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(3));

    const uploadCall = mockFetch.mock.calls[1] as [string, RequestInit];
    expect(uploadCall[0]).toContain("/api/messages/attachments");

    const sendCall = mockFetch.mock.calls[2] as [string, RequestInit];
    expect(sendCall[1]?.method).toBe("POST");
    const body = JSON.parse(String(sendCall[1]?.body ?? "{}")) as {
      attachments?: Array<{ id: string }>;
    };
    expect(body.attachments?.map((attachment) => attachment.id)).toEqual(["att-1"]);
  });

  it("shows clear upload errors for unsupported attachments", async () => {
    window.localStorage.setItem("otter-camp-org-id", "org-123");
    window.localStorage.setItem("otter_camp_token", "token-123");

    mockFetch
      .mockResolvedValueOnce({
        ok: true,
        json: () =>
          Promise.resolve({
            messages: [],
            hasMore: false,
            totalCount: 0,
          }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 415,
        headers: {
          get: () => "application/json",
        },
        json: () => Promise.resolve({ error: "unsupported attachment type" }),
      });

    const agent: Agent = {
      id: "agent-1",
      name: "Agent One",
      status: "online",
      role: "Helper",
    };

    render(<DMConversationView agent={agent} />);
    await waitFor(() => {
      expect(screen.getByPlaceholderText("Message Agent One...")).toBeInTheDocument();
    });

    const fileInput = screen.getByTestId("dm-attachment-input") as HTMLInputElement;
    const file = new File(["bad-binary"], "payload.exe", { type: "application/x-msdownload" });
    fireEvent.change(fileInput, { target: { files: [file] } });

    await waitFor(() => {
      expect(screen.getByText("unsupported attachment type")).toBeInTheDocument();
    });
  });
});
