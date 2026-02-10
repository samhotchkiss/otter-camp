import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import KnowledgePage from "./KnowledgePage";

describe("KnowledgePage", () => {
  beforeEach(() => {
    localStorage.setItem("otter-camp-org-id", "00000000-0000-0000-0000-000000000001");
    localStorage.setItem("otter_camp_token", "token");
  });

  it("loads shared knowledge entries and evaluation summary from API", async () => {
    global.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/knowledge")) {
        return new Response(
          JSON.stringify({
            items: [
              {
                id: "kb-real-1",
                title: "Stone real dataset entry",
                content: "This content came from the backend API.",
                tags: ["stone", "knowledge"],
                created_by: "Stone",
                created_at: "2026-02-06T12:00:00Z",
                updated_at: "2026-02-06T12:00:00Z",
              },
            ],
            total: 1,
          }),
          {
            status: 200,
            headers: { "Content-Type": "application/json" },
          },
        );
      }
      if (url.includes("/api/memory/evaluations/latest")) {
        return new Response(
          JSON.stringify({
            run: {
              id: "eval-1",
              created_at: "2026-02-10T16:30:00Z",
              passed: true,
              failed_gates: [],
              metrics: {
                precision_at_k: 0.67,
                false_injection_rate: 0.08,
                recovery_success_rate: 0.91,
                p95_latency_ms: 125,
              },
            },
          }),
          {
            status: 200,
            headers: { "Content-Type": "application/json" },
          },
        );
      }
      throw new Error(`unexpected url: ${url}`);
    }) as unknown as typeof fetch;

    render(
      <MemoryRouter>
        <KnowledgePage />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Stone real dataset entry")).toBeInTheDocument();
    expect(await screen.findByText("Memory Evaluation")).toBeInTheDocument();
    expect(await screen.findByText("pass")).toBeInTheDocument();
    expect(await screen.findByRole("link", { name: "View dashboard" })).toHaveAttribute("href", "/knowledge/evaluation");
  });

  it("renders evaluation error state while keeping knowledge feed visible", async () => {
    global.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/knowledge")) {
        return new Response(
          JSON.stringify({
            items: [
              {
                id: "kb-real-2",
                title: "Still renders feed",
                content: "Knowledge list should remain visible.",
                tags: ["ops"],
                created_by: "Nova",
                created_at: "2026-02-06T12:00:00Z",
                updated_at: "2026-02-06T12:00:00Z",
              },
            ],
            total: 1,
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
      if (url.includes("/api/memory/evaluations/latest")) {
        return new Response(JSON.stringify({ error: "not ready" }), {
          status: 500,
          headers: { "Content-Type": "application/json" },
        });
      }
      throw new Error(`unexpected url: ${url}`);
    }) as unknown as typeof fetch;

    render(
      <MemoryRouter>
        <KnowledgePage />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Still renders feed")).toBeInTheDocument();
    expect(await screen.findByText(/Evaluation unavailable/)).toBeInTheDocument();
  });

  it("handles knowledge entries with null tags", async () => {
    global.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/knowledge")) {
        return new Response(
          JSON.stringify({
            items: [
              {
                id: "kb-null-tags",
                title: "Null tags should not crash",
                content: "Entry remains renderable even when tags are null.",
                tags: null,
                created_by: "Stone",
                created_at: "2026-02-06T12:00:00Z",
                updated_at: "2026-02-06T12:00:00Z",
              },
            ],
            total: 1,
          }),
          {
            status: 200,
            headers: { "Content-Type": "application/json" },
          },
        );
      }
      if (url.includes("/api/memory/evaluations/latest")) {
        return new Response(JSON.stringify({ run: null }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      throw new Error(`unexpected url: ${url}`);
    }) as unknown as typeof fetch;

    render(
      <MemoryRouter>
        <KnowledgePage />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Null tags should not crash")).toBeInTheDocument();
    expect(await screen.findByText("1 entries")).toBeInTheDocument();
  });
});
