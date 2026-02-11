import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import userEvent from "@testing-library/user-event";
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

  it("treats missing evaluation endpoint as unavailable-not-error", async () => {
    global.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/knowledge")) {
        return new Response(
          JSON.stringify({
            items: [
              {
                id: "kb-404-eval",
                title: "Feed still works",
                content: "Knowledge entries should render even if eval endpoint is absent.",
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
        return new Response(JSON.stringify({ error: "not found" }), {
          status: 404,
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

    expect(await screen.findByText("Feed still works")).toBeInTheDocument();
    expect(screen.queryByText(/Evaluation unavailable/)).not.toBeInTheDocument();
    expect(await screen.findByText("No evaluation runs recorded yet.")).toBeInTheDocument();
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

  it("creates a knowledge entry from the New Entry flow", async () => {
    localStorage.setItem("otter-camp-user-name", "Sam");
    let knowledgeListCalls = 0;
    let capturedImportBody: unknown = null;
    global.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/knowledge/import")) {
        capturedImportBody = typeof init?.body === "string" ? JSON.parse(init.body) : null;
        return new Response(JSON.stringify({ inserted: 2 }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.includes("/api/knowledge")) {
        const items = knowledgeListCalls === 0
          ? [
              {
                id: "kb-existing",
                title: "Existing entry",
                content: "This already exists.",
                tags: ["ops"],
                created_by: "Stone",
                created_at: "2026-02-06T12:00:00Z",
                updated_at: "2026-02-06T12:00:00Z",
              },
            ]
          : [
              {
                id: "kb-existing",
                title: "Existing entry",
                content: "This already exists.",
                tags: ["ops"],
                created_by: "Stone",
                created_at: "2026-02-06T12:00:00Z",
                updated_at: "2026-02-06T12:00:00Z",
              },
              {
                id: "kb-new",
                title: "How to add KB entries",
                content: "Use + New Entry in the Knowledge page.",
                tags: ["knowledge", "onboarding"],
                created_by: "Sam",
                created_at: "2026-02-11T06:20:00Z",
                updated_at: "2026-02-11T06:20:00Z",
              },
            ];
        knowledgeListCalls += 1;
        return new Response(JSON.stringify({ items, total: items.length }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.includes("/api/memory/evaluations/latest")) {
        return new Response(JSON.stringify({ run: null }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      throw new Error(`unexpected url: ${url}`);
    }) as unknown as typeof fetch;

    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <KnowledgePage />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Existing entry")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "+ New Entry" }));
    await user.type(screen.getByLabelText("Title"), "How to add KB entries");
    await user.type(screen.getByLabelText("Content"), "Use + New Entry in the Knowledge page.");
    await user.type(screen.getByLabelText("Tags (comma-separated)"), "knowledge, onboarding");
    await user.click(screen.getByRole("button", { name: "Create entry" }));

    expect(await screen.findByText("How to add KB entries")).toBeInTheDocument();
    expect(capturedImportBody).toMatchObject({
      entries: [
        expect.objectContaining({ title: "Existing entry" }),
        expect.objectContaining({
          title: "How to add KB entries",
          created_by: "Sam",
          tags: ["knowledge", "onboarding"],
        }),
      ],
    });
  });
});
