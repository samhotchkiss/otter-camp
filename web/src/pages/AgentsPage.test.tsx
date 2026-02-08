import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AgentsPage from "./AgentsPage";

const openConversationMock = vi.fn();

vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => (count > 0 ? 236 : 0),
    getVirtualItems: () =>
      count > 0
        ? [
            {
              key: "row-0",
              index: 0,
              size: 236,
              start: 0,
            },
          ]
        : [],
  }),
}));

vi.mock("../contexts/WebSocketContext", () => ({
  useWS: () => ({
    connected: true,
    lastMessage: null,
    sendMessage: vi.fn(),
  }),
  useOptionalWS: () => null,
}));

vi.mock("../contexts/GlobalChatContext", () => ({
  useGlobalChat: () => ({
    openConversation: openConversationMock,
  }),
}));

describe("AgentsPage", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    openConversationMock.mockReset();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("renders persistent last action on agent cards", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/sync/agents")) {
        return new Response(
          JSON.stringify({
            agents: [
              {
                id: "main",
                name: "Frank",
                status: "online",
                role: "Lead Agent",
              },
            ],
          }),
          { status: 200 },
        );
      }

      if (url.includes("/api/activity/recent")) {
        return new Response(
          JSON.stringify({
            items: [
              {
                id: "evt-1",
                org_id: "org-123",
                agent_id: "main",
                session_key: "agent:main:main",
                trigger: "chat.slack",
                summary: "Responded to Sam in #leadership",
                status: "completed",
                tokens_used: 30,
                duration_ms: 800,
                started_at: "2026-02-08T12:58:00.000Z",
                created_at: "2026-02-08T12:58:00.000Z",
              },
            ],
          }),
          { status: 200 },
        );
      }

      throw new Error(`unexpected url: ${url}`);
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentsPage apiEndpoint="https://api.otter.camp/api/sync/agents" />);

    expect(await screen.findByText("Frank")).toBeInTheDocument();
    expect(await screen.findByText("Responded to Sam in #leadership")).toBeInTheDocument();
    expect(screen.getByText("Slack")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "View timeline" })).toHaveAttribute("href", "/agents/main");

    fireEvent.click(screen.getByText("Frank"));
    expect(openConversationMock).toHaveBeenCalledTimes(1);
  });

  it("still renders cards when activity query fails", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/sync/agents")) {
        return new Response(
          JSON.stringify({ agents: [{ id: "main", name: "Frank", status: "offline" }] }),
          { status: 200 },
        );
      }

      if (url.includes("/api/activity/recent")) {
        return new Response(JSON.stringify({ error: "boom" }), { status: 500 });
      }

      throw new Error(`unexpected url: ${url}`);
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentsPage apiEndpoint="https://api.otter.camp/api/sync/agents" />);

    expect(await screen.findByText("Frank")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByText("No recent activity")).toBeInTheDocument();
    });
  });
});
