import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ConnectionsPage from "./ConnectionsPage";

function mockJSONResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("ConnectionsPage", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "00000000-0000-0000-0000-000000000001");
    localStorage.setItem("otter_camp_token", "token");
    vi.restoreAllMocks();
  });

  it("loads and renders bridge/session diagnostics from API", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/admin/connections")) {
        return mockJSONResponse({
          bridge: {
            connected: true,
            last_sync: "2026-02-07T16:00:00Z",
            sync_healthy: true,
            diagnostics: { reconnect_count: 2 },
          },
          host: {
            hostname: "Mac-Studio",
            os: "Darwin 25.2.0",
            arch: "arm64",
            gateway_port: 18791,
            node_version: "v25.4.0",
            memory_total_bytes: 137438953472,
            memory_used_bytes: 48318382080,
          },
          sessions: [
            {
              id: "main",
              name: "Frank",
              status: "online",
              model: "claude-opus-4-6",
              context_tokens: 42000,
              channel: "slack",
              last_seen: "just now",
              stalled: false,
            },
          ],
          summary: { total: 1, online: 1, busy: 0, offline: 0, stalled: 0 },
          generated_at: "2026-02-07T16:00:10Z",
        });
      }
      if (url.includes("/api/github/sync/health")) {
        return mockJSONResponse({ stuck_jobs: 1, queue_depth: [{ status: "pending", count: 3 }] });
      }
      if (url.includes("/api/github/sync/dead-letters")) {
        return mockJSONResponse({ items: [{ id: "dl-1" }, { id: "dl-2" }] });
      }
      if (url.includes("/api/admin/logs")) {
        return mockJSONResponse({
          items: [
            {
              id: "log-1",
              timestamp: "2026-02-07T16:00:20Z",
              level: "warning",
              event_type: "sync.failed",
              message: "sync push failed: 502 [REDACTED]",
            },
          ],
          total: 1,
        });
      }
      if (url.includes("/api/admin/diagnostics")) {
        return mockJSONResponse({
          checks: [{ key: "bridge.connection", status: "pass", message: "bridge connected" }],
          generated_at: "2026-02-07T16:00:30Z",
        });
      }
      throw new Error(`unexpected url ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<ConnectionsPage />);

    expect(await screen.findByText("Connections & Diagnostics")).toBeInTheDocument();
    expect(screen.getByText("Connected")).toBeInTheDocument();
    expect(screen.getByText("Mac-Studio")).toBeInTheDocument();
    expect(screen.getByText("Dead letters: 2")).toBeInTheDocument();
    expect(screen.getByText("Frank")).toBeInTheDocument();
    expect(screen.getByText("sync push failed: 502 [REDACTED]")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    expect(await screen.findByText("bridge.connection")).toBeInTheDocument();
  });

  it("shows error state and retries successfully", async () => {
    let attempts = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/admin/connections")) {
        attempts += 1;
        if (attempts === 1) {
          return mockJSONResponse({ error: "boom" }, 500);
        }
        return mockJSONResponse({
          bridge: { connected: false, sync_healthy: false },
          sessions: [],
          summary: { total: 0, online: 0, busy: 0, offline: 0, stalled: 0 },
          generated_at: "2026-02-07T16:10:00Z",
        });
      }
      if (url.includes("/api/github/sync/health")) {
        return mockJSONResponse({ stuck_jobs: 0, queue_depth: [] });
      }
      if (url.includes("/api/github/sync/dead-letters")) {
        return mockJSONResponse({ items: [] });
      }
      if (url.includes("/api/admin/logs")) {
        return mockJSONResponse({ items: [], total: 0 });
      }
      if (url.includes("/api/admin/diagnostics")) {
        return mockJSONResponse({ checks: [], generated_at: "2026-02-07T16:10:10Z" });
      }
      throw new Error(`unexpected url ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<ConnectionsPage />);

    expect(await screen.findByText("boom")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Try Again" }));
    expect(await screen.findByText("Disconnected")).toBeInTheDocument();
    expect(attempts).toBe(2);
  });
});
