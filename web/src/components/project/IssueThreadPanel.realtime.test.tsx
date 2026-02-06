import { render, screen } from "@testing-library/react";
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
});
