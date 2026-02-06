import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useWS } from "../../contexts/WebSocketContext";
import IssueThreadPanel from "./IssueThreadPanel";

vi.mock("../../contexts/WebSocketContext", () => ({
  useWS: vi.fn(),
}));

function mockJSONResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("IssueThreadPanel", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
    vi.restoreAllMocks();
    vi.mocked(useWS).mockReturnValue({
      connected: false,
      lastMessage: null,
      sendMessage: vi.fn(() => true),
    });
  });

  it("loads full comment history and paginates correctly", async () => {
    const comments = Array.from({ length: 25 }).map((_, index) => ({
      id: `c-${index + 1}`,
      author_agent_id: "sam",
      body: `Comment ${index + 1}`,
      created_at: `2026-02-06T0${Math.min(index, 9)}:00:00Z`,
      updated_at: `2026-02-06T0${Math.min(index, 9)}:00:00Z`,
    }));

    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/issues/issue-1?")) {
        return mockJSONResponse({
          issue: {
            id: "issue-1",
            issue_number: 1,
            title: "Thread issue",
            state: "open",
            origin: "local",
          },
          participants: [],
          comments,
        });
      }
      return mockJSONResponse({
        agents: [{ id: "sam", name: "Sam" }],
      });
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<IssueThreadPanel issueID="issue-1" />);

    expect(await screen.findByText("Comment 25")).toBeInTheDocument();
    expect(screen.queryByText("Comment 1")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Load older comments" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Load older comments" }));
    expect(await screen.findByText("Comment 1")).toBeInTheDocument();
  });

  it("posts comments optimistically and reconciles persisted response", async () => {
    let resolveCommentPost: ((response: Response) => void) | null = null;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/issues/issue-1/comments")) {
        return new Promise<Response>((resolve) => {
          resolveCommentPost = resolve;
        });
      }
      if (url.includes("/api/issues/issue-1?")) {
        return mockJSONResponse({
          issue: {
            id: "issue-1",
            issue_number: 1,
            title: "Thread issue",
            state: "open",
            origin: "local",
          },
          participants: [],
          comments: [],
        });
      }
      if (url.includes("/api/agents?")) {
        return mockJSONResponse({
          agents: [{ id: "sam", name: "Sam" }],
        });
      }
      throw new Error(`unexpected url ${url} ${String(init?.method)}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    const user = userEvent.setup();
    render(<IssueThreadPanel issueID="issue-1" />);
    await screen.findByText("#1 Thread issue");

    await user.type(
      screen.getByPlaceholderText("Write a comment… use @AgentName to auto-add participants."),
      "Ship it",
    );
    await user.click(screen.getByRole("button", { name: "Post Comment" }));

    expect(screen.getByText("Ship it")).toBeInTheDocument();
    expect(screen.getByText("Sending…")).toBeInTheDocument();

    expect(resolveCommentPost).not.toBeNull();
    resolveCommentPost!(mockJSONResponse({
      id: "comment-1",
      author_agent_id: "sam",
      body: "Ship it",
      created_at: "2026-02-06T11:00:00Z",
      updated_at: "2026-02-06T11:00:00Z",
    }));

    await waitFor(() => {
      expect(screen.queryByText("Sending…")).not.toBeInTheDocument();
    });
    expect(screen.getByText("Ship it")).toBeInTheDocument();
  });

  it("@mention auto-adds referenced agents as participants", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/issues/issue-1/comments")) {
        return mockJSONResponse({
          id: "comment-2",
          author_agent_id: "sam",
          body: "Please review @Stone and @nova",
          created_at: "2026-02-06T11:10:00Z",
          updated_at: "2026-02-06T11:10:00Z",
        });
      }
      if (url.includes("/api/issues/issue-1/participants") && init?.method === "POST") {
        const payload = JSON.parse(String(init.body));
        return mockJSONResponse({
          id: `participant-${payload.agent_id}`,
          agent_id: payload.agent_id,
          role: "collaborator",
        });
      }
      if (url.includes("/api/issues/issue-1?")) {
        return mockJSONResponse({
          issue: {
            id: "issue-1",
            issue_number: 1,
            title: "Thread issue",
            state: "open",
            origin: "local",
          },
          participants: [
            { id: "owner-1", agent_id: "sam", role: "owner", removed_at: null },
          ],
          comments: [],
        });
      }
      if (url.includes("/api/agents?")) {
        return mockJSONResponse({
          agents: [
            { id: "sam", name: "Sam" },
            { id: "stone", name: "Stone" },
            { id: "nova", name: "Nova" },
          ],
        });
      }
      throw new Error(`unexpected url ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    const user = userEvent.setup();
    render(<IssueThreadPanel issueID="issue-1" />);
    await screen.findByText("#1 Thread issue");

    await user.type(
      screen.getByPlaceholderText("Write a comment… use @AgentName to auto-add participants."),
      "Please review @Stone and @nova",
    );
    await user.click(screen.getByRole("button", { name: "Post Comment" }));

    await waitFor(() => {
      const participantCalls = fetchMock.mock.calls.filter(([input, init]) =>
        String(input).includes("/api/issues/issue-1/participants") &&
        (init as RequestInit | undefined)?.method === "POST"
      );
      expect(participantCalls).toHaveLength(2);
    });
  });

  it("shows approval badge and renders linked post in markdown workspace", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/issues/issue-1?")) {
        return mockJSONResponse({
          issue: {
            id: "issue-1",
            issue_number: 1,
            title: "Linked post review",
            state: "open",
            origin: "local",
            approval_state: "ready_for_review",
            document_path: "/posts/2026-02-06-launch-plan.md",
            document_content: "# Launch plan",
          },
          participants: [],
          comments: [],
        });
      }
      if (url.includes("/api/agents?")) {
        return mockJSONResponse({
          agents: [{ id: "sam", name: "Sam" }],
        });
      }
      throw new Error(`unexpected url ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<IssueThreadPanel issueID="issue-1" />);

    expect(await screen.findByText("#1 Linked post review")).toBeInTheDocument();
    expect(screen.getByTestId("issue-thread-approval")).toHaveTextContent("Ready for Review");
    expect(screen.getByText("/posts/2026-02-06-launch-plan.md")).toBeInTheDocument();
    expect(screen.getByTestId("editor-mode-markdown")).toBeInTheDocument();
  });

  it("shows only valid next review actions and updates badge on transition", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/issues/issue-1/approval-state") && init?.method === "POST") {
        return mockJSONResponse({
          id: "issue-1",
          issue_number: 1,
          title: "Workflow issue",
          state: "open",
          origin: "local",
          approval_state: "ready_for_review",
          document_path: null,
        });
      }
      if (url.includes("/api/issues/issue-1?")) {
        return mockJSONResponse({
          issue: {
            id: "issue-1",
            issue_number: 1,
            title: "Workflow issue",
            state: "open",
            origin: "local",
            approval_state: "draft",
            document_path: null,
          },
          participants: [],
          comments: [],
        });
      }
      if (url.includes("/api/agents?")) {
        return mockJSONResponse({
          agents: [{ id: "sam", name: "Sam" }],
        });
      }
      throw new Error(`unexpected url ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    const user = userEvent.setup();
    render(<IssueThreadPanel issueID="issue-1" />);

    expect(await screen.findByText("#1 Workflow issue")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Mark Ready for Review" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Approve" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Request Changes" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Mark Ready for Review" }));

    await waitFor(() => {
      expect(screen.getByTestId("issue-thread-approval")).toHaveTextContent("Ready for Review");
    });
    expect(screen.getByRole("button", { name: "Approve" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Request Changes" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Mark Ready for Review" })).not.toBeInTheDocument();
  });
});
