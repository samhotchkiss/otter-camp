import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ProjectChatPanel from "./ProjectChatPanel";
import { useWS } from "../../contexts/WebSocketContext";

vi.mock("../../contexts/WebSocketContext", () => ({
  useWS: vi.fn(),
}));

function mockJSONResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("ProjectChatPanel", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "550e8400-e29b-41d4-a716-446655440000");
    localStorage.setItem("otter-camp-user-name", "Sam");
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("fetches and renders project-scoped chat messages", async () => {
    const sendMessage = vi.fn();
    vi.mocked(useWS).mockReturnValue({
      connected: true,
      lastMessage: null,
      sendMessage,
    });

    const fetchMock = vi.fn(async () =>
      mockJSONResponse({
        messages: [
          {
            id: "msg-1",
            project_id: "proj-1",
            author: "Stone",
            body: "Drafted opener",
            created_at: "2026-02-07T10:00:00Z",
            updated_at: "2026-02-07T10:00:00Z",
          },
        ],
      })
    );
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<ProjectChatPanel projectId="proj-1" active />);

    expect(await screen.findByText("Drafted opener")).toBeInTheDocument();
    expect(sendMessage).toHaveBeenCalledWith({
      type: "subscribe",
      org_id: "550e8400-e29b-41d4-a716-446655440000",
      channel: "project:proj-1:chat",
    });
  });

  it("optimistically sends messages and reconciles server response", async () => {
    const sendMessage = vi.fn();
    vi.mocked(useWS).mockReturnValue({
      connected: true,
      lastMessage: null,
      sendMessage,
    });

    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/chat/messages")) {
        const body = JSON.parse(String(init?.body));
        return mockJSONResponse({
          message: {
            id: "msg-2",
            project_id: "proj-1",
            author: body.author,
            body: body.body,
            created_at: "2026-02-07T10:01:00Z",
            updated_at: "2026-02-07T10:01:00Z",
          },
        });
      }
      return mockJSONResponse({ messages: [] });
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    const user = userEvent.setup();
    render(<ProjectChatPanel projectId="proj-1" active />);

    await user.type(screen.getByPlaceholderText("Share an idea for this project..."), "Ship this tonight");
    await user.click(screen.getByRole("button", { name: "Send" }));

    expect(screen.getByText("Ship this tonight")).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.queryByText("Sending...")).not.toBeInTheDocument();
    });
    expect(screen.getByText("Ship this tonight")).toBeInTheDocument();
  });

  it("runs chat search and clear resets to thread view", async () => {
    const sendMessage = vi.fn();
    vi.mocked(useWS).mockReturnValue({
      connected: true,
      lastMessage: null,
      sendMessage,
    });

    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/chat/search")) {
        return mockJSONResponse({
          items: [
            {
              message: {
                id: "msg-search",
                project_id: "proj-1",
                author: "Stone",
                body: "Launch checklist",
                created_at: "2026-02-07T10:02:00Z",
                updated_at: "2026-02-07T10:02:00Z",
              },
              relevance: 0.9,
              snippet: "<mark>Launch</mark> checklist",
            },
          ],
        });
      }
      return mockJSONResponse({
        messages: [
          {
            id: "msg-1",
            project_id: "proj-1",
            author: "Sam",
            body: "Original thread item",
            created_at: "2026-02-07T10:00:00Z",
            updated_at: "2026-02-07T10:00:00Z",
          },
        ],
      });
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    const user = userEvent.setup();
    render(<ProjectChatPanel projectId="proj-1" active />);

    expect(await screen.findByText("Original thread item")).toBeInTheDocument();

    await user.type(screen.getByPlaceholderText("Search project chat"), "launch");
    await waitFor(() => {
      expect(screen.getByText("<mark>Launch</mark> checklist")).toBeInTheDocument();
    });

    await user.click(screen.getByRole("button", { name: "Clear" }));

    await waitFor(() => {
      expect(screen.queryByText("<mark>Launch</mark> checklist")).not.toBeInTheDocument();
    });
    expect(screen.getByText("Original thread item")).toBeInTheDocument();
  });

  it("appends websocket push messages without a full refresh", async () => {
    const sendMessage = vi.fn();
    let wsState = {
      connected: true,
      lastMessage: null,
      sendMessage,
    } as ReturnType<typeof useWS>;

    vi.mocked(useWS).mockImplementation(() => wsState);

    const fetchMock = vi.fn(async () => mockJSONResponse({ messages: [] }));
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    const { rerender } = render(<ProjectChatPanel projectId="proj-1" active={false} />);
    await screen.findByText("No project chat messages yet.");

    wsState = {
      ...wsState,
      lastMessage: {
        type: "ProjectChatMessageCreated",
        data: {
          id: "msg-3",
          project_id: "proj-1",
          author: "Stone",
          body: "Realtime from websocket",
          created_at: "2026-02-07T10:03:00Z",
          updated_at: "2026-02-07T10:03:00Z",
        },
      },
    };

    rerender(<ProjectChatPanel projectId="proj-1" active={false} />);

    expect(await screen.findByText("Realtime from websocket")).toBeInTheDocument();
  });
});
