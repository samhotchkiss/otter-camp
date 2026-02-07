import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
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
    expect(url).toMatch(/^https:\/\/api\.otter\.camp\/api\/messages\?/);
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
});
