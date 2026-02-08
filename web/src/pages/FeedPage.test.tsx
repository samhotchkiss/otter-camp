import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import FeedPage from "./FeedPage";

vi.mock("../components/activity/ActivityPanel", () => ({
  default: () => <div data-testid="realtime-panel">Realtime Panel</div>,
}));

describe("FeedPage", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("switches to agent activity mode and renders timeline events", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/activity/recent")) {
        return new Response(
          JSON.stringify({
            items: [
              {
                id: "evt-1",
                org_id: "org-123",
                agent_id: "main",
                session_key: "agent:main:main",
                trigger: "cron.scheduled",
                summary: "Ran codex-progress-summary",
                status: "completed",
                tokens_used: 15,
                duration_ms: 700,
                started_at: "2026-02-08T13:00:00.000Z",
                created_at: "2026-02-08T13:00:00.000Z",
              },
            ],
          }),
          { status: 200 },
        );
      }
      if (url.includes("/api/feed")) {
        return new Response(JSON.stringify({ items: [] }), { status: 200 });
      }
      throw new Error(`unexpected url: ${url}`);
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<FeedPage />);

    expect(screen.getByTestId("realtime-panel")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Agent Activity" }));

    expect(await screen.findByText("Ran codex-progress-summary")).toBeInTheDocument();
    expect(screen.getByText("Cron")).toBeInTheDocument();
  });

  it("shows empty state when no agent events are available", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/activity/recent")) {
        return new Response(JSON.stringify({ items: [] }), { status: 200 });
      }
      if (url.includes("/api/feed")) {
        return new Response(JSON.stringify({ items: [] }), { status: 200 });
      }
      throw new Error(`unexpected url: ${url}`);
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<FeedPage />);

    fireEvent.click(screen.getByRole("button", { name: "Agent Activity" }));

    expect(await screen.findByText("No agent activity yet.")).toBeInTheDocument();
  });

  it("falls back to historical feed items when agent-activity endpoint is empty", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/activity/recent")) {
        return new Response(JSON.stringify({ items: [] }), { status: 200 });
      }
      if (url.includes("/api/feed")) {
        return new Response(
          JSON.stringify({
            items: [
              {
                id: "feed-1",
                org_id: "org-123",
                type: "task_status_changed",
                created_at: "2026-02-08T13:00:00.000Z",
                agent_name: "System",
                task_title: "Ship feed page",
                summary: "System changed task \"Ship feed page\" to done",
                metadata: { new_status: "done" },
              },
            ],
          }),
          { status: 200 },
        );
      }
      throw new Error(`unexpected url: ${url}`);
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<FeedPage />);

    fireEvent.click(screen.getByRole("button", { name: "Agent Activity" }));

    expect(await screen.findByText(/Ship feed page/i)).toBeInTheDocument();
  });

  it("includes git push items in agent fallback when realtime agent events are empty", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/activity/recent")) {
        return new Response(JSON.stringify({ items: [] }), { status: 200 });
      }
      if (url.includes("/api/feed")) {
        return new Response(
          JSON.stringify({
            items: [
              {
                id: "feed-git-1",
                org_id: "org-123",
                type: "git.push",
                created_at: "2026-02-08T13:00:00.000Z",
                agent_name: "Sam",
                summary: "Sam: git.push",
                metadata: { branch: "main", commit_message: "Fix feed fallback wiring" },
              },
            ],
          }),
          { status: 200 },
        );
      }
      throw new Error(`unexpected url: ${url}`);
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<FeedPage />);

    fireEvent.click(screen.getByRole("button", { name: "Agent Activity" }));

    expect(await screen.findByText("git.push")).toBeInTheDocument();
    expect(screen.getByText(/Sam/i)).toBeInTheDocument();
  });
});
