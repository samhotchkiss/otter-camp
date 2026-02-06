import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import KnowledgePage from "./KnowledgePage";

describe("KnowledgePage", () => {
  beforeEach(() => {
    localStorage.setItem("otter-camp-org-id", "00000000-0000-0000-0000-000000000001");
    localStorage.setItem("otter_camp_token", "token");
  });

  it("loads knowledge entries from the API instead of demo constants", async () => {
    global.fetch = vi.fn().mockResolvedValue(
      new Response(
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
      ),
    ) as unknown as typeof fetch;

    render(<KnowledgePage />);

    expect(await screen.findByText("Stone real dataset entry")).toBeInTheDocument();
    expect(screen.queryByText("Sam's email preferences")).not.toBeInTheDocument();
    expect(global.fetch).toHaveBeenCalledWith(
      expect.stringContaining("/api/knowledge"),
      expect.any(Object),
    );
  });
});
