import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AgentMemoryBrowser from "./AgentMemoryBrowser";

describe("AgentMemoryBrowser", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("loads memory files and previews selected date", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/admin/agents/main/memory/2026-02-08")) {
        return new Response(
          JSON.stringify({
            ref: "HEAD",
            path: "/memory/2026-02-08.md",
            content: "# Memory\n- Investigated queue latency",
            encoding: "utf-8",
            size: 37,
          }),
          { status: 200 },
        );
      }
      if (url.includes("/api/admin/agents/main/memory/2026-02-07")) {
        return new Response(
          JSON.stringify({
            ref: "HEAD",
            path: "/memory/2026-02-07.md",
            content: "# Memory\n- Reviewed deploy logs",
            encoding: "utf-8",
            size: 31,
          }),
          { status: 200 },
        );
      }
      if (url.includes("/api/admin/agents/main/memory")) {
        return new Response(
          JSON.stringify({
            ref: "HEAD",
            path: "/memory",
            entries: [
              { name: "2026-02-08.md", type: "file", path: "2026-02-08.md" },
              { name: "2026-02-07.md", type: "file", path: "2026-02-07.md" },
            ],
          }),
          { status: 200 },
        );
      }
      throw new Error(`unexpected url: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentMemoryBrowser agentID="main" />);

    expect(await screen.findByRole("button", { name: "2026-02-08.md" })).toBeInTheDocument();
    await waitFor(() => {
      const textarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
      expect(textarea.value).toContain("Investigated queue latency");
    });
    expect(screen.getByText("Ref: HEAD")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "2026-02-07.md" }));
    await waitFor(() => {
      const textarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
      expect(textarea.value).toContain("Reviewed deploy logs");
    });
  });

  it("renders an empty state when memory files are absent", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/admin/agents/main/memory")) {
        return new Response(JSON.stringify({ ref: "HEAD", path: "/memory", entries: [] }), { status: 200 });
      }
      throw new Error(`unexpected url: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentMemoryBrowser agentID="main" />);
    expect(await screen.findByText("No memory files found for this agent.")).toBeInTheDocument();
  });

  it("renders fetch errors", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ error: "boom" }), { status: 500 }));
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentMemoryBrowser agentID="main" />);
    await waitFor(() => {
      expect(screen.getByText(/Failed to load memory files/)).toBeInTheDocument();
    });
  });
});
