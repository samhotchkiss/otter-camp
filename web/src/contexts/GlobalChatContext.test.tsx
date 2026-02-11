import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  GlobalChatProvider,
  useGlobalChat,
} from "./GlobalChatContext";

const wsState = {
  lastMessage: null as { type: string; data: unknown } | null,
};

vi.mock("./WebSocketContext", () => ({
  useWS: () => wsState,
}));

function ConversationTitles() {
  const { conversations } = useGlobalChat();
  return (
    <ul>
      {conversations.map((conversation) => (
        <li key={conversation.key}>{conversation.title}</li>
      ))}
    </ul>
  );
}

function ResolverProbe() {
  const { resolveAgentName, agentNamesByID } = useGlobalChat();
  return (
    <div data-testid="resolver-probe">
      {resolveAgentName("agent:avatar-design")}|{resolveAgentName("dm_avatar-design")}|
      {resolveAgentName("avatar-design")}|{agentNamesByID.size}
    </div>
  );
}

function ArchiveProbe() {
  const { conversations, archiveConversation } = useGlobalChat();
  return (
    <div>
      <button type="button" onClick={() => { void archiveConversation("chat-1"); }}>
        Archive chat
      </button>
      <span data-testid="conversation-count">{conversations.length}</span>
    </div>
  );
}

function DMAgentIDProbe() {
  const { conversations } = useGlobalChat();
  const dmConversation = conversations.find((conversation) => conversation.type === "dm");
  const agentID = dmConversation && dmConversation.type === "dm"
    ? dmConversation.agent.id
    : "";
  return <div data-testid="dm-agent-id-probe">{agentID}</div>;
}

describe("GlobalChatContext", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    wsState.lastMessage = null;
    window.localStorage.clear();
    window.localStorage.setItem("otter-camp-org-id", "org-1");

    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes("/api/projects?")) {
          return {
            ok: true,
            json: async () => ({
              projects: [
                { id: "project-1", name: "Otter Camp" },
              ],
            }),
          };
        }
        if (url.includes("/api/sync/agents")) {
          return {
            ok: true,
            json: async () => ({
              agents: [
                { id: "avatar-design", name: "Jeff G", status: "online" },
              ],
            }),
          };
        }
        if (url.includes("/api/agents?")) {
          return {
            ok: true,
            json: async () => ({
              agents: [
                { id: "avatar-design", name: "Jeff G" },
              ],
            }),
          };
        }
        return {
          ok: true,
          json: async () => ({}),
        };
      }),
    );
  });

  it("reconciles stored project conversations from UUID labels to project names", async () => {
    window.localStorage.setItem(
      "otter-camp-global-chat:v1",
      JSON.stringify({
        isOpen: true,
        selectedKey: "project:project-1",
        conversations: [
          {
            key: "project:project-1",
            type: "project",
            projectId: "project-1",
            title: "Project 944adcaf",
            contextLabel: "Project",
            subtitle: "Project chat",
            unreadCount: 0,
            updatedAt: "2026-02-08T00:00:00.000Z",
          },
        ],
      }),
    );

    render(
      <GlobalChatProvider>
        <ConversationTitles />
      </GlobalChatProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("Otter Camp")).toBeInTheDocument();
    });
  });

  it("resolves DM conversation titles to agent display names", async () => {
    wsState.lastMessage = {
      type: "DMMessageReceived",
      data: {
        thread_id: "dm_avatar-design",
        message: {
          sender_type: "agent",
          sender_name: "avatar-design",
        },
      },
    };

    render(
      <GlobalChatProvider>
        <ConversationTitles />
      </GlobalChatProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("Jeff G")).toBeInTheDocument();
    });
  });

  it("keeps DM routing agent id anchored to thread id on session reset markers", async () => {
    wsState.lastMessage = {
      type: "DMMessageReceived",
      data: {
        thread_id: "dm_avatar-design",
        message: {
          sender_type: "agent",
          sender_id: "session-reset",
          sender_name: "Session",
          content: "chat_session_reset:test",
        },
      },
    };

    render(
      <GlobalChatProvider>
        <ConversationTitles />
        <DMAgentIDProbe />
      </GlobalChatProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("Jeff G")).toBeInTheDocument();
      expect(screen.getByTestId("dm-agent-id-probe")).toHaveTextContent("avatar-design");
    });
  });

  it("exposes a resolver that handles prefixed agent identifiers", async () => {
    render(
      <GlobalChatProvider>
        <ResolverProbe />
      </GlobalChatProvider>,
    );

    await waitFor(() => {
      expect(screen.getByTestId("resolver-probe")).toHaveTextContent("Jeff G|Jeff G|Jeff G|1");
    });
  });

  it("loads active chats from /api/chats on mount", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes("/api/chats")) {
          return {
            ok: true,
            json: async () => ({
              chats: [
                {
                  id: "chat-1",
                  thread_key: "project:project-1",
                  thread_type: "project",
                  project_id: "project-1",
                  title: "Server project chat",
                  last_message_preview: "Server preview",
                  last_message_at: "2026-02-10T10:00:00Z",
                },
              ],
            }),
          };
        }
        if (url.includes("/api/projects?")) {
          return {
            ok: true,
            json: async () => ({
              projects: [
                { id: "project-1", name: "Otter Camp" },
              ],
            }),
          };
        }
        if (url.includes("/api/sync/agents") || url.includes("/api/agents?")) {
          return { ok: true, json: async () => ({ agents: [] }) };
        }
        return { ok: true, json: async () => ({}) };
      }),
    );

    render(
      <GlobalChatProvider>
        <ConversationTitles />
      </GlobalChatProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("Otter Camp")).toBeInTheDocument();
    });
  });

  it("archives a server-backed chat and removes it from active conversations", async () => {
    const user = userEvent.setup();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.includes("/api/chats") && !url.includes("/archive")) {
          return {
            ok: true,
            json: async () => ({
              chats: [
                {
                  id: "chat-1",
                  thread_key: "project:project-1",
                  thread_type: "project",
                  project_id: "project-1",
                  title: "Server project chat",
                  last_message_preview: "Server preview",
                  last_message_at: "2026-02-10T10:00:00Z",
                },
              ],
            }),
          };
        }
        if (url.includes("/api/chats/chat-1/archive")) {
          return {
            ok: init?.method === "POST",
            json: async () => ({ chat: { id: "chat-1" } }),
          };
        }
        if (url.includes("/api/projects?")) {
          return { ok: true, json: async () => ({ projects: [] }) };
        }
        if (url.includes("/api/sync/agents") || url.includes("/api/agents?")) {
          return { ok: true, json: async () => ({ agents: [] }) };
        }
        return { ok: true, json: async () => ({}) };
      }),
    );

    render(
      <GlobalChatProvider>
        <ArchiveProbe />
      </GlobalChatProvider>,
    );

    await waitFor(() => {
      expect(screen.getByTestId("conversation-count")).toHaveTextContent("1");
    });

    await user.click(screen.getByRole("button", { name: "Archive chat" }));

    await waitFor(() => {
      expect(screen.getByTestId("conversation-count")).toHaveTextContent("0");
    });
  });
});
