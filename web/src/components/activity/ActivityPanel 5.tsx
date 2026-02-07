import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useWS } from "../../contexts/WebSocketContext";
import LoadingSpinner from "../LoadingSpinner";
import ActivityItemRow from "./ActivityItemRow";
import { formatRelativeTime, getTypeConfig, normalizeMetadata } from "./activityFormat";
import { SAMPLE_ACTIVITY_ITEMS, type ActivityFeedItem } from "./sampleActivity";

type FeedApiItem = {
  id: string;
  org_id: string;
  task_id?: string | null;
  agent_id?: string | null;
  type: string;
  metadata?: unknown;
  created_at: string;
  task_title?: string | null;
  agent_name?: string | null;
  summary?: string | null;
  score?: number | null;
  priority?: string | null;
};

type FeedApiResponse = {
  org_id: string;
  items: FeedApiItem[];
  total?: number;
};

function resolveOrgId(explicit?: string): string {
  if (explicit) return explicit;
  if (typeof window === "undefined") return "";
  const stored = localStorage.getItem("otter-camp-org-id");
  if (stored) return stored;
  const env = import.meta.env.VITE_ORG_ID as string | undefined;
  return env || "";
}

function parseDateInput(value: string, endOfDay = false): Date | null {
  const trimmed = value.trim();
  if (!trimmed) return null;
  const date = new Date(`${trimmed}T${endOfDay ? "23:59:59.999" : "00:00:00.000"}`);
  return Number.isNaN(date.getTime()) ? null : date;
}

function normalizeFeedItem(raw: FeedApiItem): ActivityFeedItem {
  return {
    id: String(raw.id),
    orgId: String(raw.org_id),
    taskId: raw.task_id ? String(raw.task_id) : undefined,
    agentId: raw.agent_id ? String(raw.agent_id) : undefined,
    type: String(raw.type),
    createdAt: new Date(String(raw.created_at)),
    actorName: raw.agent_name?.trim() ? String(raw.agent_name) : "System",
    taskTitle: raw.task_title?.trim() ? String(raw.task_title) : undefined,
    summary: raw.summary?.trim() ? String(raw.summary) : undefined,
    metadata: normalizeMetadata(raw.metadata),
    priority: raw.priority?.trim() ? String(raw.priority) : undefined,
  };
}

export type ActivityPanelProps = {
  className?: string;
  orgId?: string;
  apiEndpoint?: string;
  limit?: number;
};

function ActivityPanelComponent({
  className = "",
  orgId,
  apiEndpoint = "https://api.otter.camp/api/feed",
  limit = 100,
}: ActivityPanelProps) {
  const { connected, lastMessage, sendMessage } = useWS();
  const resolvedOrgId = useMemo(() => resolveOrgId(orgId), [orgId]);

  const [items, setItems] = useState<ActivityFeedItem[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [isDemoMode, setIsDemoMode] = useState(false);

  const [typeFilter, setTypeFilter] = useState<string>("all");
  const [agentFilter, setAgentFilter] = useState<string>("all");
  const [from, setFrom] = useState<string>("");
  const [to, setTo] = useState<string>("");
  const [expandedIds, setExpandedIds] = useState<Set<string>>(() => new Set());

  const abortRef = useRef<AbortController | null>(null);

  const fetchFeed = useCallback(async () => {
    const org = resolvedOrgId;
    if (!org) {
      setIsDemoMode(true);
      setItems(SAMPLE_ACTIVITY_ITEMS);
      setIsLoading(false);
      setError(null);
      return;
    }

    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    setIsLoading(true);
    setIsDemoMode(false);
    setError(null);

    try {
      const url = new URL(apiEndpoint, window.location.origin);
      url.searchParams.set("org_id", org);
      url.searchParams.set("limit", String(limit));

      const response = await fetch(url.toString(), { signal: controller.signal });
      if (!response.ok) {
        throw new Error(`Failed to load feed (${response.status})`);
      }

      const data = (await response.json()) as FeedApiResponse;
      const nextItems = (data.items || []).map(normalizeFeedItem).sort((a, b) => b.createdAt.getTime() - a.createdAt.getTime());
      setItems(nextItems);
    } catch (err) {
      if (err instanceof DOMException && err.name === "AbortError") {
        return;
      }
      setIsDemoMode(true);
      setItems(SAMPLE_ACTIVITY_ITEMS);
      setError(err instanceof Error ? err.message : "Failed to load activity");
    } finally {
      setIsLoading(false);
    }
  }, [apiEndpoint, limit, resolvedOrgId]);

  useEffect(() => {
    void fetchFeed();
    return () => abortRef.current?.abort();
  }, [fetchFeed]);

  const availableTypes = useMemo(() => {
    const unique = new Map<string, { value: string; label: string; icon: string }>();
    for (const item of items) {
      if (!item.type) continue;
      if (unique.has(item.type)) continue;
      const config = getTypeConfig(item.type);
      unique.set(item.type, { value: item.type, label: config.label, icon: config.icon });
    }
    return Array.from(unique.values()).sort((a, b) => a.label.localeCompare(b.label));
  }, [items]);

  const availableAgents = useMemo(() => {
    const set = new Set<string>();
    for (const item of items) {
      set.add(item.actorName || "System");
    }
    return Array.from(set.values()).sort((a, b) => a.localeCompare(b));
  }, [items]);

  const fromDate = useMemo(() => parseDateInput(from, false), [from]);
  const toDate = useMemo(() => parseDateInput(to, true), [to]);

  const filteredItems = useMemo(() => {
    return items.filter((item) => {
      if (typeFilter !== "all" && item.type !== typeFilter) return false;
      if (agentFilter !== "all" && item.actorName !== agentFilter) return false;
      if (fromDate && item.createdAt < fromDate) return false;
      if (toDate && item.createdAt > toDate) return false;
      return true;
    });
  }, [items, typeFilter, agentFilter, fromDate, toDate]);

  const toggleExpanded = useCallback((id: string) => {
    setExpandedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const clearFilters = useCallback(() => {
    setTypeFilter("all");
    setAgentFilter("all");
    setFrom("");
    setTo("");
  }, []);

  // Subscribe to org-scoped websocket updates (no-op for demo mode).
  useEffect(() => {
    if (!connected) return;
    if (!resolvedOrgId) return;
    sendMessage({ type: "subscribe", org_id: resolvedOrgId });
  }, [connected, resolvedOrgId, sendMessage]);

  useEffect(() => {
    if (!lastMessage || lastMessage.type !== "FeedItemsAdded") return;
    if (isDemoMode) return;
    if (!resolvedOrgId) return;

    const payload = lastMessage.data as unknown;
    const incomingRaw = Array.isArray(payload)
      ? payload
      : payload && typeof payload === "object" && Array.isArray((payload as { items?: unknown }).items)
        ? ((payload as { items: unknown[] }).items ?? [])
        : [];

    if (incomingRaw.length === 0) return;

    setItems((prev) => {
      const existingIds = new Set(prev.map((item) => item.id));
      const agentNames = new Map<string, string>();
      const taskTitles = new Map<string, string>();

      for (const item of prev) {
        if (item.agentId && item.actorName) agentNames.set(item.agentId, item.actorName);
        if (item.taskId && item.taskTitle) taskTitles.set(item.taskId, item.taskTitle);
      }

      const normalizedIncoming: ActivityFeedItem[] = [];

      for (const raw of incomingRaw) {
        if (!raw || typeof raw !== "object") continue;
        const record = raw as Record<string, unknown>;

        const id = typeof record.id === "string" ? record.id : "";
        if (!id || existingIds.has(id)) continue;

        const org = typeof record.org_id === "string" ? record.org_id : "";
        if (org && org !== resolvedOrgId) continue;

        const type = typeof record.type === "string" ? record.type : "";
        if (!type) continue;

        const createdAtValue = typeof record.created_at === "string" ? record.created_at : "";
        const createdAt = createdAtValue ? new Date(createdAtValue) : new Date();

        const taskId = typeof record.task_id === "string" ? record.task_id : undefined;
        const agentId = typeof record.agent_id === "string" ? record.agent_id : undefined;
        const metadata = normalizeMetadata(record.metadata);

        const actorFromMetadata =
          (typeof metadata.agent_name === "string" ? metadata.agent_name : "") ||
          (typeof metadata.agentName === "string" ? metadata.agentName : "") ||
          (typeof metadata.actor === "string" ? metadata.actor : "") ||
          (typeof metadata.user === "string" ? metadata.user : "") ||
          (typeof metadata.author === "string" ? metadata.author : "");

        const actorName = agentId
          ? agentNames.get(agentId) || actorFromMetadata || "Agent"
          : actorFromMetadata || "System";

        const taskFromMetadata =
          (typeof metadata.task_title === "string" ? metadata.task_title : "") ||
          (typeof metadata.taskTitle === "string" ? metadata.taskTitle : "") ||
          (typeof metadata.title === "string" ? metadata.title : "");

        const taskTitle = taskId ? taskTitles.get(taskId) || taskFromMetadata || undefined : taskFromMetadata || undefined;

        normalizedIncoming.push({
          id,
          orgId: org || resolvedOrgId,
          taskId,
          agentId,
          type,
          createdAt: Number.isNaN(createdAt.getTime()) ? new Date() : createdAt,
          actorName,
          taskTitle,
          metadata,
        });
      }

      if (normalizedIncoming.length === 0) return prev;

      const merged = [...normalizedIncoming, ...prev].sort(
        (a, b) => b.createdAt.getTime() - a.createdAt.getTime(),
      );

      return merged.slice(0, 500);
    });
  }, [isDemoMode, lastMessage, resolvedOrgId]);

  return (
    <section
      className={`overflow-hidden rounded-2xl border border-slate-200 bg-white/80 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/80 ${className}`}
      aria-label="Activity"
    >
      <header className="border-b border-slate-200 px-5 py-4 dark:border-slate-800">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <div className="flex items-center gap-3">
              <div className="inline-flex h-10 w-10 items-center justify-center rounded-full bg-emerald-100 text-lg dark:bg-emerald-900/40">
                📡
              </div>
              <div className="min-w-0">
                <h2 className="truncate text-lg font-semibold text-slate-900 dark:text-white">
                  Activity
                </h2>
                <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">
                  {isDemoMode ? (
                    <>Demo data • Connect an org to see live updates</>
                  ) : (
                    <>
                      <span
                        className={`mr-1 inline-block h-2 w-2 rounded-full ${
                          connected ? "bg-emerald-500" : "bg-slate-400"
                        }`}
                        aria-hidden="true"
                      />
                      {connected ? "Live" : "Reconnecting…"}
                      {resolvedOrgId ? <> • Org {resolvedOrgId.slice(0, 8)}…</> : null}
                    </>
                  )}
                </p>
              </div>
            </div>
          </div>

          <div className="flex flex-shrink-0 items-center gap-2">
            <button
              type="button"
              onClick={() => void fetchFeed()}
              className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm font-medium text-slate-700 shadow-sm transition hover:border-slate-300 hover:bg-slate-50 focus:outline-none focus:ring-2 focus:ring-emerald-500/30 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:border-slate-600 dark:hover:bg-slate-700"
              aria-label="Refresh activity"
            >
              ↻ Refresh
            </button>
          </div>
        </div>

        {/* Filters */}
        <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
          <div className="flex flex-col gap-1">
            <label
              htmlFor="activity-type-filter"
              className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400"
            >
              Type
            </label>
            <select
              id="activity-type-filter"
              value={typeFilter}
              onChange={(e) => setTypeFilter(e.target.value)}
              className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm transition focus:border-emerald-500 focus:outline-none focus:ring-2 focus:ring-emerald-500/20 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
            >
              <option value="all">All types</option>
              {availableTypes.map((t) => (
                <option key={t.value} value={t.value}>
                  {t.icon} {t.label}
                </option>
              ))}
            </select>
          </div>

          <div className="flex flex-col gap-1">
            <label
              htmlFor="activity-agent-filter"
              className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400"
            >
              Agent
            </label>
            <select
              id="activity-agent-filter"
              value={agentFilter}
              onChange={(e) => setAgentFilter(e.target.value)}
              className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm transition focus:border-emerald-500 focus:outline-none focus:ring-2 focus:ring-emerald-500/20 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
            >
              <option value="all">All agents</option>
              {availableAgents.map((agent) => (
                <option key={agent} value={agent}>
                  {agent}
                </option>
              ))}
            </select>
          </div>

          <div className="flex flex-col gap-1">
            <label
              htmlFor="activity-from-filter"
              className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400"
            >
              From
            </label>
            <input
              id="activity-from-filter"
              type="date"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm transition focus:border-emerald-500 focus:outline-none focus:ring-2 focus:ring-emerald-500/20 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
            />
          </div>

          <div className="flex flex-col gap-1">
            <label
              htmlFor="activity-to-filter"
              className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400"
            >
              To
            </label>
            <input
              id="activity-to-filter"
              type="date"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm transition focus:border-emerald-500 focus:outline-none focus:ring-2 focus:ring-emerald-500/20 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
            />
          </div>

          <div className="flex items-end">
            <button
              type="button"
              onClick={clearFilters}
              className="w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm font-medium text-slate-700 shadow-sm transition hover:border-slate-300 hover:bg-slate-50 focus:outline-none focus:ring-2 focus:ring-emerald-500/30 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:border-slate-600 dark:hover:bg-slate-700"
            >
              Clear
            </button>
          </div>
        </div>

        <div className="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs text-slate-500 dark:text-slate-400">
          <div>
            Showing <span className="font-medium text-slate-700 dark:text-slate-200">{filteredItems.length}</span>{" "}
            of <span className="font-medium text-slate-700 dark:text-slate-200">{items.length}</span>
          </div>
          {items[0]?.createdAt ? (
            <div>
              Latest:{" "}
              <span className="font-medium text-slate-700 dark:text-slate-200">
                {formatRelativeTime(items[0].createdAt)}
              </span>
            </div>
          ) : null}
        </div>
      </header>

      <div className="max-h-[70vh] overflow-y-auto p-4">
        {isLoading ? (
          <div className="flex min-h-[240px] items-center justify-center">
            <LoadingSpinner size="lg" message="Loading activity..." />
          </div>
        ) : filteredItems.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-14 text-center">
            <div className="text-4xl">🦦</div>
            <p className="mt-3 text-sm font-medium text-slate-700 dark:text-slate-200">
              No activity yet
            </p>
            <p className="mt-1 max-w-md text-xs text-slate-500 dark:text-slate-400">
              Try adjusting your filters, or connect a live org to see updates.
            </p>
            {error ? (
              <p className="mt-3 max-w-md rounded-xl bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">
                {error}
              </p>
            ) : null}
          </div>
        ) : (
          <div className="space-y-3">
            {filteredItems.map((item) => (
              <ActivityItemRow
                key={item.id}
                item={item}
                expanded={expandedIds.has(item.id)}
                onToggle={() => toggleExpanded(item.id)}
              />
            ))}
          </div>
        )}
      </div>
    </section>
  );
}

const ActivityPanel = memo(ActivityPanelComponent);

export default ActivityPanel;
