import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ProjectsPage from "./ProjectsPage";

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
});
