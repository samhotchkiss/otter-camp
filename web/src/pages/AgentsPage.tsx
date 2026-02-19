type PermanentAgent = {
  id: string;
  name: string;
  status: "active" | "idle";
  uptime: string;
  currentTask: string;
  color: "amber" | "lime" | "orange";
};

type ChameleonAgent = {
  id: string;
  name: string;
  definition: string;
  status: "active" | "idle";
  spawnedAt: string;
  currentProject: string | null;
  tasksCompleted: number;
};

const PERMANENT_AGENTS: PermanentAgent[] = [
  {
    id: "orchestrator",
    name: "Orchestrator",
    status: "active",
    uptime: "99.8%",
    currentTask: "Coordinating deployment pipeline",
    color: "amber",
  },
  {
    id: "staffing-manager",
    name: "Staffing Manager",
    status: "active",
    uptime: "99.5%",
    currentTask: "Allocating Agent-156 to API Gateway",
    color: "lime",
  },
  {
    id: "memory-system",
    name: "Memory System",
    status: "active",
    uptime: "99.9%",
    currentTask: "Indexing conversation context",
    color: "orange",
  },
];

const CHAMELEON_AGENTS: ChameleonAgent[] = [
  {
    id: "agent-042",
    name: "Agent-042",
    definition: "Frontend Specialist",
    status: "active",
    spawnedAt: "2h ago",
    currentProject: "Customer Portal",
    tasksCompleted: 15,
  },
  {
    id: "agent-127",
    name: "Agent-127",
    definition: "API Security Expert",
    status: "active",
    spawnedAt: "45m ago",
    currentProject: "API Gateway",
    tasksCompleted: 8,
  },
  {
    id: "agent-089",
    name: "Agent-089",
    definition: "Database Optimizer",
    status: "active",
    spawnedAt: "1h ago",
    currentProject: "Internal Tools",
    tasksCompleted: 12,
  },
  {
    id: "agent-156",
    name: "Agent-156",
    definition: "DevOps Automation",
    status: "idle",
    spawnedAt: "10m ago",
    currentProject: null,
    tasksCompleted: 0,
  },
];

function cardAccent(agent: PermanentAgent): string {
  if (agent.color === "lime") {
    return "border-lime-600/20 from-lime-600/10 to-emerald-600/5 text-lime-400 bg-lime-500";
  }
  if (agent.color === "orange") {
    return "border-orange-600/20 from-orange-600/10 to-rose-600/5 text-orange-400 bg-orange-500";
  }
  return "border-amber-600/20 from-amber-600/10 to-orange-600/5 text-amber-400 bg-amber-500";
}

export default function AgentsPage() {
  return (
    <div data-testid="agents-shell" className="space-y-8">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl font-semibold text-stone-100">Agent Status</h1>
          <p className="text-sm text-stone-500">3 Permanent Cores • {CHAMELEON_AGENTS.length} Active Chameleons</p>
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

      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        {PERMANENT_AGENTS.map((agent) => {
          const accents = cardAccent(agent).split(" ");
          const borderColor = accents[0];
          const gradient = `${accents[1]} ${accents[2]}`;
          const iconColor = accents[3];
          const pulseColor = accents[4];

          return (
            <article key={agent.id} className={`group relative overflow-hidden rounded-lg border bg-stone-900 p-5 ${borderColor}`}>
              <div className={`absolute inset-0 bg-gradient-to-br ${gradient} opacity-50 transition-opacity group-hover:opacity-100`} />
              <div className="relative z-10">
                <div className="mb-4 flex items-start justify-between">
                  <div className="flex items-center gap-3">
                    <div className="flex h-10 w-10 items-center justify-center rounded-lg border border-stone-700 bg-stone-800">
                      <span aria-hidden="true" className={iconColor}>◉</span>
                    </div>
                    <div>
                      <h3 className="text-sm font-semibold text-stone-200">{agent.name}</h3>
                      <p className="text-[10px] uppercase tracking-wider text-stone-500">{agent.id}</p>
                    </div>
                  </div>
                  <span className="inline-flex items-center gap-1.5 rounded border border-stone-800 bg-stone-950/50 px-2 py-1 text-[10px] font-bold uppercase tracking-wider text-lime-400">
                    <span className={`h-1.5 w-1.5 animate-pulse rounded-full ${pulseColor}`} />
                    {agent.status}
                  </span>
                </div>

                <div className="space-y-3">
                  <div className="flex items-center justify-between text-xs">
                    <span className="text-stone-500">Uptime</span>
                    <span className="font-mono text-lime-400">{agent.uptime}</span>
                  </div>
                  <div className="h-1.5 w-full overflow-hidden rounded-full bg-stone-800">
                    <div className={`h-full rounded-full ${pulseColor}`} style={{ width: agent.uptime }} />
                  </div>
                  <div className="mt-4 rounded border border-stone-800/60 bg-stone-950/50 p-2">
                    <div className="mb-1 flex items-center gap-2 text-[10px] uppercase tracking-wide text-stone-500">
                      <span aria-hidden="true">◌</span>
                      Current Task
                    </div>
                    <p className="truncate text-xs text-stone-300">{agent.currentTask}</p>
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
          {CHAMELEON_AGENTS.map((agent) => (
            <article key={agent.id} className="group flex items-center justify-between p-4 transition hover:bg-stone-800/30">
              <div className="flex items-center gap-4">
                <div
                  className={`flex h-8 w-8 items-center justify-center rounded border ${
                    agent.status === "active"
                      ? "border-lime-500/20 bg-lime-500/10 text-lime-400"
                      : "border-stone-700 bg-stone-800 text-stone-500"
                  }`}
                >
                  <span aria-hidden="true">🤖</span>
                </div>
                <div>
                  <div className="flex items-center gap-2">
                    <h3 className="text-sm font-medium text-stone-200">{agent.name}</h3>
                    <span className="rounded border border-stone-700 bg-stone-800 px-1.5 py-0.5 text-[10px] text-stone-400">
                      {agent.definition}
                    </span>
                  </div>
                  <div className="mt-0.5 flex items-center gap-3 text-xs text-stone-500">
                    <span className="inline-flex items-center gap-1">
                      <span aria-hidden="true">⏱</span>
                      {agent.spawnedAt}
                    </span>
                    {agent.currentProject ? (
                      <span className="text-amber-400/80">• Working on {agent.currentProject}</span>
                    ) : null}
                  </div>
                </div>
              </div>

              <div className="flex items-center gap-6">
                <div className="text-right">
                  <p className="text-lg font-bold leading-none text-stone-200">{agent.tasksCompleted}</p>
                  <p className="text-[10px] uppercase text-stone-500">Tasks</p>
                </div>
                <div
                  className={`rounded-full p-1.5 ${
                    agent.status === "active"
                      ? "bg-lime-500/10 text-lime-500"
                      : "bg-stone-800 text-stone-500"
                  }`}
                >
                  {agent.status === "active" ? <span aria-hidden="true">✓</span> : <span aria-hidden="true">⏱</span>}
                </div>
              </div>
            </article>
          ))}
        </div>
      </section>
    </div>
  );
}
