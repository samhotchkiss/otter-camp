import { describe, it, expect, beforeEach, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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

  it("shows local issue-review mode label for push-mode projects", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/github/integration/status")) {
        return new Response(JSON.stringify({ connected: true, installation: null }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.includes("/api/github/integration/repos")) {
        return new Response(JSON.stringify({ repos: [] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.includes("/api/github/integration/settings")) {
        return new Response(
          JSON.stringify({
            projects: [
              {
                project_id: "proj-1",
                project_name: "Technonymous",
                description: "Writing workflow",
                enabled: true,
                repo_full_name: "samhotchkiss/otter-camp",
                default_branch: "main",
                sync_mode: "push",
                workflow_mode: "local_issue_review",
                github_pr_enabled: false,
                auto_sync: true,
                active_branches: ["main"],
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
      expect(screen.getByText("Technonymous")).toBeInTheDocument();
    });

    expect(screen.getAllByText(/Issue Review \(local only\)/).length).toBeGreaterThan(0);
  });

  it("renders dry-run summary with blocking and non-blocking checks", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/github/integration/status")) {
        return new Response(JSON.stringify({ connected: true, installation: { installation_id: 1, account_login: "sam", account_type: "Organization", connected_at: "2026-02-06T00:00:00Z" } }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.includes("/api/github/integration/repos")) {
        return new Response(JSON.stringify({ repos: [{ id: "repo-1", full_name: "samhotchkiss/otter-camp", default_branch: "main", private: false }] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.includes("/api/github/integration/settings")) {
        return new Response(JSON.stringify({
          projects: [{
            project_id: "proj-1",
            project_name: "Otter Camp",
            description: null,
            enabled: true,
            repo_full_name: "samhotchkiss/otter-camp",
            default_branch: "main",
            sync_mode: "sync",
            auto_sync: true,
            active_branches: ["main"],
            conflict_state: "none",
          }],
        }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      if (url.includes("/api/projects/proj-1/publish")) {
        const body = JSON.parse(String(init?.body || "{}"));
        if (!body.dry_run) {
          return new Response(JSON.stringify({ error: "expected dry run call first" }), {
            status: 500,
            headers: { "Content-Type": "application/json" },
          });
        }
        return new Response(JSON.stringify({
          project_id: "proj-1",
          dry_run: true,
          status: "dry_run",
          checks: [
            { name: "fast_forward", status: "fail", detail: "Remote branch diverged.", blocking: true },
            { name: "commits_ahead", status: "info", detail: "Local branch is 2 commits ahead.", blocking: false },
          ],
          commits_ahead: 2,
        }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      return new Response(JSON.stringify({ error: "unexpected endpoint" }), {
        status: 500,
        headers: { "Content-Type": "application/json" },
      });
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);
    render(<GitHubSettings />);

    await waitFor(() => {
      expect(screen.getByText("Otter Camp")).toBeInTheDocument();
    });

    const details = screen.getByText("Otter Camp").closest("details");
    details?.setAttribute("open", "");
    fireEvent.click(screen.getByRole("button", { name: "Dry run" }));

    await waitFor(() => {
      expect(screen.getByText("Dry-run summary")).toBeInTheDocument();
    });
    expect(screen.getByText(/Blocking/)).toBeInTheDocument();
    expect(screen.getByText(/Non-blocking/)).toBeInTheDocument();
  });

  it("executes publish and updates progress log/state", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/github/integration/status")) {
        return new Response(JSON.stringify({ connected: true, installation: { installation_id: 1, account_login: "sam", account_type: "Organization", connected_at: "2026-02-06T00:00:00Z" } }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.includes("/api/github/integration/repos")) {
        return new Response(JSON.stringify({ repos: [{ id: "repo-1", full_name: "samhotchkiss/otter-camp", default_branch: "main", private: false }] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.includes("/api/github/integration/settings")) {
        return new Response(JSON.stringify({
          projects: [{
            project_id: "proj-1",
            project_name: "Otter Camp",
            description: null,
            enabled: true,
            repo_full_name: "samhotchkiss/otter-camp",
            default_branch: "main",
            sync_mode: "sync",
            auto_sync: true,
            active_branches: ["main"],
            conflict_state: "none",
          }],
        }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      if (url.includes("/api/projects/proj-1/publish")) {
        const body = JSON.parse(String(init?.body || "{}"));
        if (body.dry_run) {
          return new Response(JSON.stringify({ error: "expected publish call" }), {
            status: 500,
            headers: { "Content-Type": "application/json" },
          });
        }
        return new Response(JSON.stringify({
          project_id: "proj-1",
          dry_run: false,
          status: "published",
          checks: [{ name: "push_dry_run", status: "pass", detail: "Dry-run push succeeded.", blocking: false }],
          commits_ahead: 1,
          published_at: "2026-02-06T09:30:00Z",
        }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      return new Response(JSON.stringify({ error: "unexpected endpoint" }), {
        status: 500,
        headers: { "Content-Type": "application/json" },
      });
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);
    render(<GitHubSettings />);

    await waitFor(() => {
      expect(screen.getByText("Otter Camp")).toBeInTheDocument();
    });

    const details = screen.getByText("Otter Camp").closest("details");
    details?.setAttribute("open", "");
    fireEvent.click(screen.getByRole("button", { name: "Publish" }));

    await waitFor(() => {
      expect(screen.getAllByText(/Published at/).length).toBeGreaterThan(0);
    });
    expect(screen.getByText("Publish progress log")).toBeInTheDocument();
    expect(screen.getByText(/Publish API status: published/)).toBeInTheDocument();
  });

  it("shows actionable guidance when publish fails", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/github/integration/status")) {
        return new Response(JSON.stringify({ connected: true, installation: { installation_id: 1, account_login: "sam", account_type: "Organization", connected_at: "2026-02-06T00:00:00Z" } }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.includes("/api/github/integration/repos")) {
        return new Response(JSON.stringify({ repos: [{ id: "repo-1", full_name: "samhotchkiss/otter-camp", default_branch: "main", private: false }] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.includes("/api/github/integration/settings")) {
        return new Response(JSON.stringify({
          projects: [{
            project_id: "proj-1",
            project_name: "Otter Camp",
            description: null,
            enabled: true,
            repo_full_name: "samhotchkiss/otter-camp",
            default_branch: "main",
            sync_mode: "sync",
            auto_sync: true,
            active_branches: ["main"],
            conflict_state: "none",
          }],
        }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      if (url.includes("/api/projects/proj-1/publish")) {
        return new Response(JSON.stringify({ error: "publish push failed: remote rejected update" }), {
          status: 400,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify({ error: "unexpected endpoint" }), {
        status: 500,
        headers: { "Content-Type": "application/json" },
      });
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);
    render(<GitHubSettings />);

    await waitFor(() => {
      expect(screen.getByText("Otter Camp")).toBeInTheDocument();
    });

    const details = screen.getByText("Otter Camp").closest("details");
    details?.setAttribute("open", "");
    fireEvent.click(screen.getByRole("button", { name: "Publish" }));

    await waitFor(() => {
      expect(screen.getByText("Publish failed")).toBeInTheDocument();
    });
    expect(screen.getAllByText(/Run Sync now, verify branch state, and retry publish/).length).toBeGreaterThan(0);
  });

  it("updates summary state from dry run to published", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/github/integration/status")) {
        return new Response(JSON.stringify({ connected: true, installation: { installation_id: 1, account_login: "sam", account_type: "Organization", connected_at: "2026-02-06T00:00:00Z" } }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.includes("/api/github/integration/repos")) {
        return new Response(JSON.stringify({ repos: [{ id: "repo-1", full_name: "samhotchkiss/otter-camp", default_branch: "main", private: false }] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.includes("/api/github/integration/settings")) {
        return new Response(JSON.stringify({
          projects: [{
            project_id: "proj-1",
            project_name: "Otter Camp",
            description: null,
            enabled: true,
            repo_full_name: "samhotchkiss/otter-camp",
            default_branch: "main",
            sync_mode: "sync",
            auto_sync: true,
            active_branches: ["main"],
            conflict_state: "none",
          }],
        }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      if (url.includes("/api/projects/proj-1/publish")) {
        const body = JSON.parse(String(init?.body || "{}"));
        if (body.dry_run) {
          return new Response(JSON.stringify({
            project_id: "proj-1",
            dry_run: true,
            status: "dry_run",
            checks: [{ name: "commits_ahead", status: "info", detail: "Local branch is 1 commit ahead.", blocking: false }],
            commits_ahead: 1,
          }), { status: 200, headers: { "Content-Type": "application/json" } });
        }
        return new Response(JSON.stringify({
          project_id: "proj-1",
          dry_run: false,
          status: "published",
          checks: [{ name: "push_dry_run", status: "pass", detail: "Dry-run push succeeded.", blocking: false }],
          commits_ahead: 1,
          published_at: "2026-02-06T09:35:00Z",
        }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      return new Response(JSON.stringify({ error: "unexpected endpoint" }), {
        status: 500,
        headers: { "Content-Type": "application/json" },
      });
    });

    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);
    render(<GitHubSettings />);

    await waitFor(() => {
      expect(screen.getByText("Otter Camp")).toBeInTheDocument();
    });

    const details = screen.getByText("Otter Camp").closest("details");
    details?.setAttribute("open", "");

    fireEvent.click(screen.getByRole("button", { name: "Dry run" }));
    await waitFor(() => {
      expect(screen.getByText("Dry-run summary")).toBeInTheDocument();
    });
    expect(screen.getAllByText(/dry_run/).length).toBeGreaterThan(0);

    fireEvent.click(screen.getByRole("button", { name: "Publish" }));
    await waitFor(() => {
      expect(screen.getAllByText(/published/).length).toBeGreaterThan(0);
    });
  });
});
