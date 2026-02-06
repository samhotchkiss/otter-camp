import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import ProjectIssuesList from "./ProjectIssuesList";

type MockIssue = {
  id: string;
  issue_number: number;
  title: string;
  state: "open" | "closed";
  origin: "local" | "github";
  kind: "issue" | "pull_request";
  approval_state?: "draft" | "ready_for_review" | "needs_changes" | "approved" | null;
  owner_agent_id?: string | null;
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
    fetchMock.mockResolvedValueOnce(
      mockJSONResponse({
        items: [
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
        total: 1,
      }),
    );

    render(<ProjectIssuesList projectId="project-1" />);

    const row = await screen.findByRole("button", { name: /#77 Imported bug/i });
    expect(within(row).getByText("Issue")).toBeInTheDocument();
    expect(within(row).getByText("GitHub")).toBeInTheDocument();
    expect(within(row).getByText("Open", { selector: "span" })).toBeInTheDocument();
    expect(within(row).getByTestId("issue-approval-issue-1")).toHaveTextContent("Ready for Review");
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

  it("renders loading and empty states", async () => {
    let resolveFetch: ((value: Response) => void) | null = null;
    fetchMock.mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          resolveFetch = resolve;
        }),
    );

    render(<ProjectIssuesList projectId="project-1" />);
    expect(screen.getByText("Loading issues...")).toBeInTheDocument();

    expect(resolveFetch).not.toBeNull();
    resolveFetch!(mockJSONResponse({ items: [], total: 0 }));

    expect(
      await screen.findByText("No issues found for the selected filters."),
    ).toBeInTheDocument();
  });

  it("renders error state and retries", async () => {
    fetchMock
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValueOnce(mockJSONResponse({ items: [], total: 0 }));

    render(<ProjectIssuesList projectId="project-1" />);

    expect(await screen.findByText("boom")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(
      await screen.findByText("No issues found for the selected filters."),
    ).toBeInTheDocument();
  });
});
