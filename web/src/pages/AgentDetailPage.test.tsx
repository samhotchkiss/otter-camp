import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import AgentDetailPage from "./AgentDetailPage";

describe("AgentDetailPage", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("loads and renders activity timeline for agent route", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/admin/agents/main")) {
        return new Response(
          JSON.stringify({
            agent: {
              id: "main",
              workspace_agent_id: "11111111-1111-1111-1111-111111111111",
              name: "Main",
              status: "online",
            },
          }),
          { status: 200 },
        );
      }
      if (url.includes("/api/agents/main/activity")) {
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
                tokens_used: 45,
                duration_ms: 1100,
                started_at: "2026-02-08T12:55:00.000Z",
                created_at: "2026-02-08T12:55:00.000Z",
              },
            ],
          }),
          { status: 200 },
        );
      }

      throw new Error(`unexpected url: ${url}`);
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter initialEntries={["/agents/main"]}>
        <Routes>
          <Route path="/agents/:id" element={<AgentDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    fireEvent.click(await screen.findByRole("button", { name: "Activity" }));
    expect(await screen.findByText("Ran codex-progress-summary")).toBeInTheDocument();
    expect(screen.getByText("Cron")).toBeInTheDocument();
  });

  it("applies filter controls to activity query", async () => {
    const urls: string[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      urls.push(url);
      if (url.includes("/api/admin/agents/main")) {
        return new Response(
          JSON.stringify({
            agent: {
              id: "main",
              workspace_agent_id: "11111111-1111-1111-1111-111111111111",
              name: "Main",
              status: "online",
            },
          }),
          { status: 200 },
        );
      }
      if (url.includes("/api/agents/main/activity")) {
        return new Response(JSON.stringify({ items: [] }), { status: 200 });
      }
      throw new Error(`unexpected url: ${url}`);
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter initialEntries={["/agents/main"]}>
        <Routes>
          <Route path="/agents/:id" element={<AgentDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    fireEvent.click(await screen.findByRole("button", { name: "Activity" }));
    expect(await screen.findByText("No activity events for this agent yet.")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Status"), {
      target: { value: "failed" },
    });

    await waitFor(() => {
      expect(urls.length).toBeGreaterThanOrEqual(2);
    });

    expect(urls[urls.length - 1]).toContain("status=failed");
  });

  it("renders memory tab timeline and search interactions", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/admin/agents/main")) {
        return new Response(
          JSON.stringify({
            agent: {
              id: "main",
              workspace_agent_id: "11111111-1111-1111-1111-111111111111",
              name: "Main",
              status: "online",
            },
          }),
          { status: 200 },
        );
      }
      if (url.includes("/api/agents/main/activity")) {
        return new Response(JSON.stringify({ items: [] }), { status: 200 });
      }
      if (url.includes("/api/memory/entries?agent_id=11111111-1111-1111-1111-111111111111")) {
        return new Response(
          JSON.stringify({
            items: [
              {
                id: "entry-1",
                agent_id: "11111111-1111-1111-1111-111111111111",
                kind: "decision",
                title: "Choose pgvector",
                content: "Use pgvector for memory retrieval.",
                importance: 4,
                confidence: 0.8,
                sensitivity: "internal",
                status: "active",
                occurred_at: "2026-02-10T16:00:00Z",
                updated_at: "2026-02-10T16:00:00Z",
              },
            ],
            total: 1,
          }),
          { status: 200 },
        );
      }
      if (url.includes("/api/memory/search?agent_id=11111111-1111-1111-1111-111111111111")) {
        return new Response(
          JSON.stringify({
            items: [
              {
                id: "entry-2",
                agent_id: "11111111-1111-1111-1111-111111111111",
                kind: "lesson",
                title: "Search hit",
                content: "Compaction recovery should fail closed.",
                importance: 5,
                confidence: 0.9,
                sensitivity: "internal",
                status: "active",
                occurred_at: "2026-02-10T16:20:00Z",
                updated_at: "2026-02-10T16:20:00Z",
              },
            ],
            total: 1,
          }),
          { status: 200 },
        );
      }
      if (url.includes("/api/memory/recall?agent_id=11111111-1111-1111-1111-111111111111")) {
        return new Response(JSON.stringify({ context: "[RECALLED CONTEXT]\n- [lesson] Compaction recovery should fail closed." }), { status: 200 });
      }

      throw new Error(`unexpected url: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter initialEntries={["/agents/main"]}>
        <Routes>
          <Route path="/agents/:id" element={<AgentDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    fireEvent.click(await screen.findByRole("button", { name: "Memory" }));

    expect(await screen.findByText("Use pgvector for memory retrieval.")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Query"), {
      target: { value: "compaction recovery" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));

    expect(await screen.findByText("Search hit")).toBeInTheDocument();
    expect(await screen.findByText(/\[RECALLED CONTEXT\]/)).toBeInTheDocument();
  });
});
