import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";

import ContentReviewPage from "./ContentReviewPage";

function mockJSONResponse(body: unknown, ok = true): Response {
  return {
    ok,
    json: async () => body,
  } as Response;
}

function renderRoute(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/review/:documentId" element={<ContentReviewPage />} />
      </Routes>
    </MemoryRouter>
  );
}

describe("ContentReviewPage", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("renders the redesigned route shell and decodes alias paths", () => {
    renderRoute("/review/docs%2Fguides%2Fapi%20spec.md");

    expect(screen.getByTestId("content-review-page-shell")).toBeInTheDocument();
    expect(screen.getByTestId("content-review-page-shell")).toHaveClass("min-w-0");
    expect(screen.getByTestId("content-review-route-header")).toBeInTheDocument();
    expect(screen.getByTestId("content-review-route-header")).toHaveClass("flex-col");
    expect(screen.getByTestId("content-review-route-header")).toHaveClass("sm:flex-row");
    expect(screen.getByRole("heading", { name: "Content Review" })).toBeInTheDocument();
    expect(screen.getByTestId("content-review-route-path")).toHaveTextContent("docs/guides/api spec.md");
    expect(screen.getByTestId("content-review-page-shell")).toHaveAttribute("aria-labelledby", "content-review-page-title");
    expect(screen.getByTestId("content-review-shell")).toBeInTheDocument();

    const sourceTextarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
    expect(sourceTextarea.value).toContain("# Review: docs/guides/api spec.md");
  });

  it("loads linked issue review context when issue_id is provided", async () => {
    fetchMock.mockResolvedValueOnce(
      mockJSONResponse({
        issue: {
          id: "issue-209",
          project_id: "project-2",
          document_path: "/posts/rate-limiting-implementation.md",
          document_content: "# Live review draft\n\n- wired from API",
          approval_state: "ready_for_review",
        },
        comments: [{ id: "c1" }, { id: "c2" }],
        participants: [{ agent_id: "agent-1" }],
      }),
    );

    renderRoute("/review/posts%2Frate-limiting-implementation.md?project_id=project-2&issue_id=issue-209");

    expect(await screen.findByTestId("content-review-linked-issue")).toHaveTextContent("Linked issue: issue-209");
    expect(screen.getByTestId("content-review-linked-issue")).toHaveTextContent("Project: project-2");
    expect(screen.getByTestId("content-review-linked-issue")).toHaveTextContent("State: ready_for_review");
    expect(screen.getByTestId("content-review-linked-issue")).toHaveTextContent("Comments: 2");
    expect(screen.getByTestId("content-review-route-path")).toHaveTextContent("posts/rate-limiting-implementation.md");
    await waitFor(() => {
      const sourceTextarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
      expect(sourceTextarea.value).toContain("# Live review draft");
    });
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/issues/issue-209?org_id=org-123"),
      expect.any(Object),
    );
  });

  it("submits approve action through linked issue review context", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          issue: {
            id: "issue-209",
            project_id: "project-2",
            document_path: "/posts/rate-limiting-implementation.md",
            document_content: "# Review",
            approval_state: "draft",
          },
          comments: [],
          participants: [{ agent_id: "agent-1" }],
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ success: true }));

    renderRoute("/review/posts%2Frate-limiting-implementation.md?project_id=project-2&issue_id=issue-209");
    await screen.findByTestId("content-review-linked-issue");

    await user.click(screen.getByRole("button", { name: "Mark Ready for Review" }));
    await user.click(screen.getByRole("button", { name: "Approve Content" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Issue approved.");
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      expect.stringContaining("/api/issues/issue-209/approve?org_id=org-123"),
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("submits request-changes action through linked issue review context", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          issue: {
            id: "issue-209",
            project_id: "project-2",
            document_path: "/posts/rate-limiting-implementation.md",
            document_content: "# Review",
            approval_state: "ready_for_review",
          },
          comments: [{ id: "c1" }, { id: "c2" }],
          participants: [{ agent_id: "agent-1" }],
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ success: true }));

    renderRoute("/review/posts%2Frate-limiting-implementation.md?project_id=project-2&issue_id=issue-209");
    await screen.findByTestId("content-review-linked-issue");

    await user.click(screen.getByRole("button", { name: "Mark Ready for Review" }));
    await user.click(screen.getByRole("button", { name: "Request Changes" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Changes requested.");
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      expect.stringContaining("/api/issues/issue-209/approval-state?org_id=org-123"),
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ approval_state: "needs_changes" }),
      }),
    );
  });

  it("persists new inline comments and increments linked comment count", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          issue: {
            id: "issue-209",
            project_id: "project-2",
            document_path: "/posts/rate-limiting-implementation.md",
            document_content: "# Review",
            approval_state: "ready_for_review",
          },
          comments: [{ id: "c1" }, { id: "c2" }],
          participants: [{ agent_id: "agent-1" }],
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ id: "comment-3" }));

    renderRoute("/review/posts%2Frate-limiting-implementation.md?project_id=project-2&issue_id=issue-209");

    expect(await screen.findByTestId("content-review-linked-issue")).toHaveTextContent("Comments: 2");

    await user.click(screen.getByRole("button", { name: "Add Inline Comment" }));
    await user.type(screen.getByTestId("inline-comment-input"), "Needs tighter phrasing");
    await user.click(screen.getByRole("button", { name: "Insert Inline Comment" }));

    expect(await screen.findByTestId("content-review-linked-issue")).toHaveTextContent("Comments: 3");
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      expect.stringContaining("/api/issues/issue-209/comments?org_id=org-123"),
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("surfaces linked review context API errors", async () => {
    fetchMock.mockResolvedValueOnce(mockJSONResponse({ error: "linked context failed" }, false));

    renderRoute("/review/posts%2Frate-limiting-implementation.md?project_id=project-2&issue_id=issue-209");

    expect(await screen.findByRole("alert")).toHaveTextContent("linked context failed");
  });

  it("shows no-org error and skips linked context fetch", async () => {
    localStorage.removeItem("otter-camp-org-id");

    renderRoute("/review/posts%2Frate-limiting-implementation.md?project_id=project-2&issue_id=issue-209");

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Set an organization to load linked review context.",
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("falls back to untitled path when alias route document is empty after trim", () => {
    renderRoute("/review/%20");

    expect(screen.getByTestId("content-review-route-path")).toHaveTextContent("untitled.md");
    const sourceTextarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
    expect(sourceTextarea.value).toContain("# Review: untitled.md");
  });

  it("keeps malformed encoded route segments stable without crashing", () => {
    renderRoute("/review/%E0%A4%A.md");

    expect(screen.getByTestId("content-review-route-path")).toHaveTextContent("%E0%A4%A.md");
    const sourceTextarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
    expect(sourceTextarea.value).toContain("# Review: %E0%A4%A.md");
  });
});
