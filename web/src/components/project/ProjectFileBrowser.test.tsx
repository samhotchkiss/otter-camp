import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ProjectFileBrowser from "./ProjectFileBrowser";

vi.mock("./ProjectCommitBrowser", () => ({
  default: ({ projectId }: { projectId: string }) => (
    <div data-testid="project-commit-browser">Commit browser for {projectId}</div>
  ),
}));

function mockJSONResponse(body: unknown, ok = true): Response {
  return {
    ok,
    json: async () => body,
  } as Response;
}

describe("ProjectFileBrowser", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("fetches tree and blob content when selecting a file", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          ref: "main",
          path: "/",
          entries: [
            { name: "posts", type: "dir", path: "posts/" },
            { name: "README.md", type: "file", path: "README.md", size: 24 },
          ],
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          ref: "main",
          path: "/README.md",
          content: "# Hello from README",
          size: 18,
          encoding: "utf-8",
        }),
      );

    render(<ProjectFileBrowser projectId="project-1" />);

    const readmeButton = await screen.findByRole("button", { name: /README\.md/i });
    await user.click(readmeButton);

    expect(await screen.findByText("# Hello from README")).toBeInTheDocument();
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/api/projects/project-1/tree?"),
        expect.any(Object),
      );
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/api/projects/project-1/blob?"),
        expect.any(Object),
      );
    });
  });

  it("surfaces tree fetch errors and retries", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(mockJSONResponse({ error: "tree load failed" }, false))
      .mockResolvedValueOnce(
        mockJSONResponse({
          ref: "main",
          path: "/",
          entries: [{ name: "notes", type: "dir", path: "notes/" }],
        }),
      );

    render(<ProjectFileBrowser projectId="project-1" />);

    expect(await screen.findByText("tree load failed")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("button", { name: /notes/i })).toBeInTheDocument();
  });

  it("toggles to commit history view", async () => {
    const user = userEvent.setup();
    fetchMock.mockResolvedValueOnce(
      mockJSONResponse({
        ref: "main",
        path: "/",
        entries: [],
      }),
    );

    render(<ProjectFileBrowser projectId="project-1" />);
    expect(await screen.findByText("No files found.")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Commit history" }));
    expect(await screen.findByTestId("project-commit-browser")).toBeInTheDocument();
  });
});
