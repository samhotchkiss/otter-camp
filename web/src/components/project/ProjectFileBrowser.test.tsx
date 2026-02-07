import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ProjectFileBrowser from "./ProjectFileBrowser";

const navigateMock = vi.fn();

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return {
    ...actual,
    useNavigate: () => navigateMock,
  };
});

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
    navigateMock.mockReset();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("renders markdown with render/source toggle", async () => {
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

    expect(await screen.findByTestId("file-markdown-render")).toBeInTheDocument();
    expect(screen.getByText("Hello from README")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Source" }));
    expect(await screen.findByTestId("file-markdown-source")).toHaveTextContent("# Hello from README");
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

  it("renders syntax highlighted preview for code files", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          ref: "main",
          path: "/",
          entries: [{ name: "main.ts", type: "file", path: "main.ts", size: 16 }],
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          ref: "main",
          path: "/main.ts",
          content: "const answer = 42;",
          size: 18,
          encoding: "utf-8",
        }),
      );

    render(<ProjectFileBrowser projectId="project-1" />);
    await user.click(await screen.findByRole("button", { name: /main\.ts/i }));
    expect(await screen.findByTestId("file-code-preview")).toBeInTheDocument();
  });

  it("renders image preview for base64 image blobs", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          ref: "main",
          path: "/",
          entries: [{ name: "cover.png", type: "file", path: "cover.png", size: 92 }],
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          ref: "main",
          path: "/cover.png",
          content: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR4nGNgYAAAAAMAASsJTYQAAAAASUVORK5CYII=",
          size: 92,
          encoding: "base64",
        }),
      );

    render(<ProjectFileBrowser projectId="project-1" />);
    await user.click(await screen.findByRole("button", { name: /cover\.png/i }));
    const preview = await screen.findByTestId("file-image-preview");
    expect(preview).toHaveAttribute("src", expect.stringContaining("data:image/png;base64,"));
  });

  it("shows a safe fallback for mismatched payload/file mode", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          ref: "main",
          path: "/",
          entries: [{ name: "draft.md", type: "file", path: "draft.md", size: 24 }],
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          ref: "main",
          path: "/draft.md",
          content: "SGVsbG8=",
          size: 8,
          encoding: "base64",
        }),
      );

    render(<ProjectFileBrowser projectId="project-1" />);
    await user.click(await screen.findByRole("button", { name: /draft\.md/i }));
    expect(await screen.findByText("Unable to render file preview for this payload.")).toBeInTheDocument();
  });

  it("creates a linked issue from eligible files and navigates to the issue thread", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          ref: "main",
          path: "/",
          entries: [{ name: "2026-02-07-post.md", type: "file", path: "posts/2026-02-07-post.md", size: 42 }],
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          ref: "main",
          path: "/posts/2026-02-07-post.md",
          content: "# Draft",
          size: 7,
          encoding: "utf-8",
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "issue-123",
          issue_number: 12,
          title: "Review: 2026 02 07 post",
        }),
      );

    render(<ProjectFileBrowser projectId="project-1" />);
    await user.click(await screen.findByRole("button", { name: /2026-02-07-post\.md/i }));
    await user.click(await screen.findByRole("button", { name: "Create issue for this file" }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/api/projects/project-1/issues/link?"),
        expect.objectContaining({ method: "POST" }),
      );
      expect(navigateMock).toHaveBeenCalledWith("/projects/project-1/issues/issue-123");
    });
  });

  it("shows an actionable error when linked issue creation fails", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          ref: "main",
          path: "/",
          entries: [{ name: "2026-02-07-post.md", type: "file", path: "posts/2026-02-07-post.md", size: 42 }],
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          ref: "main",
          path: "/posts/2026-02-07-post.md",
          content: "# Draft",
          size: 7,
          encoding: "utf-8",
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ error: "document_path must point to /posts/*.md" }, false));

    render(<ProjectFileBrowser projectId="project-1" />);
    await user.click(await screen.findByRole("button", { name: /2026-02-07-post\.md/i }));
    await user.click(await screen.findByRole("button", { name: "Create issue for this file" }));

    expect(await screen.findByText("document_path must point to /posts/*.md")).toBeInTheDocument();
    expect(navigateMock).not.toHaveBeenCalled();
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
