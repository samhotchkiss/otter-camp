import { useCallback, useEffect, useMemo, useState } from "react";
import { apiFetch } from "../lib/api";

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
    return "Unknown";
  }
  const gb = raw / (1024 ** 3);
  return `${gb.toFixed(1)} GB`;
}

export default function ConnectionsPage() {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [connections, setConnections] = useState<ConnectionsResponse | null>(null);
  const [githubHealth, setGitHubHealth] = useState<GitHubSyncHealthResponse | null>(null);
  const [deadLetterCount, setDeadLetterCount] = useState<number | null>(null);

  const orgID = useMemo(() => {
    try {
      return (localStorage.getItem("otter-camp-org-id") ?? "").trim();
    } catch {
      return "";
    }
  }, []);

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
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load connection diagnostics");
      setConnections(null);
      setGitHubHealth(null);
      setDeadLetterCount(null);
    } finally {
      setLoading(false);
    }
  }, [orgID]);

  useEffect(() => {
    void load();
  }, [load]);

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
          <div className="grid gap-4 md:grid-cols-3">
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
                  </tr>
                </thead>
                <tbody>
                  {connections.sessions.map((session) => (
                    <tr key={session.id} className="border-b border-[var(--border)]/60 text-[var(--text)]">
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
                      <td className="py-2 pr-4 text-xs text-[var(--text-muted)]">{session.last_seen || "Unknown"}</td>
                    </tr>
                  ))}
                  {connections.sessions.length === 0 && (
                    <tr>
                      <td className="py-3 text-xs text-[var(--text-muted)]" colSpan={6}>No synced sessions yet.</td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </article>
        </div>
      )}
    </section>
  );
}
