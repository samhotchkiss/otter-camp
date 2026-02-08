import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import AgentActivityTimeline from "../components/agents/AgentActivityTimeline";
import AgentIdentityEditor from "../components/agents/AgentIdentityEditor";
import AgentMemoryBrowser from "../components/agents/AgentMemoryBrowser";
import { useAgentActivity } from "../hooks/useAgentActivity";

const STATUS_OPTIONS = ["started", "completed", "failed", "timeout"];
const API_URL = import.meta.env.VITE_API_URL || "https://api.otter.camp";

type AgentDetailPayload = {
  agent?: {
    id?: string;
    workspace_agent_id?: string;
    name?: string;
    status?: string;
    model?: string;
    heartbeat_every?: string;
    channel?: string;
    session_key?: string;
    last_seen?: string;
  };
  sync?: {
    current_task?: string;
    context_tokens?: number;
    total_tokens?: number;
  };
};

function toTitleCase(raw: string): string {
  return raw
    .split(/[._-]/g)
    .filter(Boolean)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

type AgentDetailTab = "overview" | "identity" | "memory" | "activity" | "settings";

const DETAIL_TABS: Array<{ id: AgentDetailTab; label: string }> = [
  { id: "overview", label: "Overview" },
  { id: "identity", label: "Identity" },
  { id: "memory", label: "Memory" },
  { id: "activity", label: "Activity" },
  { id: "settings", label: "Settings" },
];

export default function AgentDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [activeTab, setActiveTab] = useState<AgentDetailTab>("overview");
  const [agentDetail, setAgentDetail] = useState<AgentDetailPayload | null>(null);
  const [detailLoading, setDetailLoading] = useState(true);
  const [detailError, setDetailError] = useState<string | null>(null);

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

  const loadAgentDetail = useCallback(async () => {
    if (!id) {
      setAgentDetail(null);
      setDetailLoading(false);
      setDetailError("Missing agent id.");
      return;
    }
    setDetailLoading(true);
    setDetailError(null);
    try {
      const response = await fetch(`${API_URL}/api/admin/agents/${encodeURIComponent(id)}`);
      if (!response.ok) {
        throw new Error(`Failed to load agent detail (${response.status})`);
      }
      const payload = (await response.json()) as AgentDetailPayload;
      setAgentDetail(payload);
    } catch (error) {
      const message = error instanceof Error ? error.message : "Failed to load agent detail";
      setDetailError(message);
      setAgentDetail(null);
    } finally {
      setDetailLoading(false);
    }
  }, [id]);

  useEffect(() => {
    void loadAgentDetail();
  }, [loadAgentDetail]);

  const subtitle = useMemo(() => {
    const displayName = agentDetail?.agent?.name?.trim();
    if (displayName) {
      return `Managing ${displayName}`;
    };
    return id ? `Managing agent \`${id}\`` : "Managing agent";
  }, [agentDetail?.agent?.name, id]);

  const overviewStatus = agentDetail?.agent?.status?.trim() || "unknown";
  const overviewModel = agentDetail?.agent?.model?.trim() || "n/a";
  const overviewHeartbeat = agentDetail?.agent?.heartbeat_every?.trim() || "n/a";
  const overviewChannel = agentDetail?.agent?.channel?.trim() || "n/a";
  const overviewLastSeen = agentDetail?.agent?.last_seen?.trim() || "n/a";
  const overviewTask = agentDetail?.sync?.current_task?.trim() || "n/a";
  const overviewContextTokens = agentDetail?.sync?.context_tokens ?? 0;
  const overviewTotalTokens = agentDetail?.sync?.total_tokens ?? 0;

  if (!id) {
    return (
      <div className="rounded-xl border border-rose-200 bg-rose-50 p-6 text-rose-700">
        Missing agent id.
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <header className="space-y-3">
        <Link to="/agents" className="inline-flex text-sm font-medium text-[#C9A86C] hover:underline">
          Back to agents
        </Link>
        <div className="flex flex-wrap items-end justify-between gap-4">
          <div>
            <h1 className="text-2xl font-semibold text-[var(--text)]">Agent Details</h1>
            <p className="mt-1 text-sm text-[var(--text-muted)]">{subtitle}</p>
          </div>
          <button
            type="button"
            onClick={() => {
              void loadAgentDetail();
              void refresh();
            }}
            className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-sm text-[var(--text)] hover:bg-[var(--surface-alt)]"
          >
            Refresh
          </button>
        </div>
      </header>

      <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-3">
        <div className="flex flex-wrap gap-2">
          {DETAIL_TABS.map((tab) => (
            <button
              key={tab.id}
              type="button"
              onClick={() => setActiveTab(tab.id)}
              className={`rounded-lg border px-3 py-1.5 text-sm font-medium transition ${
                activeTab === tab.id
                  ? "border-[#C9A86C] bg-[#C9A86C]/15 text-[#C9A86C]"
                  : "border-[var(--border)] bg-[var(--surface)] text-[var(--text-muted)] hover:text-[var(--text)]"
              }`}
            >
              {tab.label}
            </button>
          ))}
        </div>
      </section>

      {activeTab === "overview" && (
        <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
          {detailLoading ? (
            <p className="text-sm text-[var(--text-muted)]">Loading overview...</p>
          ) : detailError ? (
            <p className="text-sm text-rose-400">{detailError}</p>
          ) : (
            <dl className="grid gap-3 sm:grid-cols-2">
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <dt className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Name</dt>
                <dd className="mt-1 text-sm font-medium text-[var(--text)]">{agentDetail?.agent?.name || id}</dd>
              </div>
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <dt className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Status</dt>
                <dd className="mt-1 text-sm font-medium text-[var(--text)]">{overviewStatus}</dd>
              </div>
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <dt className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Model</dt>
                <dd className="mt-1 text-sm font-medium text-[var(--text)]">{overviewModel}</dd>
              </div>
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <dt className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Heartbeat</dt>
                <dd className="mt-1 text-sm font-medium text-[var(--text)]">{overviewHeartbeat}</dd>
              </div>
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <dt className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Channel</dt>
                <dd className="mt-1 text-sm font-medium text-[var(--text)]">{overviewChannel}</dd>
              </div>
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <dt className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Last Seen</dt>
                <dd className="mt-1 text-sm font-medium text-[var(--text)]">{overviewLastSeen}</dd>
              </div>
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3 sm:col-span-2">
                <dt className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Current Task</dt>
                <dd className="mt-1 text-sm font-medium text-[var(--text)]">{overviewTask}</dd>
              </div>
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <dt className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Context Tokens</dt>
                <dd className="mt-1 text-sm font-medium text-[var(--text)]">{overviewContextTokens.toLocaleString()}</dd>
              </div>
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <dt className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Total Tokens</dt>
                <dd className="mt-1 text-sm font-medium text-[var(--text)]">{overviewTotalTokens.toLocaleString()}</dd>
              </div>
            </dl>
          )}
        </section>
      )}

      {activeTab === "identity" && (
        <AgentIdentityEditor agentID={id} />
      )}

      {activeTab === "memory" && (
        <AgentMemoryBrowser agentID={id} />
      )}

      {activeTab === "activity" && (
        <>
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
        </>
      )}

      {activeTab === "settings" && (
        <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
          {detailLoading ? (
            <p className="text-sm text-[var(--text-muted)]">Loading settings...</p>
          ) : detailError ? (
            <p className="text-sm text-rose-400">{detailError}</p>
          ) : (
            <dl className="grid gap-3 sm:grid-cols-2">
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <dt className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Model</dt>
                <dd className="mt-1 text-sm font-medium text-[var(--text)]">{overviewModel}</dd>
              </div>
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <dt className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Heartbeat</dt>
                <dd className="mt-1 text-sm font-medium text-[var(--text)]">{overviewHeartbeat}</dd>
              </div>
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3 sm:col-span-2">
                <dt className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Channel Binding</dt>
                <dd className="mt-1 text-sm font-medium text-[var(--text)]">{overviewChannel}</dd>
              </div>
            </dl>
          )}
        </section>
      )}
    </div>
  );
}
