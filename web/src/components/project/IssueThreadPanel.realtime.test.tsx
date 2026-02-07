import { render, screen, waitFor } from "@testing-library/react";
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

describe("IssueThreadPanel realtime integration", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
    vi.restoreAllMocks();
  });

  it("appends websocket comments for the active issue only", async () => {
    let wsState = {
      connected: true,
      lastMessage: null,
      sendMessage: vi.fn(() => true),
    } as ReturnType<typeof useWS>;

    vi.mocked(useWS).mockImplementation(() => wsState);

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

    const { rerender } = render(<IssueThreadPanel issueID="issue-1" />);
    await screen.findByText("#1 Thread issue");

    wsState = {
      ...wsState,
      lastMessage: {
        type: "IssueCommentCreated",
        data: {
          issue_id: "issue-2",
          comment: {
            id: "comment-other",
            author_agent_id: "sam",
            body: "Other issue comment",
            created_at: "2026-02-06T12:00:00Z",
            updated_at: "2026-02-06T12:00:00Z",
          },
        },
      },
    };
    rerender(<IssueThreadPanel issueID="issue-1" />);

    expect(screen.queryByText("Other issue comment")).not.toBeInTheDocument();

    wsState = {
      ...wsState,
      lastMessage: {
        type: "IssueCommentCreated",
        data: {
          issue_id: "issue-1",
          comment: {
            id: "comment-active",
            author_agent_id: "sam",
            body: "Active issue comment",
            created_at: "2026-02-06T12:01:00Z",
            updated_at: "2026-02-06T12:01:00Z",
          },
        },
      },
    };
    rerender(<IssueThreadPanel issueID="issue-1" />);

    expect(await screen.findByText("Active issue comment")).toBeInTheDocument();
  });

  it("refreshes review state when review events arrive for the active issue", async () => {
    let wsState = {
      connected: true,
      lastMessage: null,
      sendMessage: vi.fn(() => true),
    } as ReturnType<typeof useWS>;

    vi.mocked(useWS).mockImplementation(() => wsState);

    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/issues/issue-1/review/history?")) {
        return mockJSONResponse({
          issue_id: "issue-1",
          document_path: "/posts/2026-02-07-review.md",
          items: [
            {
              sha: "sha-1",
              subject: "Review v1",
              authored_at: "2026-02-07T11:00:00Z",
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
          document_path: "/posts/2026-02-07-review.md",
          base_sha: "sha-base",
          head_sha: "sha-head",
          fallback_to_first_commit: false,
          files: [{ path: "/posts/2026-02-07-review.md", change_type: "modified" }],
          total: 1,
        });
      }
      if (url.includes("/api/issues/issue-1?")) {
        return mockJSONResponse({
          issue: {
            id: "issue-1",
            issue_number: 1,
            title: "Realtime review issue",
            state: "open",
            origin: "local",
            approval_state: "ready_for_review",
            document_path: "/posts/2026-02-07-review.md",
            document_content: "# Draft",
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

    const { rerender } = render(<IssueThreadPanel issueID="issue-1" />);
    await screen.findByText("#1 Realtime review issue");

    expect(
      fetchMock.mock.calls.filter(([input]) => String(input).includes("/api/issues/issue-1/review/history?")),
    ).toHaveLength(1);
    expect(
      fetchMock.mock.calls.filter(([input]) => String(input).includes("/api/issues/issue-1/review/changes?")),
    ).toHaveLength(1);

    wsState = {
      ...wsState,
      lastMessage: {
        type: "IssueReviewSaved",
        data: {
          issue_id: "issue-2",
        },
      },
    };
    rerender(<IssueThreadPanel issueID="issue-1" />);

    await waitFor(() => {
      expect(
        fetchMock.mock.calls.filter(([input]) => String(input).includes("/api/issues/issue-1/review/history?")),
      ).toHaveLength(1);
    });

    wsState = {
      ...wsState,
      lastMessage: {
        type: "IssueReviewAddressed",
        data: {
          issue_id: "issue-1",
        },
      },
    };
    rerender(<IssueThreadPanel issueID="issue-1" />);

    await waitFor(() => {
      expect(
        fetchMock.mock.calls.filter(([input]) => String(input).includes("/api/issues/issue-1/review/history?")),
      ).toHaveLength(2);
    });
    expect(
      fetchMock.mock.calls.filter(([input]) => String(input).includes("/api/issues/issue-1/review/changes?")),
    ).toHaveLength(2);
  });
});
