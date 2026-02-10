import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { API_URL } from "../lib/api";
import { useOptionalWS } from "../contexts/WebSocketContext";

const ORG_STORAGE_KEY = "otter-camp-org-id";
const TOKEN_STORAGE_KEY = "otter_camp_token";
const DEFAULT_LIMIT = 50;
const MAX_PROCESSED_REALTIME_IDS = 1000;

export type AgentActivityStatus = "started" | "completed" | "failed" | "timeout";

export type AgentActivityEvent = {
  id: string;
  orgId: string;
  agentId: string;
  sessionKey: string;
  trigger: string;
  channel?: string;
  summary: string;
  detail?: string;
  projectId?: string;
  issueId?: string;
  issueNumber?: number;
  threadId?: string;
  tokensUsed: number;
  modelUsed?: string;
  commitSha?: string;
  commitBranch?: string;
  commitRemote?: string;
  pushStatus?: "succeeded" | "failed" | "unknown";
  durationMs: number;
  status: AgentActivityStatus;
  startedAt: Date;
  completedAt?: Date;
  createdAt: Date;
};

export type AgentActivityFilters = {
  trigger?: string;
  channel?: string;
  status?: AgentActivityStatus | string;
  projectId?: string;
  agentId?: string;
};

type AgentActivityListResponse = {
  items: AgentActivityEvent[];
  nextBefore?: string;
};

type RawAgentActivityEvent = {
  id?: unknown;
  org_id?: unknown;
  agent_id?: unknown;
  session_key?: unknown;
  trigger?: unknown;
  channel?: unknown;
  summary?: unknown;
  detail?: unknown;
  project_id?: unknown;
  issue_id?: unknown;
  issue_number?: unknown;
  thread_id?: unknown;
  tokens_used?: unknown;
  model_used?: unknown;
  commit_sha?: unknown;
  commit_branch?: unknown;
  commit_remote?: unknown;
  push_status?: unknown;
  duration_ms?: unknown;
  status?: unknown;
  started_at?: unknown;
  completed_at?: unknown;
  created_at?: unknown;
};

type RawAgentActivityResponse = {
  items?: unknown;
  next_before?: unknown;
};

export type UseAgentActivityOptions = {
  mode?: "recent" | "agent";
  agentId?: string;
  orgId?: string;
  limit?: number;
  initialFilters?: AgentActivityFilters;
};

export type UseAgentActivityResult = {
  events: AgentActivityEvent[];
  isLoading: boolean;
  isLoadingMore: boolean;
  error: string | null;
  filters: AgentActivityFilters;
  nextBefore: string | null;
  hasMore: boolean;
  setFilters: (next: Partial<AgentActivityFilters>) => void;
  refresh: () => Promise<void>;
  loadMore: () => Promise<void>;
};

function normalizeDate(value: unknown): Date {
  if (typeof value === "string") {
    const parsed = new Date(value);
    if (!Number.isNaN(parsed.getTime())) {
      return parsed;
    }
  }
  return new Date();
}

function normalizeString(value: unknown): string | undefined {
  if (typeof value !== "string") {
    return undefined;
  }
  const trimmed = value.trim();
  return trimmed || undefined;
}

function normalizeNumber(value: unknown, fallback = 0): number {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string") {
    const parsed = Number(value.trim());
    if (Number.isFinite(parsed)) {
      return parsed;
    }
  }
  return fallback;
}

function normalizeStatus(value: unknown): AgentActivityStatus {
  const normalized = normalizeString(value)?.toLowerCase();
  if (
    normalized === "started" ||
    normalized === "completed" ||
    normalized === "failed" ||
    normalized === "timeout"
  ) {
    return normalized;
  }
  return "completed";
}

export function parseAgentActivityEvent(raw: unknown): AgentActivityEvent | null {
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const record = raw as RawAgentActivityEvent;
  const id = normalizeString(record.id);
  const orgId = normalizeString(record.org_id);
  const agentId = normalizeString(record.agent_id);
  const sessionKey = normalizeString(record.session_key);
  const trigger = normalizeString(record.trigger);
  const summary = normalizeString(record.summary);

  if (!id || !orgId || !agentId || !trigger || !summary) {
    return null;
  }

  const startedAt = normalizeDate(record.started_at);
  const completedAtRaw = normalizeString(record.completed_at);
  const createdAtRaw = normalizeString(record.created_at);
  const pushStatus = normalizeString(record.push_status);

  return {
    id,
    orgId,
    agentId,
    sessionKey: sessionKey || "",
    trigger,
    channel: normalizeString(record.channel),
    summary,
    detail: normalizeString(record.detail),
    projectId: normalizeString(record.project_id),
    issueId: normalizeString(record.issue_id),
    issueNumber: normalizeNumber(record.issue_number, 0) || undefined,
    threadId: normalizeString(record.thread_id),
    tokensUsed: normalizeNumber(record.tokens_used, 0),
    modelUsed: normalizeString(record.model_used),
    commitSha: normalizeString(record.commit_sha),
    commitBranch: normalizeString(record.commit_branch),
    commitRemote: normalizeString(record.commit_remote),
    pushStatus:
      pushStatus === "succeeded" || pushStatus === "failed" || pushStatus === "unknown"
        ? pushStatus
        : undefined,
    durationMs: normalizeNumber(record.duration_ms, 0),
    status: normalizeStatus(record.status),
    startedAt,
    completedAt: completedAtRaw ? normalizeDate(completedAtRaw) : undefined,
    createdAt: createdAtRaw ? normalizeDate(createdAtRaw) : startedAt,
  };
}

function parseAgentActivityResponse(raw: unknown): AgentActivityListResponse {
  if (!raw || typeof raw !== "object") {
    return { items: [] };
  }
  const record = raw as RawAgentActivityResponse;
  const items = Array.isArray(record.items)
    ? record.items
        .map(parseAgentActivityEvent)
        .filter((event): event is AgentActivityEvent => event !== null)
    : [];

  return {
    items,
    nextBefore: normalizeString(record.next_before),
  };
}

export function parseRealtimeAgentActivityEvent(raw: unknown): AgentActivityEvent | null {
  if (raw && typeof raw === "object") {
    const record = raw as Record<string, unknown>;
    const direct = parseAgentActivityEvent(record);
    if (direct) {
      return direct;
    }
    const nestedCandidate = record.event ?? record.activity ?? record.data;
    const nested = parseAgentActivityEvent(nestedCandidate);
    if (nested) {
      return nested;
    }
  }
  return parseAgentActivityEvent(raw);
}

export function matchesActivityFilters(
  event: AgentActivityEvent,
  options: {
    mode: "recent" | "agent";
    scopedAgentID?: string;
    filters: AgentActivityFilters;
  },
): boolean {
  if (options.mode === "agent" && options.scopedAgentID && event.agentId !== options.scopedAgentID) {
    return false;
  }
  if (options.filters.agentId && event.agentId !== options.filters.agentId) {
    return false;
  }
  if (options.filters.trigger && event.trigger !== options.filters.trigger) {
    return false;
  }
  if (options.filters.channel && event.channel !== options.filters.channel) {
    return false;
  }
  if (options.filters.status && event.status !== options.filters.status) {
    return false;
  }
  if (options.filters.projectId && event.projectId !== options.filters.projectId) {
    return false;
  }
  return true;
}

export function trackProcessedRealtimeID(
  idSet: Set<string>,
  idOrder: string[],
  id: string,
  maxSize = MAX_PROCESSED_REALTIME_IDS,
): boolean {
  if (idSet.has(id)) {
    return false;
  }
  idSet.add(id);
  idOrder.push(id);
  while (idOrder.length > maxSize) {
    const oldest = idOrder.shift();
    if (oldest) {
      idSet.delete(oldest);
    }
  }
  return true;
}

function resolveOrgId(explicitOrgId?: string): string {
  const fromArg = normalizeString(explicitOrgId);
  if (fromArg) {
    return fromArg;
  }
  if (typeof window === "undefined") {
    return "";
  }
  try {
    return normalizeString(window.localStorage.getItem(ORG_STORAGE_KEY)) || "";
  } catch {
    return "";
  }
}

function resolveToken(): string {
  if (typeof window === "undefined") {
    return "";
  }
  try {
    return normalizeString(window.localStorage.getItem(TOKEN_STORAGE_KEY)) || "";
  } catch {
    return "";
  }
}

export function buildAgentActivityURL(params: {
  mode: "recent" | "agent";
  orgId: string;
  agentId?: string;
  before?: string;
  limit: number;
  filters: AgentActivityFilters;
}): string {
  const basePath =
    params.mode === "agent"
      ? `/api/agents/${encodeURIComponent(params.agentId || "")}/activity`
      : "/api/activity/recent";

  const url = new URL(basePath, API_URL);
  url.searchParams.set("org_id", params.orgId);
  url.searchParams.set("limit", String(params.limit));

  if (params.before) {
    url.searchParams.set("before", params.before);
  }
  if (params.filters.trigger) {
    url.searchParams.set("trigger", params.filters.trigger);
  }
  if (params.filters.channel) {
    url.searchParams.set("channel", params.filters.channel);
  }
  if (params.filters.status) {
    url.searchParams.set("status", params.filters.status);
  }
  if (params.filters.projectId) {
    url.searchParams.set("project_id", params.filters.projectId);
  }
  if (params.mode === "recent" && params.filters.agentId) {
    url.searchParams.set("agent_id", params.filters.agentId);
  }

  return url.toString();
}

export function useAgentActivity(options: UseAgentActivityOptions = {}): UseAgentActivityResult {
  const mode = options.mode ?? "recent";
  const limit = options.limit ?? DEFAULT_LIMIT;
  const resolvedOrgId = useMemo(() => resolveOrgId(options.orgId), [options.orgId]);
  const ws = useOptionalWS();
  const wsMessage = ws?.lastMessage ?? null;

  const [events, setEvents] = useState<AgentActivityEvent[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [isLoadingMore, setIsLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [nextBefore, setNextBefore] = useState<string | null>(null);
  const [filters, setFiltersState] = useState<AgentActivityFilters>(options.initialFilters ?? {});
  const processedRealtimeIDsRef = useRef<Set<string>>(new Set());
  const processedRealtimeIDOrderRef = useRef<string[]>([]);

  const hasMore = Boolean(nextBefore);

  const setFilters = useCallback((next: Partial<AgentActivityFilters>) => {
    setFiltersState((prev) => ({ ...prev, ...next }));
  }, []);

  const fetchPage = useCallback(
    async (before?: string, append = false) => {
      if (!resolvedOrgId) {
        setEvents([]);
        setError("Missing org_id");
        setNextBefore(null);
        return;
      }
      const agentId = normalizeString(options.agentId);
      if (mode === "agent" && !agentId) {
        setEvents([]);
        setError("Missing agent_id");
        setNextBefore(null);
        return;
      }

      if (append) {
        setIsLoadingMore(true);
      } else {
        setIsLoading(true);
      }
      setError(null);

      try {
        const url = buildAgentActivityURL({
          mode,
          orgId: resolvedOrgId,
          agentId,
          before,
          limit,
          filters,
        });

        const token = resolveToken();
        const response = await fetch(url, {
          headers: {
            "Content-Type": "application/json",
            ...(token ? { Authorization: `Bearer ${token}` } : {}),
          },
        });

        if (!response.ok) {
          const payload = (await response.json().catch(() => null)) as { error?: string } | null;
          throw new Error(payload?.error || `Failed to load activity (${response.status})`);
        }

        const parsed = parseAgentActivityResponse(await response.json());
        setNextBefore(parsed.nextBefore || null);
        if (append) {
          setEvents((prev) => {
            const merged = [...prev, ...parsed.items];
            const byID = new Map<string, AgentActivityEvent>();
            for (const event of merged) {
              byID.set(event.id, event);
            }
            return Array.from(byID.values()).sort(
              (a, b) => b.startedAt.getTime() - a.startedAt.getTime(),
            );
          });
        } else {
          setEvents(parsed.items);
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load activity");
      } finally {
        if (append) {
          setIsLoadingMore(false);
        } else {
          setIsLoading(false);
        }
      }
    },
    [filters, limit, mode, options.agentId, resolvedOrgId],
  );

  const refresh = useCallback(async () => {
    await fetchPage(undefined, false);
  }, [fetchPage]);

  const loadMore = useCallback(async () => {
    if (!nextBefore || isLoadingMore || isLoading) {
      return;
    }
    await fetchPage(nextBefore, true);
  }, [fetchPage, isLoading, isLoadingMore, nextBefore]);

  useEffect(() => {
    void fetchPage(undefined, false);
  }, [fetchPage]);

  useEffect(() => {
    if (!wsMessage || wsMessage.type !== "ActivityEventReceived") {
      return;
    }

    const realtimeEvent = parseRealtimeAgentActivityEvent(wsMessage.data);
    if (!realtimeEvent) {
      return;
    }
    const isNewRealtimeID = trackProcessedRealtimeID(
      processedRealtimeIDsRef.current,
      processedRealtimeIDOrderRef.current,
      realtimeEvent.id,
    );
    if (!isNewRealtimeID) {
      return;
    }

    const scopedAgentID = normalizeString(options.agentId);
    if (
      !matchesActivityFilters(realtimeEvent, {
        mode,
        scopedAgentID,
        filters,
      })
    ) {
      return;
    }

    setEvents((prev) => {
      const byID = new Map<string, AgentActivityEvent>();
      for (const event of prev) {
        byID.set(event.id, event);
      }
      byID.set(realtimeEvent.id, realtimeEvent);
      return Array.from(byID.values()).sort(
        (a, b) => b.startedAt.getTime() - a.startedAt.getTime(),
      );
    });
  }, [filters, mode, options.agentId, wsMessage]);

  return {
    events,
    isLoading,
    isLoadingMore,
    error,
    filters,
    nextBefore,
    hasMore,
    setFilters,
    refresh,
    loadMore,
  };
}

export default useAgentActivity;
