import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import FeedPage from "./FeedPage";

vi.mock("../components/ActivityPanel", () => ({
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
      throw new Error(`unexpected url: ${url}`);
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<FeedPage />);

    expect(screen.getByTestId("realtime-panel")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Agent Activity" }));

    expect(await screen.findByText("main · Ran codex-progress-summary")).toBeInTheDocument();
    expect(screen.getByText("Cron")).toBeInTheDocument();
  });

  it("shows empty state when no agent events are available", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/activity/recent")) {
        return new Response(JSON.stringify({ items: [] }), { status: 200 });
      }
      throw new Error(`unexpected url: ${url}`);
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<FeedPage />);

    fireEvent.click(screen.getByRole("button", { name: "Agent Activity" }));

    expect(await screen.findByText("No agent activity yet.")).toBeInTheDocument();
  });
});
