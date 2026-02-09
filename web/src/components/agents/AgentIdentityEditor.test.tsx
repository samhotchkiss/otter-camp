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

  it("saves edited identity content through project commit endpoint", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/projects") && (init?.method || "GET") === "GET") {
        return new Response(
          JSON.stringify({
            projects: [
              { id: "project-agent-files", name: "Agent Files" },
            ],
          }),
          { status: 200 },
        );
      }
      if (url.includes("/api/projects/project-agent-files/commits") && (init?.method || "GET") === "POST") {
        return new Response(
          JSON.stringify({
            project_id: "project-agent-files",
            path: "/agents/main/SOUL.md",
            commit_type: "system",
            auto_generated_message: false,
            commit: {
              id: "commit-1",
              project_id: "project-agent-files",
              repository_full_name: "otter/agent-files",
              branch_name: "main",
              sha: "abc123",
              author_name: "OtterCamp Browser",
              authored_at: "2026-02-09T00:00:00Z",
              subject: "Update agent identity file",
              message: "Update agent identity file",
              created_at: "2026-02-09T00:00:00Z",
              updated_at: "2026-02-09T00:00:00Z",
            },
          }),
          { status: 201 },
        );
      }
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
      if (url.includes("/api/admin/agents/main/files")) {
        return new Response(
          JSON.stringify({
            ref: "HEAD",
            path: "/",
            entries: [{ name: "SOUL.md", type: "file", path: "SOUL.md" }],
          }),
          { status: 200 },
        );
      }
      throw new Error(`unexpected url: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentIdentityEditor agentID="main" />);

    expect(await screen.findByRole("button", { name: "SOUL.md" })).toBeInTheDocument();
    const editor = await screen.findByTestId("source-textarea");
    fireEvent.change(editor, { target: { value: "# SOUL\nCalm under pressure." } });
    fireEvent.click(screen.getByRole("button", { name: "Save Identity File" }));

    await waitFor(() => {
      expect(
        fetchMock.mock.calls.some(([request, requestInit]) => {
          if (!String(request).includes("/api/projects/project-agent-files/commits")) {
            return false;
          }
          const method = String(requestInit?.method || "GET").toUpperCase();
          if (method !== "POST") {
            return false;
          }
          const body = JSON.parse(String(requestInit?.body || "{}")) as Record<string, unknown>;
          return body.path === "/agents/main/SOUL.md" && body.commit_type === "system";
        }),
      ).toBe(true);
    });

    expect(await screen.findByText("Saved identity file.")).toBeInTheDocument();
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
