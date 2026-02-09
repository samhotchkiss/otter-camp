import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AgentIdentityEditor from "./AgentIdentityEditor";

describe("AgentIdentityEditor", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("loads identity files and previews selected content", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/admin/agents/main/files/SOUL.md")) {
        return new Response(
          JSON.stringify({
            ref: "HEAD",
            path: "/SOUL.md",
            content: "# SOUL\nSteady and practical.",
            encoding: "utf-8",
            size: 28,
          }),
          { status: 200 },
        );
      }
      if (url.includes("/api/admin/agents/main/files/IDENTITY.md")) {
        return new Response(
          JSON.stringify({
            ref: "HEAD",
            path: "/IDENTITY.md",
            content: "# IDENTITY\n- Name: Frank",
            encoding: "utf-8",
            size: 24,
          }),
          { status: 200 },
        );
      }
      if (url.includes("/api/admin/agents/main/files")) {
        return new Response(
          JSON.stringify({
            ref: "HEAD",
            path: "/",
            entries: [
              { name: "SOUL.md", type: "file", path: "SOUL.md" },
              { name: "IDENTITY.md", type: "file", path: "IDENTITY.md" },
              { name: "memory", type: "dir", path: "memory/" },
            ],
          }),
          { status: 200 },
        );
      }
      throw new Error(`unexpected url: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentIdentityEditor agentID="main" />);

    expect(await screen.findByRole("button", { name: "SOUL.md" })).toBeInTheDocument();
    await waitFor(() => {
      const textarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
      expect(textarea.value).toContain("Steady and practical.");
    });
    expect(screen.getByText("Ref: HEAD")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "IDENTITY.md" }));
    await waitFor(() => {
      const textarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
      expect(textarea.value).toContain("- Name: Frank");
    });
  });

  it("renders an empty state when no identity files are present", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/admin/agents/main/files")) {
        return new Response(
          JSON.stringify({
            ref: "HEAD",
            path: "/",
            entries: [{ name: "memory", type: "dir", path: "memory/" }],
          }),
          { status: 200 },
        );
      }
      throw new Error(`unexpected url: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentIdentityEditor agentID="main" />);
    expect(await screen.findByText("No identity files found for this agent.")).toBeInTheDocument();
  });

  it("renders API errors", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ error: "boom" }), { status: 500 }));
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentIdentityEditor agentID="main" />);
    await waitFor(() => {
      expect(screen.getByText(/Failed to load identity files/)).toBeInTheDocument();
    });
  });
});
