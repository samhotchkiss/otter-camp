import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useWS } from "../contexts/WebSocketContext";
import type { WebSocketMessageType } from "../hooks/useWebSocket";
import useEmissions, { type Emission } from "./useEmissions";

vi.mock("../contexts/WebSocketContext", () => ({
  useWS: vi.fn(),
}));

const mockUseWS = vi.mocked(useWS);

type WSState = {
  connected: boolean;
  lastMessage: { type: WebSocketMessageType | "Unknown"; data: unknown } | null;
  sendMessage: ReturnType<typeof vi.fn>;
  reconnectReason: "initial" | "backoff" | "visibility";
};

const makeEmission = (overrides: Partial<Emission> = {}): Emission => ({
  id: "em-1",
  source_type: "agent",
  source_id: "agent-1",
  kind: "status",
  summary: "Started work",
  timestamp: "2026-02-08T12:00:00Z",
  ...overrides,
});

describe("useEmissions", () => {
  let wsState: WSState;

  beforeEach(() => {
    wsState = {
      connected: true,
      lastMessage: null,
      sendMessage: vi.fn(() => true),
      reconnectReason: "initial",
    };
    mockUseWS.mockImplementation(() => wsState);

    localStorage.setItem("otter-camp-org-id", "org-test");
    vi.stubGlobal("fetch", vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
    localStorage.clear();
  });

  it("loads recent emissions with requested scope filters", async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValue(
      new Response(
        JSON.stringify({
          items: [
            makeEmission({
              id: "em-11",
              source_id: "agent-11",
              scope: { project_id: "project-1", issue_id: "issue-1" },
            }),
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const { result } = renderHook(() =>
      useEmissions({
        projectId: "project-1",
        issueId: "issue-1",
        sourceId: "agent-11",
        limit: 10,
      }),
    );

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const firstCall = fetchMock.mock.calls[0];
    const requestURL = new URL(String(firstCall?.[0]), "http://localhost");
    expect(requestURL.searchParams.get("org_id")).toBe("org-test");
    expect(requestURL.searchParams.get("project_id")).toBe("project-1");
    expect(requestURL.searchParams.get("issue_id")).toBe("issue-1");
    expect(requestURL.searchParams.get("source_id")).toBe("agent-11");
    expect(requestURL.searchParams.get("limit")).toBe("10");

    expect(result.current.emissions).toHaveLength(1);
    expect(result.current.latestBySource.get("agent-11")?.id).toBe("em-11");
    expect(wsState.sendMessage).toHaveBeenCalledWith({
      type: "subscribe",
      topic: "project:project-1",
    });
    expect(wsState.sendMessage).toHaveBeenCalledWith({
      type: "subscribe",
      topic: "issue:issue-1",
    });
  });

  it("applies websocket emission updates and enforces scope filters", async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValue(
      new Response(JSON.stringify({ items: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const { result, rerender } = renderHook(() =>
      useEmissions({ projectId: "project-1", limit: 5 }),
    );

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });
    expect(result.current.emissions).toHaveLength(0);

    wsState.lastMessage = {
      type: "EmissionReceived",
      data: makeEmission({
        id: "em-live-1",
        source_id: "agent-live",
        scope: { project_id: "project-1" },
      }),
    };
    rerender();
    expect(result.current.emissions).toHaveLength(1);
    expect(result.current.emissions[0]?.id).toBe("em-live-1");

    wsState.lastMessage = {
      type: "EmissionReceived",
      data: makeEmission({
        id: "em-live-2",
        source_id: "agent-live",
        scope: { project_id: "project-2" },
      }),
    };
    rerender();
    expect(result.current.emissions).toHaveLength(1);

    wsState.lastMessage = {
      type: "EmissionReceived",
      data: makeEmission({
        id: "em-live-1",
        source_id: "agent-live",
        summary: "Updated summary",
        scope: { project_id: "project-1" },
      }),
    };
    rerender();
    expect(result.current.emissions).toHaveLength(1);
    expect(result.current.emissions[0]?.summary).toBe("Updated summary");
  });

  it("unsubscribes from scoped topics on cleanup", async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValue(
      new Response(JSON.stringify({ items: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const { unmount } = renderHook(() =>
      useEmissions({ projectId: "project-z", issueId: "issue-z" }),
    );

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    act(() => {
      unmount();
    });

    expect(wsState.sendMessage).toHaveBeenCalledWith({
      type: "unsubscribe",
      topic: "project:project-z",
    });
    expect(wsState.sendMessage).toHaveBeenCalledWith({
      type: "unsubscribe",
      topic: "issue:issue-z",
    });
  });
});
