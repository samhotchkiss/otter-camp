import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ProjectsPage from "./ProjectsPage";

const { mockNavigate } = vi.hoisted(() => ({
  mockNavigate: vi.fn(),
}));

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

type MockLabel = {
  id: string;
  name: string;
  color: string;
};

type MockProject = {
  id: string;
  name: string;
  description?: string;
  status?: string;
  priority?: string;
  repo?: string;
  githubSync?: boolean;
  openIssues?: number;
  inProgress?: number;
  needsApproval?: number;
  labels?: MockLabel[];
  updated_at?: string;
};

const LABEL_PRODUCT: MockLabel = { id: "label-product", name: "product", color: "#3b82f6" };
const LABEL_INFRA: MockLabel = { id: "label-infra", name: "infrastructure", color: "#6b7280" };
const LABEL_CONTENT: MockLabel = { id: "label-content", name: "content", color: "#8b5cf6" };

const PROJECTS: MockProject[] = [
  {
    id: "project-1",
    name: "Otter Camp",
    description: "Main app",
    labels: [LABEL_PRODUCT, LABEL_INFRA],
    updated_at: "2026-02-08T12:00:00Z",
  },
  {
    id: "project-2",
    name: "Website",
    description: "Marketing",
    labels: [LABEL_PRODUCT, LABEL_CONTENT],
    updated_at: "2026-02-07T12:00:00Z",
  },
  {
    id: "project-3",
    name: "Three Stones",
    description: "Content work",
    labels: [LABEL_CONTENT],
    updated_at: "2026-02-06T12:00:00Z",
  },
];

function response(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

describe("ProjectsPage", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockNavigate.mockReset();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("renders label pills on project cards", async () => {
    const fetchMock = vi.fn(async () => response({ projects: PROJECTS }));
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter>
        <ProjectsPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Otter Camp")).toBeInTheDocument();
    expect(screen.getAllByText("product").length).toBeGreaterThan(0);
    expect(screen.getAllByText("infrastructure").length).toBeGreaterThan(0);
  });

  it("applies multi-label filters and emits repeated label query params", async () => {
    const labelQueryHistory: string[][] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input));
      const labels = url.searchParams.getAll("label");
      labelQueryHistory.push(labels);

      if (labels.includes("label-product") && labels.includes("label-infra")) {
        return response({ projects: [PROJECTS[0]] });
      }
      if (labels.includes("label-product")) {
        return response({ projects: [PROJECTS[0], PROJECTS[1]] });
      }
      return response({ projects: PROJECTS });
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter>
        <ProjectsPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Otter Camp")).toBeInTheDocument();
    expect(screen.getByText("Website")).toBeInTheDocument();
    expect(screen.getByText("Three Stones")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Toggle label product" }));
    await waitFor(() => {
      expect(screen.queryByText("Three Stones")).not.toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "Toggle label infrastructure" }));
    await waitFor(() => {
      expect(screen.queryByText("Website")).not.toBeInTheDocument();
      expect(screen.queryByText("Three Stones")).not.toBeInTheDocument();
    });

    await waitFor(() => {
      expect(
        labelQueryHistory.some((labels) => {
          const sorted = [...labels].sort();
          return sorted.length === 2 && sorted[0] === "label-infra" && sorted[1] === "label-product";
        }),
      ).toBe(true);
    });
  });

  it("shows filtered-empty state and clears label filters back to full list", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input));
      const labels = url.searchParams.getAll("label").sort();

      if (labels.length === 2 && labels[0] === "label-content" && labels[1] === "label-product") {
        return response({ projects: [] });
      }

      if (labels.length === 1 && labels[0] === "label-product") {
        return response({ projects: [PROJECTS[0], PROJECTS[1]] });
      }

      if (labels.length === 1 && labels[0] === "label-content") {
        return response({ projects: [PROJECTS[1], PROJECTS[2]] });
      }

      return response({ projects: PROJECTS });
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter>
        <ProjectsPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Otter Camp")).toBeInTheDocument();
    expect(screen.getByText("Website")).toBeInTheDocument();
    expect(screen.getByText("Three Stones")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Toggle label product" }));
    await waitFor(() => {
      expect(screen.queryByText("Three Stones")).not.toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "Toggle label content" }));
    expect(await screen.findByText("No projects match the selected labels.")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Clear filters" }));
    await waitFor(() => {
      expect(screen.getByText("Otter Camp")).toBeInTheDocument();
      expect(screen.getByText("Website")).toBeInTheDocument();
      expect(screen.getByText("Three Stones")).toBeInTheDocument();
    });
  });

  it("renders redesigned card metric/status surface fields", async () => {
    const fetchMock = vi.fn(async () => response({
      projects: [
        {
          ...PROJECTS[0],
          status: "active",
          priority: "high",
          repo: "ottercamp/otter-camp",
          githubSync: true,
          openIssues: 12,
          inProgress: 4,
          needsApproval: 2,
        },
      ],
    }));
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter>
        <ProjectsPage />
      </MemoryRouter>,
    );

    const card = await screen.findByTestId("project-card-project-1");
    expect(within(card).getByText("ottercamp/otter-camp")).toBeInTheDocument();
    expect(within(card).getByText("Open")).toBeInTheDocument();
    expect(within(card).getByText("Active")).toBeInTheDocument();
    expect(within(card).getByText("Review")).toBeInTheDocument();
    expect(within(card).getByText("Synced")).toBeInTheDocument();
  });

  it("renders a recent activity surface derived from loaded projects", async () => {
    const fetchMock = vi.fn(async () => response({ projects: PROJECTS }));
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter>
        <ProjectsPage />
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { name: "Recent Activity" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "View All" })).toBeInTheDocument();
  });

  it("applies responsive class hooks to projects controls and activity rows", async () => {
    const fetchMock = vi.fn(async () => response({ projects: PROJECTS }));
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter>
        <ProjectsPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Otter Camp")).toBeInTheDocument();

    const createButton = screen.getByRole("button", { name: "New Project" });
    expect(createButton).toHaveClass("w-full");
    expect(createButton).toHaveClass("sm:w-auto");
    expect(createButton).toHaveClass("justify-center");

    const projectCountHint = screen.getByText(/projects • Press/i);
    const hintRow = projectCountHint.parentElement;
    expect(hintRow).toHaveClass("flex-col");
    expect(hintRow).toHaveClass("sm:flex-row");
    expect(hintRow).toHaveClass("items-start");
    expect(hintRow).toHaveClass("sm:items-center");

    const activityHeading = screen.getByRole("heading", { name: "Recent Activity" });
    const activitySection = activityHeading.closest("section");
    const activityRow = activitySection?.querySelector(".divide-y > div");
    expect(activityRow).not.toBeNull();
    expect(activityRow).toHaveClass("flex-col");
    expect(activityRow).toHaveClass("sm:flex-row");
    expect(activityRow).toHaveClass("items-start");
    expect(activityRow).toHaveClass("sm:items-center");
  });

  it("navigates to project detail when clicking a project card", async () => {
    const fetchMock = vi.fn(async () => response({ projects: PROJECTS }));
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter>
        <ProjectsPage />
      </MemoryRouter>,
    );

    fireEvent.click(await screen.findByTestId("project-card-project-2"));
    expect(mockNavigate).toHaveBeenCalledWith("/projects/project-2");
  });
});
