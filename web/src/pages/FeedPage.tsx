const feedItems = [
  {
    id: "feed-1",
    actor: "Mina",
    avatar: "🦦",
    action: "summarized the Q2 roadmap and flagged two risks for review",
    timestamp: "2m ago",
    type: "MESSAGE",
  },
  {
    id: "feed-2",
    actor: "Derek",
    avatar: "🦦",
    action: "merged 3 commits into Pearl for auth hardening",
    timestamp: "12m ago",
    type: "COMMIT",
  },
  {
    id: "feed-3",
    actor: "Priya",
    avatar: "🦦",
    action: "published the new warm-brown token palette",
    timestamp: "28m ago",
    type: "CONTENT",
  },
  {
    id: "feed-4",
    actor: "Andre",
    avatar: "🦦",
    action: "rolled out the cluster patch to core infra",
    timestamp: "41m ago",
    type: "DEPLOY",
  },
  {
    id: "feed-5",
    actor: "Sasha",
    avatar: "🦦",
    action: "created a high-priority escalation task for Support",
    timestamp: "1h ago",
    type: "TASK",
  },
  {
    id: "feed-6",
    actor: "Frank",
    avatar: "🦦",
    action: "shared a summary from weekly sync with stakeholders",
    timestamp: "2h ago",
    type: "MESSAGE",
  },
  {
    id: "feed-7",
    actor: "Leo",
    avatar: "🦦",
    action: "queued weekly retention analysis for Growth",
    timestamp: "3h ago",
    type: "TASK",
  },
];

const agentFilters = [
  "Frank",
  "Derek",
  "Mina",
  "Priya",
  "Andre",
  "Sasha",
  "Leo",
  "Jules",
];

const typeFilters = [
  "All",
  "Messages",
  "Tasks",
  "Code",
  "Deploys",
  "Content",
];

const typeBadgeStyles: Record<string, string> = {
  COMMIT: "bg-[#C9A86C]/15 text-[#C9A86C] border-[#C9A86C]/40",
  TASK: "bg-emerald-500/10 text-emerald-200 border-emerald-500/40",
  MESSAGE: "bg-sky-500/10 text-sky-200 border-sky-500/40",
  DEPLOY: "bg-amber-500/10 text-amber-200 border-amber-500/40",
  CONTENT: "bg-violet-500/10 text-violet-200 border-violet-500/40",
  DEFAULT: "bg-otter-dark-bg/60 text-otter-dark-text-muted border-otter-dark-border",
};

const agentStatus = [
  {
    id: "agent-1",
    name: "Frank",
    role: "Chief of Staff",
    status: "active",
    heartbeat: "12s",
    activity: "Drafting weekly recap",
  },
  {
    id: "agent-2",
    name: "Derek",
    role: "Engineering Lead",
    status: "active",
    heartbeat: "8s",
    activity: "Reviewing Pearl PRs",
  },
  {
    id: "agent-3",
    name: "Mina",
    role: "Product Strategist",
    status: "busy",
    heartbeat: "2m",
    activity: "Roadmap brief",
  },
  {
    id: "agent-4",
    name: "Priya",
    role: "Design Systems",
    status: "idle",
    heartbeat: "6m",
    activity: "Token cleanup",
  },
];

const activityStats = [
  { label: "Active Agents", value: "6" },
  { label: "Tasks In Flight", value: "14" },
  { label: "Messages Today", value: "92" },
  { label: "Deploys", value: "4" },
];

const statusDotStyles: Record<string, string> = {
  active: "bg-emerald-400",
  busy: "bg-amber-400",
  idle: "bg-otter-dark-text-muted",
};

export default function FeedPage() {
  return (
    <div className="mx-auto flex w-full max-w-7xl flex-col gap-6 px-4 pb-12 pt-4 sm:px-6 lg:px-8">
      <header className="flex flex-wrap items-center justify-between gap-4 rounded-3xl border border-otter-dark-border bg-otter-dark-surface p-6">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.3em] text-otter-dark-text-muted">
            Otter Camp Feed
          </p>
          <h1 className="mt-2 text-3xl font-semibold text-otter-dark-text">
            Activity across your raft
          </h1>
          <p className="mt-2 text-sm text-otter-dark-text-muted">
            Track every update, deployment, and signal in one warm, realtime stream.
          </p>
        </div>
        <div className="flex items-center gap-3 rounded-2xl border border-otter-dark-border bg-otter-dark-bg/60 px-4 py-3 text-sm text-otter-dark-text-muted">
          <span className="relative flex h-2.5 w-2.5">
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75" />
            <span className="relative inline-flex h-2.5 w-2.5 rounded-full bg-emerald-400" />
          </span>
          Live updates streaming
        </div>
      </header>

      <section className="grid gap-6 lg:grid-cols-[260px_minmax(0,1fr)_300px]">
        <aside className="order-2 flex flex-col gap-5 rounded-3xl border border-otter-dark-border bg-otter-dark-surface p-5 lg:order-1">
          <div>
            <h2 className="text-sm font-semibold uppercase tracking-wide text-otter-dark-text-muted">
              Filters
            </h2>
          </div>

          <div className="flex flex-col gap-3">
            <p className="text-xs font-semibold uppercase tracking-[0.2em] text-otter-dark-text-muted">
              By Type
            </p>
            <div className="flex flex-wrap gap-2">
              {typeFilters.map((filter) => (
                <button
                  key={filter}
                  type="button"
                  className="rounded-full border border-otter-dark-border bg-otter-dark-bg/60 px-3 py-1 text-xs font-semibold text-otter-dark-text transition hover:border-[#C9A86C]/60 hover:text-[#C9A86C]"
                >
                  {filter}
                </button>
              ))}
            </div>
          </div>

          <div className="flex flex-col gap-3">
            <p className="text-xs font-semibold uppercase tracking-[0.2em] text-otter-dark-text-muted">
              By Agent
            </p>
            <div className="flex flex-col gap-2">
              {agentFilters.map((agent) => (
                <label
                  key={agent}
                  className="flex items-center gap-2 rounded-2xl border border-otter-dark-border bg-otter-dark-bg/60 px-3 py-2 text-sm text-otter-dark-text"
                >
                  <input
                    type="checkbox"
                    defaultChecked={agent === "Derek" || agent === "Mina"}
                    className="h-4 w-4 rounded border-otter-dark-border bg-transparent text-[#C9A86C] focus:ring-[#C9A86C]"
                  />
                  {agent}
                </label>
              ))}
            </div>
          </div>

          <div className="flex flex-col gap-3">
            <p className="text-xs font-semibold uppercase tracking-[0.2em] text-otter-dark-text-muted">
              Time Range
            </p>
            <select
              className="w-full rounded-2xl border border-otter-dark-border bg-otter-dark-bg/60 px-3 py-2 text-sm text-otter-dark-text"
              defaultValue="today"
            >
              <option value="today">Today</option>
              <option value="24h">Last 24 hours</option>
              <option value="7d">Last 7 days</option>
              <option value="30d">Last 30 days</option>
              <option value="custom">Custom range</option>
            </select>
          </div>

          <button
            type="button"
            className="rounded-2xl border border-[#C9A86C]/60 bg-[#C9A86C]/10 px-4 py-2 text-sm font-semibold text-[#C9A86C] transition hover:bg-[#C9A86C]/20"
          >
            Apply Filters
          </button>
        </aside>

        <div className="order-1 flex flex-col gap-4 lg:order-2">
          <div className="flex items-center justify-between gap-4 rounded-3xl border border-otter-dark-border bg-otter-dark-surface p-5">
            <div>
              <h2 className="text-lg font-semibold text-otter-dark-text">Latest Feed</h2>
              <p className="text-sm text-otter-dark-text-muted">
                23 updates in the last 6 hours
              </p>
            </div>
            <button
              type="button"
              className="rounded-2xl border border-otter-dark-border bg-otter-dark-bg/60 px-3 py-2 text-xs font-semibold text-otter-dark-text"
            >
              Export
            </button>
          </div>

          <div className="flex flex-col gap-4">
            {feedItems.map((item) => {
              const badgeStyle = typeBadgeStyles[item.type] ?? typeBadgeStyles.DEFAULT;
              return (
                <article
                  key={item.id}
                  className="rounded-3xl border border-otter-dark-border bg-otter-dark-surface p-5"
                >
                  <div className="flex flex-wrap items-start justify-between gap-4">
                    <div className="flex items-start gap-3">
                      <div className="flex h-12 w-12 items-center justify-center rounded-2xl border border-otter-dark-border bg-otter-dark-bg/70 text-xl">
                        {item.avatar}
                      </div>
                      <div>
                        <p className="text-sm text-otter-dark-text">
                          <span className="font-semibold text-[#C9A86C]">{item.actor}</span> {item.action}
                        </p>
                        <p className="mt-2 text-xs text-otter-dark-text-muted">{item.timestamp}</p>
                      </div>
                    </div>
                    <span
                      className={`rounded-full border px-3 py-1 text-xs font-semibold tracking-wide ${badgeStyle}`}
                    >
                      {item.type}
                    </span>
                  </div>
                </article>
              );
            })}
          </div>
        </div>

        <aside className="order-3 flex flex-col gap-5 rounded-3xl border border-otter-dark-border bg-otter-dark-surface p-5">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-sm font-semibold uppercase tracking-wide text-otter-dark-text-muted">
                Agent Status
              </h2>
              <p className="mt-1 text-lg font-semibold text-otter-dark-text">
                Live heartbeat
              </p>
            </div>
            <div className="flex items-center gap-2 text-xs font-semibold text-[#C9A86C]">
              <span className="relative flex h-2.5 w-2.5">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-[#C9A86C] opacity-70" />
                <span className="relative inline-flex h-2.5 w-2.5 rounded-full bg-[#C9A86C]" />
              </span>
              LIVE
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            {activityStats.map((stat) => (
              <div
                key={stat.label}
                className="rounded-2xl border border-otter-dark-border bg-otter-dark-bg/60 p-3"
              >
                <p className="text-xs uppercase tracking-[0.2em] text-otter-dark-text-muted">
                  {stat.label}
                </p>
                <p className="mt-2 text-2xl font-semibold text-otter-dark-text">
                  {stat.value}
                </p>
              </div>
            ))}
          </div>

          <div className="flex flex-col gap-3">
            {agentStatus.map((agent) => (
              <div
                key={agent.id}
                className="flex items-center justify-between gap-3 rounded-2xl border border-otter-dark-border bg-otter-dark-bg/60 px-4 py-3"
              >
                <div>
                  <p className="text-sm font-semibold text-otter-dark-text">{agent.name}</p>
                  <p className="text-xs text-otter-dark-text-muted">{agent.role}</p>
                  <p className="mt-1 text-xs text-otter-dark-text-muted">{agent.activity}</p>
                </div>
                <div className="flex flex-col items-end gap-2">
                  <span
                    className={`h-2.5 w-2.5 rounded-full ${
                      statusDotStyles[agent.status] ?? statusDotStyles.idle
                    }`}
                  />
                  <span className="text-xs text-otter-dark-text-muted">{agent.heartbeat}</span>
                </div>
              </div>
            ))}
          </div>
        </aside>
      </section>
    </div>
  );
}
