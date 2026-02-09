import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import AgentDetailPage from "./AgentDetailPage";

describe("AgentDetailPage", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("loads and renders agent detail heading", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/admin/agents/main")) {
        return new Response(
          JSON.stringify({
            id: "main",
            name: "Frank",
            status: "online",
            model: "claude-opus-4-6",
          }),
          { status: 200 },
        );
      }
      // activity hook
      if (url.includes("/api/agents/main/activity")) {
        return new Response(JSON.stringify({ items: [] }), { status: 200 });
      }
      return new Response(JSON.stringify({}), { status: 200 });
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(
      <MemoryRouter initialEntries={["/agents/main"]}>
        <Routes>
          <Route path="/agents/:id" element={<AgentDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(
      await screen.findByRole("heading", { name: "Agent Details" }),
    ).toBeInTheDocument();
  });
});
