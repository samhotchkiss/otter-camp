import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import AgentDetailPage from "./AgentDetailPage";

describe("AgentDetailPage", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("loads and renders tabbed agent detail with overview data", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/admin/agents/main")) {
        return new Response(
          JSON.stringify({
            agent: {
              id: "main",
              workspace_agent_id: "11111111-1111-1111-1111-111111111111",
              name: "Frank",
              status: "online",
              model: "gpt-5.2-codex",
              heartbeat_every: "15m",
              channel: "slack:#engineering",
              session_key: "agent:main:main",
              last_seen: "just now",
            },
            sync: {
              current_task: "Coordinating deployment",
              context_tokens: 4200,
              total_tokens: 22000,
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

    expect(await screen.findByRole("heading", { name: "Agent Details" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Overview" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Identity" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Memory" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Activity" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Settings" })).toBeInTheDocument();

    expect(await screen.findByText("Frank")).toBeInTheDocument();
    expect(await screen.findByText("gpt-5.2-codex")).toBeInTheDocument();
    expect(await screen.findByText("15m")).toBeInTheDocument();
    expect(await screen.findByText("Coordinating deployment")).toBeInTheDocument();
  });

  it("keeps activity filters working from the activity tab", async () => {
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
              name: "Frank",
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

    expect(await screen.findByRole("heading", { name: "Agent Details" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Activity" }));

    fireEvent.change(screen.getByLabelText("Status"), {
      target: { value: "failed" },
    });

    await waitFor(() => {
      expect(urls.length).toBeGreaterThanOrEqual(2);
    });

    expect(urls[urls.length - 1]).toContain("status=failed");
  });
});
