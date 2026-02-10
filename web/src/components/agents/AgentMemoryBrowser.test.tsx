import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AgentMemoryBrowser from "./AgentMemoryBrowser";

describe("AgentMemoryBrowser", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
    localStorage.setItem("otter_camp_token", "token-123");
  });

  it("loads timeline entries, writes memory, and supports semantic search + recall preview", async () => {
    let timelineLoadCount = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = String(init?.method || "GET").toUpperCase();

      if (url.includes("/api/memory/entries?agent_id=11111111-1111-1111-1111-111111111111") && method === "GET") {
        timelineLoadCount += 1;
        return new Response(
          JSON.stringify({
            items: [
              {
                id: "entry-1",
                agent_id: "11111111-1111-1111-1111-111111111111",
                kind: "decision",
                title: "Choose pgvector",
                content: timelineLoadCount > 1 ? "Use pgvector and ship rollout notes." : "Use pgvector for memory retrieval.",
                importance: 4,
                confidence: 0.9,
                sensitivity: "internal",
                status: "active",
                occurred_at: "2026-02-10T16:00:00Z",
                updated_at: "2026-02-10T16:00:00Z",
              },
            ],
          }),
          { status: 200 },
        );
      }

      if (url.endsWith("/api/memory/entries") && method === "POST") {
        return new Response(
          JSON.stringify({
            id: "entry-2",
            agent_id: "11111111-1111-1111-1111-111111111111",
            kind: "decision",
            title: "Ship rollout notes",
            content: "Use pgvector and ship rollout notes.",
          }),
          { status: 201 },
        );
      }

      if (url.includes("/api/memory/search?agent_id=11111111-1111-1111-1111-111111111111")) {
        return new Response(
          JSON.stringify({
            items: [
              {
                id: "search-1",
                agent_id: "11111111-1111-1111-1111-111111111111",
                kind: "decision",
                title: "Search hit",
                content: "Compaction recovery should fail closed.",
                importance: 5,
                confidence: 0.91,
                sensitivity: "internal",
                status: "active",
                occurred_at: "2026-02-10T16:10:00Z",
                updated_at: "2026-02-10T16:10:00Z",
                relevance: 0.88,
              },
            ],
            total: 1,
          }),
          { status: 200 },
        );
      }

      if (url.includes("/api/memory/recall?agent_id=11111111-1111-1111-1111-111111111111")) {
        return new Response(
          JSON.stringify({
            context: "[RECALLED CONTEXT]\n- [decision] Compaction recovery should fail closed.",
          }),
          { status: 200 },
        );
      }

      throw new Error(`unexpected url: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentMemoryBrowser agentID="main" workspaceAgentID="11111111-1111-1111-1111-111111111111" />);

    expect(await screen.findByText("Use pgvector for memory retrieval.")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Kind"), { target: { value: "decision" } });
    fireEvent.change(screen.getByLabelText("Title"), { target: { value: "Ship rollout notes" } });
    fireEvent.change(screen.getByLabelText("Content"), { target: { value: "Use pgvector and ship rollout notes." } });
    fireEvent.click(screen.getByRole("button", { name: "Save Memory" }));

    expect(await screen.findByText("Saved memory entry.")).toBeInTheDocument();
    expect(await screen.findByText("Use pgvector and ship rollout notes.")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Query"), { target: { value: "compaction recovery" } });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));

    expect(await screen.findByText("Search hit")).toBeInTheDocument();
    expect(await screen.findByText(/\[RECALLED CONTEXT\]/)).toBeInTheDocument();
    expect((await screen.findAllByText(/Compaction recovery should fail closed\./)).length).toBeGreaterThan(0);

    const searched = fetchMock.mock.calls.some(([request]) => String(request).includes("/api/memory/search"));
    const recalled = fetchMock.mock.calls.some(([request]) => String(request).includes("/api/memory/recall"));
    expect(searched).toBe(true);
    expect(recalled).toBe(true);
  });

  it("renders empty timeline state", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/memory/entries?agent_id=11111111-1111-1111-1111-111111111111")) {
        return new Response(JSON.stringify({ items: [], total: 0 }), { status: 200 });
      }
      throw new Error(`unexpected url: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentMemoryBrowser agentID="main" workspaceAgentID="11111111-1111-1111-1111-111111111111" />);
    expect(await screen.findByText("No memory entries for this agent yet.")).toBeInTheDocument();
  });

  it("renders timeline errors without breaking search flow", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/memory/entries?agent_id=11111111-1111-1111-1111-111111111111")) {
        return new Response(JSON.stringify({ error: "timeline down" }), { status: 500 });
      }
      if (url.includes("/api/memory/search?agent_id=11111111-1111-1111-1111-111111111111")) {
        return new Response(
          JSON.stringify({
            items: [
              {
                id: "search-err-1",
                agent_id: "11111111-1111-1111-1111-111111111111",
                kind: "lesson",
                title: "Still searchable",
                content: "UI keeps working when timeline load fails.",
                importance: 3,
                confidence: 0.8,
                sensitivity: "internal",
                status: "active",
                occurred_at: "2026-02-10T16:10:00Z",
                updated_at: "2026-02-10T16:10:00Z",
              },
            ],
            total: 1,
          }),
          { status: 200 },
        );
      }
      if (url.includes("/api/memory/recall?agent_id=11111111-1111-1111-1111-111111111111")) {
        return new Response(JSON.stringify({ context: "" }), { status: 200 });
      }
      throw new Error(`unexpected url: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentMemoryBrowser agentID="main" workspaceAgentID="11111111-1111-1111-1111-111111111111" />);

    expect(await screen.findByText(/Failed to load memory entries/)).toBeInTheDocument();
    expect(screen.getByText("Search Memory")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Query"), { target: { value: "resilience" } });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));

    expect(await screen.findByText("Still searchable")).toBeInTheDocument();
  });

  it("aborts in-flight timeline requests on unmount", async () => {
    let capturedSignal: AbortSignal | undefined;
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      capturedSignal = init?.signal as AbortSignal | undefined;
      return new Promise<Response>(() => {
        // Keep pending until unmount aborts.
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
