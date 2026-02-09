import { useEffect, useMemo, useState } from "react";
import ActivityPanel from "../components/activity/ActivityPanel";
import AgentActivityTimeline from "../components/agents/AgentActivityTimeline";
import { getActivityDescription, normalizeMetadata } from "../components/activity/activityFormat";
import { useAgentActivity, type AgentActivityEvent } from "../hooks/useAgentActivity";
import { API_URL } from "../lib/api";

type FeedFallbackItem = {
  id?: string;
  org_id?: string;
  agent_id?: string | null;
  agent_name?: string | null;
  type?: string;
  created_at?: string;
  task_title?: string | null;
  summary?: string | null;
  metadata?: unknown;
};

type FeedFallbackResponse = {
  items?: FeedFallbackItem[];
};

function toDate(value: string | undefined): Date {
  if (!value) return new Date();
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? new Date() : parsed;
}

function resolveOrgId(): string {
  if (typeof window === "undefined") return "";
  return localStorage.getItem("otter-camp-org-id") || "";
}

function resolveToken(): string {
  if (typeof window === "undefined") return "";
  return localStorage.getItem("otter_camp_token") || "";
}

function resolveChannel(type: string): string {
  if (!type) return "activity";
  if (type.includes(".")) return type.split(".")[0] || "activity";
  if (type.includes("_")) return type.split("_")[0] || "activity";
  return type;
}

function mapFallbackItem(raw: FeedFallbackItem): AgentActivityEvent | null {
  const id = String(raw.id || "").trim();
  const orgId = String(raw.org_id || "").trim();
  const type = String(raw.type || "").trim();
  if (!id || !orgId || !type) return null;

  const actorName = String(raw.agent_name || "").trim() || "System";
  const taskTitle = String(raw.task_title || "").trim();
  const metadata = normalizeMetadata(raw.metadata);
  const description = getActivityDescription({
    type,
    actorName,
    taskTitle: taskTitle || undefined,
    summary: String(raw.summary || "").trim() || undefined,
    metadata,
  });

  const projectID = typeof metadata.project_id === "string" ? metadata.project_id : undefined;
  const createdAt = toDate(raw.created_at);

  return {
    id: `feed-${id}`,
    orgId,
    agentId: String(raw.agent_id || "").trim() || actorName.toLowerCase(),
    sessionKey: `feed:${type}:${id}`,
    trigger: type,
    channel: resolveChannel(type),
    summary: `${actorName} ${description}`,
    detail: undefined,
    projectId: projectID,
    issueId: undefined,
    issueNumber: undefined,
    threadId: undefined,
    tokensUsed: 0,
    modelUsed: undefined,
    durationMs: 0,
    status: "completed",
    startedAt: createdAt,
    completedAt: createdAt,
    createdAt,
  };
}

export default function FeedPage() {
  const [mode, setMode] = useState<"realtime" | "agent">("realtime");
  const { events, isLoading, isLoadingMore, error, hasMore, loadMore } = useAgentActivity({
    mode: "recent",
    limit: 100,
  });
  const [fallbackEvents, setFallbackEvents] = useState<AgentActivityEvent[]>([]);
  const [isFallbackLoading, setIsFallbackLoading] = useState(false);
  const [fallbackError, setFallbackError] = useState<string | null>(null);

  useEffect(() => {
    if (events.length > 0) {
      setFallbackEvents([]);
      setFallbackError(null);
      return;
    }

    const orgId = resolveOrgId();
    if (!orgId) {
      setFallbackEvents([]);
      setFallbackError("Missing org_id");
      return;
    }

    const token = resolveToken();
    const url = new URL("/api/feed", API_URL);
    url.searchParams.set("org_id", orgId);
    url.searchParams.set("limit", "100");

    const controller = new AbortController();
    setIsFallbackLoading(true);
    setFallbackError(null);

    void fetch(url.toString(), {
      signal: controller.signal,
      headers: {
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
    })
      .then(async (response) => {
        if (!response.ok) {
          throw new Error(`Failed to load fallback feed (${response.status})`);
        }
        const payload = (await response.json()) as FeedFallbackResponse;
        const items = (payload.items || [])
          .map(mapFallbackItem)
          .filter((item): item is AgentActivityEvent => item !== null);
        setFallbackEvents(items);
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") {
          return;
        }
        setFallbackEvents([]);
        setFallbackError(err instanceof Error ? err.message : "Failed to load fallback feed");
      })
      .finally(() => {
        setIsFallbackLoading(false);
      });

    return () => controller.abort();
  }, [events]);

  const agentEvents = useMemo(() => (events.length > 0 ? events : fallbackEvents), [events, fallbackEvents]);
  const agentLoading = isLoading || (events.length === 0 && isFallbackLoading);
  const agentError = agentEvents.length === 0 ? error || fallbackError : null;

  return (
    <div className="w-full">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold text-[var(--text)]">
          Activity Feed
        </h1>
        <p className="mt-1 text-sm text-[var(--text-muted)]">
          Real-time updates from across your projects and agents
        </p>
      </div>

      <div className="mb-4 inline-flex rounded-lg border border-[var(--border)] bg-[var(--surface)] p-1">
        <button
          type="button"
          onClick={() => setMode("realtime")}
          className={`rounded-md px-3 py-1.5 text-sm font-medium ${
            mode === "realtime"
              ? "bg-[#C9A86C] text-white"
              : "text-[var(--text-muted)] hover:bg-[var(--surface-alt)]"
          }`}
        >
          Realtime
        </button>
        <button
          type="button"
          onClick={() => setMode("agent")}
          className={`rounded-md px-3 py-1.5 text-sm font-medium ${
            mode === "agent"
              ? "bg-[#C9A86C] text-white"
              : "text-[var(--text-muted)] hover:bg-[var(--surface-alt)]"
          }`}
        >
          Agent Activity
        </button>
      </div>

      {mode === "realtime" ? (
        <ActivityPanel className="min-h-[70vh]" />
      ) : (
        <AgentActivityTimeline
          events={agentEvents}
          isLoading={agentLoading}
          isLoadingMore={isLoadingMore}
          error={agentError}
          hasMore={hasMore}
          onLoadMore={() => void loadMore()}
          emptyMessage="No agent activity yet."
        />
      )}
    </div>
  );
}
