import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ProjectsPage from "./ProjectsPage";

vi.mock("../lib/demo", () => ({
  isDemoMode: () => false,
}));

describe("ProjectsPage", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("visually distinguishes archived projects", async () => {
    const fetchMock = vi.fn(async () =>
      new Response(
        JSON.stringify({
          projects: [
            {
              id: "active-1",
              name: "Active Project",
              status: "active",
              taskCount: 4,
              completedCount: 2,
            },
            {
              id: "archived-1",
              name: "Archived Project",
              status: "archived",
              taskCount: 3,
              completedCount: 3,
            },
          ],
        }),
        { status: 200 },
      ),
    );
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter>
        <ProjectsPage apiEndpoint="/api/projects-test" />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Archived Project")).toBeInTheDocument();
    expect(screen.getByText("Archived")).toBeInTheDocument();
    const archivedCard = screen.getByTestId("project-card-archived-1");
    expect(archivedCard.className).toContain("opacity-70");
  });

  it("renders visible progress bars with accessible values", async () => {
    const fetchMock = vi.fn(async () =>
      new Response(
        JSON.stringify({
          projects: [
            {
              id: "progress-1",
              name: "Progress Project",
              status: "active",
              taskCount: 8,
              completedCount: 4,
            },
            {
              id: "progress-2",
              name: "No Progress Project",
              status: "active",
              taskCount: 5,
              completedCount: 0,
            },
          ],
        }),
        { status: 200 },
      ),
    );
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter>
        <ProjectsPage apiEndpoint="/api/projects-test" />
      </MemoryRouter>,
    );

    await screen.findByText("Progress Project");

    const progressBar = screen.getByRole("progressbar", { name: "Progress for Progress Project" });
    expect(progressBar).toHaveAttribute("aria-valuenow", "50");

    const zeroProgressBar = screen.getByRole("progressbar", { name: "Progress for No Progress Project" });
    expect(zeroProgressBar).toHaveAttribute("aria-valuenow", "0");
    expect(zeroProgressBar.className).toContain("border-[var(--border)]");

    await waitFor(() => {
      expect(screen.getByTestId("project-progress-fill-progress-1")).toHaveStyle({ width: "50%" });
      expect(screen.getByTestId("project-progress-fill-progress-2")).toHaveStyle({ width: "0%" });
    });
  });

  it("uses 'No tasks yet' phrasing for projects with zero tasks", async () => {
    const fetchMock = vi.fn(async () =>
      new Response(
        JSON.stringify({
          projects: [
            {
              id: "empty-1",
              name: "Empty Project",
              status: "active",
              taskCount: 0,
              completedCount: 0,
            },
          ],
        }),
        { status: 200 },
      ),
    );
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter>
        <ProjectsPage apiEndpoint="/api/projects-test" />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Empty Project")).toBeInTheDocument();
    expect(screen.getByText("No tasks yet")).toBeInTheDocument();
  });
});
