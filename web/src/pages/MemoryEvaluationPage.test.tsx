import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import MemoryEvaluationPage from "./MemoryEvaluationPage";

describe("MemoryEvaluationPage", () => {
  beforeEach(() => {
    localStorage.setItem("otter-camp-org-id", "00000000-0000-0000-0000-000000000001");
    localStorage.setItem("otter_camp_token", "token");
  });

  it("renders loading state while evaluation request is pending", () => {
    global.fetch = vi.fn(
      () =>
        new Promise<Response>(() => {
          // Intentionally unresolved to keep page in loading state.
        }),
    ) as unknown as typeof fetch;

    render(
      <MemoryRouter>
        <MemoryEvaluationPage />
      </MemoryRouter>,
    );

    expect(screen.getByText("Loading evaluation status...")).toBeInTheDocument();
  });

  it("renders error state when evaluation request fails", async () => {
    global.fetch = vi.fn(async () => {
      return new Response(JSON.stringify({ error: "backend unavailable" }), {
        status: 500,
        headers: { "Content-Type": "application/json" },
      });
    }) as unknown as typeof fetch;

    render(
      <MemoryRouter>
        <MemoryEvaluationPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Unable to load memory evaluation status: backend unavailable")).toBeInTheDocument();
  });

  it("renders pass/fail metrics and failed gates from latest run", async () => {
    global.fetch = vi.fn(async () => {
      return new Response(
        JSON.stringify({
          run: {
            id: "eval-2026-02-10",
            created_at: "2026-02-10T18:00:00Z",
            passed: false,
            failed_gates: ["precision_at_k", "false_injection_rate"],
            metrics: {
              precision_at_k: 0.42,
              false_injection_rate: 0.26,
              recovery_success_rate: 0.88,
              p95_latency_ms: 321,
            },
          },
        }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      );
    }) as unknown as typeof fetch;

    render(
      <MemoryRouter>
        <MemoryEvaluationPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText("fail")).toBeInTheDocument();
    expect(await screen.findByText("0.42")).toBeInTheDocument();
    expect(await screen.findByText("0.26")).toBeInTheDocument();
    expect(await screen.findByText("0.88")).toBeInTheDocument();
    expect(await screen.findByText("321ms")).toBeInTheDocument();
    expect(await screen.findByText("precision_at_k")).toBeInTheDocument();
    expect(await screen.findByText("false_injection_rate")).toBeInTheDocument();
  });
});
