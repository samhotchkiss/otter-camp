import { useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import AgentActivityTimeline from "../components/agents/AgentActivityTimeline";
import { useAgentActivity } from "../hooks/useAgentActivity";

const STATUS_OPTIONS = ["started", "completed", "failed", "timeout"];
const API_URL = import.meta.env.VITE_API_URL || "https://api.otter.camp";

type AgentSummary = {
  id: string;
  name: string;
  role?: string;
};

function toTitleCase(raw: string): string {
  return raw
    .split(/[._-]/g)
    .filter(Boolean)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

export default function AgentDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [agentSummary, setAgentSummary] = useState<AgentSummary | null>(null);

  const {
    events,
    isLoading,
    isLoadingMore,
    error,
    hasMore,
    filters,
    setFilters,
    refresh,
    loadMore,
  } = useAgentActivity({
    mode: "agent",
    agentId: id,
    limit: 50,
  });

  const triggerOptions = useMemo(() => {
    const unique = new Set<string>();
    for (const event of events) {
      if (event.trigger) {
        unique.add(event.trigger);
      }
    }
    return Array.from(unique).sort();
  }, [events]);

  const channelOptions = useMemo(() => {
    const unique = new Set<string>();
    for (const event of events) {
      if (event.channel) {
        unique.add(event.channel);
      }
    }
    return Array.from(unique).sort();
  }, [events]);

  useEffect(() => {
    if (!id) {
      setAgentSummary(null);
      return;
    }

    let cancelled = false;
    const loadAgentSummary = async () => {
      try {
        const response = await fetch(`${API_URL}/api/sync/agents`);
        if (!response.ok) {
          return;
        }
        const payload = await response.json();
        const candidates = Array.isArray(payload?.agents)
          ? payload.agents
          : Array.isArray(payload)
            ? payload
            : [];
        const matched = candidates.find((entry: unknown) => {
          if (!entry || typeof entry !== "object") {
            return false;
          }
          const record = entry as Record<string, unknown>;
          return typeof record.id === "string" && record.id.trim().toLowerCase() === id.trim().toLowerCase();
        });
        if (!matched || cancelled) {
          return;
        }
        const record = matched as Record<string, unknown>;
        const name = typeof record.name === "string" ? record.name.trim() : "";
        const role = typeof record.role === "string" ? record.role.trim() : "";
        if (!name) {
          return;
        }
        setAgentSummary({
          id,
          name,
          role: role || undefined,
        });
      } catch {
        if (!cancelled) {
          setAgentSummary(null);
        }
      }
    };

    void loadAgentSummary();
    return () => {
      cancelled = true;
    };
  }, [id]);

  if (!id) {
    return (
      <div className="rounded-xl border border-rose-200 bg-rose-50 p-6 text-rose-700">
        Missing agent id.
      </div>
    );
  }

  const subtitle = agentSummary
    ? `Timeline for ${agentSummary.name}${agentSummary.role ? ` (${agentSummary.role})` : ""}`
    : `Timeline for agent \`${id}\``;

  return (
    <div className="space-y-6">
      <header className="space-y-3">
        <Link to="/agents" className="inline-flex text-sm font-medium text-[#C9A86C] hover:underline">
          Back to agents
        </Link>
        <div className="flex flex-wrap items-end justify-between gap-4">
          <div>
            <h1 className="text-2xl font-semibold text-[var(--text)]">Agent Activity</h1>
            <p className="mt-1 text-sm text-[var(--text-muted)]">{subtitle}</p>
          </div>
          <button
            type="button"
            onClick={() => void refresh()}
            className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-sm text-[var(--text)] hover:bg-[var(--surface-alt)]"
          >
            Refresh
          </button>
        </div>
      </header>

      <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
        <div className="grid gap-3 md:grid-cols-3">
          <label className="flex flex-col gap-1 text-sm text-[var(--text-muted)]" htmlFor="agent-activity-trigger">
            Trigger
            <select
              id="agent-activity-trigger"
              value={filters.trigger || ""}
              onChange={(event) => setFilters({ trigger: event.target.value || undefined })}
              className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)]"
            >
              <option value="">All triggers</option>
              {triggerOptions.map((trigger) => (
                <option key={trigger} value={trigger}>
                  {toTitleCase(trigger)}
                </option>
              ))}
            </select>
          </label>

          <label className="flex flex-col gap-1 text-sm text-[var(--text-muted)]" htmlFor="agent-activity-channel">
            Channel
            <select
              id="agent-activity-channel"
              value={filters.channel || ""}
              onChange={(event) => setFilters({ channel: event.target.value || undefined })}
              className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)]"
            >
              <option value="">All channels</option>
              {channelOptions.map((channel) => (
                <option key={channel} value={channel}>
                  {toTitleCase(channel)}
                </option>
              ))}
            </select>
          </label>

          <label className="flex flex-col gap-1 text-sm text-[var(--text-muted)]" htmlFor="agent-activity-status">
            Status
            <select
              id="agent-activity-status"
              value={filters.status || ""}
              onChange={(event) => setFilters({ status: event.target.value || undefined })}
              className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)]"
            >
              <option value="">All statuses</option>
              {STATUS_OPTIONS.map((status) => (
                <option key={status} value={status}>
                  {toTitleCase(status)}
                </option>
              ))}
            </select>
          </label>
        </div>
      </section>

      <AgentActivityTimeline
        events={events}
        isLoading={isLoading}
        isLoadingMore={isLoadingMore}
        error={error}
        hasMore={hasMore}
        onLoadMore={() => void loadMore()}
        emptyMessage="No activity events for this agent yet."
      />
    </div>
  );
}
