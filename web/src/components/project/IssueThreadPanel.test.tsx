import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
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

    expect(screen.getByTestId("issue-thread-shell")).toBeInTheDocument();
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

  it("renders issue detail metadata for body, priority, work status, and assignee", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/issues/issue-1?")) {
        return mockJSONResponse({
          issue: {
            id: "issue-1",
            issue_number: 1,
            title: "Metadata issue",
            state: "open",
            origin: "local",
            body: "Fix the issue detail metadata rendering path.",
            priority: "P1",
            work_status: "in_progress",
            owner_agent_id: "sam",
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

    expect(await screen.findByText("#1 Metadata issue")).toBeInTheDocument();
    expect(screen.getByText("Priority: P1")).toBeInTheDocument();
    expect(screen.getByText("Work Status: In Progress")).toBeInTheDocument();
    expect(screen.getByText("Assignee: Sam")).toBeInTheDocument();
    expect(screen.getByText("Fix the issue detail metadata rendering path.")).toBeInTheDocument();
  });

  it("renders project-scoped issue not-found state with back link", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/issues/missing-issue?")) {
        return mockJSONResponse({ error: "not found" }, 404);
      }
      if (url.includes("/api/agents?")) {
        return mockJSONResponse({
          agents: [{ id: "sam", name: "Sam" }],
        });
      }
      throw new Error(`unexpected url ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter>
        <IssueThreadPanel issueID="missing-issue" projectID="project-1" />
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { name: "Issue not found" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Back to Project" })).toHaveAttribute("href", "/projects/project-1");
    expect(screen.queryByText(/demo/i)).not.toBeInTheDocument();
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

  it("hides manual review transition controls while in draft", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
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

    render(<IssueThreadPanel issueID="issue-1" />);

    expect(await screen.findByText("#1 Workflow issue")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Mark Ready for Review" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Approve" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Request Changes" })).not.toBeInTheDocument();
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

  it("renders workflow pipeline and updates stage when selecting the next stage", async () => {
    const patchStatuses: string[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url.includes("/api/issues/issue-1?") && method === "PATCH") {
        const payload = JSON.parse(String(init?.body));
        patchStatuses.push(payload.work_status);
        return mockJSONResponse({
          id: "issue-1",
          issue_number: 1,
          title: "Flow issue",
          state: "open",
          origin: "local",
          work_status: payload.work_status,
          last_activity_at: "2026-02-08T20:30:00Z",
        });
      }
      if (url.includes("/api/issues/issue-1?")) {
        return mockJSONResponse({
          issue: {
            id: "issue-1",
            issue_number: 1,
            title: "Flow issue",
            state: "open",
            origin: "local",
            work_status: "in_progress",
            last_activity_at: "2026-02-08T20:00:00Z",
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

    expect(await screen.findByText("#1 Flow issue")).toBeInTheDocument();
    expect(screen.getByTestId("pipeline-stage-in_progress")).toHaveAttribute("data-stage-state", "current");

    await user.click(screen.getByTestId("pipeline-stage-review"));

    await waitFor(() => {
      expect(patchStatuses).toEqual(["review"]);
    });
    expect(screen.getByTestId("pipeline-stage-review")).toHaveAttribute("data-stage-state", "current");
  });

  it("asks for confirmation before skipping workflow stages", async () => {
    const patchStatuses: string[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url.includes("/api/issues/issue-1?") && method === "PATCH") {
        const payload = JSON.parse(String(init?.body));
        patchStatuses.push(payload.work_status);
        return mockJSONResponse({
          id: "issue-1",
          issue_number: 1,
          title: "Skip flow issue",
          state: payload.work_status === "done" ? "closed" : "open",
          origin: "local",
          work_status: payload.work_status,
          last_activity_at: "2026-02-08T21:00:00Z",
        });
      }
      if (url.includes("/api/issues/issue-1?")) {
        return mockJSONResponse({
          issue: {
            id: "issue-1",
            issue_number: 1,
            title: "Skip flow issue",
            state: "open",
            origin: "local",
            work_status: "queued",
            last_activity_at: "2026-02-08T20:00:00Z",
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

    expect(await screen.findByText("#1 Skip flow issue")).toBeInTheDocument();
    await user.click(screen.getByTestId("pipeline-stage-done"));

    expect(screen.getByText("Skip workflow stages?")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Continue" }));

    await waitFor(() => {
      expect(patchStatuses).toEqual(["in_progress", "review", "done"]);
    });
    expect(screen.getByTestId("pipeline-stage-done")).toHaveAttribute("data-stage-state", "current");
  });
});
