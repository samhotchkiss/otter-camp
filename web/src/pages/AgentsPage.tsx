import { useCallback, useEffect, useMemo, useState, useRef, memo } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import AgentCard, { type AgentCardData } from "../components/AgentCard";
import { type AgentStatus } from "../components/AgentDM";
import { useWS } from "../contexts/WebSocketContext";
import { useGlobalChat } from "../contexts/GlobalChatContext";
import useEmissions from "../hooks/useEmissions";

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

const CARD_HEIGHT = 220; // Estimated height of AgentCard
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
import { isDemoMode } from '../lib/demo';

const API_URL = import.meta.env.VITE_API_URL || 'https://api.otter.camp';

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

  return trimmed;
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

function AgentsPageComponent({
  apiEndpoint = isDemoMode() 
    ? `${API_URL}/api/agents?demo=true`
    : `${API_URL}/api/sync/agents`,
}: AgentsPageProps) {
  const [agents, setAgents] = useState<AgentCardData[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [columns, setColumns] = useState(GRID_COLUMNS.lg);
  const parentRef = useRef<HTMLDivElement>(null);

  const { lastMessage, connected } = useWS();
  const { openConversation } = useGlobalChat();
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
    const status = normalizeAgentStatus(agent.status) ?? "offline";
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

  // Fetch agents from API
  const fetchAgents = useCallback(async () => {
    try {
      const response = await fetch(apiEndpoint);
      if (!response.ok) {
        throw new Error("Failed to fetch agents");
      }
      const data = await response.json();
      const agents = data.agents || data || [];
      return agents.map(mapAgentData);
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
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load agents");
      } finally {
        setIsLoading(false);
      }
    };

    loadAgents();
  }, [fetchAgents]);

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
  const counts = useMemo(() => {
    const result = { all: agents.length, online: 0, busy: 0, offline: 0 };
    for (const agent of agents) {
      result[agent.status]++;
    }
    return result;
  }, [agents]);

  const agentsWithLiveActivity = useMemo(() => {
    return agents.map((agent) => {
      const latestEmission = latestBySource.get(agent.id);
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
  }, [agents, latestBySource]);

  // Filter agents by status - memoized
  const filteredAgents = useMemo(() => {
    if (statusFilter === "all") {
      return agentsWithLiveActivity;
    }
    return agentsWithLiveActivity.filter((agent) => agent.status === statusFilter);
  }, [agentsWithLiveActivity, statusFilter]);

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
      contextLabel: "Direct message",
      subtitle: agent.role || "Agent chat",
    });
  }, [openConversation]);

  // Filter button handlers - memoized
  const handleFilterAll = useCallback(() => setStatusFilter("all"), []);
  const handleFilterOnline = useCallback(() => setStatusFilter("online"), []);
  const handleFilterBusy = useCallback(() => setStatusFilter("busy"), []);
  const handleFilterOffline = useCallback(() => setStatusFilter("offline"), []);

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

          {/* Connection status */}
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

        {/* Status filters */}
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
                  style={{
                    position: "absolute",
                    top: 0,
                    left: 0,
                    width: "100%",
                    height: `${virtualRow.size}px`,
                    transform: `translateY(${virtualRow.start}px)`,
                  }}
                  className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4"
                >
                  {rowAgents.map((agent) => (
                    <AgentCard
                      key={agent.id}
                      agent={agent}
                      onClick={handleAgentClick}
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
