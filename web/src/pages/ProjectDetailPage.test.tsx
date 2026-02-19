import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ProjectDetailPage from "./ProjectDetailPage";

const { projectMock, issuesMock } = vi.hoisted(() => ({
  projectMock: vi.fn(),
  issuesMock: vi.fn(),
}));

vi.mock("../lib/api", () => ({
  default: {
    project: projectMock,
    issues: issuesMock,
  },
}));

const PROJECT_PAYLOAD = {
  id: "project-2",
  name: "API Gateway",
  description:
    "Core API gateway handling authentication, rate limiting, and request routing for all services",
  repo_url: "https://github.com/company/api-gateway",
  taskCount: 9,
  completedCount: 4,
  updated_at: "2026-02-18T20:00:00Z",
};

const ISSUES_PAYLOAD = {
  items: [
    {
      id: "issue-209",
      issue_number: 209,
      title: "Fix API rate limiting",
      approval_state: "ready_for_review",
      priority: "P1",
      owner_agent_id: "Agent-007",
      last_activity_at: "2026-02-18T20:01:00Z",
      state: "open",
      origin: "local",
      kind: "issue",
      project_id: "project-2",
    },
  ],
  total: 1,
};

function renderProjectDetailPage(initialEntry = "/projects/project-2") {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <Routes>
        <Route path="/projects/:id" element={<ProjectDetailPage />} />
        <Route path="/issue/:issueId" element={<div data-testid="issue-detail-route" />} />
        <Route path="/review/:documentId" element={<div data-testid="content-review-route" />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ProjectDetailPage", () => {
  beforeEach(() => {
    projectMock.mockReset();
    issuesMock.mockReset();
    projectMock.mockResolvedValue(PROJECT_PAYLOAD);
    issuesMock.mockResolvedValue(ISSUES_PAYLOAD);
  });

  it("renders API-driven project metadata scaffold", async () => {
    renderProjectDetailPage();

    expect(await screen.findByRole("heading", { level: 1, name: "API Gateway" })).toBeInTheDocument();
    expect(screen.getByText(/Core API gateway handling authentication/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /github\.com\/company\/api-gateway/i })).toBeInTheDocument();
    expect(projectMock).toHaveBeenCalledWith("project-2");
    expect(issuesMock).toHaveBeenCalledWith({ projectID: "project-2", state: "open", limit: 200 });
  });

  it("renders mapped project stats and open issues list", async () => {
    renderProjectDetailPage();

    expect(await screen.findByText("Issues")).toBeInTheDocument();
    expect(screen.getByText("Branches")).toBeInTheDocument();
    expect(screen.getByText("Commits")).toBeInTheDocument();
    expect(screen.getByText("Contributors")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Open Issues" })).toBeInTheDocument();
    expect(screen.getByText("Fix API rate limiting")).toBeInTheDocument();
    expect(screen.getByText("approval needed")).toBeInTheDocument();
  });

  it("navigates to issue detail when an issue row is clicked", async () => {
    const user = userEvent.setup();
    renderProjectDetailPage();

    await user.click(await screen.findByRole("link", { name: /Fix API rate limiting/i }));

    expect(screen.getByTestId("issue-detail-route")).toBeInTheDocument();
  });

  it("keeps activity and file explorer in right rail position", async () => {
    renderProjectDetailPage();

    await screen.findByRole("heading", { name: "API Gateway" });
    const rightRail = screen.getByTestId("project-detail-right-rail");
    expect(rightRail).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Recent Activity" })).toBeInTheDocument();
    expect(screen.getByTestId("project-detail-file-explorer")).toBeInTheDocument();
    expect(screen.getByText("README.md")).toBeInTheDocument();
  });

  it("routes markdown review links from the static file explorer", async () => {
    const user = userEvent.setup();
    renderProjectDetailPage();

    await user.click(await screen.findByRole("link", { name: /README\.md/i }));

    expect(screen.getByTestId("content-review-route")).toBeInTheDocument();
  });

  it("shows loading state while project detail requests are pending", () => {
    projectMock.mockReturnValue(new Promise(() => {}));
    issuesMock.mockReturnValue(new Promise(() => {}));

    renderProjectDetailPage();

    expect(screen.getByText("Loading issues...")).toBeInTheDocument();
  });

  it("shows error state and retries project detail loading", async () => {
    projectMock.mockRejectedValueOnce(new Error("project detail failed"));
    projectMock.mockResolvedValueOnce(PROJECT_PAYLOAD);

    renderProjectDetailPage();

    expect(await screen.findByText("project detail failed")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("heading", { level: 1, name: "API Gateway" })).toBeInTheDocument();
  });
});
