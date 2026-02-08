import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import useEmissions from "../../hooks/useEmissions";
import { useWS } from "../../contexts/WebSocketContext";
import IssueThreadPanel from "./IssueThreadPanel";

vi.mock("../../contexts/WebSocketContext", () => ({
  useWS: vi.fn(),
}));
vi.mock("../../hooks/useEmissions", () => ({
  default: vi.fn(),
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
    vi.mocked(useEmissions).mockReturnValue({
      emissions: [],
      latestBySource: new Map(),
      loading: false,
      error: null,
      refresh: vi.fn(),
    });
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

  it("renders issue labels and supports inline add/remove actions", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (
        url.includes("/api/projects/project-1/issues/issue-1/labels/label-bug") &&
        init?.method === "DELETE"
      ) {
        return mockJSONResponse({});
      }
      if (url.includes("/api/projects/project-1/issues/issue-1/labels?") && init?.method === "POST") {
        return mockJSONResponse({});
      }
      if (url.includes("/api/labels?")) {
        return mockJSONResponse({
          labels: [
            { id: "label-bug", name: "bug", color: "#ef4444" },
            { id: "label-feature", name: "feature", color: "#22c55e" },
          ],
        });
      }
      if (url.includes("/api/issues/issue-1?")) {
        return mockJSONResponse({
          issue: {
            id: "issue-1",
            project_id: "project-1",
            issue_number: 1,
            title: "Label issue",
            state: "open",
            origin: "local",
            labels: [{ id: "label-bug", name: "bug", color: "#ef4444" }],
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

    expect(await screen.findByText("#1 Label issue")).toBeInTheDocument();
    expect(screen.getAllByText("bug").length).toBeGreaterThan(0);

    await user.click(screen.getByRole("button", { name: "Manage labels" }));
    await user.click(await screen.findByRole("button", { name: "Add label feature" }));

    await waitFor(() => {
      expect(screen.getAllByText("feature").length).toBeGreaterThan(0);
    });

    await user.click(screen.getByRole("button", { name: "Manage labels" }));
    await user.click(screen.getByRole("button", { name: "Remove label bug" }));

    await waitFor(() => {
      expect(screen.queryByText("bug")).not.toBeInTheDocument();
    });

    expect(
      fetchMock.mock.calls.some(([input, reqInit]) =>
        String(input).includes("/api/projects/project-1/issues/issue-1/labels?") &&
        (reqInit as RequestInit | undefined)?.method === "POST",
      ),
    ).toBe(true);
    expect(
      fetchMock.mock.calls.some(([input, reqInit]) =>
        String(input).includes("/api/projects/project-1/issues/issue-1/labels/label-bug?") &&
        (reqInit as RequestInit | undefined)?.method === "DELETE",
      ),
    ).toBe(true);
  });

  it("shows approval badge and renders linked post in markdown workspace", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/issues/issue-1/review/history?")) {
        return mockJSONResponse({
          issue_id: "issue-1",
          document_path: "/posts/2026-02-06-launch-plan.md",
          items: [
            {
              sha: "sha-latest",
              subject: "Latest",
              authored_at: "2026-02-06T12:00:00Z",
              author_name: "Sam",
              is_review_checkpoint: false,
            },
          ],
          total: 1,
        });
      }
      if (url.includes("/api/issues/issue-1/review/changes?")) {
        return mockJSONResponse({
          issue_id: "issue-1",
          document_path: "/posts/2026-02-06-launch-plan.md",
          base_sha: "sha-base",
          head_sha: "sha-head",
          fallback_to_first_commit: true,
          files: [],
          total: 0,
        });
      }
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

  it("renders issue-scoped live activity indicator and stream", async () => {
    vi.mocked(useEmissions).mockReturnValue({
      emissions: [
        {
          id: "em-1",
          source_type: "agent",
          source_id: "agent-1",
          kind: "progress",
          summary: "Finished lint cleanup",
          timestamp: new Date().toISOString(),
          scope: { issue_id: "issue-1" },
        },
      ],
      latestBySource: new Map(),
      loading: false,
      error: null,
      refresh: vi.fn(),
    });

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
          comments: [],
        });
      }
      return mockJSONResponse({
        agents: [{ id: "sam", name: "Sam" }],
      });
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<IssueThreadPanel issueID="issue-1" />);

    expect(await screen.findByText("#1 Thread issue")).toBeInTheDocument();
    expect(screen.getByText(/Agent is working on this issue/i)).toBeInTheDocument();
    expect(screen.getByText("Finished lint cleanup")).toBeInTheDocument();
    expect(useEmissions).toHaveBeenCalledWith({ issueId: "issue-1", limit: 30 });
  });

  it("lists history and opens historical version with read-only comment rendering", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/issues/issue-1/review/history/sha-old?")) {
        return mockJSONResponse({
          issue_id: "issue-1",
          document_path: "/posts/2026-02-06-review.md",
          sha: "sha-old",
          content: "# Old\n\nBody {>>AB: old comment<<}",
          read_only: true,
        });
      }
      if (url.includes("/api/issues/issue-1/review/history?")) {
        return mockJSONResponse({
          issue_id: "issue-1",
          document_path: "/posts/2026-02-06-review.md",
          items: [
            {
              sha: "sha-new",
              subject: "Review v2",
              authored_at: "2026-02-06T12:00:00Z",
              author_name: "Sam",
              is_review_checkpoint: false,
            },
            {
              sha: "sha-old",
              subject: "Review v1",
              authored_at: "2026-02-06T11:00:00Z",
              author_name: "Sam",
              is_review_checkpoint: true,
            },
          ],
          total: 2,
        });
      }
      if (url.includes("/api/issues/issue-1/review/changes?")) {
        return mockJSONResponse({
          issue_id: "issue-1",
          document_path: "/posts/2026-02-06-review.md",
          base_sha: "sha-old",
          head_sha: "sha-new",
          fallback_to_first_commit: false,
          files: [{ path: "/posts/2026-02-06-review.md", change_type: "modified" }],
          total: 1,
        });
      }
      if (url.includes("/api/issues/issue-1?")) {
        return mockJSONResponse({
          issue: {
            id: "issue-1",
            issue_number: 1,
            title: "History issue",
            state: "open",
            origin: "local",
            approval_state: "ready_for_review",
            document_path: "/posts/2026-02-06-review.md",
            document_content: "# New\n\nBody",
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

    expect(await screen.findByText("#1 History issue")).toBeInTheDocument();
    expect(await screen.findByTestId("issue-review-history-list")).toBeInTheDocument();

    await user.click(screen.getByTestId("issue-review-open-sha-old"));
    expect(await screen.findByTestId("content-review-read-only")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add Inline Comment" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /resolve comment/i })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Rendered" }));
    expect(await screen.findByText("old comment")).toBeInTheDocument();
    expect(screen.getByTestId("critic-comment-bubble")).toBeInTheDocument();
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

  it("approving from ready-for-review triggers confetti once and updates state badges", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/issues/issue-1/approve") && init?.method === "POST") {
        return mockJSONResponse({
          id: "issue-1",
          issue_number: 1,
          title: "Approval issue",
          state: "closed",
          origin: "local",
          approval_state: "approved",
          document_path: null,
        });
      }
      if (url.includes("/api/issues/issue-1?")) {
        return mockJSONResponse({
          issue: {
            id: "issue-1",
            issue_number: 1,
            title: "Approval issue",
            state: "open",
            origin: "local",
            approval_state: "ready_for_review",
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

    expect(await screen.findByText("#1 Approval issue")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Approve" }));

    await waitFor(() => {
      expect(screen.getByTestId("issue-thread-approval")).toHaveTextContent("Approved");
    });
    expect(screen.getByText(/Local · Closed/)).toBeInTheDocument();
    expect(screen.getByTestId("approval-confetti")).toBeInTheDocument();
    expect(screen.getAllByTestId("approval-confetti")).toHaveLength(1);
  });

  it("shows parent issue context and child sub-issue summary", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/issues?") && url.includes("parent_issue_id=issue-child")) {
        return mockJSONResponse({
          items: [
            {
              id: "issue-grandchild",
              issue_number: 3,
              title: "Grandchild issue",
              parent_issue_id: "issue-child",
              state: "open",
              origin: "local",
              kind: "issue",
              last_activity_at: "2026-02-06T09:00:00Z",
            },
          ],
          total: 1,
        });
      }
      if (url.includes("/api/issues/issue-parent?")) {
        return mockJSONResponse({
          issue: {
            id: "issue-parent",
            issue_number: 1,
            title: "Parent issue",
            state: "open",
            origin: "local",
          },
          participants: [],
          comments: [],
        });
      }
      if (url.includes("/api/issues/issue-child?")) {
        return mockJSONResponse({
          issue: {
            id: "issue-child",
            project_id: "project-1",
            parent_issue_id: "issue-parent",
            issue_number: 2,
            title: "Child issue",
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
      throw new Error(`unexpected url ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<IssueThreadPanel issueID="issue-child" />);

    expect(await screen.findByText("#2 Child issue")).toBeInTheDocument();
    expect(await screen.findByText("Parent: #1 Parent issue")).toBeInTheDocument();
    expect(await screen.findByText("Sub-issues: 1")).toBeInTheDocument();
    expect(await screen.findByText("#3 Grandchild issue")).toBeInTheDocument();
  });
});
