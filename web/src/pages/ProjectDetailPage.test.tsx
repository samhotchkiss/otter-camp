import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ProjectDetailPage from "./ProjectDetailPage";

vi.mock("../components/project/ProjectFileBrowser", () => ({
  default: ({ projectId }: { projectId: string }) => (
    <div data-testid="project-file-browser-mock">File browser: {projectId}</div>
  ),
}));

vi.mock("../components/project/ProjectChatPanel", () => ({
  default: () => <div data-testid="project-chat-panel-mock" />,
}));

vi.mock("../components/project/ProjectIssuesList", () => ({
  default: () => <div data-testid="project-issues-list-mock" />,
}));

vi.mock("../components/project/IssueThreadPanel", () => ({
  default: () => <div data-testid="issue-thread-panel-mock" />,
}));

vi.mock("../contexts/GlobalChatContext", () => ({
  useGlobalChat: () => ({
    upsertConversation: vi.fn(),
    openConversation: vi.fn(),
  }),
}));

function mockJSONResponse(body: unknown, ok = true): Response {
  return {
    ok,
    json: async () => body,
  } as Response;
}

describe("ProjectDetailPage files tab", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("shows Files tab and renders ProjectFileBrowser when selected", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "project-1",
          name: "Technonymous",
          status: "active",
          description: "Long-form writing",
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ agents: [] }))
      .mockResolvedValueOnce(mockJSONResponse({ tasks: [] }))
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }));

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Files" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Code" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Files" }));
    expect(await screen.findByTestId("project-file-browser-mock")).toBeInTheDocument();
  });
});
