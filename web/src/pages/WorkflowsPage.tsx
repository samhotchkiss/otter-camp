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
    <div style={{ maxWidth: 960, margin: "0 auto", padding: "24px 16px" }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start", marginBottom: 24 }}>
        <div>
          <h1 style={{ fontSize: 24, fontWeight: 700, color: "var(--text-primary, #e2e8f0)", margin: 0 }}>
            Workflows
          </h1>
          <p style={{ color: "var(--text-muted, #64748b)", margin: "4px 0 0", fontSize: 14 }}>
            {loading
              ? "Loading..."
              : `${projects.length} workflow projects · ${activeCount} active · ${pausedCount} paused`}
          </p>
        </div>
      </div>

      {error && (
        <div
          style={{
            padding: "12px 16px",
            borderRadius: 8,
            background: "rgba(239,68,68,0.1)",
            border: "1px solid rgba(239,68,68,0.3)",
            color: "#fca5a5",
            marginBottom: 16,
            fontSize: 14,
          }}
        >
          {error}
        </div>
      )}

      {!loading && projects.length === 0 && !error && (
        <div style={{ textAlign: "center", padding: "60px 20px", color: "var(--text-muted, #64748b)" }}>
          <div style={{ fontSize: 48, marginBottom: 16 }}>🔁</div>
          <h3 style={{ color: "var(--text-primary, #e2e8f0)", margin: "0 0 8px" }}>No workflow projects yet</h3>
          <p style={{ maxWidth: 420, margin: "0 auto" }}>
            Enable workflow fields on a project to schedule recurring task creation.
          </p>
        </div>
      )}

      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        {projects.map((project) => {
          const isPending = actionPending === project.id;
          const latest = latestRuns[project.id];
          return (
            <div
              key={project.id}
              style={{
                background: "var(--bg-card, #1e293b)",
                border: "1px solid var(--border, #334155)",
                borderRadius: 10,
                padding: "16px 20px",
                opacity: isPending ? 0.6 : 1,
              }}
            >
              <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
                <span style={{ fontSize: 18 }}>{project.workflow_enabled ? "🟢" : "🟡"}</span>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ fontWeight: 600, fontSize: 15, color: "var(--text-primary, #e2e8f0)" }}>
                    {project.name}
                  </div>
                  {project.description && (
                    <div style={{ fontSize: 13, color: "var(--text-muted, #64748b)" }}>{project.description}</div>
                  )}
                </div>
                <div style={{ display: "flex", alignItems: "center", gap: 8, flexShrink: 0 }}>
                  <button
                    onClick={() => void triggerRun(project.id)}
                    disabled={isPending || !project.workflow_enabled}
                    style={{
                      background: "transparent",
                      border: "1px solid var(--border, #334155)",
                      borderRadius: 6,
                      padding: "4px 10px",
                      fontSize: 12,
                      color: project.workflow_enabled ? "var(--text-primary, #e2e8f0)" : "var(--text-muted, #64748b)",
                      cursor: project.workflow_enabled ? "pointer" : "not-allowed",
                    }}
                  >
                    Run now
                  </button>
                  <button
                    onClick={() => void toggleWorkflow(project)}
                    disabled={isPending}
                    style={{
                      borderRadius: 6,
                      padding: "4px 10px",
                      fontSize: 12,
                      border: "1px solid " + (project.workflow_enabled ? "rgba(239,68,68,0.3)" : "rgba(34,197,94,0.3)"),
                      background: project.workflow_enabled ? "rgba(239,68,68,0.1)" : "rgba(34,197,94,0.1)",
                      color: project.workflow_enabled ? "#fca5a5" : "#86efac",
                      cursor: "pointer",
                    }}
                  >
                    {project.workflow_enabled ? "Pause" : "Resume"}
                  </button>
                </div>
              </div>

              <div
                style={{
                  marginTop: 12,
                  fontSize: 12,
                  color: "var(--text-muted, #64748b)",
                  display: "flex",
                  flexWrap: "wrap",
                  gap: 14,
                }}
              >
                <span>Schedule: {humanizeSchedule(project.workflow_schedule)}</span>
                <span>Runs: {project.workflow_run_count || 0}</span>
                <span>Last run: {timeAgo(project.workflow_last_run_at)}</span>
                <span>Next run: {timeUntil(project.workflow_next_run_at)}</span>
                {latest?.title && <span>Latest task: {latest.title}</span>}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
