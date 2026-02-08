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
      if (url.includes("/api/sync/agents")) {
        return new Response(
          JSON.stringify({
            agents: [{ id: "main", name: "Frank", role: "Chief of Staff", status: "online" }],
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

    expect(await screen.findByRole("heading", { name: "Agent Activity" })).toBeInTheDocument();
    expect(await screen.findByText("Timeline for Frank (Chief of Staff)")).toBeInTheDocument();
    expect(await screen.findByText("Ran codex-progress-summary")).toBeInTheDocument();
    expect(screen.getByText("Cron")).toBeInTheDocument();
  });

  it("applies filter controls to activity query", async () => {
    const urls: string[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      urls.push(url);
      if (url.includes("/api/sync/agents")) {
        return new Response(JSON.stringify({ agents: [] }), { status: 200 });
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

    expect(await screen.findByRole("heading", { name: "Agent Activity" })).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Status"), {
      target: { value: "failed" },
    });

    await waitFor(() => {
      expect(urls.length).toBeGreaterThanOrEqual(2);
    });

    expect(urls[urls.length - 1]).toContain("status=failed");
  });
});
