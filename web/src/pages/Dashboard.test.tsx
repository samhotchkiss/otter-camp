import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Dashboard from "./Dashboard";
import { api } from "../lib/api";
import useEmissions from "../hooks/useEmissions";

vi.mock("../components/CommandPalette", () => ({
  default: () => null,
}));

vi.mock("../components/OnboardingTour", () => ({
  default: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

vi.mock("../components/TaskDetail", () => ({
  default: () => null,
}));

vi.mock("../components/NewTaskModal", () => ({
  default: () => null,
}));

vi.mock("../contexts/KeyboardShortcutsContext", () => ({
  useKeyboardShortcutsContext: () => ({
    isCommandPaletteOpen: false,
    closeCommandPalette: vi.fn(),
    openCommandPalette: vi.fn(),
    selectedTaskId: null,
    closeTaskDetail: vi.fn(),
    isNewTaskOpen: false,
    closeNewTask: vi.fn(),
  }),
}));

vi.mock("../hooks/useEmissions", () => ({
  default: vi.fn(),
}));

vi.mock("../lib/demo", () => ({
  isDemoMode: () => false,
}));

vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return {
    ...actual,
    api: {
      ...actual.api,
      feed: vi.fn(),
      projects: vi.fn(),
      syncAgents: vi.fn(),
    },
  };
});

describe("Dashboard", () => {
  beforeEach(() => {
    vi.mocked(useEmissions).mockReturnValue({
      emissions: [
        {
          id: "em-1",
          source_type: "agent",
          source_id: "agent-1",
          kind: "status",
          summary: "Live emission summary",
          timestamp: "2026-02-08T12:00:00Z",
        },
      ],
      latestBySource: new Map(),
      loading: false,
      error: null,
      refresh: vi.fn(),
    });

    vi.mocked(api.feed).mockResolvedValue({
      actionItems: [],
      feedItems: [],
    } as Awaited<ReturnType<typeof api.feed>>);
    vi.mocked(api.projects).mockResolvedValue({
      projects: [
        { id: "project-1", name: "Project One", status: "active" },
      ],
    } as Awaited<ReturnType<typeof api.projects>>);
    vi.mocked(api.syncAgents).mockResolvedValue({
      last_sync: "2026-02-08T12:00:00Z",
    } as Awaited<ReturnType<typeof api.syncAgents>>);
  });

  it("renders live emission ticker content", async () => {
    render(<Dashboard />);

    expect(await screen.findByText("Live emission summary")).toBeInTheDocument();
    expect(screen.getByText("LIVE")).toBeInTheDocument();
  });
});
