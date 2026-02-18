import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import ProjectIssuesList from "./ProjectIssuesList";

type MockIssue = {
  id: string;
  issue_number: number;
  title: string;
  parent_issue_id?: string | null;
  state: "open" | "closed";
  origin: "local" | "github";
  kind: "issue" | "pull_request";
  approval_state?: "draft" | "ready_for_review" | "needs_changes" | "approved" | null;
  owner_agent_id?: string | null;
  work_status?: string;
  last_activity_at: string;
  github_number?: number | null;
  github_url?: string | null;
};

function mockJSONResponse(body: unknown, ok = true) {
  return {
    ok,
    json: async () => body,
  } as Response;
}

function mockIssuesAndAgents(issues: MockIssue[], agents?: Array<{ id: string; name: string }>) {
  return async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes("/api/agents")) {
      return mockJSONResponse({ agents: agents ?? [] });
    }
    return mockJSONResponse({ items: issues, total: issues.length });
  };
}

describe("ProjectIssuesList", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  afterEach(() => {
    localStorage.clear();
  });

  it("renders origin metadata, github fields, and approval badges", async () => {
    fetchMock.mockImplementation(
      mockIssuesAndAgents(
        [
          {
            id: "issue-1",
            issue_number: 77,
            title: "Imported bug",
            state: "open",
            origin: "github",
            kind: "issue",
            approval_state: "ready_for_review",
            owner_agent_id: "stone",
            last_activity_at: "2026-02-06T06:00:00Z",
            github_number: 77,
            github_url: "https://github.com/samhotchkiss/otter-camp/issues/77",
          } satisfies MockIssue,
        ],
        [{ id: "stone", name: "Stone" }],
      ),
    );

    render(<ProjectIssuesList projectId="project-1" />);

    const row = await screen.findByRole("button", { name: /#77 Imported bug/i });
    expect(screen.getByTestId("project-issues-shell")).toBeInTheDocument();
    expect(within(row).getByText("Issue")).toBeInTheDocument();
    expect(within(row).getByText("GitHub")).toBeInTheDocument();
    expect(within(row).getByText("Open", { selector: "span" })).toBeInTheDocument();
    expect(within(row).getByTestId("issue-approval-issue-1")).toHaveTextContent("Ready for Review");
    expect(within(row).getByText("Owner: Stone")).toBeInTheDocument();
    expect(within(row).getByText("GitHub #77", { exact: false })).toBeInTheDocument();
    expect(within(row).getByRole("link", { name: "Open" })).toHaveAttribute(
      "href",
      "https://github.com/samhotchkiss/otter-camp/issues/77",
    );
  });

  it("updates API query and visible rows when filters change", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const raw = typeof input === "string" ? input : input.toString();
      const url = new URL(raw);
      const kind = url.searchParams.get("kind");
      if (url.pathname.includes("/api/agents")) {
        return mockJSONResponse({ agents: [{ id: "sam", name: "Sam" }] });
      }
      if (kind === "pull_request") {
        return mockJSONResponse({
          items: [
            {
              id: "issue-pr",
              issue_number: 22,
              title: "PR item",
              state: "open",
              origin: "github",
              kind: "pull_request",
              owner_agent_id: "sam",
              last_activity_at: "2026-02-06T06:30:00Z",
              github_number: 22,
              github_url: "https://github.com/samhotchkiss/otter-camp/pull/22",
            } satisfies MockIssue,
          ],
          total: 1,
        });
      }
      return mockJSONResponse({
        items: [
          {
            id: "issue-plain",
            issue_number: 10,
            title: "Issue item",
            state: "open",
            origin: "local",
            kind: "issue",
            owner_agent_id: null,
            last_activity_at: "2026-02-06T05:00:00Z",
          } satisfies MockIssue,
        ],
        total: 1,
      });
    });

    render(<ProjectIssuesList projectId="project-1" />);
    expect(await screen.findByText("#10 Issue item")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Issue type filter"), {
      target: { value: "pull_request" },
    });

    expect(await screen.findByText("#22 PR item")).toBeInTheDocument();
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("kind=pull_request"),
        expect.any(Object),
      );
    });
  });

  it("requests default open filter from API and renders server-filtered rows", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const raw = String(input);
      const url = new URL(raw);
      if (url.pathname.includes("/api/agents")) {
        return mockJSONResponse({ agents: [] });
      }
      const state = url.searchParams.get("state");
      if (state === "open") {
        return mockJSONResponse({
          items: [
            {
              id: "issue-open-cli",
              issue_number: 77,
              title: "CLI-created open issue",
              state: "open",
              origin: "local",
              kind: "issue",
              owner_agent_id: null,
              last_activity_at: "2026-02-08T07:00:00Z",
            } satisfies MockIssue,
          ],
          total: 1,
        });
      }
      return mockJSONResponse({
        items: [
          {
            id: "issue-closed",
            issue_number: 78,
            title: "Closed issue",
            state: "closed",
            origin: "local",
            kind: "issue",
            owner_agent_id: null,
            last_activity_at: "2026-02-07T07:00:00Z",
          } satisfies MockIssue,
        ],
        total: 2,
      });
    });

    render(<ProjectIssuesList projectId="project-1" />);

    expect(await screen.findByText("#77 CLI-created open issue")).toBeInTheDocument();
    expect(screen.queryByText("#78 Closed issue")).not.toBeInTheDocument();
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("state=open"),
        expect.any(Object),
      );
    });
  });

  it("renders loading and empty states", async () => {
    let resolveIssuesFetch: ((value: Response) => void) | null = null;
    fetchMock.mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/agents")) {
        return Promise.resolve(mockJSONResponse({ agents: [] }));
      }
      return new Promise<Response>((resolve) => {
        resolveIssuesFetch = resolve;
      });
    });

    render(<ProjectIssuesList projectId="project-1" />);
    expect(screen.getByText("Loading issues...")).toBeInTheDocument();

    expect(resolveIssuesFetch).not.toBeNull();
    resolveIssuesFetch!(mockJSONResponse({ items: [], total: 0 }));

    expect(
      await screen.findByText("No issues found for the selected filters."),
    ).toBeInTheDocument();
  });

  it("shows parent and child relationship metadata", async () => {
    fetchMock.mockImplementation(
      mockIssuesAndAgents(
        [
          {
            id: "issue-parent",
            issue_number: 10,
            title: "Parent issue",
            parent_issue_id: null,
            state: "open",
            origin: "local",
            kind: "issue",
            owner_agent_id: "sam",
            last_activity_at: "2026-02-06T05:00:00Z",
          } satisfies MockIssue,
          {
            id: "issue-child",
            issue_number: 11,
            title: "Child issue",
            parent_issue_id: "issue-parent",
            state: "open",
            origin: "local",
            kind: "issue",
            owner_agent_id: "sam",
            last_activity_at: "2026-02-06T05:30:00Z",
          } satisfies MockIssue,
        ],
        [{ id: "sam", name: "Sam" }],
      ),
    );

    render(<ProjectIssuesList projectId="project-1" />);

    const parentRow = await screen.findByRole("button", { name: /#10 Parent issue/i });
    const childRow = await screen.findByRole("button", { name: /#11 Child issue/i });

    expect(within(parentRow).getByText("Sub-issues: 1")).toBeInTheDocument();
    expect(within(childRow).getByText("Sub-issue of #10")).toBeInTheDocument();
    expect(within(parentRow).queryByText("GitHub metadata unavailable")).not.toBeInTheDocument();
    expect(within(childRow).queryByText("GitHub metadata unavailable")).not.toBeInTheDocument();
  });

  it("renders error state and retries", async () => {
    fetchMock
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValueOnce(mockJSONResponse({ items: [], total: 0 }))
      .mockResolvedValueOnce(mockJSONResponse({ agents: [] }))
      .mockResolvedValueOnce(mockJSONResponse({ items: [], total: 0 }))
      .mockResolvedValueOnce(mockJSONResponse({ agents: [] }));

    render(<ProjectIssuesList projectId="project-1" />);

    expect(await screen.findByText("boom")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(
      await screen.findByText("No issues found for the selected filters."),
    ).toBeInTheDocument();
  });

  it("shows unknown agent instead of raw owner UUID when agent metadata is unavailable", async () => {
    fetchMock.mockImplementation(
      mockIssuesAndAgents(
        [
          {
            id: "issue-uuid-owner",
            issue_number: 42,
            title: "Owner should be friendly",
            state: "open",
            origin: "local",
            kind: "issue",
            owner_agent_id: "6715afdc-9213-4830-b0a8-3a063c5b4209",
            last_activity_at: "2026-02-06T05:00:00Z",
          } satisfies MockIssue,
        ],
        [],
      ),
    );

    render(<ProjectIssuesList projectId="project-1" />);

    const row = await screen.findByRole("button", { name: /#42 Owner should be friendly/i });
    expect(within(row).getByText("Owner: Unknown agent")).toBeInTheDocument();
    expect(within(row).queryByText(/6715afdc-9213-4830-b0a8-3a063c5b4209/)).not.toBeInTheDocument();
  });

  it("renders mini pipeline progress for each issue row", async () => {
    fetchMock.mockImplementation(
      mockIssuesAndAgents(
        [
          {
            id: "issue-progress",
            issue_number: 56,
            title: "Show progress",
            state: "open",
            origin: "local",
            kind: "issue",
            owner_agent_id: null,
            work_status: "review",
            last_activity_at: "2026-02-06T05:00:00Z",
          } satisfies MockIssue,
        ],
        [],
      ),
    );

    render(<ProjectIssuesList projectId="project-1" />);

    const row = await screen.findByRole("button", { name: /#56 Show progress/i });
    expect(within(row).getByLabelText("Issue progress")).toBeInTheDocument();
    expect(within(row).getByTestId("mini-stage-review")).toHaveAttribute("data-stage-state", "current");
  });
});
