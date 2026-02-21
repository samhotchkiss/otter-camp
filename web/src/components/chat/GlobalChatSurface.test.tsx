import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, beforeEach, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import GlobalChatSurface from "./GlobalChatSurface";
import type {
  GlobalDMConversation,
  GlobalIssueConversation,
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

type MockMessageHistoryProps = {
  messages: Array<Record<string, unknown>>;
  onSubmitQuestionnaire?: (
    questionnaireID: string,
    responses: Record<string, unknown>,
  ) => Promise<void> | void;
};

let lastMessageHistoryProps: MockMessageHistoryProps | null = null;

vi.mock("../messaging/MessageHistory", () => ({
  default: (props: MockMessageHistoryProps) => {
    lastMessageHistoryProps = props;
    const pendingQuestionnaire = props.messages.find((entry) => {
      const questionnaire = entry.questionnaire as
        | { id?: string; responses?: Record<string, unknown> }
        | undefined;
      return questionnaire?.id && !questionnaire.responses;
    });
    const pendingID = (pendingQuestionnaire?.questionnaire as { id?: string } | undefined)?.id;
    return (
      <div data-testid="message-history">
        {pendingID && props.onSubmitQuestionnaire ? (
          <button
            type="button"
            onClick={() =>
              props.onSubmitQuestionnaire?.(pendingID, { q1: "WebSocket" })
            }
          >
            Submit mock questionnaire
          </button>
        ) : null}
      </div>
    );
  },
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
    wsState.connected = true;
    wsState.lastMessage = null;
    wsState.sendMessage.mockReset();
    lastMessageHistoryProps = null;
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

  it("shows a project context cue in the chat surface", async () => {
    const projectConversation: GlobalProjectConversation = {
      key: "project:project-1",
      type: "project",
      projectId: "project-1",
      title: "Project One",
      contextLabel: "Project • Project One",
      subtitle: "Team thread",
      unreadCount: 0,
      updatedAt: "2026-02-07T00:00:00.000Z",
    };

    render(<GlobalChatSurface conversation={projectConversation} />);

    await screen.findByPlaceholderText("Message Project One...");
    expect(screen.getByText("Project context")).toBeInTheDocument();
  });

  it("shows a DM context cue in the chat surface", async () => {
    render(<GlobalChatSurface conversation={baseConversation} />);

    await screen.findByPlaceholderText("Message Stone...");
    expect(screen.getByText("DM context")).toBeInTheDocument();
  });

  it("shows an issue context cue in the chat surface", async () => {
    const issueConversation: GlobalIssueConversation = {
      key: "issue:issue-1",
      type: "issue",
      issueId: "issue-1",
      projectId: "project-1",
      title: "Issue One",
      contextLabel: "Issue • Issue One",
      subtitle: "Issue conversation",
      unreadCount: 0,
      updatedAt: "2026-02-07T00:00:00.000Z",
    };

    render(<GlobalChatSurface conversation={issueConversation} />);

    await screen.findByPlaceholderText("Message Issue One...");
    expect(screen.getByText("Issue context")).toBeInTheDocument();
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

  it("submits questionnaire responses from project chat timeline", async () => {
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

    let responsePayload: Record<string, unknown> | null = null;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/projects/project-1/chat?")) {
        return {
          ok: true,
          json: async () => ({
            messages: [],
            questionnaires: [
              {
                id: "qn-1",
                context_type: "project_chat",
                context_id: "project-1",
                author: "Planner",
                title: "Design decisions",
                questions: [
                  {
                    id: "q1",
                    text: "Protocol?",
                    type: "select",
                    options: ["WebSocket", "Polling"],
                    required: true,
                  },
                ],
                created_at: "2026-02-08T00:00:00.000Z",
              },
            ],
          }),
        };
      }
      if (url.includes("/api/questionnaires/qn-1/response")) {
        responsePayload = JSON.parse(String(init?.body ?? "{}"));
        return {
          ok: true,
          json: async () => ({
            id: "qn-1",
            context_type: "project_chat",
            context_id: "project-1",
            author: "Planner",
            title: "Design decisions",
            questions: [
              {
                id: "q1",
                text: "Protocol?",
                type: "select",
                options: ["WebSocket", "Polling"],
                required: true,
              },
            ],
            responses: { q1: "WebSocket" },
            responded_by: "Sam",
            responded_at: "2026-02-08T01:00:00.000Z",
            created_at: "2026-02-08T00:00:00.000Z",
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
    await screen.findByRole("button", { name: "Submit mock questionnaire" });
    await user.click(screen.getByRole("button", { name: "Submit mock questionnaire" }));

    await waitFor(() => {
      expect(responsePayload).toEqual({
        responded_by: "Sam",
        responses: { q1: "WebSocket" },
      });
    });
    expect(lastMessageHistoryProps?.messages.some((entry) => {
      const questionnaire = entry.questionnaire as { responses?: Record<string, unknown> } | undefined;
      return questionnaire?.responses?.q1 === "WebSocket";
    })).toBe(true);
  });

  it("reconciles websocket user echo with optimistic project message without duplicate rows", async () => {
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

    let resolvePost:
      | ((response: { ok: boolean; json: () => Promise<unknown> }) => void)
      | null = null;
    const nowISO = new Date().toISOString();
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/projects/project-1/chat?")) {
        return Promise.resolve({
          ok: true,
          json: async () => ({ messages: [] }),
        });
      }
      if (url.includes("/api/projects/project-1/chat/messages")) {
        return new Promise((resolve) => {
          resolvePost = resolve as (response: { ok: boolean; json: () => Promise<unknown> }) => void;
        });
      }
      return Promise.resolve({
        ok: true,
        json: async () => ({}),
      });
    });
    vi.stubGlobal("fetch", fetchMock);

    const { rerender } = render(<GlobalChatSurface conversation={projectConversation} />);
    const composer = await screen.findByPlaceholderText("Message Project One...");
    await user.type(composer, "Echo once");
    await user.click(screen.getByRole("button", { name: "Send message" }));

    await waitFor(() => {
      const matches = (lastMessageHistoryProps?.messages ?? []).filter(
        (entry) => String(entry.content ?? "") === "Echo once",
      );
      expect(matches).toHaveLength(1);
      expect(String(matches[0]?.id ?? "")).toMatch(/^temp-/);
    });

    wsState.lastMessage = {
      type: "ProjectChatMessageCreated",
      data: {
        id: "msg-echo-1",
        project_id: "project-1",
        author: "Sam",
        body: "Echo once",
        created_at: nowISO,
        updated_at: nowISO,
      },
    };
    rerender(<GlobalChatSurface conversation={projectConversation} />);

    await waitFor(() => {
      const matches = (lastMessageHistoryProps?.messages ?? []).filter(
        (entry) => String(entry.content ?? "") === "Echo once",
      );
      expect(matches).toHaveLength(1);
      expect(String(matches[0]?.id ?? "")).toBe("msg-echo-1");
    });

    resolvePost?.({
      ok: true,
      json: async () => ({
        message: {
          id: "msg-echo-1",
          project_id: "project-1",
          author: "Sam",
          body: "Echo once",
          created_at: nowISO,
          updated_at: nowISO,
        },
        delivery: { delivered: true },
      }),
    });

    await waitFor(() => {
      const matches = (lastMessageHistoryProps?.messages ?? []).filter(
        (entry) => String(entry.content ?? "") === "Echo once",
      );
      expect(matches).toHaveLength(1);
      expect(String(matches[0]?.id ?? "")).toBe("msg-echo-1");
    });
  });

  it("allows DM follow-up sends while prior turn is pending and updates delivery feedback per send", async () => {
    const user = userEvent.setup();
    const sentBodies: Array<Record<string, unknown>> = [];

    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/messages?")) {
        return {
          ok: true,
          json: async () => ({ messages: [] }),
        };
      }
      if (url.endsWith("/api/messages") && init?.method === "POST") {
        const parsedBody = JSON.parse(String(init.body ?? "{}")) as Record<string, unknown>;
        sentBodies.push(parsedBody);
        if (sentBodies.length === 1) {
          return {
            ok: true,
            json: async () => ({
              message: {
                id: "msg-user-1",
                thread_id: "dm_agent-stone",
                sender_id: "user-1",
                sender_name: "Sam",
                sender_type: "user",
                content: "Initial direction",
                created_at: "2026-02-08T00:00:00.000Z",
                updated_at: "2026-02-08T00:00:00.000Z",
              },
              delivery: {
                delivered: false,
                error: "OpenClaw delivery unavailable; message queued for retry",
              },
            }),
          };
        }
        return {
          ok: true,
          json: async () => ({
            message: {
              id: "msg-user-2",
              thread_id: "dm_agent-stone",
              sender_id: "user-1",
              sender_name: "Sam",
              sender_type: "user",
              content: "Follow-up context",
              created_at: "2026-02-08T00:00:01.000Z",
              updated_at: "2026-02-08T00:00:01.000Z",
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

    render(<GlobalChatSurface conversation={baseConversation} />);
    const composer = await screen.findByPlaceholderText("Message Stone...");
    const sendButton = screen.getByRole("button", { name: "Send message" });

    await user.type(composer, "Initial direction");
    await user.click(sendButton);

    await waitFor(() => {
      expect(screen.getByText("Saved; delivery pending")).toBeInTheDocument();
    });
    await waitFor(() => {
      expect(composer).toBeEnabled();
    });

    await user.type(composer, "Follow-up context");
    await user.click(sendButton);

    await waitFor(() => {
      expect(screen.getByText("Delivered to bridge")).toBeInTheDocument();
    });
    expect(sentBodies).toHaveLength(2);
    expect(sentBodies[0]?.content).toBe("Initial direction");
    expect(sentBodies[1]?.content).toBe("Follow-up context");

    const userMessages = (lastMessageHistoryProps?.messages ?? []).filter(
      (entry) => entry.senderType === "user",
    );
    expect(userMessages.some((entry) => entry.content === "Initial direction")).toBe(true);
    expect(userMessages.some((entry) => entry.content === "Follow-up context")).toBe(true);
  });

  it("applies issue websocket comments when issue_id is provided in nested comment payload", async () => {
    const issueConversation: GlobalIssueConversation = {
      key: "issue:issue-1",
      type: "issue",
      issueId: "issue-1",
      projectId: "project-1",
      title: "Issue One",
      contextLabel: "Issue • Project One",
      subtitle: "Issue conversation",
      unreadCount: 0,
      updatedAt: "2026-02-07T00:00:00.000Z",
    };

    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/issues/issue-1?")) {
        return {
          ok: true,
          json: async () => ({
            issue: {
              id: "issue-1",
              project_id: "project-1",
            },
            comments: [],
            participants: [{ agent_id: "agent-2", role: "owner" }],
            questionnaires: [],
          }),
        };
      }
      return {
        ok: true,
        json: async () => ({}),
      };
    });
    vi.stubGlobal("fetch", fetchMock);

    const { rerender } = render(<GlobalChatSurface conversation={issueConversation} />);
    await screen.findByPlaceholderText("Message Issue One...");

    wsState.lastMessage = {
      type: "IssueCommentCreated",
      data: {
        comment: {
          id: "comment-1",
          issue_id: "issue-1",
          author_agent_id: "agent-2",
          body: "Nested issue comment event",
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
        },
      },
    };
    rerender(<GlobalChatSurface conversation={issueConversation} />);

    await waitFor(() => {
      const match = (lastMessageHistoryProps?.messages ?? []).find(
        (entry) => String(entry.content ?? "") === "Nested issue comment event",
      );
      expect(match).toBeTruthy();
    });
  });

  it("deduplicates repeated agent DM websocket payloads with equivalent content", async () => {
    const nowISO = new Date().toISOString();
    const { rerender } = render(<GlobalChatSurface conversation={baseConversation} />);
    await screen.findByPlaceholderText("Message Stone...");

    wsState.lastMessage = {
      type: "DMMessageReceived",
      data: {
        threadId: "dm_agent-stone",
        message: {
          id: "dm-agent-1",
          threadId: "dm_agent-stone",
          senderType: "agent",
          senderName: "Stone",
          content: "Same answer",
          createdAt: nowISO,
          updatedAt: nowISO,
        },
      },
    };
    rerender(<GlobalChatSurface conversation={baseConversation} />);

    await waitFor(() => {
      const matches = (lastMessageHistoryProps?.messages ?? []).filter(
        (entry) => String(entry.content ?? "") === "Same answer",
      );
      expect(matches).toHaveLength(1);
    });

    wsState.lastMessage = {
      type: "DMMessageReceived",
      data: {
        threadId: "dm_agent-stone",
        message: {
          id: "dm-agent-2",
          threadId: "dm_agent-stone",
          senderType: "agent",
          senderName: "Stone",
          content: "Same   answer",
          createdAt: nowISO,
          updatedAt: nowISO,
        },
      },
    };
    rerender(<GlobalChatSurface conversation={baseConversation} />);

    await waitFor(() => {
      const matches = (lastMessageHistoryProps?.messages ?? []).filter(
        (entry) => String(entry.content ?? "").replace(/\s+/g, " ").trim() === "Same answer",
      );
      expect(matches).toHaveLength(1);
    });
  });

  it("renders a single in-place emission message in the timeline for active dm conversation", async () => {
    const { rerender } = render(<GlobalChatSurface conversation={baseConversation} />);
    await screen.findByPlaceholderText("Message Stone...");

    wsState.lastMessage = {
      type: "EmissionReceived",
      data: {
        id: "em-1",
        source_type: "bridge",
        source_id: "dm:dm_agent-stone",
        kind: "status",
        summary: "Running write",
        timestamp: "2026-02-08T00:00:00.000Z",
      },
    };
    rerender(<GlobalChatSurface conversation={baseConversation} />);

    await waitFor(() => {
      const emissions = (lastMessageHistoryProps?.messages ?? []).filter(
        (entry) => entry.senderType === "emission",
      );
      expect(emissions).toHaveLength(1);
      expect(emissions[0]?.content).toBe("Running write");
      expect(emissions[0]?.id).toBe("emission-dm:dm_agent-stone");
    });

    wsState.lastMessage = {
      type: "EmissionReceived",
      data: {
        id: "em-2",
        source_type: "bridge",
        source_id: "dm:dm_agent-stone",
        kind: "status",
        summary: "Reading docs",
        timestamp: "2026-02-08T00:00:01.000Z",
      },
    };
    rerender(<GlobalChatSurface conversation={baseConversation} />);

    await waitFor(() => {
      const emissions = (lastMessageHistoryProps?.messages ?? []).filter(
        (entry) => entry.senderType === "emission",
      );
      expect(emissions).toHaveLength(1);
      expect(emissions[0]?.content).toBe("Reading docs");
      expect(emissions[0]?.id).toBe("emission-dm:dm_agent-stone");
    });
  });

  it("replaces timeline emission message in-place when final agent reply arrives", async () => {
    const nowISO = "2026-02-08T00:00:00.000Z";
    const { rerender } = render(<GlobalChatSurface conversation={baseConversation} />);
    await screen.findByPlaceholderText("Message Stone...");

    wsState.lastMessage = {
      type: "EmissionReceived",
      data: {
        id: "em-1",
        source_type: "bridge",
        source_id: "dm:dm_agent-stone",
        kind: "status",
        summary: "Planning response",
        timestamp: nowISO,
      },
    };
    rerender(<GlobalChatSurface conversation={baseConversation} />);
    let emissionIndex = -1;
    await waitFor(() => {
      emissionIndex = (lastMessageHistoryProps?.messages ?? []).findIndex(
        (entry) => entry.senderType === "emission",
      );
      expect(emissionIndex).toBeGreaterThanOrEqual(0);
    });

    wsState.lastMessage = {
      type: "DMMessageReceived",
      data: {
        threadId: "dm_agent-stone",
        message: {
          id: "msg-agent-1",
          threadId: "dm_agent-stone",
          senderType: "agent",
          senderName: "Stone",
          content: "Done and shipped.",
          createdAt: nowISO,
          updatedAt: nowISO,
        },
      },
    };
    rerender(<GlobalChatSurface conversation={baseConversation} />);

    await waitFor(() => {
      const messages = lastMessageHistoryProps?.messages ?? [];
      const emissions = messages.filter((entry) => entry.senderType === "emission");
      expect(emissions).toHaveLength(0);
      expect(messages[emissionIndex]?.id).toBe("msg-agent-1");
      expect(messages[emissionIndex]?.content).toBe("Done and shipped.");
    });
  });

  it("shows stalled emission warning after 120s when no emission or reply arrives after send", async () => {
    const sendAt = "2026-02-08T00:00:00.000Z";
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/messages?")) {
        return { ok: true, json: async () => ({ messages: [] }) };
      }
      if (url.endsWith("/api/messages")) {
        return {
          ok: true,
          json: async () => ({
            message: {
              id: "msg-user-1",
              threadId: "dm_agent-stone",
              senderType: "user",
              senderName: "Sam",
              content: "Ship it",
              createdAt: sendAt,
              updatedAt: sendAt,
            },
            delivery: { delivered: false },
          }),
        };
      }
      return { ok: true, json: async () => ({}) };
    });
    vi.stubGlobal("fetch", fetchMock);
    const timeoutSpy = vi.spyOn(window, "setTimeout");

    render(<GlobalChatSurface conversation={baseConversation} />);
    await screen.findByPlaceholderText("Message Stone...");

    await act(async () => {
      fireEvent.change(screen.getByLabelText("Message composer"), {
        target: { value: "Ship it" },
      });
      fireEvent.click(screen.getByLabelText("Send message"));
    });

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/messages"),
      expect.objectContaining({ method: "POST" }),
    );

    const stalledTimeout = timeoutSpy.mock.calls.find(([, delay]) => delay === 120_000)?.[0];
    expect(typeof stalledTimeout).toBe("function");
    act(() => {
      (stalledTimeout as () => void)();
    });
    await waitFor(() => {
      const warningEmission = (lastMessageHistoryProps?.messages ?? []).find(
        (entry) =>
          entry.senderType === "emission" &&
          entry.content === "Agent may be unresponsive" &&
          entry.emissionWarning === true,
      );
      expect(warningEmission).toBeDefined();
    });
  });

  it("clears stalled warning when matching emission arrives", async () => {
    const sendAt = "2026-02-08T00:00:00.000Z";
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/messages?")) {
        return { ok: true, json: async () => ({ messages: [] }) };
      }
      if (url.endsWith("/api/messages")) {
        return {
          ok: true,
          json: async () => ({
            message: {
              id: "msg-user-1",
              threadId: "dm_agent-stone",
              senderType: "user",
              senderName: "Sam",
              content: "Ship it",
              createdAt: sendAt,
              updatedAt: sendAt,
            },
            delivery: { delivered: false },
          }),
        };
      }
      return { ok: true, json: async () => ({}) };
    });
    vi.stubGlobal("fetch", fetchMock);
    const timeoutSpy = vi.spyOn(window, "setTimeout");

    const { rerender } = render(<GlobalChatSurface conversation={baseConversation} />);
    await screen.findByPlaceholderText("Message Stone...");

    await act(async () => {
      fireEvent.change(screen.getByLabelText("Message composer"), {
        target: { value: "Ship it" },
      });
      fireEvent.click(screen.getByLabelText("Send message"));
    });

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/messages"),
      expect.objectContaining({ method: "POST" }),
    );

    const stalledTimeout = timeoutSpy.mock.calls.find(([, delay]) => delay === 120_000)?.[0];
    expect(typeof stalledTimeout).toBe("function");
    act(() => {
      (stalledTimeout as () => void)();
    });
    await waitFor(() => {
      const warningEmission = (lastMessageHistoryProps?.messages ?? []).find(
        (entry) => entry.senderType === "emission" && entry.content === "Agent may be unresponsive",
      );
      expect(warningEmission).toBeDefined();
    });

    wsState.lastMessage = {
      type: "EmissionReceived",
      data: {
        id: "em-clear-1",
        source_type: "bridge",
        source_id: "dm:dm_agent-stone",
        kind: "status",
        summary: "Running command",
        timestamp: "2026-02-08T00:01:01.000Z",
      },
    };
    act(() => {
      rerender(<GlobalChatSurface conversation={baseConversation} />);
    });

    await waitFor(() => {
      const emissions = (lastMessageHistoryProps?.messages ?? []).filter(
        (entry) => entry.senderType === "emission",
      );
      expect(emissions).toHaveLength(1);
      expect(emissions[0]?.content).toBe("Running command");
      expect(emissions[0]?.emissionWarning).not.toBe(true);
    });
  });

  it("clears stalled warning when final agent reply arrives", async () => {
    const sendAt = "2026-02-08T00:00:00.000Z";
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/messages?")) {
        return { ok: true, json: async () => ({ messages: [] }) };
      }
      if (url.endsWith("/api/messages")) {
        return {
          ok: true,
          json: async () => ({
            message: {
              id: "msg-user-1",
              threadId: "dm_agent-stone",
              senderType: "user",
              senderName: "Sam",
              content: "Ship it",
              createdAt: sendAt,
              updatedAt: sendAt,
            },
            delivery: { delivered: false },
          }),
        };
      }
      return { ok: true, json: async () => ({}) };
    });
    vi.stubGlobal("fetch", fetchMock);
    const timeoutSpy = vi.spyOn(window, "setTimeout");

    const { rerender } = render(<GlobalChatSurface conversation={baseConversation} />);
    await screen.findByPlaceholderText("Message Stone...");

    await act(async () => {
      fireEvent.change(screen.getByLabelText("Message composer"), {
        target: { value: "Ship it" },
      });
      fireEvent.click(screen.getByLabelText("Send message"));
    });

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/messages"),
      expect.objectContaining({ method: "POST" }),
    );

    const stalledTimeout = timeoutSpy.mock.calls.find(([, delay]) => delay === 120_000)?.[0];
    expect(typeof stalledTimeout).toBe("function");
    act(() => {
      (stalledTimeout as () => void)();
    });
    await waitFor(() => {
      const warningEmission = (lastMessageHistoryProps?.messages ?? []).find(
        (entry) => entry.senderType === "emission" && entry.content === "Agent may be unresponsive",
      );
      expect(warningEmission).toBeDefined();
    });

    wsState.lastMessage = {
      type: "DMMessageReceived",
      data: {
        threadId: "dm_agent-stone",
        message: {
          id: "msg-agent-1",
          threadId: "dm_agent-stone",
          senderType: "agent",
          senderName: "Stone",
          content: "Done",
          createdAt: "2026-02-08T00:01:01.000Z",
          updatedAt: "2026-02-08T00:01:01.000Z",
        },
      },
    };
    act(() => {
      rerender(<GlobalChatSurface conversation={baseConversation} />);
    });

    await waitFor(() => {
      const messages = lastMessageHistoryProps?.messages ?? [];
      const emissions = messages.filter((entry) => entry.senderType === "emission");
      expect(emissions).toHaveLength(0);
      expect(messages.some((entry) => entry.content === "Done")).toBe(true);
    });
  });
});
