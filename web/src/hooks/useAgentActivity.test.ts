import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

let wsLastMessage: { type: string; data: unknown } | null = null;

vi.mock("../contexts/WebSocketContext", () => ({
  useOptionalWS: () =>
    wsLastMessage
      ? {
          connected: true,
          lastMessage: wsLastMessage,
          sendMessage: vi.fn(),
          reconnectReason: "initial",
        }
      : null,
}));

import {
  buildAgentActivityURL,
  parseAgentActivityEvent,
  trackProcessedRealtimeID,
  useAgentActivity,
} from "./useAgentActivity";

describe("parseAgentActivityEvent", () => {
  it("normalizes API records into typed activity events", () => {
    const parsed = parseAgentActivityEvent({
      id: "evt-1",
      org_id: "org-1",
      agent_id: "main",
      session_key: "agent:main:main",
      trigger: "chat.slack",
      summary: "Responded in Slack",
      tokens_used: "45",
      duration_ms: "1200",
      status: "failed",
      started_at: "2026-02-08T20:10:00.000Z",
      completed_at: "2026-02-08T20:10:01.000Z",
      created_at: "2026-02-08T20:10:02.000Z",
      issue_number: 42,
    });

    expect(parsed).not.toBeNull();
    expect(parsed?.tokensUsed).toBe(45);
    expect(parsed?.durationMs).toBe(1200);
    expect(parsed?.status).toBe("failed");
    expect(parsed?.issueNumber).toBe(42);
    expect(parsed?.startedAt.toISOString()).toBe("2026-02-08T20:10:00.000Z");
  });
});

describe("buildAgentActivityURL", () => {
  it("builds query params for recent mode", () => {
    const url = buildAgentActivityURL({
      mode: "recent",
      orgId: "org-1",
      limit: 25,
      before: "2026-02-08T20:00:00Z",
      filters: {
        agentId: "main",
        trigger: "cron.scheduled",
        channel: "cron",
        status: "failed",
        projectId: "project-1",
      },
    });

    expect(url).toContain("/api/activity/recent");
    expect(url).toContain("org_id=org-1");
    expect(url).toContain("limit=25");
    expect(url).toContain("agent_id=main");
    expect(url).toContain("trigger=cron.scheduled");
    expect(url).toContain("channel=cron");
    expect(url).toContain("status=failed");
    expect(url).toContain("project_id=project-1");
  });
});

describe("trackProcessedRealtimeID", () => {
  it("keeps processed realtime ids bounded while preserving dedupe behavior", () => {
    const ids = new Set<string>();
    const order: string[] = [];

    for (let i = 0; i < 1005; i += 1) {
      expect(trackProcessedRealtimeID(ids, order, `evt-${i}`, 1000)).toBe(true);
    }

    expect(ids.size).toBe(1000);
    expect(order).toHaveLength(1000);
    expect(ids.has("evt-0")).toBe(false);
    expect(ids.has("evt-4")).toBe(false);
    expect(ids.has("evt-5")).toBe(true);

    expect(trackProcessedRealtimeID(ids, order, "evt-1004", 1000)).toBe(false);
    expect(trackProcessedRealtimeID(ids, order, "evt-0", 1000)).toBe(true);
    expect(ids.size).toBe(1000);
  });
});

describe("useAgentActivity", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
    vi.restoreAllMocks();
    wsLastMessage = null;
  });

  it("fetches agent activity and applies filters + pagination", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            items: [
              {
                id: "evt-1",
                org_id: "org-123",
                agent_id: "main",
                session_key: "agent:main:main",
                trigger: "chat.slack",
                channel: "slack",
                summary: "Responded in Slack",
                tokens_used: 10,
                duration_ms: 200,
                status: "completed",
                started_at: "2026-02-08T20:10:00.000Z",
                created_at: "2026-02-08T20:10:00.000Z",
              },
            ],
            next_before: "2026-02-08T20:00:00.000Z",
          }),
          { status: 200 },
        ),
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            items: [
              {
                id: "evt-2",
                org_id: "org-123",
                agent_id: "main",
                session_key: "agent:main:main",
                trigger: "cron.scheduled",
                summary: "Ran cron",
                tokens_used: 5,
                duration_ms: 120,
                status: "completed",
                started_at: "2026-02-08T20:09:00.000Z",
                created_at: "2026-02-08T20:09:00.000Z",
              },
            ],
          }),
          { status: 200 },
        ),
      );

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    const { result } = renderHook(() =>
      useAgentActivity({ mode: "agent", agentId: "main", limit: 1 }),
    );

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
      expect(result.current.events).toHaveLength(1);
    });

    expect(fetchMock.mock.calls[0]?.[0]).toContain("/api/agents/main/activity");
    expect(fetchMock.mock.calls[0]?.[0]).toContain("org_id=org-123");
    expect(result.current.hasMore).toBe(true);

    await act(async () => {
      await result.current.loadMore();
    });

    await waitFor(() => {
      expect(result.current.events).toHaveLength(2);
    });
    expect(fetchMock.mock.calls[1]?.[0]).toContain("before=2026-02-08T20%3A00%3A00.000Z");
  });

  it("refetches when filters change", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify({ items: [] }), {
          status: 200,
        }),
      );
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    const { result } = renderHook(() => useAgentActivity({ mode: "recent", limit: 10 }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    act(() => {
      result.current.setFilters({ trigger: "cron.scheduled", agentId: "main" });
    });

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(2);
    });

    const secondUrl = String(fetchMock.mock.calls[1]?.[0]);
    expect(secondUrl).toContain("trigger=cron.scheduled");
    expect(secondUrl).toContain("agent_id=main");
  });

  it("returns a missing-org error without calling fetch", async () => {
    localStorage.removeItem("otter-camp-org-id");
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    const { result } = renderHook(() => useAgentActivity({ mode: "recent" }));

    await waitFor(() => {
      expect(result.current.error).toBe("Missing org_id");
    });

    expect(fetchMock).not.toHaveBeenCalled();
    expect(result.current.events).toEqual([]);
  });

  it("appends realtime activity websocket events and dedupes by id", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          items: [
            {
              id: "evt-1",
              org_id: "org-123",
              agent_id: "main",
              session_key: "agent:main:main",
              trigger: "chat.slack",
              summary: "Initial event",
              status: "completed",
              tokens_used: 10,
              duration_ms: 120,
              started_at: "2026-02-08T20:00:00.000Z",
              created_at: "2026-02-08T20:00:00.000Z",
            },
          ],
        }),
        { status: 200 },
      ),
    );
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    const { result, rerender } = renderHook(() => useAgentActivity({ mode: "recent" }));

    await waitFor(() => {
      expect(result.current.events).toHaveLength(1);
    });

    act(() => {
      wsLastMessage = {
        type: "ActivityEventReceived",
        data: {
          event: {
            id: "evt-2",
            org_id: "org-123",
            agent_id: "main",
            session_key: "agent:main:main",
            trigger: "cron.scheduled",
            summary: "Realtime event",
            status: "completed",
            tokens_used: 8,
            duration_ms: 95,
            started_at: "2026-02-08T20:01:00.000Z",
            created_at: "2026-02-08T20:01:00.000Z",
          },
        },
      };
      rerender();
    });

    await waitFor(() => {
      expect(result.current.events).toHaveLength(2);
      expect(result.current.events[0]?.id).toBe("evt-2");
    });

    act(() => {
      wsLastMessage = {
        type: "ActivityEventReceived",
        data: {
          event: {
            id: "evt-2",
            org_id: "org-123",
            agent_id: "main",
            session_key: "agent:main:main",
            trigger: "cron.scheduled",
            summary: "Realtime duplicate",
            status: "completed",
            tokens_used: 8,
            duration_ms: 95,
            started_at: "2026-02-08T20:01:00.000Z",
            created_at: "2026-02-08T20:01:00.000Z",
          },
        },
      };
      rerender();
    });

    await waitFor(() => {
      expect(result.current.events).toHaveLength(2);
    });
  });

  it("ignores realtime activity events that do not match active filters", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ items: [] }), {
        status: 200,
      }),
    );
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    const { result, rerender } = renderHook(() =>
      useAgentActivity({ mode: "recent", initialFilters: { status: "failed" } }),
    );

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
      expect(result.current.events).toEqual([]);
    });

    act(() => {
      wsLastMessage = {
        type: "ActivityEventReceived",
        data: {
          event: {
            id: "evt-3",
            org_id: "org-123",
            agent_id: "main",
            session_key: "agent:main:main",
            trigger: "chat.slack",
            summary: "Completed realtime event",
            status: "completed",
            tokens_used: 5,
            duration_ms: 80,
            started_at: "2026-02-08T20:02:00.000Z",
            created_at: "2026-02-08T20:02:00.000Z",
          },
        },
      };
      rerender();
    });

    await waitFor(() => {
      expect(result.current.events).toEqual([]);
    });
  });
});
