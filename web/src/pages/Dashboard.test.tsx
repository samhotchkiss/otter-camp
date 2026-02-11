import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Dashboard from "./Dashboard";
import { api } from "../lib/api";
import useEmissions from "../hooks/useEmissions";

let wsLastMessage: { type: string; data?: unknown } | null = null;

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

vi.mock("../contexts/WebSocketContext", () => ({
  useOptionalWS: () => ({
    lastMessage: wsLastMessage,
  }),
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
      activityRecent: vi.fn(),
      feed: vi.fn(),
      projects: vi.fn(),
      syncAgents: vi.fn(),
    },
  };
});

describe("Dashboard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    wsLastMessage = null;

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
    vi.mocked(api.activityRecent).mockResolvedValue({
      items: [],
    } as Awaited<ReturnType<typeof api.activityRecent>>);
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

  it("resolves git push actor from metadata sender_login and avoids redundant push wording", async () => {
    vi.mocked(api.activityRecent).mockResolvedValueOnce({
      items: [],
    } as Awaited<ReturnType<typeof api.activityRecent>>);
    vi.mocked(api.feed).mockResolvedValueOnce({
      org_id: "org-1",
      items: [
        {
          id: "feed-1",
          org_id: "org-1",
          type: "git.push",
          created_at: "2026-02-08T12:00:00Z",
          metadata: {
            sender_login: "samhotchkiss",
            branch: "main",
            commit_message: "Fix feed actor fallback",
          },
        },
      ],
    } as Awaited<ReturnType<typeof api.feed>>);

    render(<Dashboard />);

    expect(await screen.findByText("samhotchkiss")).toBeInTheDocument();
    expect(screen.getByText(/main: "Fix feed actor fallback"/i)).toBeInTheDocument();
    expect(screen.queryByText(/pushed to/i)).not.toBeInTheDocument();
  });

  it("prefers recent activity endpoint when items are available", async () => {
    vi.mocked(api.activityRecent).mockResolvedValueOnce({
      items: [
        {
          id: "act-1",
          org_id: "org-1",
          agent_id: "marcus",
          trigger: "dispatch.project_chat",
          summary: "Dispatched project chat for Testerooni",
          created_at: "2026-02-08T12:00:00Z",
        },
      ],
    } as Awaited<ReturnType<typeof api.activityRecent>>);

    render(<Dashboard />);

    expect(await screen.findByText("marcus")).toBeInTheDocument();
    expect(screen.getByText(/Dispatched project chat for Testerooni/i)).toBeInTheDocument();
    expect(api.feed).not.toHaveBeenCalled();
  });

  it("prepends realtime activity websocket events to the feed", async () => {
    vi.mocked(api.activityRecent).mockResolvedValueOnce({
      items: [],
    } as Awaited<ReturnType<typeof api.activityRecent>>);
    vi.mocked(api.feed).mockResolvedValueOnce({
      org_id: "org-1",
      items: [],
    } as Awaited<ReturnType<typeof api.feed>>);

    const { rerender } = render(<Dashboard />);
    await screen.findByText("No activity yet.");

    wsLastMessage = {
      type: "ActivityEventReceived",
      data: {
        event: {
          id: "event-live-1",
          org_id: "org-1",
          agent_id: "marcus",
          trigger: "dispatch.project_chat",
          summary: "Realtime dispatch from websocket",
          created_at: "2026-02-10T23:59:00Z",
        },
      },
    };
    rerender(<Dashboard />);

    expect(await screen.findByText("marcus")).toBeInTheDocument();
    expect(screen.getByText(/Realtime dispatch from websocket/i)).toBeInTheDocument();
  });
});
