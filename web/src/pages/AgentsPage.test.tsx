import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import AgentsPage from "./AgentsPage";

const { adminAgentsMock, adminAgentMock, projectsMock } = vi.hoisted(() => ({
  adminAgentsMock: vi.fn(),
  adminAgentMock: vi.fn(),
  projectsMock: vi.fn(),
}));

vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return {
    ...actual,
    default: {
      ...actual.default,
      adminAgents: adminAgentsMock,
      adminAgent: adminAgentMock,
      projects: projectsMock,
    },
  };
});

const AGENTS_PAYLOAD = {
  total: 4,
  agents: [
    {
      id: "elephant",
      workspace_agent_id: "00000000-0000-0000-0000-000000000001",
      name: "Ellie",
      status: "online",
      is_ephemeral: false,
      context_tokens: 1400,
      total_tokens: 2200,
      last_seen: "2026-02-19T09:41:00.000Z",
      model: "claude-opus",
      channel: "dm",
    },
    {
      id: "staffing-manager",
      workspace_agent_id: "00000000-0000-0000-0000-000000000002",
      name: "Marcus",
      status: "busy",
      is_ephemeral: false,
      context_tokens: 980,
      total_tokens: 1900,
      last_seen: "2026-02-19T09:32:00.000Z",
      model: "claude-sonnet",
      channel: "bridge",
    },
    {
      id: "agent-042",
      workspace_agent_id: "00000000-0000-0000-0000-000000000003",
      name: "Agent-042",
      status: "online",
      is_ephemeral: true,
      context_tokens: 320,
      total_tokens: 810,
      project_id: "project-42",
      model: "frontend-specialist",
      last_seen: "2026-02-19T09:44:00.000Z",
    },
    {
      id: "agent-156",
      workspace_agent_id: "00000000-0000-0000-0000-000000000004",
      name: "Agent-156",
      status: "offline",
      is_ephemeral: true,
      context_tokens: 0,
      total_tokens: 0,
      project_id: null,
      model: "",
      last_seen: "2026-02-18T10:00:00.000Z",
    },
  ],
};

describe("AgentsPage", () => {
  beforeEach(() => {
    adminAgentsMock.mockReset();
    adminAgentMock.mockReset();
    projectsMock.mockReset();

    adminAgentsMock.mockResolvedValue(AGENTS_PAYLOAD);
    projectsMock.mockResolvedValue({
      projects: [
        { id: "project-42", name: "Customer Portal" },
      ],
    });
    adminAgentMock.mockImplementation(async (id: string) => {
      if (id === "elephant") {
        return { sync: { current_task: "Coordinating inbox triage" } };
      }
      if (id === "staffing-manager") {
        return { sync: { current_task: "Allocating Agent-042 to Customer Portal" } };
      }
      return { sync: {} };
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders API-driven permanent and chameleon agent rows", async () => {
    render(<MemoryRouter><AgentsPage /></MemoryRouter>);

    expect(screen.getByRole("heading", { name: "Agent Status" })).toBeInTheDocument();
    expect(await screen.findByText("2 Permanent Cores • 1 Active Chameleons")).toBeInTheDocument();

    expect(screen.getByRole("button", { name: "Logs" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Spawn Agent" })).toBeInTheDocument();

    expect(screen.getByText("Ellie")).toBeInTheDocument();
    expect(screen.getByText("Marcus")).toBeInTheDocument();
    expect(screen.getByText("Coordinating inbox triage")).toBeInTheDocument();
    expect(screen.getByText("Allocating Agent-042 to Customer Portal")).toBeInTheDocument();

    expect(screen.getByRole("heading", { name: "Chameleon Agents (On-Demand)" })).toBeInTheDocument();
    expect(screen.getByText("Agent-042")).toBeInTheDocument();
    expect(screen.getByText("frontend-specialist")).toBeInTheDocument();
    expect(screen.getByText(/Working on Customer Portal/)).toBeInTheDocument();

    expect(screen.queryByText("Orchestrator")).not.toBeInTheDocument();
  });

  it("shows load failures and allows retry", async () => {
    adminAgentsMock
      .mockRejectedValueOnce(new Error("agents load failed"))
      .mockResolvedValueOnce(AGENTS_PAYLOAD);

    render(<MemoryRouter><AgentsPage /></MemoryRouter>);

    expect(await screen.findByText("agents load failed")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByText("2 Permanent Cores • 1 Active Chameleons")).toBeInTheDocument();
    expect(screen.getByText("Ellie")).toBeInTheDocument();
  });
});
