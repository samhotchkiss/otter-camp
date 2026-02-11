import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import GlobalChatDock from "./GlobalChatDock";
import type { GlobalChatConversation } from "../../contexts/GlobalChatContext";

const globalChatState = {
  isOpen: true,
  totalUnread: 0,
  agentNamesByID: new Map<string, string>(),
  resolveAgentName: (raw: string) => raw,
  conversations: [] as GlobalChatConversation[],
  selectedConversation: null as GlobalChatConversation | null,
  selectedKey: null as string | null,
  setDockOpen: vi.fn(),
  toggleDock: vi.fn(),
  selectConversation: vi.fn(),
  markConversationRead: vi.fn(),
  removeConversation: vi.fn(),
  archiveConversation: vi.fn(async () => true),
};

vi.mock("../../contexts/GlobalChatContext", () => ({
  useGlobalChat: () => globalChatState,
}));

vi.mock("./GlobalChatSurface", () => ({
  default: () => <div data-testid="global-chat-surface" />,
}));

describe("GlobalChatDock", () => {
  it("renders initials from display names in chat list rows", () => {
    globalChatState.resolveAgentName = (raw: string) =>
      raw === "avatar-design" ? "Jeff G" : raw;
    globalChatState.conversations = [
      {
        key: "dm:dm_avatar-design",
        type: "dm",
        threadId: "dm_avatar-design",
        title: "avatar-design",
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

    render(
      <MemoryRouter initialEntries={["/projects"]}>
        <GlobalChatDock />
      </MemoryRouter>,
    );

    expect(screen.getByTestId("chat-initials-dm:dm_avatar-design")).toHaveTextContent("JG");
    expect(screen.getAllByText("Jeff G").length).toBeGreaterThan(0);
    expect(screen.queryByText("avatar-design")).not.toBeInTheDocument();
    expect(screen.getByTestId("chat-initials-project:project-1")).toHaveTextContent("OC");
  });

  it("shows error banner on archive failure", async () => {
    const user = userEvent.setup();
    globalChatState.resolveAgentName = (raw: string) => raw;
    globalChatState.archiveConversation = vi.fn(async () => false);
    globalChatState.conversations = [
      {
        key: "dm:dm_archive-failure",
        chatId: "chat-archive-failure",
        type: "dm",
        threadId: "dm_archive-failure",
        title: "Archive Failure Chat",
        contextLabel: "Direct message",
        subtitle: "Agent chat",
        unreadCount: 0,
        updatedAt: "2026-02-08T00:00:00.000Z",
        agent: {
          id: "archive-failure",
          name: "Archive Failure",
          status: "online",
        },
      },
    ];
    globalChatState.selectedConversation = globalChatState.conversations[0];
    globalChatState.selectedKey = globalChatState.conversations[0].key;

    render(
      <MemoryRouter initialEntries={["/projects"]}>
        <GlobalChatDock />
      </MemoryRouter>,
    );

    await user.click(screen.getByRole("button", { name: "Archive" }));

    expect(await screen.findByText("Failed to archive chat")).toBeInTheDocument();
    expect(globalChatState.archiveConversation).toHaveBeenCalledWith("chat-archive-failure");
  });
});
