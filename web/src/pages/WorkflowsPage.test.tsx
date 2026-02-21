import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import WorkflowsPage from "./WorkflowsPage";

type MockWorkflowProject = {
  id: string;
  name: string;
  workflow_enabled: boolean;
  workflow_schedule?: unknown;
  workflow_last_run_at?: string | null;
  workflow_next_run_at?: string | null;
  workflow_run_count?: number;
};

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as Response;
}

describe("WorkflowsPage", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.useRealTimers();
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    localStorage.clear();
    localStorage.setItem("otter_camp_token", "test-token");
  });

  it("loads workflow projects from /api/projects?workflow=true and displays summary", async () => {
    const projects: MockWorkflowProject[] = [
      {
        id: "project-1",
        name: "Morning Briefing",
        workflow_enabled: true,
        workflow_schedule: { kind: "cron", expr: "0 6 * * *", tz: "America/Denver" },
        workflow_run_count: 4,
      },
      {
        id: "project-2",
        name: "System Heartbeat",
        workflow_enabled: false,
        workflow_schedule: { kind: "every", everyMs: 900000 },
        workflow_run_count: 10,
      },
    ];

    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/projects?workflow=true")) {
        return jsonResponse({ projects });
      }
      if (url.includes("/api/projects/project-1/runs/latest")) {
        return jsonResponse({ title: "Morning run issue" });
      }
      if (url.includes("/api/projects/project-2/runs/latest")) {
        return jsonResponse({ error: "not found" }, 404);
      }
      throw new Error(`Unexpected request: ${url} ${init?.method || "GET"}`);
    });

    render(<WorkflowsPage />);

    expect(await screen.findByRole("heading", { level: 1, name: "Workflows" })).toBeInTheDocument();
    expect(screen.getByText("2 workflow projects · 1 active · 1 paused")).toBeInTheDocument();
    expect(screen.getByText("Morning Briefing")).toBeInTheDocument();
    expect(screen.getByText("System Heartbeat")).toBeInTheDocument();
    expect(screen.getByText("Latest task: Morning run issue")).toBeInTheDocument();
  });

  it("toggles workflow_enabled via project patch endpoint", async () => {
    const user = userEvent.setup();
    const state: { project: MockWorkflowProject } = {
      project: {
        id: "project-1",
        name: "Morning Briefing",
        workflow_enabled: true,
        workflow_schedule: { kind: "cron", expr: "0 6 * * *", tz: "America/Denver" },
        workflow_run_count: 2,
      },
    };
    const requests: Array<{ url: string; method: string; body: string }> = [];

    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method || "GET";
      requests.push({ url, method, body: typeof init?.body === "string" ? init.body : "" });
      if (url.includes("/api/projects?workflow=true")) {
        return jsonResponse({ projects: [state.project] });
      }
      if (url.includes("/runs/latest")) {
        return jsonResponse({ title: "Latest run" });
      }
      if (url.includes(`/api/projects/${state.project.id}`) && method === "PATCH") {
        const payload = JSON.parse((init?.body as string) || "{}");
        state.project.workflow_enabled = payload.workflow_enabled;
        return jsonResponse({ id: state.project.id, workflow_enabled: state.project.workflow_enabled });
      }
      throw new Error(`Unexpected request: ${url} ${method}`);
    });

    render(<WorkflowsPage />);
    expect(await screen.findByText("Morning Briefing")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Pause" }));

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Resume" })).toBeInTheDocument();
    });

    const patchRequest = requests.find(
      (request) => request.method === "PATCH" && request.url.includes(`/api/projects/${state.project.id}`),
    );
    expect(patchRequest).toBeDefined();
    expect(JSON.parse(patchRequest?.body || "{}")).toEqual({ workflow_enabled: false });
  });

  it("triggers manual workflow run via /runs/trigger", async () => {
    const user = userEvent.setup();
    const state: { project: MockWorkflowProject } = {
      project: {
        id: "project-1",
        name: "Morning Briefing",
        workflow_enabled: true,
        workflow_schedule: { kind: "every", everyMs: 900000 },
        workflow_run_count: 2,
      },
    };
    const requests: Array<{ url: string; method: string }> = [];

    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method || "GET";
      requests.push({ url, method });
      if (url.includes("/api/projects?workflow=true")) {
        return jsonResponse({ projects: [state.project] });
      }
      if (url.includes("/runs/latest")) {
        return jsonResponse({ title: "Latest run" });
      }
      if (url.includes(`/api/projects/${state.project.id}/runs/trigger`) && method === "POST") {
        state.project.workflow_run_count = (state.project.workflow_run_count || 0) + 1;
        return jsonResponse({ run_number: state.project.workflow_run_count });
      }
      throw new Error(`Unexpected request: ${url} ${method}`);
    });

    render(<WorkflowsPage />);
    expect(await screen.findByText("Runs: 2")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Run now" }));

    await waitFor(() => {
      expect(screen.getByText("Runs: 3")).toBeInTheDocument();
    });

    expect(
      requests.some(
        (request) => request.method === "POST" && request.url.includes(`/api/projects/${state.project.id}/runs/trigger`),
      ),
    ).toBe(true);
  });

  it("shows error banner when workflow toggle fails", async () => {
    const user = userEvent.setup();
    const project: MockWorkflowProject = {
      id: "project-1",
      name: "Morning Briefing",
      workflow_enabled: true,
      workflow_schedule: { kind: "cron", expr: "0 6 * * *", tz: "America/Denver" },
      workflow_run_count: 2,
    };

    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method || "GET";
      if (url.includes("/api/projects?workflow=true")) {
        return jsonResponse({ projects: [project] });
      }
      if (url.includes("/runs/latest")) {
        return jsonResponse({ title: "Latest run" });
      }
      if (url.includes(`/api/projects/${project.id}`) && method === "PATCH") {
        return jsonResponse({ error: "failed" }, 500);
      }
      throw new Error(`Unexpected request: ${url} ${method}`);
    });

    render(<WorkflowsPage />);
    expect(await screen.findByText("Morning Briefing")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Pause" }));

    expect(await screen.findByText("Failed to update workflow state")).toBeInTheDocument();
  });

  it("shows error banner when manual trigger fails", async () => {
    const user = userEvent.setup();
    const project: MockWorkflowProject = {
      id: "project-1",
      name: "Morning Briefing",
      workflow_enabled: true,
      workflow_schedule: { kind: "every", everyMs: 900000 },
      workflow_run_count: 2,
    };

    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method || "GET";
      if (url.includes("/api/projects?workflow=true")) {
        return jsonResponse({ projects: [project] });
      }
      if (url.includes("/runs/latest")) {
        return jsonResponse({ title: "Latest run" });
      }
      if (url.includes(`/api/projects/${project.id}/runs/trigger`) && method === "POST") {
        return jsonResponse({ error: "failed" }, 500);
      }
      throw new Error(`Unexpected request: ${url} ${method}`);
    });

    render(<WorkflowsPage />);
    expect(await screen.findByText("Morning Briefing")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Run now" }));

    expect(await screen.findByText("Failed to trigger workflow run")).toBeInTheDocument();
  });

  it("shows list error banner when loading workflows fails", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/projects?workflow=true")) {
        return jsonResponse({ error: "failed" }, 500);
      }
      throw new Error(`Unexpected request: ${url} ${init?.method || "GET"}`);
    });

    render(<WorkflowsPage />);

    expect(await screen.findByText("Failed to fetch workflow projects")).toBeInTheDocument();
  });

  it("shows empty state when workflow project list is empty", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/projects?workflow=true")) {
        return jsonResponse({ projects: [] });
      }
      throw new Error(`Unexpected request: ${url} ${init?.method || "GET"}`);
    });

    render(<WorkflowsPage />);

    expect(await screen.findByText("No workflow projects yet")).toBeInTheDocument();
    expect(
      screen.getByText("Enable workflow fields on a project to schedule recurring task creation."),
    ).toBeInTheDocument();
  });
});
