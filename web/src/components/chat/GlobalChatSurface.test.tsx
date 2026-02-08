import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, beforeEach, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import GlobalChatSurface from "./GlobalChatSurface";
import type {
  GlobalDMConversation,
  GlobalProjectConversation,
} from "../../contexts/GlobalChatContext";

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

  it("uploads files from picker and sends attachment_ids for project chat", async () => {
    const user = userEvent.setup();
    const projectConversation: GlobalProjectConversation = {
      key: "project:project-1",
      type: "project",
      projectId: "project-1",
      title: "Project One",
      contextLabel: "Project chat",
      subtitle: "Team thread",
      unreadCount: 0,
      updatedAt: "2026-02-07T00:00:00.000Z",
    };

    let sentPayload: Record<string, unknown> | null = null;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/projects/project-1/chat?")) {
        return {
          ok: true,
          json: async () => ({ messages: [] }),
        };
      }
      if (url.includes("/api/messages/attachments")) {
        return {
          ok: true,
          json: async () => ({
            attachment: {
              id: "att-1",
              filename: "note.txt",
              size_bytes: 4,
              mime_type: "text/plain",
              url: "/uploads/org-1/note.txt",
            },
          }),
        };
      }
      if (url.includes("/api/projects/project-1/chat/messages")) {
        sentPayload = JSON.parse(String(init?.body ?? "{}"));
        return {
          ok: true,
          json: async () => ({
            message: {
              id: "msg-1",
              project_id: "project-1",
              author: "Sam",
              body: "",
              attachments: [
                {
                  id: "att-1",
                  filename: "note.txt",
                  size_bytes: 4,
                  mime_type: "text/plain",
                  url: "/uploads/org-1/note.txt",
                },
              ],
              created_at: "2026-02-08T00:00:00.000Z",
              updated_at: "2026-02-08T00:00:00.000Z",
            },
            delivery: { delivered: true },
          }),
        };
      }
      return {
        ok: true,
        json: async () => ({}),
      };
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<GlobalChatSurface conversation={projectConversation} />);

    await screen.findByPlaceholderText("Message Project One...");

    const file = new File(["note"], "note.txt", { type: "text/plain" });
    const fileInput = screen
      .getAllByLabelText("Attach files")
      .find((element) => element.tagName.toLowerCase() === "input") as HTMLInputElement;
    await user.upload(fileInput, file);

    await waitFor(() => {
      expect(
        fetchMock.mock.calls.some(([url]) => String(url).includes("/api/messages/attachments")),
      ).toBe(true);
    });

    expect(screen.getByText("note.txt")).toBeInTheDocument();

    const sendButton = screen.getByRole("button", { name: "Send message" });
    expect(sendButton).toBeEnabled();
    await user.click(sendButton);

    await waitFor(() => {
      expect(sentPayload).not.toBeNull();
      expect(sentPayload?.attachment_ids).toEqual(["att-1"]);
    });
  });

  it("uploads files from drag and drop", async () => {
    const projectConversation: GlobalProjectConversation = {
      key: "project:project-1",
      type: "project",
      projectId: "project-1",
      title: "Project One",
      contextLabel: "Project chat",
      subtitle: "Team thread",
      unreadCount: 0,
      updatedAt: "2026-02-07T00:00:00.000Z",
    };

    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/projects/project-1/chat?")) {
        return {
          ok: true,
          json: async () => ({ messages: [] }),
        };
      }
      if (url.includes("/api/messages/attachments")) {
        return {
          ok: true,
          json: async () => ({
            attachment: {
              id: "att-drop-1",
              filename: "drop.txt",
              size_bytes: 4,
              mime_type: "text/plain",
              url: "/uploads/org-1/drop.txt",
            },
          }),
        };
      }
      return {
        ok: true,
        json: async () => ({}),
      };
    });
    vi.stubGlobal("fetch", fetchMock);

    const { container } = render(<GlobalChatSurface conversation={projectConversation} />);
    await screen.findByPlaceholderText("Message Project One...");

    const root = container.firstElementChild as HTMLElement;
    const droppedFile = new File(["drop"], "drop.txt", { type: "text/plain" });

    fireEvent.drop(root, {
      dataTransfer: {
        files: [droppedFile],
      },
    });

    await waitFor(() => {
      expect(
        fetchMock.mock.calls.some(([url]) => String(url).includes("/api/messages/attachments")),
      ).toBe(true);
    });
  });

  it("uploads pasted image files from textarea", async () => {
    const projectConversation: GlobalProjectConversation = {
      key: "project:project-1",
      type: "project",
      projectId: "project-1",
      title: "Project One",
      contextLabel: "Project chat",
      subtitle: "Team thread",
      unreadCount: 0,
      updatedAt: "2026-02-07T00:00:00.000Z",
    };

    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/projects/project-1/chat?")) {
        return {
          ok: true,
          json: async () => ({ messages: [] }),
        };
      }
      if (url.includes("/api/messages/attachments")) {
        return {
          ok: true,
          json: async () => ({
            attachment: {
              id: "att-paste-1",
              filename: "paste.png",
              size_bytes: 10,
              mime_type: "image/png",
              url: "/uploads/org-1/paste.png",
            },
          }),
        };
      }
      return {
        ok: true,
        json: async () => ({}),
      };
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<GlobalChatSurface conversation={projectConversation} />);
    await screen.findByPlaceholderText("Message Project One...");

    const textarea = screen.getByPlaceholderText("Message Project One...");
    const pastedImage = new File(["img"], "paste.png", { type: "image/png" });

    fireEvent.paste(textarea, {
      clipboardData: {
        items: [
          {
            kind: "file",
            getAsFile: () => pastedImage,
          },
        ],
      },
    });

    await waitFor(() => {
      expect(
        fetchMock.mock.calls.some(([url]) => String(url).includes("/api/messages/attachments")),
      ).toBe(true);
    });
  });

  it("handles nested realtime project payload envelopes", async () => {
    const projectConversation: GlobalProjectConversation = {
      key: "project:project-1",
      type: "project",
      projectId: "project-1",
      title: "Project One",
      contextLabel: "Project chat",
      subtitle: "Team thread",
      unreadCount: 0,
      updatedAt: "2026-02-07T00:00:00.000Z",
    };

    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({ messages: [] }),
      }),
    );

    const { rerender } = render(<GlobalChatSurface conversation={projectConversation} />);
    await screen.findByPlaceholderText("Message Project One...");

    wsState.lastMessage = {
      type: "ProjectChatMessageCreated",
      data: {
        data: {
          message: {
            id: "msg-2",
            project_id: "project-1",
            author: "Stone",
            body: "Reply",
            created_at: "2026-02-08T00:00:00.000Z",
            updated_at: "2026-02-08T00:00:00.000Z",
          },
        },
      },
    };

    rerender(<GlobalChatSurface conversation={projectConversation} />);

    await waitFor(() => {
      expect(screen.getByText("Agent replied")).toBeInTheDocument();
    });
  });
});
