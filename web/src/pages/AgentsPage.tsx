import { useCallback, useEffect, useMemo, useState } from "react";
import AgentCard, { type AgentCardData } from "../components/AgentCard";
import AgentDM, { type AgentStatus } from "../components/AgentDM";
import { useWS } from "../contexts/WebSocketContext";

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

/**
 * Status filter button component.
 */
function StatusFilterButton({
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
  const activeStyles: Record<StatusFilter, string> = {
    all: "bg-slate-700 text-slate-100",
    online: "bg-emerald-500/20 text-emerald-300 border-emerald-500/50",
    busy: "bg-amber-500/20 text-amber-300 border-amber-500/50",
    offline: "bg-slate-700 text-slate-300",
  };

  const dotStyles: Record<StatusFilter, string> = {
    all: "bg-slate-400",
    online: "bg-emerald-500",
    busy: "bg-amber-500",
    offline: "bg-slate-500",
  };

  return (
    <button
      type="button"
      onClick={onClick}
      className={`inline-flex items-center gap-2 rounded-full border px-4 py-2 text-sm font-medium transition ${
        isActive
          ? activeStyles[status]
          : "border-slate-700 bg-slate-800/50 text-slate-400 hover:border-slate-600 hover:text-slate-300"
      }`}
    >
      {status !== "all" && (
        <span className={`h-2 w-2 rounded-full ${dotStyles[status]}`} />
      )}
      {label}
      <span
        className={`rounded-full px-2 py-0.5 text-xs ${
          isActive ? "bg-black/20" : "bg-slate-700"
        }`}
      >
        {count}
      </span>
    </button>
  );
}

/**
 * AgentsPage - Grid view of all agents with filtering and DM modal.
 *
 * Features:
 * - Responsive grid of agent cards
 * - Filter by status (all/online/busy/offline)
 * - Click card to open AgentDM modal
 * - Real-time status updates via WebSocket
 */
export default function AgentsPage({
  apiEndpoint = "/api/agents",
}: AgentsPageProps) {
  const [agents, setAgents] = useState<AgentCardData[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [selectedAgent, setSelectedAgent] = useState<AgentCardData | null>(
    null
  );

  const { lastMessage, connected } = useWS();

  // Fetch agents from API
  const fetchAgents = useCallback(async () => {
    try {
      const response = await fetch(apiEndpoint);
      if (!response.ok) {
        throw new Error("Failed to fetch agents");
      }
      const data = await response.json();
      return (data.agents || data || []) as AgentCardData[];
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

    if (lastMessage.type === "AgentStatusUpdated") {
      const data = lastMessage.data as {
        agentId?: string;
        status?: AgentStatus;
        currentTask?: string;
        lastActive?: string;
      };

      if (data.agentId) {
        setAgents((prev) =>
          prev.map((agent) =>
            agent.id === data.agentId
              ? {
                  ...agent,
                  status: data.status ?? agent.status,
                  currentTask: data.currentTask ?? agent.currentTask,
                  lastActive: data.lastActive ?? agent.lastActive,
                }
              : agent
          )
        );

        // Update selected agent if it's the one that changed
        setSelectedAgent((prev) =>
          prev && prev.id === data.agentId
            ? {
                ...prev,
                status: data.status ?? prev.status,
                currentTask: data.currentTask ?? prev.currentTask,
                lastActive: data.lastActive ?? prev.lastActive,
              }
            : prev
        );
      }
    }
  }, [lastMessage]);

  // Calculate counts for filters
  const counts = useMemo(() => {
    const result = { all: agents.length, online: 0, busy: 0, offline: 0 };
    for (const agent of agents) {
      result[agent.status]++;
    }
    return result;
  }, [agents]);

  // Filter agents by status
  const filteredAgents = useMemo(() => {
    if (statusFilter === "all") {
      return agents;
    }
    return agents.filter((agent) => agent.status === statusFilter);
  }, [agents, statusFilter]);

  // Handle card click
  const handleAgentClick = (agent: AgentCardData) => {
    setSelectedAgent(agent);
  };

  // Close DM modal
  const handleCloseDM = () => {
    setSelectedAgent(null);
  };

  // Handle backdrop click
  const handleBackdropClick = (event: React.MouseEvent) => {
    if (event.target === event.currentTarget) {
      handleCloseDM();
    }
  };

  // Handle escape key to close modal
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && selectedAgent) {
        handleCloseDM();
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [selectedAgent]);

  if (isLoading) {
    return (
      <div className="flex min-h-[400px] items-center justify-center">
        <div className="flex items-center gap-3 text-slate-400">
          <div className="h-5 w-5 animate-spin rounded-full border-2 border-slate-600 border-t-emerald-500" />
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
          className="rounded-lg bg-slate-800 px-4 py-2 text-sm text-slate-300 hover:bg-slate-700"
        >
          Try Again
        </button>
      </div>
    );
  }

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
                ? "bg-emerald-500/20 text-emerald-400"
                : "bg-red-500/20 text-red-400"
            }`}
          >
            <span
              className={`h-2 w-2 rounded-full ${
                connected ? "bg-emerald-500 animate-pulse" : "bg-red-500"
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
            onClick={() => setStatusFilter("all")}
          />
          <StatusFilterButton
            status="online"
            label="Online"
            count={counts.online}
            isActive={statusFilter === "online"}
            onClick={() => setStatusFilter("online")}
          />
          <StatusFilterButton
            status="busy"
            label="Busy"
            count={counts.busy}
            isActive={statusFilter === "busy"}
            onClick={() => setStatusFilter("busy")}
          />
          <StatusFilterButton
            status="offline"
            label="Offline"
            count={counts.offline}
            isActive={statusFilter === "offline"}
            onClick={() => setStatusFilter("offline")}
          />
        </div>
      </div>

      {/* Agent grid */}
      {filteredAgents.length === 0 ? (
        <div className="flex min-h-[200px] flex-col items-center justify-center rounded-2xl border border-dashed border-slate-700 bg-slate-900/50">
          <p className="text-slate-500">
            {statusFilter === "all"
              ? "No agents found"
              : `No ${statusFilter} agents`}
          </p>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {filteredAgents.map((agent) => (
            <AgentCard
              key={agent.id}
              agent={agent}
              onClick={handleAgentClick}
            />
          ))}
        </div>
      )}

      {/* DM Modal */}
      {selectedAgent && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm"
          onClick={handleBackdropClick}
          role="dialog"
          aria-modal="true"
          aria-labelledby="dm-modal-title"
        >
          <div className="w-full max-w-2xl">
            {/* Close button */}
            <div className="mb-2 flex justify-end">
              <button
                type="button"
                onClick={handleCloseDM}
                className="rounded-full bg-slate-800 p-2 text-slate-400 transition hover:bg-slate-700 hover:text-slate-200"
                aria-label="Close"
              >
                <svg
                  xmlns="http://www.w3.org/2000/svg"
                  viewBox="0 0 20 20"
                  fill="currentColor"
                  className="h-5 w-5"
                >
                  <path d="M6.28 5.22a.75.75 0 0 0-1.06 1.06L8.94 10l-3.72 3.72a.75.75 0 1 0 1.06 1.06L10 11.06l3.72 3.72a.75.75 0 1 0 1.06-1.06L11.06 10l3.72-3.72a.75.75 0 0 0-1.06-1.06L10 8.94 6.28 5.22Z" />
                </svg>
              </button>
            </div>

            {/* DM Component */}
            <AgentDM
              agent={{
                id: selectedAgent.id,
                name: selectedAgent.name,
                avatarUrl: selectedAgent.avatarUrl,
                status: selectedAgent.status,
                role: selectedAgent.role,
              }}
            />
          </div>
        </div>
      )}
    </div>
  );
}
