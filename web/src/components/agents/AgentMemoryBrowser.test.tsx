import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AgentMemoryBrowser from "./AgentMemoryBrowser";

function getAuthorizationHeader(init?: RequestInit): string | undefined {
  const headers = init?.headers;
  if (!headers) {
    return undefined;
  }
  if (headers instanceof Headers) {
    return headers.get("Authorization") ?? undefined;
  }
  if (Array.isArray(headers)) {
    const entry = headers.find(([name]) => name.toLowerCase() === "authorization");
    return entry?.[1];
  }
  const record = headers as Record<string, string>;
  return record.Authorization ?? record.authorization;
}

describe("AgentMemoryBrowser", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it("loads memory entries and supports creating a new memory entry", async () => {
    localStorage.setItem("otter_camp_token", "test-token");

    let loadCount = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = String(init?.method || "GET").toUpperCase();
      if (url.includes("/api/agents/11111111-1111-1111-1111-111111111111/memory") && method === "GET") {
        loadCount += 1;
        return new Response(
          JSON.stringify({
            agent_id: "11111111-1111-1111-1111-111111111111",
            daily: [
              {
                id: "daily-1",
                agent_id: "11111111-1111-1111-1111-111111111111",
                kind: "daily",
                date: loadCount > 1 ? "2026-02-09" : "2026-02-08",
                content: loadCount > 1 ? "Shipped cutover checklist." : "Investigated queue latency",
                created_at: "2026-02-09T00:00:00Z",
                updated_at: "2026-02-09T00:00:00Z",
              },
            ],
            long_term: [
              {
                id: "lt-1",
                agent_id: "11111111-1111-1111-1111-111111111111",
                kind: "long_term",
                content: "Prefers short standups.",
                created_at: "2026-02-08T00:00:00Z",
                updated_at: "2026-02-08T00:00:00Z",
              },
            ],
          }),
          { status: 200 },
        );
      }
      if (url.includes("/api/agents/11111111-1111-1111-1111-111111111111/memory") && method === "POST") {
        return new Response(
          JSON.stringify({ id: "daily-new", kind: "daily", date: "2026-02-09", content: "Shipped cutover checklist." }),
          { status: 201 },
        );
      }
      throw new Error(`unexpected url: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentMemoryBrowser agentID="main" workspaceAgentID="11111111-1111-1111-1111-111111111111" />);

    expect(await screen.findByText("Investigated queue latency")).toBeInTheDocument();
    expect(screen.getByText("Prefers short standups.")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Kind"), { target: { value: "daily" } });
    fireEvent.change(screen.getByLabelText("Date"), { target: { value: "2026-02-09" } });
    fireEvent.change(screen.getByLabelText("Memory Content"), { target: { value: "Shipped cutover checklist." } });
    fireEvent.click(screen.getByRole("button", { name: "Save Memory" }));

    await waitFor(() => {
      expect(
        fetchMock.mock.calls.some(([request, requestInit]) => {
          if (!String(request).includes("/api/agents/11111111-1111-1111-1111-111111111111/memory")) {
            return false;
          }
          const method = String(requestInit?.method || "GET").toUpperCase();
          if (method !== "POST") {
            return false;
          }
          const body = JSON.parse(String(requestInit?.body || "{}")) as Record<string, unknown>;
          return body.kind === "daily" && body.date === "2026-02-09" && body.content === "Shipped cutover checklist.";
        }),
      ).toBe(true);
    });

    expect(
      fetchMock.mock.calls
        .filter(([request]) => String(request).includes("/api/agents/11111111-1111-1111-1111-111111111111/memory"))
        .every(([, requestInit]) => getAuthorizationHeader(requestInit) === "Bearer test-token"),
    ).toBe(true);

    expect(await screen.findByText("Saved memory entry.")).toBeInTheDocument();
    expect(await screen.findByText("Shipped cutover checklist.")).toBeInTheDocument();
  });

  it("renders an empty state when memory entries are absent", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/agents/11111111-1111-1111-1111-111111111111/memory")) {
        return new Response(JSON.stringify({ agent_id: "11111111-1111-1111-1111-111111111111", daily: [], long_term: [] }), { status: 200 });
      }
      throw new Error(`unexpected url: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentMemoryBrowser agentID="main" workspaceAgentID="11111111-1111-1111-1111-111111111111" />);
    expect(await screen.findByText("No memory entries for this agent yet.")).toBeInTheDocument();
  });

  it("renders fetch errors", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ error: "boom" }), { status: 500 }));
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentMemoryBrowser agentID="main" workspaceAgentID="11111111-1111-1111-1111-111111111111" />);
    await waitFor(() => {
      expect(screen.getByText(/Failed to load memory entries/)).toBeInTheDocument();
    });
  });

  it("aborts in-flight memory load requests on unmount", async () => {
    let capturedSignal: AbortSignal | undefined;
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      capturedSignal = init?.signal as AbortSignal | undefined;
      return new Promise<Response>(() => {
        // Keep request pending so unmount can trigger cancellation.
      });
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    const { unmount } = render(
      <AgentMemoryBrowser agentID="main" workspaceAgentID="11111111-1111-1111-1111-111111111111" />,
    );

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
    expect(capturedSignal).toBeDefined();
    expect(capturedSignal?.aborted).toBe(false);

    unmount();
    expect(capturedSignal?.aborted).toBe(true);
  });
});
