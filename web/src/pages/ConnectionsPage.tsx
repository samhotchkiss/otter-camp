import { useCallback, useEffect, useMemo, useState } from "react";
import { apiFetch } from "../lib/api";
import { useAgentActivity } from "../hooks/useAgentActivity";

type BridgeDiagnostics = {
  uptime_seconds?: number;
  reconnect_count?: number;
  last_sync_duration_ms?: number;
  sync_count_total?: number;
  dispatch_queue_depth?: number;
  errors_last_hour?: number;
};

type HostDiagnostics = {
  hostname?: string;
  os?: string;
  arch?: string;
  platform?: string;
  uptime_seconds?: number;
  gateway_port?: number;
  gateway_pid?: number;
  gateway_version?: string;
  gateway_build?: string;
  node_version?: string;
  cpu_model?: string;
  cpu_cores?: number;
  memory_total_bytes?: number;
  memory_used_bytes?: number;
  memory_available_bytes?: number;
  disk_total_bytes?: number;
  disk_used_bytes?: number;
  disk_free_bytes?: number;
};

type ConnectionSession = {
  id: string;
  name: string;
  status: string;
  model?: string;
  context_tokens?: number;
  total_tokens?: number;
  channel?: string;
  session_key?: string;
  last_seen?: string;
  stalled: boolean;
};

type ConnectionSummary = {
  total: number;
  online: number;
  busy: number;
  offline: number;
  stalled: number;
};

type ConnectionsResponse = {
  bridge: {
    connected: boolean;
    last_sync?: string;
    sync_healthy: boolean;
    diagnostics?: BridgeDiagnostics;
  };
  host?: HostDiagnostics;
  sessions: ConnectionSession[];
  summary: ConnectionSummary;
  generated_at: string;
};

type GitHubSyncHealthResponse = {
  stuck_jobs?: number;
  queue_depth?: Array<{ status: string; count: number }>;
};

type GitHubDeadLettersResponse = {
  items?: unknown[];
};

type DiagnosticsResponse = {
  checks: Array<{
    key: string;
    status: "pass" | "warn" | "fail";
    message: string;
  }>;
  generated_at: string;
};

type LogsResponse = {
  items: Array<{
    id: string;
    timestamp: string;
    level: string;
    event_type: string;
    message: string;
  }>;
  total: number;
};

type CronJob = {
  id: string;
  name?: string;
  schedule?: string;
  session_target?: string;
  payload_type?: string;
  last_run_at?: string;
  last_status?: string;
  next_run_at?: string;
  enabled: boolean;
};

type CronJobsResponse = {
  items: CronJob[];
  total: number;
};

type ProcessInfo = {
  id: string;
  command?: string;
  pid?: number;
  status?: string;
  duration_seconds?: number;
  agent_id?: string;
  session_key?: string;
  started_at?: string;
};

type ProcessesResponse = {
  items: ProcessInfo[];
  total: number;
};

type SessionActivitySummary = {
  latestSummary: string;
  latestAt: Date;
  countLastHour: number;
  hasRecentFailure: boolean;
};

const CONNECTIONS_POLL_INTERVAL_MS = 30_000;

function formatRelativeOrUnknown(raw?: string): string {
  if (!raw) {
    return "Unknown";
  }
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) {
    return "Unknown";
  }
  return parsed.toLocaleString();
}

function formatBytes(raw?: number): string {
  if (!raw || raw <= 0) {
    return "N/A";
  }
  const gb = raw / (1024 ** 3);
  return `${gb.toFixed(1)} GB`;
}

function formatSessionLastSeen(raw?: string): string {
  if (!raw) {
    return "Unknown";
  }
  const trimmed = raw.trim();
  if (!trimmed) {
    return "Unknown";
  }
  const parsed = new Date(trimmed);
  if (Number.isNaN(parsed.getTime())) {
    return trimmed;
  }
  const elapsedMs = Date.now() - parsed.getTime();
  if (elapsedMs < 0) {
    return parsed.toLocaleString();
  }
  const elapsedMinutes = Math.floor(elapsedMs / 60000);
  if (elapsedMinutes < 1) {
    return "Just now";
  }
  if (elapsedMinutes < 60) {
    return `${elapsedMinutes}m ago`;
  }
  const elapsedHours = Math.floor(elapsedMinutes / 60);
  if (elapsedHours < 24) {
    return `${elapsedHours}h ago`;
  }
  const elapsedDays = Math.floor(elapsedHours / 24);
  if (elapsedDays < 7) {
    return `${elapsedDays}d ago`;
  }
  return parsed.toLocaleString();
}

export default function ConnectionsPage() {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [connections, setConnections] = useState<ConnectionsResponse | null>(null);
  const [githubHealth, setGitHubHealth] = useState<GitHubSyncHealthResponse | null>(null);
  const [deadLetterCount, setDeadLetterCount] = useState<number | null>(null);
  const [diagnostics, setDiagnostics] = useState<DiagnosticsResponse | null>(null);
  const [diagnosticsLoading, setDiagnosticsLoading] = useState(false);
  const [diagnosticsError, setDiagnosticsError] = useState<string | null>(null);
  const [logs, setLogs] = useState<LogsResponse["items"]>([]);
  const [logsLoading, setLogsLoading] = useState(false);
  const [logsError, setLogsError] = useState<string | null>(null);
  const [logSearch, setLogSearch] = useState("");
  const [cronJobs, setCronJobs] = useState<CronJob[]>([]);
  const [cronLoading, setCronLoading] = useState(false);
  const [cronError, setCronError] = useState<string | null>(null);
  const [cronActionID, setCronActionID] = useState<string | null>(null);
  const [processes, setProcesses] = useState<ProcessInfo[]>([]);
  const [processesLoading, setProcessesLoading] = useState(false);
  const [processesError, setProcessesError] = useState<string | null>(null);
  const [processActionID, setProcessActionID] = useState<string | null>(null);

  const orgID = useMemo(() => {
    try {
      return (localStorage.getItem("otter-camp-org-id") ?? "").trim();
    } catch {
      return "";
    }
  }, []);

  const { events: recentActivityEvents } = useAgentActivity({
    mode: "recent",
    limit: 500,
  });

  const sessionActivityByAgent = useMemo(() => {
    const nowMs = Date.now();
    const hourAgo = nowMs - 60 * 60 * 1000;
    const dayAgo = nowMs - 24 * 60 * 60 * 1000;

    const byAgent = new Map<string, SessionActivitySummary>();

    for (const event of recentActivityEvents) {
      const key = event.agentId.trim();
      if (!key) {
        continue;
      }

      const startedAtMs = event.startedAt.getTime();
      const existing = byAgent.get(key);
      if (!existing) {
        byAgent.set(key, {
          latestSummary: event.summary,
          latestAt: event.startedAt,
          countLastHour: startedAtMs >= hourAgo ? 1 : 0,
          hasRecentFailure: event.status === "failed" && startedAtMs >= dayAgo,
        });
        continue;
      }

      existing.countLastHour += startedAtMs >= hourAgo ? 1 : 0;
      existing.hasRecentFailure = existing.hasRecentFailure || (event.status === "failed" && startedAtMs >= dayAgo);
      if (startedAtMs > existing.latestAt.getTime()) {
        existing.latestAt = event.startedAt;
        existing.latestSummary = event.summary;
      }
    }

    return byAgent;
  }, [recentActivityEvents]);

  const loadLogs = useCallback(async () => {
    if (!orgID) {
      return;
    }
    setLogsLoading(true);
    setLogsError(null);
    try {
      const suffix = `?org_id=${encodeURIComponent(orgID)}`;
      const payload = await apiFetch<LogsResponse>(`/api/admin/logs${suffix}&limit=100`);
      setLogs(Array.isArray(payload.items) ? payload.items : []);
    } catch (err) {
      setLogs([]);
      setLogsError(err instanceof Error ? err.message : "Failed to load logs");
    } finally {
      setLogsLoading(false);
    }
  }, [orgID]);

  const loadCronJobs = useCallback(async () => {
    if (!orgID) {
      return;
    }
    setCronLoading(true);
    setCronError(null);
    try {
      const suffix = `?org_id=${encodeURIComponent(orgID)}`;
      const payload = await apiFetch<CronJobsResponse>(`/api/admin/cron/jobs${suffix}`);
      setCronJobs(Array.isArray(payload.items) ? payload.items : []);
    } catch (err) {
      setCronJobs([]);
      setCronError(err instanceof Error ? err.message : "Failed to load cron jobs");
    } finally {
      setCronLoading(false);
    }
  }, [orgID]);

  const loadProcesses = useCallback(async () => {
    if (!orgID) {
      return;
    }
    setProcessesLoading(true);
    setProcessesError(null);
    try {
      const suffix = `?org_id=${encodeURIComponent(orgID)}`;
      const payload = await apiFetch<ProcessesResponse>(`/api/admin/processes${suffix}`);
      setProcesses(Array.isArray(payload.items) ? payload.items : []);
    } catch (err) {
      setProcesses([]);
      setProcessesError(err instanceof Error ? err.message : "Failed to load processes");
    } finally {
      setProcessesLoading(false);
    }
  }, [orgID]);

  const runCronJob = useCallback(async (jobID: string) => {
    if (!orgID) {
      return;
    }
    setCronActionID(`run:${jobID}`);
    setCronError(null);
    try {
      const suffix = `?org_id=${encodeURIComponent(orgID)}`;
      await apiFetch(`/api/admin/cron/jobs/${encodeURIComponent(jobID)}/run${suffix}`, {
        method: "POST",
      });
      await Promise.all([loadCronJobs(), loadLogs()]);
    } catch (err) {
      setCronError(err instanceof Error ? err.message : "Failed to run cron job");
    } finally {
      setCronActionID(null);
    }
  }, [loadCronJobs, loadLogs, orgID]);

  const toggleCronJob = useCallback(async (job: CronJob) => {
    if (!orgID) {
      return;
    }
    setCronActionID(`toggle:${job.id}`);
    setCronError(null);
    try {
      const suffix = `?org_id=${encodeURIComponent(orgID)}`;
      await apiFetch(`/api/admin/cron/jobs/${encodeURIComponent(job.id)}${suffix}`, {
        method: "PATCH",
        body: JSON.stringify({ enabled: !job.enabled }),
      });
      await Promise.all([loadCronJobs(), loadLogs()]);
    } catch (err) {
      setCronError(err instanceof Error ? err.message : "Failed to update cron job");
    } finally {
      setCronActionID(null);
    }
  }, [loadCronJobs, loadLogs, orgID]);

  const killProcess = useCallback(async (processID: string) => {
    if (!orgID) {
      return;
    }
    if (typeof window !== "undefined" && !window.confirm(`Kill process ${processID}?`)) {
      return;
    }
    setProcessActionID(processID);
    setProcessesError(null);
    try {
      const suffix = `?org_id=${encodeURIComponent(orgID)}`;
      await apiFetch(`/api/admin/processes/${encodeURIComponent(processID)}/kill${suffix}`, {
        method: "POST",
      });
      await Promise.all([loadProcesses(), loadLogs()]);
    } catch (err) {
      setProcessesError(err instanceof Error ? err.message : "Failed to kill process");
    } finally {
      setProcessActionID(null);
    }
  }, [loadLogs, loadProcesses, orgID]);

  const load = useCallback(async () => {
    if (!orgID) {
      setError("Missing organization context");
      setLoading(false);
      return;
    }

    setLoading(true);
    setError(null);
    try {
      const suffix = `?org_id=${encodeURIComponent(orgID)}`;
      const payload = await apiFetch<ConnectionsResponse>(`/api/admin/connections${suffix}`);
      setConnections(payload);

      const [healthResult, deadLettersResult] = await Promise.allSettled([
        apiFetch<GitHubSyncHealthResponse>(`/api/github/sync/health${suffix}`),
        apiFetch<GitHubDeadLettersResponse>(`/api/github/sync/dead-letters${suffix}`),
      ]);

      if (healthResult.status === "fulfilled") {
        setGitHubHealth(healthResult.value);
      } else {
        setGitHubHealth(null);
      }

      if (deadLettersResult.status === "fulfilled") {
        setDeadLetterCount(Array.isArray(deadLettersResult.value.items) ? deadLettersResult.value.items.length : 0);
      } else {
        setDeadLetterCount(null);
      }

      await Promise.all([loadLogs(), loadCronJobs(), loadProcesses()]);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load connection diagnostics");
      setConnections(null);
      setGitHubHealth(null);
      setDeadLetterCount(null);
      setLogs([]);
      setCronJobs([]);
      setProcesses([]);
      setDiagnostics(null);
    } finally {
      setLoading(false);
    }
  }, [loadCronJobs, loadLogs, loadProcesses, orgID]);

  const runDiagnostics = useCallback(async () => {
    if (!orgID) {
      return;
    }
    setDiagnosticsLoading(true);
    setDiagnosticsError(null);
    try {
      const suffix = `?org_id=${encodeURIComponent(orgID)}`;
      const payload = await apiFetch<DiagnosticsResponse>(`/api/admin/diagnostics${suffix}`, {
        method: "POST",
      });
      setDiagnostics(payload);
    } catch (err) {
      setDiagnosticsError(err instanceof Error ? err.message : "Failed to run diagnostics");
    } finally {
      setDiagnosticsLoading(false);
    }
  }, [orgID]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (!orgID || typeof window === "undefined") {
      return undefined;
    }
    const intervalID = window.setInterval(() => {
      void load();
    }, CONNECTIONS_POLL_INTERVAL_MS);
    return () => {
      window.clearInterval(intervalID);
    };
  }, [load, orgID]);

  return (
    <section className="space-y-6">
      <header className="space-y-2">
        <p className="inline-flex items-center rounded-full border border-[#C9A86C]/35 bg-[#C9A86C]/10 px-3 py-1 text-xs font-semibold uppercase tracking-[0.15em] text-[#C9A86C]">
          Operations
        </p>
        <h1 className="text-3xl font-semibold text-[var(--text)]">Connections & Diagnostics</h1>
        <p className="text-sm text-[var(--text-muted)]">
          Live bridge health, session state, and sync visibility for remote operations.
        </p>
      </header>
      <article className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4">
        <h2 className="text-sm font-semibold text-[var(--text)]">Chameleon routing policy</h2>
        <p className="mt-2 text-xs text-[var(--text-muted)]">Without project_id, chats stay read-only.</p>
        <p className="mt-1 text-xs text-[var(--text-muted)]">
          Writable tasks must dispatch with project context and stay within that project root.
        </p>
      </article>

      {loading && (
        <div className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-5 text-sm text-[var(--text-muted)]">
          Loading connection status...
        </div>
      )}

      {!loading && error && (
        <div className="rounded-2xl border border-red-300 bg-red-50 p-5 text-sm text-red-700">
          <p>{error}</p>
          <button
            type="button"
            className="mt-3 rounded-lg bg-[#C9A86C] px-4 py-2 text-xs font-semibold text-white hover:bg-[#b79658]"
            onClick={() => void load()}
          >
            Try Again
          </button>
        </div>
      )}

      {!loading && !error && connections && (
        <div className="space-y-6">
          <div className="grid gap-4 md:grid-cols-4">
            <article className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4">
              <p className="text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">Bridge</p>
              <p
                className={`mt-2 text-lg font-semibold ${
                  connections.bridge.connected ? "text-emerald-600" : "text-red-600"
                }`}
              >
                {connections.bridge.connected ? "Connected" : "Disconnected"}
              </p>
              <p className="mt-2 text-xs text-[var(--text-muted)]">
                Last sync: {formatRelativeOrUnknown(connections.bridge.last_sync)}
              </p>
              <p className="mt-1 text-xs text-[var(--text-muted)]">
                Sync health: {connections.bridge.sync_healthy ? "Healthy" : "Degraded"}
              </p>
              {connections.bridge.diagnostics && (
                <p className="mt-1 text-xs text-[var(--text-muted)]">
                  Reconnects: {connections.bridge.diagnostics.reconnect_count ?? 0}
                </p>
              )}
            </article>

            <article className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4">
              <p className="text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">Host</p>
              <p className="mt-2 text-lg font-semibold text-[var(--text)]">
                {connections.host?.hostname ?? "Unknown host"}
              </p>
              <p className="mt-2 text-xs text-[var(--text-muted)]">
                OS: {connections.host?.os ?? "Unknown"} {connections.host?.arch ? `(${connections.host.arch})` : ""}
              </p>
              <p className="mt-1 text-xs text-[var(--text-muted)]">
                Gateway: {connections.host?.gateway_version ?? "Unknown"} · Port {connections.host?.gateway_port ?? "?"}
              </p>
              <p className="mt-1 text-xs text-[var(--text-muted)]">
                Node: {connections.host?.node_version ?? "Unknown"}
              </p>
            </article>

            <article className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4">
              <p className="text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">GitHub Sync</p>
              <p className="mt-2 text-sm text-[var(--text)]">
                Stuck jobs: {githubHealth?.stuck_jobs ?? "N/A"}
              </p>
              <p className="mt-1 text-sm text-[var(--text)]">
                Dead letters: {deadLetterCount ?? "N/A"}
              </p>
              <p className="mt-2 text-xs text-[var(--text-muted)]">
                Queue states: {githubHealth?.queue_depth?.length ?? 0}
              </p>
            </article>

            <article className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4">
              <div className="flex items-center justify-between gap-3">
                <p className="text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">Diagnostics</p>
                <button
                  type="button"
                  className="rounded-lg bg-[#C9A86C] px-3 py-1.5 text-xs font-semibold text-white hover:bg-[#b79658] disabled:opacity-60"
                  disabled={diagnosticsLoading}
                  onClick={() => void runDiagnostics()}
                >
                  {diagnosticsLoading ? "Running..." : "Run"}
                </button>
              </div>
              {diagnosticsError && (
                <p className="mt-2 text-xs text-red-700">{diagnosticsError}</p>
              )}
              {diagnostics && diagnostics.checks.length > 0 && (
                <p className="mt-2 text-xs text-[var(--text-muted)]">
                  {diagnostics.checks.filter((check) => check.status === "fail").length} failing checks
                </p>
              )}
            </article>
          </div>

          <article className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4">
            <h2 className="text-sm font-semibold text-[var(--text)]">Session Summary</h2>
            <div className="mt-3 grid gap-2 text-xs text-[var(--text-muted)] sm:grid-cols-5">
              <p>Total: {connections.summary.total}</p>
              <p>Online: {connections.summary.online}</p>
              <p>Busy: {connections.summary.busy}</p>
              <p>Offline: {connections.summary.offline}</p>
              <p>Stalled: {connections.summary.stalled}</p>
            </div>
            <p className="mt-2 text-xs text-[var(--text-muted)]">
              Memory used: {formatBytes(connections.host?.memory_used_bytes)} / {formatBytes(connections.host?.memory_total_bytes)}
            </p>

            {diagnostics && diagnostics.checks.length > 0 && (
              <ul className="mt-3 space-y-1 text-xs">
                {diagnostics.checks.map((check) => (
                  <li
                    key={check.key}
                    className={`rounded border px-2 py-1 ${
                      check.status === "pass"
                        ? "border-emerald-200 bg-emerald-50 text-emerald-800"
                        : check.status === "warn"
                          ? "border-amber-200 bg-amber-50 text-amber-800"
                          : "border-red-200 bg-red-50 text-red-800"
                    }`}
                  >
                    <span className="font-semibold">{check.key}</span>: {check.message}
                  </li>
                ))}
              </ul>
            )}
          </article>

          <article className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold text-[var(--text)]">Agent Sessions</h2>
              <p className="text-xs text-[var(--text-muted)]">Updated {formatRelativeOrUnknown(connections.generated_at)}</p>
            </div>
            <div className="mt-3 overflow-x-auto">
              <table className="min-w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-[var(--border)] text-xs uppercase tracking-wide text-[var(--text-muted)]">
                    <th className="py-2 pr-4">Agent</th>
                    <th className="py-2 pr-4">Status</th>
                    <th className="py-2 pr-4">Model</th>
                    <th className="py-2 pr-4">Context</th>
                    <th className="py-2 pr-4">Channel</th>
                    <th className="py-2 pr-4">Last Seen</th>
                    <th className="py-2 pr-4">Last Activity</th>
                    <th className="py-2 pr-4">1h Count</th>
                    <th className="py-2 pr-4">Errors</th>
                  </tr>
                </thead>
                <tbody>
                  {connections.sessions.map((session) => {
                    const activity =
                      sessionActivityByAgent.get(session.id) ||
                      sessionActivityByAgent.get(session.id.toLowerCase()) ||
                      sessionActivityByAgent.get(session.name) ||
                      sessionActivityByAgent.get(session.name.toLowerCase());

                    return (
                    <tr
                      key={session.id}
                      className={`border-b border-[var(--border)]/60 text-[var(--text)] ${
                        activity?.hasRecentFailure ? "bg-rose-50/40" : ""
                      }`}
                    >
                      <td className="py-2 pr-4 font-medium">{session.name}</td>
                      <td className="py-2 pr-4">
                        <span
                          className={`rounded-full px-2 py-0.5 text-xs font-semibold ${
                            session.stalled
                              ? "bg-amber-100 text-amber-800"
                              : session.status === "online"
                                ? "bg-emerald-100 text-emerald-800"
                                : session.status === "busy"
                                  ? "bg-sky-100 text-sky-800"
                                  : "bg-slate-100 text-slate-700"
                          }`}
                        >
                          {session.stalled ? "stalled" : session.status}
                        </span>
                      </td>
                      <td className="py-2 pr-4 text-xs text-[var(--text-muted)]">{session.model ?? "Unknown"}</td>
                      <td className="py-2 pr-4 text-xs text-[var(--text-muted)]">{session.context_tokens ?? 0}</td>
                      <td className="py-2 pr-4 text-xs text-[var(--text-muted)]">{session.channel ?? "—"}</td>
                      <td className="py-2 pr-4 text-xs text-[var(--text-muted)]">{formatSessionLastSeen(session.last_seen)}</td>
                      <td className="py-2 pr-4 text-xs text-[var(--text-muted)]">
                        {activity ? (
                          <div className="space-y-0.5">
                            <p className="line-clamp-2 text-[var(--text)]">{activity.latestSummary}</p>
                            <p className="text-[10px] text-[var(--text-muted)]">{formatRelativeOrUnknown(activity.latestAt.toISOString())}</p>
                          </div>
                        ) : (
                          "No recent activity"
                        )}
                      </td>
                      <td className="py-2 pr-4 text-xs text-[var(--text-muted)]">{activity?.countLastHour ?? 0}</td>
                      <td className="py-2 pr-4 text-xs">
                        {activity?.hasRecentFailure ? (
                          <span className="rounded-full bg-rose-100 px-2 py-0.5 font-semibold text-rose-700">Failed</span>
                        ) : (
                          <span className="rounded-full bg-emerald-100 px-2 py-0.5 font-semibold text-emerald-700">OK</span>
                        )}
                      </td>
                    </tr>
                    );
                  })}
                  {connections.sessions.length === 0 && (
                    <tr>
                      <td className="py-3 text-xs text-[var(--text-muted)]" colSpan={9}>No synced sessions yet.</td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </article>

          <article className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h2 className="text-sm font-semibold text-[var(--text)]">Gateway Logs</h2>
              <div className="flex items-center gap-2">
                <input
                  value={logSearch}
                  onChange={(event) => setLogSearch(event.target.value)}
                  placeholder="Search logs..."
                  className="rounded-md border border-[var(--border)] bg-[var(--surface-alt)] px-2 py-1 text-xs text-[var(--text)]"
                />
                <button
                  type="button"
                  className="rounded-md border border-[var(--border)] px-2 py-1 text-xs text-[var(--text-muted)] hover:bg-[var(--surface-alt)]"
                  onClick={() => void loadLogs()}
                >
                  Refresh
                </button>
              </div>
            </div>
            {logsError && (
              <p className="mt-2 text-xs text-red-700">{logsError}</p>
            )}
            {logsLoading && (
              <p className="mt-2 text-xs text-[var(--text-muted)]">Loading logs...</p>
            )}
            {!logsLoading && (
              <div className="mt-2 max-h-64 overflow-y-auto rounded border border-[var(--border)] bg-[var(--surface-alt)]">
                {logs
                  .filter((item) => {
                    if (!logSearch.trim()) {
                      return true;
                    }
                    const query = logSearch.trim().toLowerCase();
                    return item.message.toLowerCase().includes(query) || item.event_type.toLowerCase().includes(query);
                  })
                  .slice(0, 80)
                  .map((item) => (
                    <div key={item.id} className="border-b border-[var(--border)]/60 px-3 py-2 text-xs">
                      <p className="text-[var(--text-muted)]">
                        {new Date(item.timestamp).toLocaleString()} · {item.level}
                      </p>
                      <p className="font-semibold text-[var(--text)]">{item.event_type}</p>
                      <p className="text-[var(--text-muted)]">{item.message}</p>
                    </div>
                  ))}
                {logs.length === 0 && (
                  <p className="px-3 py-3 text-xs text-[var(--text-muted)]">
                    {connections.bridge.connected ? "No logs available." : "Connect bridge to view logs."}
                  </p>
                )}
              </div>
            )}
          </article>

          <article className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold text-[var(--text)]">Cron Jobs</h2>
              <button
                type="button"
                className="rounded-md border border-[var(--border)] px-2 py-1 text-xs text-[var(--text-muted)] hover:bg-[var(--surface-alt)]"
                onClick={() => void loadCronJobs()}
              >
                Refresh
              </button>
            </div>
            {cronError && <p className="mt-2 text-xs text-red-700">{cronError}</p>}
            {cronLoading && <p className="mt-2 text-xs text-[var(--text-muted)]">Loading cron jobs...</p>}
            {!cronLoading && (
              <div className="mt-2 overflow-x-auto">
                <table className="min-w-full text-left text-xs">
                  <thead>
                    <tr className="border-b border-[var(--border)] text-[10px] uppercase tracking-wide text-[var(--text-muted)]">
                      <th className="py-2 pr-3">Name</th>
                      <th className="py-2 pr-3">Schedule</th>
                      <th className="py-2 pr-3">Last Status</th>
                      <th className="py-2 pr-3">Last Run</th>
                      <th className="py-2 pr-3">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {cronJobs.map((job) => (
                      <tr key={job.id} className="border-b border-[var(--border)]/60 text-[var(--text)]">
                        <td className="py-2 pr-3 font-medium">{job.name || job.id}</td>
                        <td className="py-2 pr-3 text-[var(--text-muted)]">{job.schedule || "—"}</td>
                        <td className="py-2 pr-3 text-[var(--text-muted)]">{job.last_status || "unknown"}</td>
                        <td className="py-2 pr-3 text-[var(--text-muted)]">{formatRelativeOrUnknown(job.last_run_at)}</td>
                        <td className="py-2 pr-3">
                          <div className="flex items-center gap-2">
                            <button
                              type="button"
                              className="rounded-md border border-[var(--border)] px-2 py-1 text-[10px] font-semibold text-[var(--text-muted)] hover:bg-[var(--surface-alt)] disabled:opacity-60"
                              disabled={cronActionID === `run:${job.id}` || cronActionID === `toggle:${job.id}`}
                              onClick={() => void runCronJob(job.id)}
                            >
                              Run
                            </button>
                            <button
                              type="button"
                              className="rounded-md border border-[var(--border)] px-2 py-1 text-[10px] font-semibold text-[var(--text-muted)] hover:bg-[var(--surface-alt)] disabled:opacity-60"
                              disabled={cronActionID === `run:${job.id}` || cronActionID === `toggle:${job.id}`}
                              onClick={() => void toggleCronJob(job)}
                            >
                              {job.enabled ? "Disable" : "Enable"}
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                    {cronJobs.length === 0 && (
                      <tr>
                        <td className="py-3 text-[var(--text-muted)]" colSpan={5}>No cron jobs reported.</td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
            )}
          </article>

          <article className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold text-[var(--text)]">Active Processes</h2>
              <button
                type="button"
                className="rounded-md border border-[var(--border)] px-2 py-1 text-xs text-[var(--text-muted)] hover:bg-[var(--surface-alt)]"
                onClick={() => void loadProcesses()}
              >
                Refresh
              </button>
            </div>
            {processesError && <p className="mt-2 text-xs text-red-700">{processesError}</p>}
            {processesLoading && <p className="mt-2 text-xs text-[var(--text-muted)]">Loading processes...</p>}
            {!processesLoading && (
              <div className="mt-2 overflow-x-auto">
                <table className="min-w-full text-left text-xs">
                  <thead>
                    <tr className="border-b border-[var(--border)] text-[10px] uppercase tracking-wide text-[var(--text-muted)]">
                      <th className="py-2 pr-3">Process</th>
                      <th className="py-2 pr-3">Status</th>
                      <th className="py-2 pr-3">Duration</th>
                      <th className="py-2 pr-3">Agent</th>
                      <th className="py-2 pr-3">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {processes.map((process) => (
                      <tr key={process.id} className="border-b border-[var(--border)]/60 text-[var(--text)]">
                        <td className="py-2 pr-3">
                          <p className="font-medium">{process.id}</p>
                          <p className="text-[var(--text-muted)]">{process.command || "Unknown command"}</p>
                        </td>
                        <td className="py-2 pr-3 text-[var(--text-muted)]">{process.status || "running"}</td>
                        <td className="py-2 pr-3 text-[var(--text-muted)]">
                          {typeof process.duration_seconds === "number" ? `${Math.round(process.duration_seconds)}s` : "—"}
                        </td>
                        <td className="py-2 pr-3 text-[var(--text-muted)]">{process.agent_id || "—"}</td>
                        <td className="py-2 pr-3">
                          <button
                            type="button"
                            className="rounded-md border border-red-300 px-2 py-1 text-[10px] font-semibold text-red-700 hover:bg-red-50 disabled:opacity-60"
                            disabled={processActionID === process.id}
                            onClick={() => void killProcess(process.id)}
                          >
                            Kill
                          </button>
                        </td>
                      </tr>
                    ))}
                    {processes.length === 0 && (
                      <tr>
                        <td className="py-3 text-[var(--text-muted)]" colSpan={5}>No active processes reported.</td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
            )}
          </article>
        </div>
      )}
    </section>
  );
}
