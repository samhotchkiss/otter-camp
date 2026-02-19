import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import IssueDetailPage from "./IssueDetailPage";

vi.mock("../components/project/IssueThreadPanel", () => ({
  default: ({ issueID, projectID }: { issueID: string; projectID?: string }) => (
    <div data-testid="issue-thread-panel">
      panel:{projectID ?? "none"}:{issueID}
    </div>
  ),
}));

function mockJSONResponse(body: unknown, ok = true): Response {
  return {
    ok,
    json: async () => body,
  } as Response;
}

function renderIssueDetailPage(path = "/projects/project-2/issues/issue-209") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/projects/:id/issues/:issueId" element={<IssueDetailPage />} />
        <Route path="/issue/:issueId" element={<IssueDetailPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("IssueDetailPage", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("loads issue data and renders API-backed title and context", async () => {
    fetchMock.mockResolvedValueOnce(
      mockJSONResponse({
        issue: {
          id: "issue-209",
          issue_number: 209,
          title: "Fix API rate limiting",
          approval_state: "ready_for_review",
          project_id: "project-2",
        },
      }),
    );

    renderIssueDetailPage();

    expect(await screen.findByRole("heading", { level: 1, name: "Fix API rate limiting" })).toBeInTheDocument();
    expect(screen.getByText("Issue #209")).toBeInTheDocument();
    expect(screen.getByText("Ready for Review")).toBeInTheDocument();
    expect(screen.getByText("project-2")).toBeInTheDocument();
    expect(screen.getByTestId("issue-thread-panel")).toHaveTextContent("panel:project-2:issue-209");
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/issues/issue-209?org_id=org-123"),
      expect.any(Object),
    );
  });

  it("sends approve action and surfaces success feedback", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          issue: {
            id: "issue-209",
            issue_number: 209,
            title: "Fix API rate limiting",
            approval_state: "ready_for_review",
            project_id: "project-2",
          },
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ success: true }));

    renderIssueDetailPage();
    await screen.findByRole("heading", { level: 1, name: "Fix API rate limiting" });

    await user.click(screen.getByRole("button", { name: "Approve" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Issue approved.");
    await waitFor(() => {
      expect(fetchMock).toHaveBeenNthCalledWith(
        2,
        expect.stringContaining("/api/issues/issue-209/approve?org_id=org-123"),
        expect.objectContaining({ method: "POST" }),
      );
    });
  });

  it("renders fetch error state and retries loading issue details", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(mockJSONResponse({ error: "Issue load failed" }, false))
      .mockResolvedValueOnce(
        mockJSONResponse({
          issue: {
            id: "issue-209",
            issue_number: 209,
            title: "Recovered issue context",
            approval_state: "ready_for_review",
            project_id: "project-2",
          },
        }),
      );

    renderIssueDetailPage();

    expect(await screen.findByText("Issue load failed")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByRole("heading", { level: 1, name: "Recovered issue context" })).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("sends needs_changes approval action and surfaces success feedback", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          issue: {
            id: "issue-209",
            issue_number: 209,
            title: "Fix API rate limiting",
            approval_state: "ready_for_review",
            project_id: "project-2",
          },
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ success: true }));

    renderIssueDetailPage();
    await screen.findByRole("heading", { level: 1, name: "Fix API rate limiting" });

    await user.click(screen.getByRole("button", { name: "Request Changes" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Changes requested.");
    await waitFor(() => {
      expect(fetchMock).toHaveBeenNthCalledWith(
        2,
        expect.stringContaining("/api/issues/issue-209/approval-state?org_id=org-123"),
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ approval_state: "needs_changes" }),
        }),
      );
    });
  });
});
