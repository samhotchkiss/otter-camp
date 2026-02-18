import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
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

function LocationProbe() {
  const location = useLocation();
  return <div data-testid="location-probe">{location.pathname}</div>;
}

describe("GlobalChatDock", () => {
  beforeEach(() => {
    globalChatState.resolveAgentName = (raw: string) => raw;
    globalChatState.conversations = [];
    globalChatState.selectedConversation = null;
    globalChatState.selectedKey = null;
    globalChatState.totalUnread = 0;
    window.localStorage.clear();
    vi.restoreAllMocks();
  });

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

  it("prefers resolved DM identity over stale title placeholders", () => {
    globalChatState.resolveAgentName = (raw: string) => {
      const normalized = raw.trim();
      if (normalized === "agent-uuid" || normalized === "marcus" || normalized === "dm_marcus") {
        return "Marcus";
      }
      return raw;
    };
    globalChatState.conversations = [
      {
        key: "dm:dm_marcus",
        type: "dm",
        threadId: "dm_marcus",
        title: "You",
        contextLabel: "Direct message",
        subtitle: "Agent chat",
        unreadCount: 0,
        updatedAt: "2026-02-12T00:00:00.000Z",
        agent: {
          id: "agent-uuid",
          name: "You",
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

    expect(screen.getAllByText("Marcus").length).toBeGreaterThan(0);
    expect(screen.queryByText(/^You$/)).not.toBeInTheDocument();
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

  it("resets DM sessions before inserting reset marker", async () => {
    const agentID = "550e8400-e29b-41d4-a716-446655440111";
    globalChatState.conversations = [
      {
        key: `dm:dm_${agentID}`,
        type: "dm",
        threadId: `dm_${agentID}`,
        title: "Marcus",
        contextLabel: "Chameleon-routed chat",
        subtitle: "Agent chat",
        unreadCount: 0,
        updatedAt: "2026-02-08T00:00:00.000Z",
        agent: {
          id: "session-reset",
          name: "Marcus",
          status: "online",
        },
      },
    ];
    globalChatState.selectedConversation = globalChatState.conversations[0];
    globalChatState.selectedKey = globalChatState.conversations[0].key;

    window.localStorage.setItem("otter-camp-org-id", "org-1");
    window.localStorage.setItem("otter_camp_token", "tok");

    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.includes(`/api/admin/agents/${encodeURIComponent(agentID)}/reset`)) {
          return new Response(JSON.stringify({ ok: true }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          });
        }
        if (url.includes("/api/messages") && init?.method === "POST") {
          return new Response(
            JSON.stringify({
              message: {
                id: "msg-1",
                thread_id: `dm_${agentID}`,
                sender_type: "agent",
                sender_name: "Session",
                content: "chat_session_reset:test",
                created_at: "2026-02-08T00:00:00.000Z",
              },
            }),
            {
              status: 200,
              headers: { "Content-Type": "application/json" },
            },
          );
        }
        return new Response(JSON.stringify({ error: "unexpected request" }), {
          status: 500,
          headers: { "Content-Type": "application/json" },
        });
      },
    );

    render(
      <MemoryRouter initialEntries={["/projects"]}>
        <GlobalChatDock />
      </MemoryRouter>,
    );

    const clearButtons = screen.getAllByRole("button", { name: /clear session/i });
    fireEvent.click(clearButtons[0]);

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(2);
    });

    const firstURL = String(fetchMock.mock.calls[0]?.[0] ?? "");
    expect(firstURL).toContain(`/api/admin/agents/${encodeURIComponent(agentID)}/reset`);

    const secondURL = String(fetchMock.mock.calls[1]?.[0] ?? "");
    expect(secondURL).toContain("/api/messages");

    const secondInit = fetchMock.mock.calls[1]?.[1] as RequestInit | undefined;
    const secondBody = JSON.parse(String(secondInit?.body ?? "{}")) as Record<string, unknown>;
    expect(secondBody.thread_id).toBe(`dm_${agentID}`);
    expect(String(secondBody.content || "")).toContain("chat_session_reset:");
  });

  it("offers an issue jump action for selected issue chats", async () => {
    const user = userEvent.setup();
    globalChatState.conversations = [
      {
        key: "issue:issue-1",
        type: "issue",
        issueId: "issue-1",
        projectId: "project-1",
        title: "Write a poem about testing OtterCamp",
        contextLabel: "Issue • Testerooni",
        subtitle: "Issue conversation",
        unreadCount: 0,
        updatedAt: "2026-02-11T10:00:00.000Z",
      },
    ];
    globalChatState.selectedConversation = globalChatState.conversations[0];
    globalChatState.selectedKey = globalChatState.conversations[0].key;

    render(
      <MemoryRouter initialEntries={["/chats"]}>
        <Routes>
          <Route
            path="*"
            element={(
              <>
                <GlobalChatDock />
                <LocationProbe />
              </>
            )}
          />
        </Routes>
      </MemoryRouter>,
    );

    expect(screen.getByRole("button", { name: "Open issue" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Open issue" }));
    expect(screen.getByTestId("location-probe")).toHaveTextContent("/projects/project-1/issues/issue-1");
  });

  it("provides a minimize action in the selected chat header", async () => {
    const user = userEvent.setup();
    globalChatState.conversations = [
      {
        key: "dm:dm_marcus",
        type: "dm",
        threadId: "dm_marcus",
        title: "Marcus",
        contextLabel: "Chameleon-routed chat",
        subtitle: "Agent chat",
        unreadCount: 0,
        updatedAt: "2026-02-11T10:00:00.000Z",
        agent: {
          id: "marcus",
          name: "Marcus",
          status: "online",
        },
      },
    ];
    globalChatState.selectedConversation = globalChatState.conversations[0];
    globalChatState.selectedKey = globalChatState.conversations[0].key;

    render(
      <MemoryRouter initialEntries={["/chats"]}>
        <GlobalChatDock />
      </MemoryRouter>,
    );

    await user.click(screen.getByRole("button", { name: "Minimize global chat" }));
    expect(globalChatState.setDockOpen).toHaveBeenCalledWith(false);
  });

  it("shows a context cue for selected project conversations", () => {
    globalChatState.conversations = [
      {
        key: "project:project-1",
        type: "project",
        projectId: "project-1",
        title: "Otter Camp",
        contextLabel: "Project • Otter Camp",
        subtitle: "Project chat",
        unreadCount: 0,
        updatedAt: "2026-02-11T10:00:00.000Z",
      },
    ];
    globalChatState.selectedConversation = globalChatState.conversations[0];
    globalChatState.selectedKey = globalChatState.conversations[0].key;

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <GlobalChatDock />
      </MemoryRouter>,
    );

    expect(screen.getByText("Project context")).toBeInTheDocument();
  });

  it("shows a DM context cue for selected direct conversations", () => {
    globalChatState.conversations = [
      {
        key: "dm:dm_stone",
        type: "dm",
        threadId: "dm_stone",
        title: "Stone",
        contextLabel: "Direct message",
        subtitle: "Agent chat",
        unreadCount: 0,
        updatedAt: "2026-02-11T10:00:00.000Z",
        agent: {
          id: "stone",
          name: "Stone",
          status: "online",
        },
      },
    ];
    globalChatState.selectedConversation = globalChatState.conversations[0];
    globalChatState.selectedKey = globalChatState.conversations[0].key;

    render(
      <MemoryRouter initialEntries={["/chats"]}>
        <GlobalChatDock />
      </MemoryRouter>,
    );

    expect(screen.getByText("DM context")).toBeInTheDocument();
  });
});
