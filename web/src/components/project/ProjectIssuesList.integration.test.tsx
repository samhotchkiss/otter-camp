import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ProjectIssuesList from "./ProjectIssuesList";

function mockJSONResponse(body: unknown, ok = true) {
  return {
    ok,
    json: async () => body,
  } as Response;
}

function ProjectIssuesRoute() {
  const navigate = useNavigate();
  return (
    <ProjectIssuesList
      projectId="project-1"
      onSelectIssue={(issueID) => navigate(`/projects/project-1/issues/${issueID}`)}
    />
  );
}

describe("ProjectIssuesList navigation integration", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("navigates to issue thread route when selecting an issue row", async () => {
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          items: [
            {
              id: "issue-33",
              issue_number: 33,
              title: "Navigate me",
              state: "open",
              origin: "local",
              kind: "issue",
              owner_agent_id: "sam",
              last_activity_at: "2026-02-06T07:00:00Z",
            },
          ],
          total: 1,
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ agents: [] }));

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectIssuesRoute />} />
          <Route path="/projects/:id/issues/:issueId" element={<div>Issue Thread View</div>} />
        </Routes>
      </MemoryRouter>,
    );

    fireEvent.click(await screen.findByRole("button", { name: /#33 Navigate me/i }));
    expect(await screen.findByText("Issue Thread View")).toBeInTheDocument();
  });

  it("applies responsive overflow classes to filters and issue rows", async () => {
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          items: [
            {
              id: "issue-44",
              issue_number: 44,
              title: "Long issue title row for responsive overflow assertions",
              state: "open",
              origin: "local",
              kind: "issue",
              owner_agent_id: "sam",
              last_activity_at: "2026-02-06T07:00:00Z",
            },
          ],
          total: 1,
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ agents: [] }));

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectIssuesRoute />} />
        </Routes>
      </MemoryRouter>,
    );

    const shell = await screen.findByTestId("project-issues-shell");
    expect(shell).toHaveClass("min-w-0");

    const stateFilter = screen.getByLabelText("Issue state filter");
    const filterRow = stateFilter.closest("div")?.parentElement;
    expect(filterRow).toHaveClass("flex-col");
    expect(filterRow).toHaveClass("sm:flex-row");

    const issueRow = screen.getByRole("button", { name: /#44 Long issue title row/i });
    expect(issueRow).toHaveClass("min-w-0");

    const issueTitle = screen.getByText(/Long issue title row/i);
    expect(issueTitle).toHaveClass("break-words");
  });
});
