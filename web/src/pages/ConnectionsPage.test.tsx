import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
    vi.stubGlobal("confirm", vi.fn(() => true));
  });

  it("loads and renders bridge/session diagnostics from API", async () => {
    let cronEnabled = true;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const requestMethod = input instanceof Request ? input.method : undefined;
      const method = (init?.method || requestMethod || "GET").toUpperCase();
      if (url.includes("/api/activity/recent")) {
        return mockJSONResponse({
          items: [
            {
              id: "evt-1",
              org_id: "00000000-0000-0000-0000-000000000001",
              agent_id: "main",
              session_key: "agent:main:main",
              trigger: "chat.slack",
              summary: "Responded in #leadership",
              status: "completed",
              tokens_used: 30,
              duration_ms: 800,
              started_at: "2026-02-08T16:00:00Z",
              created_at: "2026-02-08T16:00:00Z",
            },
            {
              id: "evt-2",
              org_id: "00000000-0000-0000-0000-000000000001",
              agent_id: "main",
              session_key: "agent:main:main",
              trigger: "dispatch.issue",
              summary: "Bridge dispatch failed",
              status: "failed",
              tokens_used: 10,
              duration_ms: 500,
              started_at: "2026-02-08T16:05:00Z",
              created_at: "2026-02-08T16:05:00Z",
            },
          ],
        });
      }
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
      if (url.includes("/api/admin/cron/jobs/job-1/run")) {
        return mockJSONResponse({ ok: true, action: "cron.run" });
      }
      if (url.includes("/api/admin/cron/jobs/job-1") && method === "PATCH") {
        cronEnabled = false;
        return mockJSONResponse({ ok: true, action: "cron.disable" });
      }
      if (url.includes("/api/admin/cron/jobs")) {
        return mockJSONResponse({
          items: [
            {
              id: "job-1",
              name: "Hourly Heartbeat",
              schedule: "0 * * * *",
              enabled: cronEnabled,
              last_status: "success",
              last_run_at: "2026-02-07T15:59:00Z",
            },
          ],
          total: 1,
        });
      }
      if (url.includes("/api/admin/processes/proc-1/kill")) {
        return mockJSONResponse({ ok: true, action: "process.kill" });
      }
      if (url.includes("/api/admin/processes")) {
        return mockJSONResponse({
          items: [
            {
              id: "proc-1",
              command: "openclaw run --session agent:main:main",
              status: "running",
              duration_seconds: 12,
              agent_id: "main",
            },
          ],
          total: 1,
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
    expect(screen.getByText("Last Activity")).toBeInTheDocument();
    expect(screen.getByText("Bridge dispatch failed")).toBeInTheDocument();
    expect(screen.getByText("Failed")).toBeInTheDocument();
    expect(screen.getByText("Hourly Heartbeat")).toBeInTheDocument();
    expect(screen.getByText("proc-1")).toBeInTheDocument();

    const runButtonsBeforeActions = screen.getAllByRole("button", { name: "Run" });
    fireEvent.click(runButtonsBeforeActions[0]);
    expect(await screen.findByText("bridge.connection")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Disable" }));
    await waitFor(() => {
      const hasCronToggleCall = fetchMock.mock.calls.some(([input, requestInit]) => {
        const url = String(input);
        return url.includes("/api/admin/cron/jobs/job-1") && !url.includes("/run") && String(requestInit?.method).toUpperCase() === "PATCH";
      });
      expect(hasCronToggleCall).toBe(true);
    });

    const runButtonsAfterToggle = screen.getAllByRole("button", { name: "Run" });
    fireEvent.click(runButtonsAfterToggle[1]);
    fireEvent.click(screen.getByRole("button", { name: "Kill" }));

    await waitFor(() => {
      const hasCronRunCall = fetchMock.mock.calls.some(([input, requestInit]) => {
        return String(input).includes("/api/admin/cron/jobs/job-1/run") && String(requestInit?.method).toUpperCase() === "POST";
      });
      expect(hasCronRunCall).toBe(true);
    });

    await waitFor(() => {
      const hasKillCall = fetchMock.mock.calls.some(([input, requestInit]) => {
        return String(input).includes("/api/admin/processes/proc-1/kill") && String(requestInit?.method).toUpperCase() === "POST";
      });
      expect(hasKillCall).toBe(true);
    });
  });

  it("shows error state and retries successfully", async () => {
    let attempts = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/activity/recent")) {
        return mockJSONResponse({ items: [] });
      }
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
      if (url.includes("/api/admin/cron/jobs")) {
        return mockJSONResponse({ items: [], total: 0 });
      }
      if (url.includes("/api/admin/processes")) {
        return mockJSONResponse({ items: [], total: 0 });
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

  it("formats last-seen timestamps, renders memory N/A fallback, and shows disconnected log guidance", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/activity/recent")) {
        return mockJSONResponse({ items: [] });
      }
      if (url.includes("/api/admin/connections")) {
        return mockJSONResponse({
          bridge: {
            connected: false,
            last_sync: "2026-02-08T23:20:00Z",
            sync_healthy: false,
          },
          host: {
            hostname: "Mac-Studio",
            memory_total_bytes: 0,
            memory_used_bytes: 0,
          },
          sessions: [
            {
              id: "main",
              name: "Frank",
              status: "online",
              channel: "slack",
              last_seen: "2026-02-08T20:25:19Z",
              stalled: false,
            },
          ],
          summary: { total: 1, online: 1, busy: 0, offline: 0, stalled: 0 },
          generated_at: "2026-02-08T23:25:19Z",
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
        return mockJSONResponse({ checks: [], generated_at: "2026-02-08T23:25:19Z" });
      }
      if (url.includes("/api/admin/cron/jobs")) {
        return mockJSONResponse({ items: [], total: 0 });
      }
      if (url.includes("/api/admin/processes")) {
        return mockJSONResponse({ items: [], total: 0 });
      }
      throw new Error(`unexpected url ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<ConnectionsPage />);

    expect(await screen.findByText("Connections & Diagnostics")).toBeInTheDocument();
    expect(screen.getByText("Memory used: N/A / N/A")).toBeInTheDocument();
    expect(screen.queryByText("2026-02-08T20:25:19Z")).not.toBeInTheDocument();
    expect(screen.getByText("Connect bridge to view logs.")).toBeInTheDocument();
  });
});
