import { render, waitFor } from "@testing-library/react";
import { describe, expect, it, beforeEach, vi } from "vitest";
import GlobalChatSurface from "./GlobalChatSurface";
import type { GlobalDMConversation } from "../../contexts/GlobalChatContext";

const wsState = {
  connected: true,
  lastMessage: null as unknown,
  sendMessage: vi.fn(),
};

vi.mock("../../contexts/WebSocketContext", () => ({
  useWS: () => wsState,
}));

vi.mock("../messaging/MessageHistory", () => ({
  default: () => <div data-testid="message-history" />,
}));

describe("GlobalChatSurface", () => {
  const baseConversation: GlobalDMConversation = {
    key: "dm:dm_agent-stone",
    type: "dm",
    threadId: "dm_agent-stone",
    title: "Stone",
    contextLabel: "Direct message",
    subtitle: "Agent chat",
    unreadCount: 0,
    updatedAt: "2026-02-07T00:00:00.000Z",
    agent: {
      id: "agent-stone",
      name: "Stone",
      status: "online",
    },
  };

  beforeEach(() => {
    vi.clearAllMocks();
    window.localStorage.clear();
    window.localStorage.setItem("otter-camp-org-id", "org-1");
    window.localStorage.setItem("otter-camp-user-name", "Sam");
    window.localStorage.setItem("otter_camp_token", "token-1");

    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({ messages: [] }),
      }),
    );
  });

  it("does not refetch when only conversation metadata changes", async () => {
    const { rerender } = render(<GlobalChatSurface conversation={baseConversation} />);

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledTimes(1);
    });

    const metadataUpdatedConversation: GlobalDMConversation = {
      ...baseConversation,
      title: "Stone (Online)",
      unreadCount: 3,
      updatedAt: "2026-02-07T00:01:00.000Z",
    };

    rerender(<GlobalChatSurface conversation={metadataUpdatedConversation} />);

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledTimes(1);
    });
  });

  it("refetches when conversation identity changes", async () => {
    const { rerender } = render(<GlobalChatSurface conversation={baseConversation} />);

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledTimes(1);
    });

    const differentConversation: GlobalDMConversation = {
      ...baseConversation,
      key: "dm:dm_agent-ivy",
      threadId: "dm_agent-ivy",
      title: "Ivy",
      agent: {
        id: "agent-ivy",
        name: "Ivy",
        status: "online",
      },
    };

    rerender(<GlobalChatSurface conversation={differentConversation} />);

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledTimes(2);
    });
  });
});
