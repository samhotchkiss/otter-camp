import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import GitHubSettings from "./GitHubSettings";

describe("GitHubSettings", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it("loads integration status, repos, and project settings from API", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/github/integration/status")) {
        return new Response(
          JSON.stringify({
            connected: true,
            installation: {
              installation_id: 123,
              account_login: "the-trawl",
              account_type: "Organization",
              connected_at: "2026-02-06T00:00:00Z",
            },
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      }
      if (url.includes("/api/github/integration/repos")) {
        return new Response(
          JSON.stringify({
            repos: [
              {
                id: "repo-1",
                full_name: "the-trawl/otter-camp",
                default_branch: "main",
                private: false,
              },
            ],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      }
      if (url.includes("/api/github/integration/settings")) {
        return new Response(
          JSON.stringify({
            projects: [
              {
                project_id: "proj-1",
                project_name: "Otter Camp",
                description: "Task management",
                enabled: true,
                repo_full_name: "the-trawl/otter-camp",
                default_branch: "main",
                sync_mode: "sync",
                auto_sync: true,
                active_branches: ["main"],
                last_synced_at: "2026-02-06T00:10:00Z",
                conflict_state: "none",
              },
            ],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      }

      return new Response(JSON.stringify({ error: "unexpected endpoint" }), {
        status: 500,
        headers: { "Content-Type": "application/json" },
      });
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<GitHubSettings />);

    await waitFor(() => {
      expect(screen.getByText("@the-trawl")).toBeInTheDocument();
    });

    expect(screen.getByText("Otter Camp")).toBeInTheDocument();
    expect(screen.getAllByText("the-trawl/otter-camp").length).toBeGreaterThan(0);
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });
});
