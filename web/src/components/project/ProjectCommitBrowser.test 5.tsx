import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ProjectCommitBrowser, { EMPTY_BODY_FALLBACK } from "./ProjectCommitBrowser";

function mockJSONResponse(body: unknown, ok = true): Response {
  return {
    ok,
    json: async () => body,
  } as Response;
}

describe("ProjectCommitBrowser", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("renders commit list metadata from API", async () => {
    fetchMock.mockResolvedValueOnce(
      mockJSONResponse({
        items: [
          {
            id: "commit-1",
            project_id: "project-1",
            repository_full_name: "samhotchkiss/otter-camp",
            branch_name: "main",
            sha: "abcdef1234567890",
            author_name: "Sam",
            authored_at: "2026-02-06T12:30:00Z",
            subject: "Refactor parser",
            message: "Refactor parser",
            created_at: "2026-02-06T12:30:00Z",
            updated_at: "2026-02-06T12:30:00Z",
          },
        ],
        has_more: false,
        limit: 50,
        offset: 0,
        total: 1,
      }),
    );

    render(<ProjectCommitBrowser projectId="project-1" />);

    expect(await screen.findByText("Refactor parser")).toBeInTheDocument();
    expect(screen.getByText("main")).toBeInTheDocument();
    expect(screen.getByText("Author: Sam")).toBeInTheDocument();
    expect(screen.getByText("abcdef1")).toBeInTheDocument();

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/api/projects/project-1/commits?"),
        expect.any(Object),
      );
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("org_id=org-123"),
        expect.any(Object),
      );
    });
  });

  it("expands a commit row and shows the verbose commit body", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          items: [
            {
              id: "commit-1",
              project_id: "project-1",
              repository_full_name: "samhotchkiss/otter-camp",
              branch_name: "main",
              sha: "abcdef1234567890",
              author_name: "Sam",
              authored_at: "2026-02-06T12:30:00Z",
              subject: "Refactor parser",
              body: null,
              message: "Refactor parser",
              created_at: "2026-02-06T12:30:00Z",
              updated_at: "2026-02-06T12:30:00Z",
            },
          ],
          has_more: false,
          limit: 50,
          offset: 0,
          total: 1,
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "commit-1",
          project_id: "project-1",
          repository_full_name: "samhotchkiss/otter-camp",
          branch_name: "main",
          sha: "abcdef1234567890",
          author_name: "Sam",
          authored_at: "2026-02-06T12:30:00Z",
          subject: "Refactor parser",
          body: "This commit rewires parser state transitions and adds stricter validation.",
          message: "Refactor parser",
          created_at: "2026-02-06T12:30:00Z",
          updated_at: "2026-02-06T12:30:00Z",
        }),
      );

    render(<ProjectCommitBrowser projectId="project-1" />);

    await screen.findByText("Refactor parser");
    await user.click(screen.getByTestId("commit-expand-abcdef1234567890"));

    expect(
      await screen.findByText(
        "This commit rewires parser state transitions and adds stricter validation.",
      ),
    ).toBeInTheDocument();
  });

  it("shows fallback copy when commit body is empty", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          items: [
            {
              id: "commit-1",
              project_id: "project-1",
              repository_full_name: "samhotchkiss/otter-camp",
              branch_name: "main",
              sha: "abcdef1234567890",
              author_name: "Sam",
              authored_at: "2026-02-06T12:30:00Z",
              subject: "Refactor parser",
              body: null,
              message: "Refactor parser",
              created_at: "2026-02-06T12:30:00Z",
              updated_at: "2026-02-06T12:30:00Z",
            },
          ],
          has_more: false,
          limit: 50,
          offset: 0,
          total: 1,
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "commit-1",
          project_id: "project-1",
          repository_full_name: "samhotchkiss/otter-camp",
          branch_name: "main",
          sha: "abcdef1234567890",
          author_name: "Sam",
          authored_at: "2026-02-06T12:30:00Z",
          subject: "Refactor parser",
          body: "   ",
          message: "Refactor parser",
          created_at: "2026-02-06T12:30:00Z",
          updated_at: "2026-02-06T12:30:00Z",
        }),
      );

    render(<ProjectCommitBrowser projectId="project-1" />);

    await screen.findByText("Refactor parser");
    await user.click(screen.getByTestId("commit-expand-abcdef1234567890"));

    expect(await screen.findByText(EMPTY_BODY_FALLBACK)).toBeInTheDocument();
  });

  it("fetches diff panel content and surfaces API errors with retry", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          items: [
            {
              id: "commit-1",
              project_id: "project-1",
              repository_full_name: "samhotchkiss/otter-camp",
              branch_name: "main",
              sha: "abcdef1234567890",
              author_name: "Sam",
              authored_at: "2026-02-06T12:30:00Z",
              subject: "Refactor parser",
              body: "Body",
              message: "Refactor parser",
              created_at: "2026-02-06T12:30:00Z",
              updated_at: "2026-02-06T12:30:00Z",
            },
          ],
          has_more: false,
          limit: 50,
          offset: 0,
          total: 1,
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "commit-1",
          project_id: "project-1",
          repository_full_name: "samhotchkiss/otter-camp",
          branch_name: "main",
          sha: "abcdef1234567890",
          author_name: "Sam",
          authored_at: "2026-02-06T12:30:00Z",
          subject: "Refactor parser",
          body: "Body",
          message: "Refactor parser",
          created_at: "2026-02-06T12:30:00Z",
          updated_at: "2026-02-06T12:30:00Z",
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ error: "diff failed" }, false))
      .mockResolvedValueOnce(
        mockJSONResponse({
          sha: "abcdef1234567890",
          total: 1,
          files: [
            {
              path: "src/parser.ts",
              change_type: "modified",
              patch: "@@ -1 +1 @@\n-old\n+new",
            },
          ],
        }),
      );

    render(<ProjectCommitBrowser projectId="project-1" />);

    await screen.findByText("Refactor parser");
    await user.click(screen.getByTestId("commit-expand-abcdef1234567890"));
    await user.click(screen.getByTestId("commit-diff-toggle-abcdef1234567890"));

    expect(await screen.findByText("diff failed")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Retry diff" }));
    expect(await screen.findByText("src/parser.ts")).toBeInTheDocument();
    expect(screen.getByText("+new")).toBeInTheDocument();
  });
});
