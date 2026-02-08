import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import GlobalChatDock from "./GlobalChatDock";
import type { GlobalChatConversation } from "../../contexts/GlobalChatContext";

const globalChatState = {
  isOpen: true,
  totalUnread: 0,
  conversations: [] as GlobalChatConversation[],
  selectedConversation: null as GlobalChatConversation | null,
  selectedKey: null as string | null,
  setDockOpen: vi.fn(),
  toggleDock: vi.fn(),
  selectConversation: vi.fn(),
  markConversationRead: vi.fn(),
  removeConversation: vi.fn(),
};

vi.mock("../../contexts/GlobalChatContext", () => ({
  useGlobalChat: () => globalChatState,
}));

vi.mock("./GlobalChatSurface", () => ({
  default: () => <div data-testid="global-chat-surface" />,
}));

describe("GlobalChatDock", () => {
  it("renders initials from display names in chat list rows", () => {
    globalChatState.conversations = [
      {
        key: "dm:dm_avatar-design",
        type: "dm",
        threadId: "dm_avatar-design",
        title: "Jeff G",
        contextLabel: "Direct message",
        subtitle: "Agent chat",
        unreadCount: 0,
        updatedAt: "2026-02-08T00:00:00.000Z",
        agent: {
          id: "avatar-design",
          name: "Jeff G",
          status: "online",
        },
      },
      {
        key: "project:project-1",
        type: "project",
        projectId: "project-1",
        title: "Otter Camp",
        contextLabel: "Project • Otter Camp",
        subtitle: "Project chat",
        unreadCount: 0,
        updatedAt: "2026-02-08T00:00:00.000Z",
      },
    ];
    globalChatState.selectedConversation = globalChatState.conversations[0];
    globalChatState.selectedKey = globalChatState.conversations[0].key;

    render(<GlobalChatDock />);

    expect(screen.getByTestId("chat-initials-dm:dm_avatar-design")).toHaveTextContent("JG");
    expect(screen.getByTestId("chat-initials-project:project-1")).toHaveTextContent("OC");
  });
});
