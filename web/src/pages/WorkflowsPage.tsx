import { useCallback, useEffect, useMemo, useState } from "react";
import { API_URL } from "../lib/api";

type WorkflowSchedule = {
  kind?: string;
  expr?: string;
  tz?: string;
  everyMs?: number;
  at?: string;
  cron_id?: string;
};

type WorkflowProject = {
  id: string;
  name: string;
  description?: string;
  workflow_enabled?: boolean;
  workflow_schedule?: WorkflowSchedule | null;
  workflow_agent_id?: string | null;
  workflow_last_run_at?: string | null;
  workflow_next_run_at?: string | null;
  workflow_run_count?: number;
};

type WorkflowLatestRun = {
  issue_number?: number;
  title?: string;
  state?: string;
  work_status?: string;
  created_at?: string;
  closed_at?: string;
};

function getAuthHeaders(): Record<string, string> {
  const headers: Record<string, string> = {};
  try {
    const legacy = localStorage.getItem("otter_auth");
    if (legacy) {
      const parsed = JSON.parse(legacy);
      if (parsed?.token) {
        headers.Authorization = `Bearer ${parsed.token}`;
      }
    }
  } catch {
    // ignore
  }
  try {
    const token = (localStorage.getItem("otter_camp_token") || "").trim();
    if (token !== "") {
      headers.Authorization = `Bearer ${token}`;
    }
  } catch {
    // ignore
  }
  try {
    const orgID = (localStorage.getItem("otter-camp-org-id") || "").trim();
    if (orgID !== "") {
      headers["X-Org-ID"] = orgID;
    }
  } catch {
    // ignore
  }
  return headers;
}

function timeAgo(dateString?: string | null): string {
  if (!dateString) return "Never";
  const date = new Date(dateString);
  if (Number.isNaN(date.getTime())) return "Unknown";
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  if (diffMs < 60_000) return "Just now";
  const mins = Math.floor(diffMs / 60_000);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

function timeUntil(dateString?: string | null): string {
  if (!dateString) return "n/a";
  const date = new Date(dateString);
  if (Number.isNaN(date.getTime())) return "n/a";
  const now = new Date();
  const diffMs = date.getTime() - now.getTime();
  if (diffMs <= 0) return "Overdue";
  const mins = Math.floor(diffMs / 60_000);
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  return `${days}d`;
}

function humanizeSchedule(schedule?: WorkflowSchedule | null): string {
  if (!schedule || !schedule.kind) return "Manual";
  if (schedule.kind === "cron" && schedule.expr) {
    return schedule.tz ? `${schedule.expr} (${schedule.tz})` : schedule.expr;
  }
  if (schedule.kind === "every" && typeof schedule.everyMs === "number" && schedule.everyMs > 0) {
    if (schedule.everyMs % 3_600_000 === 0) return `Every ${schedule.everyMs / 3_600_000}h`;
    if (schedule.everyMs % 60_000 === 0) return `Every ${schedule.everyMs / 60_000}m`;
    return `Every ${Math.floor(schedule.everyMs / 1000)}s`;
  }
  if (schedule.kind === "at" && schedule.at) return `At ${schedule.at}`;
  return "Manual";
}

export default function WorkflowsPage() {
  const [projects, setProjects] = useState<WorkflowProject[]>([]);
  const [latestRuns, setLatestRuns] = useState<Record<string, WorkflowLatestRun>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [actionPending, setActionPending] = useState<string | null>(null);

  const fetchWorkflowProjects = useCallback(async () => {
    const response = await fetch(`${API_URL}/api/projects?workflow=true`, {
      headers: getAuthHeaders(),
    });
    if (!response.ok) {
      throw new Error("Failed to fetch workflow projects");
    }
    const payload = await response.json();
    return (payload.projects || []) as WorkflowProject[];
  }, []);

  const fetchLatestRun = useCallback(async (projectID: string) => {
    const response = await fetch(`${API_URL}/api/projects/${encodeURIComponent(projectID)}/runs/latest`, {
      headers: getAuthHeaders(),
    });
    if (response.status === 404) return null;
    if (!response.ok) return null;
    return (await response.json()) as WorkflowLatestRun;
  }, []);

  const refresh = useCallback(async () => {
    try {
      const nextProjects = await fetchWorkflowProjects();
      setProjects(nextProjects);
      const runEntries = await Promise.all(
        nextProjects.map(async (project) => [project.id, await fetchLatestRun(project.id)] as const),
      );
      const latest: Record<string, WorkflowLatestRun> = {};
      for (const [projectID, run] of runEntries) {
        if (run) {
          latest[projectID] = run;
        }
      }
      setLatestRuns(latest);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load workflows");
    } finally {
      setLoading(false);
    }
  }, [fetchLatestRun, fetchWorkflowProjects]);

  useEffect(() => {
    void refresh();
    const interval = setInterval(() => {
      void refresh();
    }, 30_000);
    return () => clearInterval(interval);
  }, [refresh]);

  const toggleWorkflow = useCallback(async (project: WorkflowProject) => {
    setActionPending(project.id);
    try {
      const response = await fetch(`${API_URL}/api/projects/${encodeURIComponent(project.id)}`, {
        method: "PATCH",
        headers: { ...getAuthHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ workflow_enabled: !project.workflow_enabled }),
      });
      if (!response.ok) {
        throw new Error("Failed to update workflow state");
      }
      await refresh();
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to update workflow state");
    } finally {
      setActionPending(null);
    }
  }, [refresh]);

  const triggerRun = useCallback(async (projectID: string) => {
    setActionPending(projectID);
    try {
      const response = await fetch(`${API_URL}/api/projects/${encodeURIComponent(projectID)}/runs/trigger`, {
        method: "POST",
        headers: getAuthHeaders(),
      });
      if (!response.ok) {
        throw new Error("Failed to trigger workflow run");
      }
      await refresh();
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to trigger workflow run");
    } finally {
      setActionPending(null);
    }
  }, [refresh]);

  const activeCount = useMemo(
    () => projects.filter((project) => project.workflow_enabled).length,
    [projects],
  );
  const pausedCount = projects.length - activeCount;

  return (
    <div data-testid="workflows-shell" className="mx-auto w-full max-w-[1240px] space-y-4">
      <div className="rounded-3xl border border-[var(--border)] bg-[var(--surface)]/70 p-6 shadow-sm">
        <div>
          <h1 className="text-3xl font-semibold text-[var(--text)]">Workflows</h1>
          <p className="mt-1 text-sm text-[var(--text-muted)]">
            {loading
              ? "Loading..."
              : `${projects.length} workflow projects · ${activeCount} active · ${pausedCount} paused`}
          </p>
        </div>
      </div>

      {error && (
        <div className="rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">
          {error}
        </div>
      )}

      {!loading && projects.length === 0 && !error && (
        <div className="rounded-2xl border border-dashed border-[var(--border)] bg-[var(--surface)]/50 px-5 py-14 text-center text-[var(--text-muted)]">
          <div className="mb-4 text-5xl">🔁</div>
          <h3 className="mb-2 text-base font-semibold text-[var(--text)]">No workflow projects yet</h3>
          <p className="mx-auto max-w-[420px] text-sm">
            Enable workflow fields on a project to schedule recurring issue creation.
          </p>
        </div>
      )}

      <div className="flex flex-col gap-3">
        {projects.map((project) => {
          const isPending = actionPending === project.id;
          const latest = latestRuns[project.id];
          return (
            <div
              key={project.id}
              className={`rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4 shadow-sm transition ${
                isPending ? "opacity-60" : "opacity-100"
              }`}
            >
              <div className="flex items-center gap-3">
                <span className="text-lg">{project.workflow_enabled ? "🟢" : "🟡"}</span>
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-semibold text-[var(--text)]">{project.name}</div>
                  {project.description && (
                    <div className="text-xs text-[var(--text-muted)]">{project.description}</div>
                  )}
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <button
                    onClick={() => void triggerRun(project.id)}
                    disabled={isPending || !project.workflow_enabled}
                    className={`rounded-md border px-2.5 py-1 text-xs font-medium ${
                      project.workflow_enabled
                        ? "border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text)] hover:border-[#C9A86C]/50"
                        : "cursor-not-allowed border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text-muted)]"
                    }`}
                  >
                    Run now
                  </button>
                  <button
                    onClick={() => void toggleWorkflow(project)}
                    disabled={isPending}
                    className={`rounded-md border px-2.5 py-1 text-xs font-medium transition ${
                      project.workflow_enabled
                        ? "border-red-400/40 bg-red-500/10 text-red-300 hover:border-red-400/60"
                        : "border-emerald-400/40 bg-emerald-500/10 text-emerald-300 hover:border-emerald-400/60"
                    }`}
                  >
                    {project.workflow_enabled ? "Pause" : "Resume"}
                  </button>
                </div>
              </div>

              <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs text-[var(--text-muted)]">
                <span>Schedule: {humanizeSchedule(project.workflow_schedule)}</span>
                <span>Runs: {project.workflow_run_count || 0}</span>
                <span>Last run: {timeAgo(project.workflow_last_run_at)}</span>
                <span>Next run: {timeUntil(project.workflow_next_run_at)}</span>
                {latest?.title && <span>Latest issue: {latest.title}</span>}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
