import { memo } from "react";
import AgentCard, { type AgentCardData } from "../components/AgentCard";

const agents: AgentCardData[] = [
  {
    id: "1",
    name: "Frank",
    role: "Chief of Staff",
    status: "active",
    tasks: 34,
    commits: 89,
    messages: 132,
    activeTime: "4h 18m",
    heartbeat: "12s",
    activity: "3m",
    lastAction: "Sent update to Sam",
    projects: ["Onboarding", "Weekly Sync", "Ops"],
  },
  {
    id: "2",
    name: "Derek",
    role: "Engineering Lead",
    status: "active",
    tasks: 28,
    commits: 42,
    messages: 98,
    activeTime: "6h 02m",
    heartbeat: "8s",
    activity: "45s",
    lastAction: "Pushed 3 commits to Pearl",
    projects: ["Pearl", "Edge API"],
  },
  {
    id: "3",
    name: "Mina",
    role: "Product Strategist",
    status: "busy",
    tasks: 19,
    commits: 12,
    messages: 74,
    activeTime: "3h 44m",
    heartbeat: "2m",
    activity: "4m",
    lastAction: "Drafted Q2 roadmap brief",
    projects: ["Raft", "Insights"],
  },
  {
    id: "4",
    name: "Priya",
    role: "Design Systems",
    status: "active",
    tasks: 22,
    commits: 31,
    messages: 120,
    activeTime: "5h 11m",
    heartbeat: "20s",
    activity: "1m",
    lastAction: "Shared updated token palette",
    projects: ["UI Kit", "Docs"],
  },
  {
    id: "5",
    name: "Leo",
    role: "Data Analyst",
    status: "idle",
    tasks: 11,
    commits: 7,
    messages: 56,
    activeTime: "1h 09m",
    heartbeat: "9m",
    activity: "12m",
    lastAction: "Queued weekly retention report",
    projects: ["Metrics", "Growth"],
  },
  {
    id: "6",
    name: "Sasha",
    role: "Customer Success",
    status: "busy",
    tasks: 27,
    commits: 5,
    messages: 186,
    activeTime: "7h 33m",
    heartbeat: "40s",
    activity: "6m",
    lastAction: "Escalated priority ticket",
    projects: ["Support", "Playbooks"],
  },
  {
    id: "7",
    name: "Andre",
    role: "Infrastructure",
    status: "active",
    tasks: 16,
    commits: 54,
    messages: 64,
    activeTime: "8h 21m",
    heartbeat: "15s",
    activity: "55s",
    lastAction: "Rolled out cluster patch",
    projects: ["Core", "SRE"],
  },
  {
    id: "8",
    name: "Jules",
    role: "Research",
    status: "idle",
    tasks: 9,
    commits: 3,
    messages: 44,
    activeTime: "52m",
    heartbeat: "14m",
    activity: "18m",
    lastAction: "Summarized user interviews",
    projects: ["Discovery", "Labs"],
  },
];

const stats = [
  {
    label: "Active Now",
    value: agents.filter((agent) => agent.status === "active").length.toString(),
    caption: "currently in motion",
  },
  {
    label: "Tasks This Week",
    value: agents.reduce((sum, agent) => sum + agent.tasks, 0).toString(),
    caption: "in flight across teams",
  },
  {
    label: "Commits",
    value: agents.reduce((sum, agent) => sum + agent.commits, 0).toString(),
    caption: "merged in the last 7d",
  },
  {
    label: "Uptime %",
    value: "99.8",
    caption: "last 30 days",
  },
];

function AgentsPageComponent() {
  return (
    <div className="w-full">
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-8 px-4 pb-12 sm:px-6 lg:px-8">
        <section className="mt-6 rounded-3xl border border-otter-dark-border bg-otter-dark-surface p-6 sm:p-8">
          <div className="flex flex-wrap items-center justify-between gap-6">
            <div>
              <div className="flex items-center gap-3 text-3xl font-semibold text-otter-dark-text">
                <span className="text-3xl" aria-hidden="true">
                  🦦
                </span>
                <h1>Your Raft</h1>
              </div>
              <p className="mt-2 text-sm text-otter-dark-text-muted">
                {agents.length} agents working together
              </p>
            </div>
            <div className="rounded-2xl border border-otter-dark-border bg-otter-dark-bg/80 px-4 py-3 text-sm text-otter-dark-text-muted">
              Last sync: 2 minutes ago
            </div>
          </div>
        </section>

        <section className="grid gap-4 rounded-3xl border border-otter-dark-border bg-otter-dark-surface p-6 sm:grid-cols-2 lg:grid-cols-4">
          {stats.map((stat) => (
            <div
              key={stat.label}
              className="flex flex-col gap-2 rounded-2xl border border-otter-dark-border bg-otter-dark-bg/70 p-4"
            >
              <div className="text-xs font-semibold uppercase tracking-wide text-otter-dark-text-muted">
                {stat.label}
              </div>
              <div className="text-2xl font-semibold text-otter-dark-text">{stat.value}</div>
              <div className="text-xs text-otter-dark-text-muted">{stat.caption}</div>
            </div>
          ))}
        </section>

        <section>
          <div className="grid grid-cols-[repeat(auto-fill,minmax(340px,1fr))] gap-6">
            {agents.map((agent) => (
              <AgentCard key={agent.id} agent={agent} />
            ))}
          </div>
        </section>

        <section className="rounded-3xl border border-otter-dark-border bg-gradient-to-r from-otter-dark-surface via-otter-dark-bg to-otter-dark-surface p-6 text-otter-dark-text">
          <div className="flex flex-wrap items-center justify-between gap-6">
            <div>
              <p className="text-sm uppercase tracking-[0.3em] text-otter-dark-text-muted">
                Raft Status
              </p>
              <h2 className="mt-2 text-2xl font-semibold">All paws on deck.</h2>
              <p className="mt-1 text-sm text-otter-dark-text-muted">
                Your agents are coordinating in real time to keep projects moving.
              </p>
            </div>
            <div className="flex items-center gap-3 rounded-2xl border border-otter-dark-border bg-otter-dark-bg/70 px-5 py-4">
              <span className="text-4xl">🦦</span>
              <div>
                <div className="text-sm font-semibold">Otter banner</div>
                <div className="text-xs text-otter-dark-text-muted">Floating together since 2024.</div>
              </div>
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}

const AgentsPage = memo(AgentsPageComponent);

export default AgentsPage;
