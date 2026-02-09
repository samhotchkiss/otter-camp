import { useState, useEffect, useCallback } from "react";
import { API_URL } from "../lib/api";

interface WorkflowTrigger {
  type: string;
  every?: string;
  cron?: string;
  event?: string;
  label?: string;
}

interface Workflow {
  id: string;
  name: string;
  description?: string;
  trigger: WorkflowTrigger;
  status: string;
  enabled: boolean;
  last_run?: string;
  next_run?: string;
  last_status?: string;
  agent_id?: string;
  agent_name?: string;
  source?: string;
}

function getAuthHeaders(): Record<string, string> {
  const headers: Record<string, string> = {};
  try {
    const stored = localStorage.getItem("otter_auth");
    if (stored) {
      const parsed = JSON.parse(stored);
      if (parsed.token) headers.Authorization = `Bearer ${parsed.token}`;
    }
  } catch {
    // ignore
  }
  return headers;
}

function timeAgo(dateString: string): string {
  const date = new Date(dateString);
  if (isNaN(date.getTime())) return "Unknown";
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  if (diffMs < 0) return "Just now";
  const diffSecs = Math.floor(diffMs / 1000);
  if (diffSecs < 60) return "Just now";
  const diffMins = Math.floor(diffSecs / 60);
  if (diffMins < 60) return `${diffMins}m ago`;
  const diffHours = Math.floor(diffMins / 60);
  if (diffHours < 24) return `${diffHours}h ago`;
  const diffDays = Math.floor(diffHours / 24);
  return `${diffDays}d ago`;
}

function timeUntil(dateString: string): string {
  const date = new Date(dateString);
  if (isNaN(date.getTime())) return "";
  const now = new Date();
  const diffMs = date.getTime() - now.getTime();
  if (diffMs < 0) return "Overdue";
  const diffSecs = Math.floor(diffMs / 1000);
  if (diffSecs < 60) return "< 1m";
  const diffMins = Math.floor(diffSecs / 60);
  if (diffMins < 60) return `${diffMins}m`;
  const diffHours = Math.floor(diffMins / 60);
  if (diffHours < 24) return `${diffHours}h`;
  const diffDays = Math.floor(diffHours / 24);
  return `${diffDays}d`;
}

function triggerIcon(trigger?: WorkflowTrigger): string {
  if (!trigger) return "⚙️";
  switch (trigger.type) {
    case "cron":
      return "🕐";
    case "interval":
      return "🔄";
    case "event":
      return "⚡";
    case "manual":
      return "👆";
    default:
      return "⚙️";
  }
}

function statusColor(status: string): string {
  switch (status) {
    case "active":
      return "var(--color-green, #22c55e)";
    case "paused":
      return "var(--color-amber, #f59e0b)";
    default:
      return "var(--text-muted, #64748b)";
  }
}

function lastStatusBadge(status: string): { label: string; color: string } {
  switch (status) {
    case "ok":
      return { label: "✓ OK", color: "var(--color-green, #22c55e)" };
    case "error":
    case "failed":
      return { label: "✗ Failed", color: "var(--color-red, #ef4444)" };
    case "timeout":
      return { label: "⏱ Timeout", color: "var(--color-amber, #f59e0b)" };
    default:
      return { label: status || "—", color: "var(--text-muted, #64748b)" };
  }
}

export default function WorkflowsPage() {
  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [actionPending, setActionPending] = useState<string | null>(null);

  const fetchWorkflows = useCallback(async () => {
    try {
      const res = await fetch(`${API_URL}/api/workflows`, {
        headers: getAuthHeaders(),
      });
      if (!res.ok) throw new Error("Failed to fetch workflows");
      const data = await res.json();
      setWorkflows(Array.isArray(data) ? data : []);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchWorkflows();
    const interval = setInterval(fetchWorkflows, 30000); // refresh every 30s
    return () => clearInterval(interval);
  }, [fetchWorkflows]);

  const toggleWorkflow = async (id: string, currentEnabled: boolean) => {
    setActionPending(id);
    try {
      await fetch(`${API_URL}/api/workflows/${encodeURIComponent(id)}`, {
        method: "PATCH",
        headers: { ...getAuthHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ enabled: !currentEnabled }),
      });
      // Optimistic update
      setWorkflows((prev) =>
        prev.map((w) =>
          w.id === id
            ? { ...w, enabled: !currentEnabled, status: !currentEnabled ? "active" : "paused" }
            : w
        )
      );
    } catch {
      // Will refresh on next interval
    } finally {
      setActionPending(null);
    }
  };

  const runWorkflow = async (id: string) => {
    setActionPending(id);
    try {
      await fetch(`${API_URL}/api/workflows/${encodeURIComponent(id)}/run`, {
        method: "POST",
        headers: getAuthHeaders(),
      });
      // Refresh after a short delay to pick up the run
      setTimeout(fetchWorkflows, 2000);
    } catch {
      // ignore
    } finally {
      setActionPending(null);
    }
  };

  const activeCount = workflows.filter((w) => w.enabled).length;
  const pausedCount = workflows.filter((w) => !w.enabled).length;

  return (
    <div style={{ maxWidth: 960, margin: "0 auto", padding: "24px 16px" }}>
      {/* Header */}
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start", marginBottom: 24 }}>
        <div>
          <h1
            style={{
              fontSize: 24,
              fontWeight: 700,
              color: "var(--text-primary, #e2e8f0)",
              margin: 0,
            }}
          >
            Workflows
          </h1>
          <p style={{ color: "var(--text-muted, #64748b)", margin: "4px 0 0", fontSize: 14 }}>
            {loading
              ? "Loading..."
              : `${workflows.length} recurring workflows · ${activeCount} active · ${pausedCount} paused`}
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

      {!loading && workflows.length === 0 && !error && (
        <div
          style={{
            textAlign: "center",
            padding: "60px 20px",
            color: "var(--text-muted, #64748b)",
          }}
        >
          <div style={{ fontSize: 48, marginBottom: 16 }}>🔄</div>
          <h3 style={{ color: "var(--text-primary, #e2e8f0)", margin: "0 0 8px" }}>
            No workflows configured
          </h3>
          <p style={{ maxWidth: 400, margin: "0 auto" }}>
            Connect OpenClaw to see your recurring cron jobs, heartbeats, and automations here.
          </p>
        </div>
      )}

      {/* Workflow list */}
      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        {workflows.map((workflow) => {
          const isPending = actionPending === workflow.id;
          const badge = lastStatusBadge(workflow.last_status || "");

          return (
            <div
              key={workflow.id}
              style={{
                background: "var(--bg-card, #1e293b)",
                border: "1px solid var(--border, #334155)",
                borderRadius: 10,
                padding: "16px 20px",
                opacity: isPending ? 0.6 : 1,
                transition: "opacity 0.2s, border-color 0.2s",
              }}
            >
              {/* Top row: icon, name, status dot, toggle */}
              <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
                <span style={{ fontSize: 20, lineHeight: 1 }}>{triggerIcon(workflow.trigger)}</span>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                    <span
                      style={{
                        fontWeight: 600,
                        fontSize: 15,
                        color: "var(--text-primary, #e2e8f0)",
                        whiteSpace: "nowrap",
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                      }}
                    >
                      {workflow.name}
                    </span>
                    <span
                      style={{
                        width: 8,
                        height: 8,
                        borderRadius: "50%",
                        background: statusColor(workflow.status),
                        flexShrink: 0,
                      }}
                    />
                  </div>
                  {workflow.description && (
                    <div
                      style={{
                        fontSize: 13,
                        color: "var(--text-muted, #64748b)",
                        marginTop: 2,
                      }}
                    >
                      {workflow.description}
                    </div>
                  )}
                </div>

                {/* Actions */}
                <div style={{ display: "flex", alignItems: "center", gap: 8, flexShrink: 0 }}>
                  <button
                    onClick={() => runWorkflow(workflow.id)}
                    disabled={isPending || !workflow.enabled}
                    title="Run now"
                    style={{
                      background: "transparent",
                      border: "1px solid var(--border, #334155)",
                      borderRadius: 6,
                      padding: "4px 10px",
                      fontSize: 12,
                      color: workflow.enabled
                        ? "var(--text-primary, #e2e8f0)"
                        : "var(--text-muted, #64748b)",
                      cursor: workflow.enabled ? "pointer" : "not-allowed",
                    }}
                  >
                    ▶ Run
                  </button>
                  <button
                    onClick={() => toggleWorkflow(workflow.id, workflow.enabled)}
                    disabled={isPending}
                    style={{
                      background: workflow.enabled
                        ? "rgba(239,68,68,0.1)"
                        : "rgba(34,197,94,0.1)",
                      border: "1px solid " + (workflow.enabled ? "rgba(239,68,68,0.3)" : "rgba(34,197,94,0.3)"),
                      borderRadius: 6,
                      padding: "4px 10px",
                      fontSize: 12,
                      color: workflow.enabled ? "#fca5a5" : "#86efac",
                      cursor: "pointer",
                    }}
                  >
                    {workflow.enabled ? "⏸ Pause" : "▶ Resume"}
                  </button>
                </div>
              </div>

              {/* Bottom row: metadata chips */}
              <div
                style={{
                  display: "flex",
                  flexWrap: "wrap",
                  gap: 12,
                  marginTop: 10,
                  fontSize: 12,
                  color: "var(--text-muted, #64748b)",
                }}
              >
                <span title="Schedule">{workflow.trigger.label || workflow.trigger.type}</span>
                {workflow.agent_name && <span title="Agent">👤 {workflow.agent_name}</span>}
                {workflow.last_run && (
                  <span title={`Last run: ${new Date(workflow.last_run).toLocaleString()}`}>
                    Last: {timeAgo(workflow.last_run)}
                  </span>
                )}
                {workflow.last_status && (
                  <span style={{ color: badge.color }}>{badge.label}</span>
                )}
                {workflow.next_run && workflow.enabled && (
                  <span title={`Next run: ${new Date(workflow.next_run).toLocaleString()}`}>
                    Next: {timeUntil(workflow.next_run)}
                  </span>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
