import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ProjectsPage from "./ProjectsPage";

const { projectsMock } = vi.hoisted(() => ({
  projectsMock: vi.fn(),
}));

vi.mock("../lib/api", () => ({
  default: {
    projects: projectsMock,
  },
}));

const DEFAULT_PROJECTS_PAYLOAD = {
  projects: [
    {
      id: "project-1",
      name: "Customer Portal",
      repo_url: "https://github.com/ottercamp/customer-portal.git",
      taskCount: 12,
      completedCount: 5,
      in_progress: 4,
      needs_approval: 2,
      tech_stack: ["React", "Tailwind"],
    },
    {
      id: "project-2",
      name: "Internal Tools",
      repo_url: "",
      taskCount: 2,
      completedCount: 2,
      tech_stack: ["Python"],
    },
  ],
};

function renderProjectsPage() {
  return render(
    <MemoryRouter initialEntries={["/projects"]}>
      <Routes>
        <Route path="/projects" element={<ProjectsPage />} />
        <Route path="/projects/:id" element={<div data-testid="project-detail-route" />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ProjectsPage", () => {
  beforeEach(() => {
    projectsMock.mockReset();
    projectsMock.mockResolvedValue(DEFAULT_PROJECTS_PAYLOAD);
  });

  it("renders API-driven projects scaffold", async () => {
    renderProjectsPage();

    expect(await screen.findByRole("heading", { name: "Projects" })).toBeInTheDocument();
    expect(screen.getByText("Git-backed repositories & tracking")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New Project" })).toBeInTheDocument();
    expect(projectsMock).toHaveBeenCalledTimes(1);
  });

  it("renders mapped project cards and metric chips", async () => {
    renderProjectsPage();

    const customerPortalCard = await screen.findByTestId("project-card-project-1");
    expect(customerPortalCard).toBeInTheDocument();
    expect(screen.getByText("Customer Portal")).toBeInTheDocument();
    expect(screen.getByText("github.com/ottercamp/customer-portal")).toBeInTheDocument();
    expect(screen.getAllByText("Open")[0]).toBeInTheDocument();
    expect(screen.getAllByText("Active")[0]).toBeInTheDocument();
    expect(screen.getAllByText("Review")[0]).toBeInTheDocument();
    expect(screen.getAllByText("Synced").length).toBeGreaterThan(0);
    expect(screen.getByText("Local")).toBeInTheDocument();
  });

  it("navigates to project detail route when a project card is clicked", async () => {
    const user = userEvent.setup();
    renderProjectsPage();

    await user.click(await screen.findByRole("link", { name: /Customer Portal/i }));

    expect(screen.getByTestId("project-detail-route")).toBeInTheDocument();
  });

  it("shows loading state while project request is pending", () => {
    projectsMock.mockReturnValue(new Promise(() => {}));
    renderProjectsPage();

    expect(screen.getByText("Loading projects...")).toBeInTheDocument();
  });

  it("shows error state and retries project loading", async () => {
    projectsMock.mockRejectedValueOnce(new Error("projects failed"));
    projectsMock.mockResolvedValueOnce({ projects: [] });

    renderProjectsPage();

    expect(await screen.findByText("projects failed")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("No projects found.")).toBeInTheDocument();
    expect(projectsMock).toHaveBeenCalledTimes(2);
  });

  it("renders API-driven recent activity rows", async () => {
    renderProjectsPage();

    expect(await screen.findByRole("heading", { name: "Recent Activity" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "View all recent activity" })).toBeInTheDocument();
    expect(screen.getByText("2 item(s) awaiting review")).toBeInTheDocument();
    expect(screen.getByText("0 open issue(s) queued")).toBeInTheDocument();
    expect(screen.getAllByText("needs approval").length).toBeGreaterThan(0);
  });

  it("renders todo activity rows when no projects need approval or active work", async () => {
    projectsMock.mockResolvedValueOnce({
      projects: [
        {
          id: "project-a",
          name: "Planning Board",
          repo_url: "https://github.com/ottercamp/planning-board.git",
          taskCount: 5,
          completedCount: 5,
          in_progress: 0,
          needs_approval: 0,
          tech_stack: ["TypeScript"],
        },
        {
          id: "project-b",
          name: "Content Hub",
          repo_url: "https://github.com/ottercamp/content-hub.git",
          taskCount: 3,
          completedCount: 3,
          in_progress: 0,
          needs_approval: 0,
          tech_stack: ["Go"],
        },
      ],
    });

    renderProjectsPage();

    expect(await screen.findByRole("heading", { name: "Recent Activity" })).toBeInTheDocument();
    expect(screen.getAllByText("0 open issue(s) queued")).toHaveLength(2);
    expect(screen.getAllByText("todo")).toHaveLength(2);
  });
});
