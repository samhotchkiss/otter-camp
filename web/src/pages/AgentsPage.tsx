import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import api, { type AdminAgentSummary, type Project } from "../lib/api";

type AgentStatusTone = "online" | "busy" | "offline";

type CardAccent = {
  border: string;
  gradient: string;
  icon: string;
  pulse: string;
};

const CARD_ACCENTS: CardAccent[] = [
  {
    border: "border-amber-600/20",
    gradient: "from-amber-600/10 to-orange-600/5",
    icon: "text-amber-400",
    pulse: "bg-amber-500",
  },
  {
    border: "border-lime-600/20",
    gradient: "from-lime-600/10 to-emerald-600/5",
    icon: "text-lime-400",
    pulse: "bg-lime-500",
  },
  {
    border: "border-orange-600/20",
    gradient: "from-orange-600/10 to-rose-600/5",
    icon: "text-orange-400",
    pulse: "bg-orange-500",
  },
];

function normalizeStatus(status: string | undefined): AgentStatusTone {
  const normalized = (status ?? "").trim().toLowerCase();
  if (normalized === "online") {
    return "online";
  }
  if (normalized === "busy") {
    return "busy";
  }
  return "offline";
}

function statusBadgeClasses(status: AgentStatusTone): string {
  if (status === "online") {
    return "border-lime-500/20 bg-lime-500/10 text-lime-400";
  }
  if (status === "busy") {
    return "border-amber-500/20 bg-amber-500/10 text-amber-400";
  }
  return "border-stone-700 bg-stone-950/70 text-stone-500";
}

function statusDotClasses(status: AgentStatusTone): string {
  if (status === "online") {
    return "bg-lime-500";
  }
  if (status === "busy") {
    return "bg-amber-500";
  }
  return "bg-stone-600";
}

function toRelativeTimestamp(input: string | undefined): string {
  if (!input) {
    return "No heartbeat";
  }

  const parsed = new Date(input);
  if (Number.isNaN(parsed.getTime())) {
    return "Unknown";
  }

  const diffMs = Date.now() - parsed.getTime();
  if (diffMs <= 0) {
    return "Just now";
  }

  const minutes = Math.floor(diffMs / 60000);
  if (minutes < 1) {
    return "Just now";
  }
  if (minutes < 60) {
    return `${minutes}m ago`;
  }

  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours}h ago`;
  }

  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

function normalizeErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message.trim()) {
    return error.message;
  }
  return "Failed to load agents";
}

function tokenUsagePercent(agent: AdminAgentSummary): number {
  const contextTokens = Math.max(0, agent.context_tokens ?? 0);
  const totalTokens = Math.max(0, agent.total_tokens ?? 0);
  if (contextTokens <= 0 || totalTokens <= 0) {
    return 0;
  }
  return Math.min(100, Math.round((contextTokens / totalTokens) * 100));
}

function primaryAgentTag(agent: AdminAgentSummary): string {
  const model = (agent.model ?? "").trim();
  if (model) {
    return model;
  }
  const channel = (agent.channel ?? "").trim();
  if (channel) {
    return channel.replace(/[._-]/g, " ");
  }
  return "Unclassified";
}

export default function AgentsPage() {
  const navigate = useNavigate();
  const [agents, setAgents] = useState<AdminAgentSummary[]>([]);
  const [projectNamesByID, setProjectNamesByID] = useState<Record<string, string>>({});
  const [currentTaskByAgentID, setCurrentTaskByAgentID] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    let cancelled = false;

    async function loadAgents(): Promise<void> {
      setLoading(true);
      setLoadError(null);

      try {
        const [agentsPayload, projectsPayload] = await Promise.all([
          api.adminAgents(),
          api.projects().catch(() => ({ projects: [] as Project[] })),
        ]);

        if (cancelled) {
          return;
        }

        const nextAgents = Array.isArray(agentsPayload.agents) ? agentsPayload.agents : [];
        const projectMap: Record<string, string> = {};
        for (const project of projectsPayload.projects ?? []) {
          const id = (project.id ?? "").trim();
          const name = (project.name ?? "").trim();
          if (id && name) {
            projectMap[id] = name;
          }
        }

        const coreAgents = nextAgents.filter((agent) => !agent.is_ephemeral && (agent.id ?? "").trim() !== "");
        const currentTaskEntries = await Promise.all(
          coreAgents.map(async (agent) => {
            try {
              const detail = await api.adminAgent(agent.id);
              return [agent.id, (detail.sync?.current_task ?? "").trim()] as const;
            } catch {
              return [agent.id, ""] as const;
            }
          }),
        );

        if (cancelled) {
          return;
        }

        const nextCurrentTaskByID: Record<string, string> = {};
        for (const [id, task] of currentTaskEntries) {
          const normalizedID = id.trim();
          if (normalizedID && task) {
            nextCurrentTaskByID[normalizedID] = task;
          }
        }

        setAgents(nextAgents);
        setProjectNamesByID(projectMap);
        setCurrentTaskByAgentID(nextCurrentTaskByID);
      } catch (error) {
        if (!cancelled) {
          setAgents([]);
          setProjectNamesByID({});
          setCurrentTaskByAgentID({});
          setLoadError(normalizeErrorMessage(error));
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    }

    void loadAgents();
    return () => {
      cancelled = true;
    };
  }, [refreshKey]);

  const coreAgents = useMemo(
    () => agents.filter((agent) => !agent.is_ephemeral),
    [agents],
  );
  const chameleonAgents = useMemo(
    () => agents.filter((agent) => agent.is_ephemeral),
    [agents],
  );
  const activeChameleonCount = useMemo(
    () => chameleonAgents.filter((agent) => normalizeStatus(agent.status) !== "offline").length,
    [chameleonAgents],
  );

  return (
    <div data-testid="agents-shell" className="space-y-8">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl font-semibold text-stone-100">Agent Status</h1>
          <p className="text-sm text-stone-500">{coreAgents.length} Permanent Cores • {activeChameleonCount} Active Chameleons</p>
        </div>
        <div className="flex gap-2">
          <button className="inline-flex items-center gap-2 rounded border border-stone-700 bg-stone-800 px-3 py-1.5 text-xs font-medium text-stone-200 transition hover:bg-stone-700" type="button">
            <span aria-hidden="true">⌨</span>
            Logs
          </button>
          <button className="inline-flex items-center gap-2 rounded bg-amber-600 px-3 py-1.5 text-xs font-medium text-white transition hover:bg-amber-500" type="button">
            <span aria-hidden="true">🤖</span>
            Spawn Agent
          </button>
        </div>
      </div>

      {loading ? (
        <section className="rounded-lg border border-stone-800 bg-stone-900 p-4">
          <p className="text-sm text-stone-400">Loading agents...</p>
        </section>
      ) : loadError ? (
        <section className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-rose-500/20 bg-rose-500/10 p-4">
          <p className="text-sm text-rose-300">{loadError}</p>
          <button
            type="button"
            onClick={() => setRefreshKey((value) => value + 1)}
            className="rounded border border-rose-400/40 bg-rose-500/10 px-3 py-1.5 text-xs font-medium text-rose-200 hover:bg-rose-500/20"
          >
            Retry
          </button>
        </section>
      ) : (
        <>
          <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
            {coreAgents.length === 0 ? (
              <article className="rounded-lg border border-stone-800 bg-stone-900 p-5 md:col-span-3">
                <p className="text-sm text-stone-400">No permanent core agents were found for this workspace.</p>
              </article>
            ) : null}

            {coreAgents.map((agent, index) => {
              const accent = CARD_ACCENTS[index % CARD_ACCENTS.length];
              const status = normalizeStatus(agent.status);
              const contextTokens = Math.max(0, agent.context_tokens ?? 0);
              const totalTokens = Math.max(0, agent.total_tokens ?? 0);
              const usagePercent = tokenUsagePercent(agent);
              const currentTask = (currentTaskByAgentID[agent.id] ?? "").trim()
                || (status === "offline" ? "No active task (offline)." : "Waiting for task sync.");
              const agentID = (agent.id ?? "").trim() || (agent.workspace_agent_id ?? "").trim();

              return (
                <article key={agent.workspace_agent_id || agent.id || String(index)} className={`group relative cursor-pointer overflow-hidden rounded-lg border bg-stone-900 p-5 ${accent.border}`} onClick={() => navigate(`/agents/${encodeURIComponent(agentID)}`)}>
                  <div className={`absolute inset-0 bg-gradient-to-br ${accent.gradient} opacity-50 transition-opacity group-hover:opacity-100`} />
                  <div className="relative z-10">
                    <div className="mb-4 flex items-start justify-between">
                      <div className="flex min-w-0 items-center gap-3">
                        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border border-stone-700 bg-stone-800">
                          <span aria-hidden="true" className={accent.icon}>◉</span>
                        </div>
                        <div className="min-w-0">
                          <h3 className="truncate text-sm font-semibold text-stone-200">{agent.name || agentID || "Unnamed agent"}</h3>
                          <p className="truncate text-[10px] uppercase tracking-wider text-stone-500">{agentID || "unknown"}</p>
                        </div>
                      </div>
                      <span className={`inline-flex items-center gap-1.5 rounded border px-2 py-1 text-[10px] font-bold uppercase tracking-wider ${statusBadgeClasses(status)}`}>
                        <span className={`h-1.5 w-1.5 rounded-full ${statusDotClasses(status)}`} />
                        {status}
                      </span>
                    </div>

                    <div className="space-y-3">
                      <div className="flex items-center justify-between text-xs">
                        <span className="text-stone-500">Last seen</span>
                        <span className="font-mono text-stone-300">{toRelativeTimestamp(agent.last_seen)}</span>
                      </div>
                      <div className="flex items-center justify-between text-xs">
                        <span className="text-stone-500">Context / Total</span>
                        <span className="font-mono text-lime-400">{contextTokens.toLocaleString()} / {totalTokens.toLocaleString()}</span>
                      </div>
                      <div className="h-1.5 w-full overflow-hidden rounded-full bg-stone-800">
                        <div className={`h-full rounded-full ${accent.pulse}`} style={{ width: `${usagePercent}%` }} />
                      </div>
                      <div className="mt-4 rounded border border-stone-800/60 bg-stone-950/50 p-2">
                        <div className="mb-1 flex items-center gap-2 text-[10px] uppercase tracking-wide text-stone-500">
                          <span aria-hidden="true">◌</span>
                          Current Task
                        </div>
                        <p className="truncate text-xs text-stone-300">{currentTask}</p>
                      </div>
                    </div>
                  </div>
                </article>
              );
            })}
          </div>

          <section className="overflow-hidden rounded-lg border border-stone-800 bg-stone-900">
            <header className="flex items-center gap-2 border-b border-stone-800 px-5 py-4">
              <span aria-hidden="true" className="text-amber-400">⚡</span>
              <h2 className="text-sm font-semibold text-stone-200">Chameleon Agents (On-Demand)</h2>
            </header>
            <div className="divide-y divide-stone-800/50">
              {chameleonAgents.length === 0 ? (
                <article className="p-4">
                  <p className="text-sm text-stone-400">No chameleon agents are running.</p>
                </article>
              ) : null}

              {chameleonAgents.map((agent, index) => {
                const status = normalizeStatus(agent.status);
                const projectID = (agent.project_id ?? "").trim();
                const projectName = projectID ? (projectNamesByID[projectID] || projectID) : "";
                const totalTokens = Math.max(0, agent.total_tokens ?? 0);
                const agentID = (agent.id ?? "").trim() || (agent.workspace_agent_id ?? "").trim();

                return (
                  <article key={agent.workspace_agent_id || agent.id || String(index)} className="group flex cursor-pointer items-center justify-between p-4 transition hover:bg-stone-800/30" onClick={() => navigate(`/agents/${encodeURIComponent(agentID)}`)}>
                    <div className="flex min-w-0 items-center gap-4">
                      <div
                        className={`flex h-8 w-8 shrink-0 items-center justify-center rounded border ${
                          status === "offline"
                            ? "border-stone-700 bg-stone-800 text-stone-500"
                            : "border-lime-500/20 bg-lime-500/10 text-lime-400"
                        }`}
                      >
                        <span aria-hidden="true">🤖</span>
                      </div>
                      <div className="min-w-0">
                        <div className="flex min-w-0 items-center gap-2">
                          <h3 className="truncate text-sm font-medium text-stone-200">{agent.name || agent.id || "Unnamed agent"}</h3>
                          <span className="max-w-[260px] truncate rounded border border-stone-700 bg-stone-800 px-1.5 py-0.5 text-[10px] text-stone-400">
                            {primaryAgentTag(agent)}
                          </span>
                        </div>
                        <div className="mt-0.5 flex min-w-0 items-center gap-3 text-xs text-stone-500">
                          <span className="inline-flex items-center gap-1">
                            <span aria-hidden="true">⏱</span>
                            {toRelativeTimestamp(agent.last_seen)}
                          </span>
                          {projectName ? (
                            <span className="truncate text-amber-400/80">• Working on {projectName}</span>
                          ) : null}
                        </div>
                      </div>
                    </div>

                    <div className="flex shrink-0 items-center gap-6">
                      <div className="text-right">
                        <p className="text-lg font-bold leading-none text-stone-200">{totalTokens.toLocaleString()}</p>
                        <p className="text-[10px] uppercase text-stone-500">Tokens</p>
                      </div>
                      <div
                        className={`rounded-full p-1.5 ${
                          status === "offline"
                            ? "bg-stone-800 text-stone-500"
                            : status === "busy"
                              ? "bg-amber-500/10 text-amber-500"
                              : "bg-lime-500/10 text-lime-500"
                        }`}
                      >
                        {status === "offline" ? (
                          <span aria-hidden="true">⏱</span>
                        ) : status === "busy" ? (
                          <span aria-hidden="true">●</span>
                        ) : (
                          <span aria-hidden="true">✓</span>
                        )}
                      </div>
                    </div>
                  </article>
                );
              })}
            </div>
          </section>
        </>
      )}
    </div>
  );
}
