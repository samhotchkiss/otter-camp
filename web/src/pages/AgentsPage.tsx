import { useCallback, useEffect, useMemo, useState, useRef, memo } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import AgentCard, { type AgentCardData, formatLastActive } from "../components/AgentCard";
import { type AgentStatus } from "../components/AgentDM";
import { useWS } from "../contexts/WebSocketContext";
import { useGlobalChat } from "../contexts/GlobalChatContext";
import { useAgentActivity } from "../hooks/useAgentActivity";
import { isDemoMode } from "../lib/demo";
import useEmissions from "../hooks/useEmissions";
import { API_URL, apiFetch } from "../lib/api";

/**
 * Status filter options including "all".
 */
type StatusFilter = AgentStatus | "all";

/**
 * Props for the AgentsPage component.
 */
export type AgentsPageProps = {
  apiEndpoint?: string;
};

type AdminRosterAgent = {
  id: string;
  workspaceAgentID?: string;
  name: string;
  status: AgentStatus;
  model?: string;
  contextTokens?: number;
  totalTokens?: number;
  heartbeatEvery?: string;
  channel?: string;
  sessionKey?: string;
  lastSeen?: string;
};

type RosterSort = "name" | "status" | "model" | "last_seen";

type AdminConnectionsSession = {
  id?: string;
  name?: string;
  status?: string;
  model?: string;
  context_tokens?: number;
  total_tokens?: number;
  channel?: string;
  session_key?: string;
  last_seen?: string;
};

type AdminConnectionsPayload = {
  sessions?: AdminConnectionsSession[];
};

// Status filter styles - memoized outside component
// Using gold/amber for active states per DESIGN-SPEC.md
const ACTIVE_STYLES: Record<StatusFilter, string> = {
  all: "bg-[var(--surface-alt)] text-[var(--text)]",
  online: "bg-[#C9A86C]/20 text-[#C9A86C] border-[#C9A86C]/50",
  busy: "bg-amber-500/20 text-amber-300 border-amber-500/50",
  offline: "bg-[var(--surface-alt)] text-[var(--text-muted)]",
};

const DOT_STYLES: Record<StatusFilter, string> = {
  all: "bg-[var(--text-muted)]",
  online: "bg-[#C9A86C]",
  busy: "bg-amber-500",
  offline: "bg-[var(--text-muted)]",
};

/**
 * Status filter button component - Memoized.
 */
const StatusFilterButton = memo(function StatusFilterButton({
  status,
  label,
  count,
  isActive,
  onClick,
}: {
  status: StatusFilter;
  label: string;
  count: number;
  isActive: boolean;
  onClick: () => void;
}) {
  const className = useMemo(() => {
    const base = "inline-flex items-center gap-2 rounded-full border px-4 py-2 text-sm font-medium transition";
    if (isActive) {
      return `${base} ${ACTIVE_STYLES[status]}`;
    }
    return `${base} border-[var(--border)] bg-[var(--surface)]/50 text-[var(--text-muted)] hover:border-[var(--accent)]/50 hover:text-[var(--text)]`;
  }, [isActive, status]);

  return (
    <button type="button" onClick={onClick} className={className}>
      {status !== "all" && (
        <span className={`h-2 w-2 rounded-full ${DOT_STYLES[status]}`} />
      )}
      {label}
      <span
        className={`rounded-full px-2 py-0.5 text-xs ${
          isActive ? "bg-black/20" : "bg-[var(--surface-alt)]"
        }`}
      >
        {count}
      </span>
    </button>
  );
});

// Number of columns in the grid
const GRID_COLUMNS = {
  sm: 2,
  lg: 3,
  xl: 4,
};

const CARD_HEIGHT = 360; // Conservative estimate; rows are measured after render.
const GAP = 16;

/**
 * AgentsPage - Grid view of all agents with filtering and DM modal.
 * Uses virtual scrolling for performance with large agent lists.
 *
 * Features:
 * - Responsive grid of agent cards (virtualized)
 * - Filter by status (all/online/busy/offline)
 * - Click card to open AgentDM modal
 * - Real-time status updates via WebSocket
 */
const SLACK_THREAD_GROUP_HINTS: Record<string, string> = {
  "g-c0abhd38u05": "essie",
};

function normalizeAgentStatus(value: unknown): AgentStatus | undefined {
  if (typeof value !== "string") {
    return undefined;
  }

  const normalized = value.trim().toLowerCase();
  if (normalized === "online" || normalized === "active") {
    return "online";
  }
  if (normalized === "busy" || normalized === "working") {
    return "busy";
  }
  if (normalized === "offline" || normalized === "inactive") {
    return "offline";
  }
  return undefined;
}

function normalizeCurrentTaskText(value: string): string {
  const withoutEmojiCodes = value.replace(/:[a-z0-9_+-]+:/gi, " ");
  return withoutEmojiCodes.replace(/\s+/g, " ").trim();
}

function toTitleFromSlug(value: string): string {
  return value
    .split(/[-_.]/g)
    .map((part) => part.trim())
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1).toLowerCase())
    .join(" ");
}

function humanizeCurrentTask(value: string): string {
  const trimmed = value.trim();
  const lower = trimmed.toLowerCase();

  if (lower.startsWith("slack:#")) {
    const channel = trimmed.slice("slack:".length).trim();
    if (channel) {
      return `Active in ${channel}`;
    }
  }

  const slackThreadMatch = /^slack:(g-[a-z0-9]+)-thread-[a-z0-9._:-]+$/i.exec(trimmed);
  if (slackThreadMatch?.[1]) {
    const groupID = slackThreadMatch[1].toLowerCase();
    const channel = SLACK_THREAD_GROUP_HINTS[groupID];
    if (channel) {
      return `Thread in #${channel}`;
    }
    return "Thread in Slack";
  }

  const webchatMatch = /^webchat:g-agent-([a-z0-9-]+)-main$/i.exec(trimmed);
  if (webchatMatch?.[1]) {
    const sessionName = toTitleFromSlug(webchatMatch[1]);
    if (sessionName) {
      return `Active in ${sessionName} webchat`;
    }
    return "Webchat session";
  }
  if (lower.startsWith("webchat:")) {
    return "Webchat session";
  }

  return normalizeCurrentTaskText(trimmed);
}

function normalizeCurrentTask(value: unknown): string | undefined {
  if (typeof value !== "string") {
    return undefined;
  }

  const trimmed = value.trim();
  if (!trimmed) {
    return undefined;
  }

  const upper = trimmed.toUpperCase();
  if (upper === "HEARTBEAT_OK" || upper === "HEARTBEAT OK" || upper === "HEARTBEAT") {
    return undefined;
  }
  if (upper.startsWith("HEARTBEAT_")) {
    return undefined;
  }

  const humanized = humanizeCurrentTask(trimmed);
  if (!humanized) {
    return undefined;
  }
  if (humanized.length <= 120) {
    return humanized;
  }
  return `${humanized.slice(0, 117).trimEnd()}...`;
}

function normalizeAgentId(value: unknown): string | undefined {
  if (typeof value !== "string") {
    return undefined;
  }

  const trimmed = value.trim();
  return trimmed ? trimmed : undefined;
}

function normalizeLastActive(value: unknown): string | number | null | undefined {
  if (value === null || value === undefined) {
    return value;
  }
  if (typeof value === "string" || typeof value === "number") {
    return value;
  }
  return undefined;
}

function normalizeActivityLookupKey(value: string | undefined): string | undefined {
  if (!value) {
    return undefined;
  }
  const trimmed = value.trim().toLowerCase();
  return trimmed || undefined;
}

function parseSessionAgentIdentity(sessionKey: string | undefined): string | undefined {
  if (!sessionKey) {
    return undefined;
  }
  const trimmed = sessionKey.trim();
  if (!trimmed) {
    return undefined;
  }

  const canonicalMatch = /^agent:chameleon:oc:([0-9a-f-]{36})$/i.exec(trimmed);
  if (canonicalMatch?.[1]) {
    return canonicalMatch[1].toLowerCase();
  }

  if (!trimmed.toLowerCase().startsWith("agent:")) {
    return undefined;
  }
  const rest = trimmed.slice("agent:".length).trim();
  if (!rest) {
    return undefined;
  }
  const delimiterIdx = rest.indexOf(":");
  const token = delimiterIdx === -1 ? rest : rest.slice(0, delimiterIdx).trim();
  return token ? token.toLowerCase() : undefined;
}

function appendAlias(target: Set<string>, value: string | undefined): void {
  const normalized = normalizeActivityLookupKey(value);
  if (!normalized) {
    return;
  }
  target.add(normalized);
}

function buildIdleFallbackText(agent: AgentCardData): string {
  const lastActive = formatLastActive(agent.lastActive);
  if (lastActive === "Never") {
    return "Idle";
  }
  return `Idle ${lastActive.toLowerCase()}`;
}

function AgentsPageComponent({
  apiEndpoint = isDemoMode() 
    ? `${API_URL}/api/agents?demo=true`
    : `${API_URL}/api/agents`,
}: AgentsPageProps) {
  const [agents, setAgents] = useState<AgentCardData[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [rosterAgents, setRosterAgents] = useState<AdminRosterAgent[]>([]);
  const [rosterSort, setRosterSort] = useState<RosterSort>("name");
  const [rosterSortAsc, setRosterSortAsc] = useState(true);
  const [columns, setColumns] = useState(GRID_COLUMNS.lg);
  const parentRef = useRef<HTMLDivElement>(null);

  const { lastMessage, connected } = useWS();
  const { openConversation } = useGlobalChat();
  const { events: recentActivityEvents } = useAgentActivity({
    mode: "recent",
    limit: 100,
  });
  const { latestBySource } = useEmissions({ limit: 200 });

  // Responsive column count
  useEffect(() => {
    const updateColumns = () => {
      const width = window.innerWidth;
      if (width >= 1280) {
        setColumns(GRID_COLUMNS.xl);
      } else if (width >= 1024) {
        setColumns(GRID_COLUMNS.lg);
      } else {
        setColumns(GRID_COLUMNS.sm);
      }
    };

    updateColumns();
    window.addEventListener("resize", updateColumns);
    return () => window.removeEventListener("resize", updateColumns);
  }, []);

  // Map sync API response to AgentCardData format
  const mapAgentData = (agent: Record<string, unknown>): AgentCardData => {
    const id =
      normalizeAgentId(agent.id) ||
      normalizeAgentId(agent.agentId) ||
      normalizeAgentId(agent.slug) ||
      "unknown";
    const name =
      (typeof agent.name === "string" && agent.name.trim()) ||
      (typeof agent.displayName === "string" && agent.displayName.trim()) ||
      id;
    const statusFromPayload = normalizeAgentStatus(agent.status) ?? "offline";
    const stalled = agent.stalled === true;
    const status = stalled ? "offline" : statusFromPayload;
    const avatarRaw =
      (typeof agent.avatarUrl === "string" && agent.avatarUrl) ||
      (typeof agent.avatar === "string" && agent.avatar) ||
      (typeof agent.avatar_url === "string" && agent.avatar_url) ||
      undefined;
    const avatarUrl = avatarRaw && avatarRaw.startsWith("http") ? avatarRaw : undefined;
    const currentTask =
      normalizeCurrentTask(agent.currentTask) ||
      normalizeCurrentTask(agent.current_task) ||
      normalizeCurrentTask(agent.displayName);
    const lastActive =
      agent.lastSeen ||
      agent.last_seen ||
      agent.lastActive ||
      agent.last_active ||
      agent.updatedAt ||
      agent.updated_at;

    return {
      id,
      name,
      avatarUrl,
      status,
      role: (typeof agent.role === "string" && agent.role) || undefined,
      currentTask,
      lastActive: lastActive as string | number | null | undefined,
    };
  };

  const fetchAdminRoster = useCallback(async () => {
    try {
      const payload = (await apiFetch<{ agents?: Array<Record<string, unknown>> }>(`/api/admin/agents`));
      let connectionSessions: AdminConnectionsSession[] = [];
      try {
        const connectionsPayload = await apiFetch<AdminConnectionsPayload>(`/api/admin/connections`);
        connectionSessions = Array.isArray(connectionsPayload.sessions)
          ? connectionsPayload.sessions
          : [];
      } catch {
        connectionSessions = [];
      }

      const sessionByAlias = new Map<string, AdminConnectionsSession>();
      for (const session of connectionSessions) {
        const aliases = new Set<string>();
        appendAlias(aliases, typeof session.id === "string" ? session.id : undefined);
        appendAlias(aliases, typeof session.name === "string" ? session.name : undefined);
        appendAlias(
          aliases,
          parseSessionAgentIdentity(typeof session.session_key === "string" ? session.session_key : undefined),
        );
        for (const alias of aliases) {
          if (!sessionByAlias.has(alias)) {
            sessionByAlias.set(alias, session);
          }
        }
      }

      const next = (payload.agents || []).map((agent) => ({
        id: String(agent.id || "").trim(),
        workspaceAgentID:
          typeof agent.workspace_agent_id === "string" && agent.workspace_agent_id.trim() !== ""
            ? agent.workspace_agent_id.trim()
            : undefined,
        name: String(agent.name || agent.id || "unknown").trim(),
        status: normalizeAgentStatus(agent.status) || "offline",
        model: typeof agent.model === "string" && agent.model.trim() !== ""
          ? agent.model
          : undefined,
        contextTokens: typeof agent.context_tokens === "number" ? agent.context_tokens : undefined,
        totalTokens: typeof agent.total_tokens === "number" ? agent.total_tokens : undefined,
        heartbeatEvery: typeof agent.heartbeat_every === "string" ? agent.heartbeat_every : undefined,
        channel: typeof agent.channel === "string" ? agent.channel : undefined,
        sessionKey: typeof agent.session_key === "string" ? agent.session_key : undefined,
        lastSeen: typeof agent.last_seen === "string" ? agent.last_seen : undefined,
      }))
        .map((agent) => {
          const aliases = new Set<string>();
          appendAlias(aliases, agent.id);
          appendAlias(aliases, agent.workspaceAgentID);
          appendAlias(aliases, agent.name);
          appendAlias(aliases, parseSessionAgentIdentity(agent.sessionKey));
          const matchedSession = [...aliases]
            .map((alias) => sessionByAlias.get(alias))
            .find((entry): entry is AdminConnectionsSession => Boolean(entry));

          return {
            ...agent,
            status:
              agent.status ||
              normalizeAgentStatus(matchedSession?.status) ||
              "offline",
            model:
              agent.model ||
              (typeof matchedSession?.model === "string" ? matchedSession.model : undefined),
            contextTokens:
              agent.contextTokens ??
              (typeof matchedSession?.context_tokens === "number"
                ? matchedSession.context_tokens
                : undefined),
            totalTokens:
              agent.totalTokens ??
              (typeof matchedSession?.total_tokens === "number"
                ? matchedSession.total_tokens
                : undefined),
            channel:
              agent.channel ||
              (typeof matchedSession?.channel === "string" ? matchedSession.channel : undefined),
            sessionKey:
              agent.sessionKey ||
              (typeof matchedSession?.session_key === "string" ? matchedSession.session_key : undefined),
            lastSeen:
              agent.lastSeen ||
              (typeof matchedSession?.last_seen === "string" ? matchedSession.last_seen : undefined),
          };
        });
      setRosterAgents(next);
    } catch {
      setRosterAgents([]);
    }
  }, []);

  // Fetch agents from API
  const fetchAgents = useCallback(async () => {
    try {
      const endpoint = apiEndpoint.replace(API_URL, '');
      const data = await apiFetch<unknown>(endpoint);
      const payload = data && typeof data === "object" ? (data as Record<string, unknown>) : null;
      const rawAgents = Array.isArray(payload?.agents)
        ? payload.agents
        : Array.isArray(data)
          ? data
          : [];
      return rawAgents.map((agent) => mapAgentData(agent as Record<string, unknown>));
    } catch (err) {
      throw err instanceof Error ? err : new Error("Failed to load agents");
    }
  }, [apiEndpoint]);

  // Initial fetch
  useEffect(() => {
    const loadAgents = async () => {
      setIsLoading(true);
      try {
        const fetchedAgents = await fetchAgents();
        setAgents(fetchedAgents);
        await fetchAdminRoster();
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load agents");
      } finally {
        setIsLoading(false);
      }
    };

    loadAgents();
  }, [fetchAgents, fetchAdminRoster]);

  // Handle WebSocket messages for real-time status updates
  useEffect(() => {
    if (!lastMessage) {
      return;
    }

    if (lastMessage.type === "AgentStatusUpdated" || lastMessage.type === "AgentStatusChanged") {
      const payload =
        lastMessage.data && typeof lastMessage.data === "object"
          ? (lastMessage.data as Record<string, unknown>)
          : {};
      const nestedAgent =
        payload.agent && typeof payload.agent === "object"
          ? (payload.agent as Record<string, unknown>)
          : null;

      const agentId =
        (typeof payload.agentId === "string" && payload.agentId) ||
        (nestedAgent && typeof nestedAgent.id === "string" ? nestedAgent.id : "");
      if (!agentId) {
        return;
      }

      const status =
        normalizeAgentStatus(payload.status) ??
        normalizeAgentStatus(nestedAgent?.status);
      const currentTask =
        normalizeCurrentTask(payload.currentTask) ||
        normalizeCurrentTask(payload.current_task) ||
        normalizeCurrentTask(nestedAgent?.current_task) ||
        normalizeCurrentTask(nestedAgent?.currentTask);
      const lastActive =
        normalizeLastActive(payload.lastActive) ??
        normalizeLastActive(payload.last_active) ??
        normalizeLastActive(payload.lastSeen) ??
        normalizeLastActive(payload.last_seen) ??
        normalizeLastActive(payload.updatedAt) ??
        normalizeLastActive(payload.updated_at) ??
        normalizeLastActive(
          nestedAgent && (nestedAgent.last_seen || nestedAgent.lastSeen || nestedAgent.updated_at || nestedAgent.updatedAt)
        );

      setAgents((prev) =>
        prev.map((agent) =>
          agent.id === agentId
            ? {
                ...agent,
                status: status ?? agent.status,
                currentTask: currentTask ?? agent.currentTask,
                lastActive: lastActive ?? agent.lastActive,
              }
            : agent
        )
      );

    }
  }, [lastMessage]);

  // Calculate counts for filters - memoized
  const latestActivityByAgent = useMemo(() => {
    const map = new Map<string, NonNullable<AgentCardData["lastAction"]>>();
    for (const event of recentActivityEvents) {
      const aliases = new Set<string>();
      appendAlias(aliases, event.agentId);
      appendAlias(aliases, event.sessionKey);
      appendAlias(aliases, parseSessionAgentIdentity(event.sessionKey));
      if (aliases.size === 0) {
        continue;
      }

      for (const lookupKey of aliases) {
        const existing = map.get(lookupKey);
        if (!existing || new Date(existing.startedAt || 0).getTime() < event.startedAt.getTime()) {
          map.set(lookupKey, {
            summary: event.summary,
            trigger: event.trigger,
            channel: event.channel,
            status: event.status,
            startedAt: event.startedAt,
          });
        }
      }
    }
    return map;
  }, [recentActivityEvents]);

  const rosterByAlias = useMemo(() => {
    const lookup = new Map<string, AdminRosterAgent>();
    for (const rosterAgent of rosterAgents) {
      const aliases = new Set<string>();
      appendAlias(aliases, rosterAgent.id);
      appendAlias(aliases, rosterAgent.workspaceAgentID);
      appendAlias(aliases, rosterAgent.name);
      appendAlias(aliases, rosterAgent.sessionKey);
      appendAlias(aliases, parseSessionAgentIdentity(rosterAgent.sessionKey));
      for (const alias of aliases) {
        if (!lookup.has(alias)) {
          lookup.set(alias, rosterAgent);
        }
      }
    }
    return lookup;
  }, [rosterAgents]);

  const agentsWithLastAction = useMemo(
    () =>
      agents.map((agent) => {
        const aliases = new Set<string>();
        appendAlias(aliases, agent.id);
        appendAlias(aliases, agent.name);
        const matchedRosterAgent =
          [...aliases]
            .map((alias) => rosterByAlias.get(alias))
            .find((entry): entry is AdminRosterAgent => Boolean(entry)) ||
          null;

        appendAlias(aliases, matchedRosterAgent?.workspaceAgentID);
        appendAlias(aliases, matchedRosterAgent?.sessionKey);
        appendAlias(aliases, parseSessionAgentIdentity(matchedRosterAgent?.sessionKey));

        const candidateKeys = [...aliases];

        let lastAction: AgentCardData["lastAction"];
        for (const key of candidateKeys) {
          const matched = latestActivityByAgent.get(key);
          if (matched) {
            lastAction = matched;
            break;
          }
        }

        return {
          ...agent,
          lastAction,
          lastActionFallbackText: lastAction ? undefined : buildIdleFallbackText(agent),
        };
      }),
    [agents, latestActivityByAgent, rosterByAlias],
  );

  const agentsWithLiveActivity = useMemo(() => {
    return agentsWithLastAction.map((agent) => {
      const aliases = new Set<string>();
      appendAlias(aliases, agent.id);
      appendAlias(aliases, agent.name);
      const matchedRosterAgent =
        [...aliases]
          .map((alias) => rosterByAlias.get(alias))
          .find((entry): entry is AdminRosterAgent => Boolean(entry)) ||
        null;
      appendAlias(aliases, matchedRosterAgent?.workspaceAgentID);
      appendAlias(aliases, parseSessionAgentIdentity(matchedRosterAgent?.sessionKey));

      const latestEmission = [...aliases]
        .map((alias) => latestBySource.get(alias) || latestBySource.get(alias.toLowerCase()))
        .find((entry) => Boolean(entry));
      if (!latestEmission) {
        return agent;
      }
      return {
        ...agent,
        lastEmission: {
          summary: latestEmission.summary,
          timestamp: latestEmission.timestamp,
        },
      };
    });
  }, [agentsWithLastAction, latestBySource, rosterByAlias]);

  // Calculate counts for filters - memoized
  const counts = useMemo(() => {
    const result = { all: agentsWithLiveActivity.length, online: 0, busy: 0, offline: 0 };
    for (const agent of agentsWithLiveActivity) {
      result[agent.status]++;
    }
    return result;
  }, [agentsWithLiveActivity]);

  // Filter agents by status - memoized
  const filteredAgents = useMemo(() => {
    if (statusFilter === "all") {
      return agentsWithLiveActivity;
    }
    return agentsWithLiveActivity.filter((agent) => agent.status === statusFilter);
  }, [agentsWithLiveActivity, statusFilter]);

  const filteredRoster = useMemo(() => {
    if (statusFilter === "all") {
      return rosterAgents;
    }
    return rosterAgents.filter((agent) => agent.status === statusFilter);
  }, [rosterAgents, statusFilter]);

  const sortedRoster = useMemo(() => {
    const items = [...filteredRoster];
    items.sort((left, right) => {
      let leftValue = "";
      let rightValue = "";
      switch (rosterSort) {
        case "status":
          leftValue = left.status;
          rightValue = right.status;
          break;
        case "model":
          leftValue = left.model || "";
          rightValue = right.model || "";
          break;
        case "last_seen":
          leftValue = left.lastSeen || "";
          rightValue = right.lastSeen || "";
          break;
        default:
          leftValue = left.name;
          rightValue = right.name;
          break;
      }
      const comparison = leftValue.localeCompare(rightValue);
      return rosterSortAsc ? comparison : -comparison;
    });
    return items;
  }, [filteredRoster, rosterSort, rosterSortAsc]);

  // Calculate rows for virtualization
  const rowCount = useMemo(() => 
    Math.ceil(filteredAgents.length / columns), 
    [filteredAgents.length, columns]
  );

  // Virtual list for rows
  const rowVirtualizer = useVirtualizer({
    count: rowCount,
    getScrollElement: () => parentRef.current,
    estimateSize: () => CARD_HEIGHT + GAP,
    overscan: 2,
  });

  useEffect(() => {
    if (typeof rowVirtualizer.measure === "function") {
      rowVirtualizer.measure();
    }
  }, [columns, filteredAgents.length, latestBySource.size, recentActivityEvents.length, rowVirtualizer]);

  // Handle card click - memoized
  const handleAgentClick = useCallback((agent: AgentCardData) => {
    openConversation({
      type: "dm",
      agent: {
        id: agent.id,
        name: agent.name,
        status: agent.status,
        avatarUrl: agent.avatarUrl,
        role: agent.role,
      },
      title: agent.name,
      contextLabel: "Chameleon-routed chat",
      subtitle: "Identity injected on open. Project required for writable tasks.",
    });
  }, [openConversation]);

  // Filter button handlers - memoized
  const handleFilterAll = useCallback(() => setStatusFilter("all"), []);
  const handleFilterOnline = useCallback(() => setStatusFilter("online"), []);
  const handleFilterBusy = useCallback(() => setStatusFilter("busy"), []);
  const handleFilterOffline = useCallback(() => setStatusFilter("offline"), []);

  const toggleRosterSort = useCallback((nextSort: RosterSort) => {
    setRosterSort((previous) => {
      if (previous === nextSort) {
        setRosterSortAsc((current) => !current);
        return previous;
      }
      setRosterSortAsc(true);
      return nextSort;
    });
  }, []);

  if (isLoading) {
    return (
      <div className="flex min-h-[400px] items-center justify-center">
        <div className="flex items-center gap-3 text-[var(--text-muted)]">
          <div className="h-5 w-5 animate-spin rounded-full border-2 border-[var(--border)] border-t-[#C9A86C]" />
          <span>Loading agents...</span>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex min-h-[400px] flex-col items-center justify-center gap-4">
        <div className="text-red-400">{error}</div>
        <button
          type="button"
          onClick={() => window.location.reload()}
          className="rounded-lg bg-[var(--surface)] px-4 py-2 text-sm text-[var(--text-muted)] hover:bg-[var(--surface-alt)]"
        >
          Try Again
        </button>
      </div>
    );
  }

  const virtualItems = rowVirtualizer.getVirtualItems();

  return (
    <div className="w-full">
      {/* Header */}
      <div className="mb-8">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-3xl font-semibold text-slate-100">Agents</h1>
            <p className="mt-1 text-slate-500">
              {counts.all} agents • {counts.online} online
            </p>
          </div>

          <div className="flex items-center gap-3">
            <a
              href="/agents/new"
              className="rounded-lg border border-[#C9A86C]/60 bg-[#C9A86C]/20 px-3 py-1.5 text-xs font-medium text-[#C9A86C]"
            >
              Add Agent
            </a>
            <div
              className={`flex items-center gap-2 rounded-full px-3 py-1.5 text-xs font-medium ${
                connected
                  ? "bg-[#C9A86C]/20 text-[#C9A86C]"
                  : "bg-red-500/20 text-red-400"
              }`}
            >
              <span
                className={`h-2 w-2 rounded-full ${
                  connected ? "bg-[#C9A86C] animate-pulse" : "bg-red-500"
                }`}
              />
              {connected ? "Live" : "Disconnected"}
            </div>
          </div>
        </div>

        {/* Status filters */}
        <p className="mt-4 text-xs text-[var(--text-muted)]">
          Chats are routed through Chameleon identity injection.
        </p>
        <div className="mt-6 flex flex-wrap gap-2">
          <StatusFilterButton
            status="all"
            label="All"
            count={counts.all}
            isActive={statusFilter === "all"}
            onClick={handleFilterAll}
          />
          <StatusFilterButton
            status="online"
            label="Online"
            count={counts.online}
            isActive={statusFilter === "online"}
            onClick={handleFilterOnline}
          />
          <StatusFilterButton
            status="busy"
            label="Busy"
            count={counts.busy}
            isActive={statusFilter === "busy"}
            onClick={handleFilterBusy}
          />
          <StatusFilterButton
            status="offline"
            label="Offline"
            count={counts.offline}
            isActive={statusFilter === "offline"}
            onClick={handleFilterOffline}
          />
        </div>
      </div>

      <section className="mb-6 rounded-2xl border border-[var(--border)] bg-[var(--surface)]/60 p-4">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h2 className="text-lg font-medium text-[var(--text)]">Management Roster</h2>
          <div className="flex flex-wrap gap-2 text-xs">
            <button
              type="button"
              onClick={() => toggleRosterSort("name")}
              className="rounded border border-[var(--border)] px-2 py-1 text-[var(--text-muted)]"
            >
              Name
            </button>
            <button
              type="button"
              onClick={() => toggleRosterSort("status")}
              className="rounded border border-[var(--border)] px-2 py-1 text-[var(--text-muted)]"
            >
              Status
            </button>
            <button
              type="button"
              onClick={() => toggleRosterSort("model")}
              className="rounded border border-[var(--border)] px-2 py-1 text-[var(--text-muted)]"
            >
              Model
            </button>
            <button
              type="button"
              onClick={() => toggleRosterSort("last_seen")}
              className="rounded border border-[var(--border)] px-2 py-1 text-[var(--text-muted)]"
            >
              Last Active
            </button>
          </div>
        </div>

        {sortedRoster.length === 0 ? (
          <p className="text-sm text-[var(--text-muted)]">No roster entries available yet.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-[var(--border)] text-sm">
              <thead>
                <tr className="text-left text-xs uppercase tracking-wide text-[var(--text-muted)]">
                  <th className="px-2 py-2">Name</th>
                  <th className="px-2 py-2">Slot</th>
                  <th className="px-2 py-2">Status</th>
                  <th className="px-2 py-2">Model</th>
                  <th className="px-2 py-2">Tokens</th>
                  <th className="px-2 py-2">Last Active</th>
                  <th className="px-2 py-2">Heartbeat</th>
                  <th className="px-2 py-2">Channels</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[var(--border)]">
                {sortedRoster.map((agent) => (
                  <tr key={agent.id} data-testid={`roster-row-${agent.id}`} className="text-[var(--text)]">
                    <td className="px-2 py-2 font-medium">
                      <a href={`/agents/${encodeURIComponent(agent.id)}`} className="hover:underline">
                        {agent.name}
                      </a>
                    </td>
                    <td className="px-2 py-2">{agent.id}</td>
                    <td className="px-2 py-2">{agent.status}</td>
                    <td className="px-2 py-2">{agent.model || "n/a"}</td>
                    <td className="px-2 py-2">
                      {Number.isFinite(agent.totalTokens)
                        ? Number(agent.totalTokens).toLocaleString()
                        : "—"}
                    </td>
                    <td className="px-2 py-2">{agent.lastSeen || "n/a"}</td>
                    <td className="px-2 py-2">{agent.heartbeatEvery || "n/a"}</td>
                    <td className="px-2 py-2">{agent.channel || "n/a"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {/* Agent grid with virtual scrolling */}
      {filteredAgents.length === 0 ? (
        <div className="flex min-h-[200px] flex-col items-center justify-center rounded-2xl border border-dashed border-[var(--border)] bg-[var(--surface)]/50">
          <p className="text-[var(--text-muted)]">
            {statusFilter === "all"
              ? "No agents found"
              : `No ${statusFilter} agents`}
          </p>
        </div>
      ) : (
        <div
          ref={parentRef}
          className="max-h-[70vh] overflow-y-auto"
        >
          <div
            style={{
              height: `${rowVirtualizer.getTotalSize()}px`,
              width: "100%",
              position: "relative",
            }}
          >
            {virtualItems.map((virtualRow) => {
              const startIndex = virtualRow.index * columns;
              const rowAgents = filteredAgents.slice(startIndex, startIndex + columns);
              
              return (
                <div
                  key={virtualRow.key}
                  ref={typeof rowVirtualizer.measureElement === "function" ? rowVirtualizer.measureElement : undefined}
                  style={{
                    position: "absolute",
                    top: 0,
                    left: 0,
                    width: "100%",
                    transform: `translateY(${virtualRow.start}px)`,
                  }}
                  className="grid gap-4 pb-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4"
                >
                  {rowAgents.map((agent) => (
                    <AgentCard
                      key={agent.id}
                      agent={agent}
                      onClick={handleAgentClick}
                      detailHref={`/agents/${encodeURIComponent(agent.id)}`}
                    />
                  ))}
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

const AgentsPage = memo(AgentsPageComponent);

export default AgentsPage;
