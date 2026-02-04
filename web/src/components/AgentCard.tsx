import { memo, useMemo } from "react";

export type AgentStatus = "active" | "busy" | "idle";

export type AgentCardData = {
  id: string;
  name: string;
  role: string;
  status: AgentStatus;
  tasks: number;
  commits: number;
  messages: number;
  activeTime: string;
  heartbeat: string;
  activity: string;
  lastAction: string;
  projects: string[];
  avatarUrl?: string;
};

export type AgentCardProps = {
  agent: AgentCardData;
};

const STATUS_STYLES: Record<AgentStatus, string> = {
  active: "bg-otter-green",
  busy: "bg-otter-orange",
  idle: "bg-otter-dark-text-muted",
};

const STATUS_LABELS: Record<AgentStatus, string> = {
  active: "Active",
  busy: "Busy",
  idle: "Idle",
};

const AVATAR_BACKGROUNDS = [
  "bg-emerald-500/20 text-emerald-300",
  "bg-sky-500/20 text-sky-300",
  "bg-amber-500/20 text-amber-300",
  "bg-rose-500/20 text-rose-300",
  "bg-indigo-500/20 text-indigo-300",
  "bg-otter-orange/15 text-otter-orange",
];

function getInitials(name: string): string {
  return name
    .split(" ")
    .map((part) => part[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();
}

function getAvatarClass(name: string): string {
  let hash = 0;
  for (let i = 0; i < name.length; i += 1) {
    hash = name.charCodeAt(i) + ((hash << 5) - hash);
  }
  const index = Math.abs(hash) % AVATAR_BACKGROUNDS.length;
  return AVATAR_BACKGROUNDS[index];
}

function parseDurationToSeconds(value: string): number {
  const match = value.trim().toLowerCase().match(/^(\d+)(s|m|h|d)$/);
  if (!match) {
    return Number.POSITIVE_INFINITY;
  }
  const amount = Number.parseInt(match[1] ?? "0", 10);
  const unit = match[2];
  if (unit === "s") return amount;
  if (unit === "m") return amount * 60;
  if (unit === "h") return amount * 3600;
  return amount * 86400;
}

function getPulseClass(value: string): string {
  const seconds = parseDurationToSeconds(value);
  if (seconds < 30) {
    return "text-otter-green";
  }
  if (seconds < 300) {
    return "text-otter-orange";
  }
  return "text-red-400";
}

function AgentCardComponent({ agent }: AgentCardProps) {
  const initials = useMemo(() => getInitials(agent.name), [agent.name]);
  const avatarClass = useMemo(() => getAvatarClass(agent.name), [agent.name]);
  const heartbeatClass = useMemo(() => getPulseClass(agent.heartbeat), [agent.heartbeat]);
  const activityClass = useMemo(() => getPulseClass(agent.activity), [agent.activity]);

  return (
    <article className="rounded-2xl border border-otter-dark-border bg-otter-dark-surface p-5 shadow-sm transition hover:-translate-y-0.5 hover:border-otter-green/50 hover:shadow-lg">
      <div className="flex items-start justify-between gap-4">
        <div className="flex items-center gap-3">
          <div className="relative">
            {agent.avatarUrl ? (
              <img
                src={agent.avatarUrl}
                alt={agent.name}
                loading="lazy"
                decoding="async"
                className="h-14 w-14 rounded-2xl object-cover ring-2 ring-otter-dark-border"
              />
            ) : (
              <div
                className={`flex h-14 w-14 items-center justify-center rounded-2xl text-lg font-semibold ring-2 ring-otter-dark-border ${avatarClass}`}
              >
                {initials}
              </div>
            )}
            <span
              className={`absolute -bottom-1 -right-1 h-3.5 w-3.5 rounded-full border-2 border-otter-dark-surface ${STATUS_STYLES[agent.status]}`}
              title={STATUS_LABELS[agent.status]}
            />
          </div>
          <div>
            <h3 className="text-base font-semibold text-otter-dark-text">{agent.name}</h3>
            <p className="text-sm text-otter-dark-text-muted">{agent.role}</p>
          </div>
        </div>
        <span className="rounded-full border border-otter-dark-border bg-otter-dark-bg px-3 py-1 text-xs font-semibold uppercase tracking-wide text-otter-dark-text-muted">
          {STATUS_LABELS[agent.status]}
        </span>
      </div>

      <div className="mt-4 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-otter-dark-border bg-otter-dark-bg/60 px-3 py-2 text-xs text-otter-dark-text-muted">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-semibold text-otter-dark-text">
            {agent.tasks} tasks
          </span>
          <span>·</span>
          <span className="font-semibold text-otter-dark-text">
            {agent.commits} commits
          </span>
          <span>·</span>
          <span className="font-semibold text-otter-dark-text">
            {agent.messages} messages
          </span>
        </div>
        <div className="text-xs">
          Active {agent.activeTime}
        </div>
      </div>

      <div className="mt-4 grid grid-cols-2 gap-3 text-xs">
        <div className="rounded-xl border border-otter-dark-border bg-otter-dark-bg/60 px-3 py-2">
          <div className="flex items-center gap-2 text-otter-dark-text-muted">
            <span className={heartbeatClass}>💓</span>
            Heartbeat
          </div>
          <div className={`mt-1 text-sm font-semibold ${heartbeatClass}`}>{agent.heartbeat}</div>
        </div>
        <div className="rounded-xl border border-otter-dark-border bg-otter-dark-bg/60 px-3 py-2">
          <div className="flex items-center gap-2 text-otter-dark-text-muted">
            <span className={activityClass}>⚡</span>
            Activity
          </div>
          <div className={`mt-1 text-sm font-semibold ${activityClass}`}>{agent.activity}</div>
        </div>
      </div>

      <p className="mt-4 text-sm text-otter-dark-text">
        <span className="text-otter-dark-text-muted">Last:</span> {agent.lastAction}
      </p>

      <div className="mt-4 flex flex-wrap gap-2">
        {agent.projects.map((project) => (
          <span
            key={project}
            className="rounded-full border border-otter-dark-border bg-otter-dark-bg/60 px-3 py-1 text-xs font-medium text-otter-dark-text-muted"
          >
            {project}
          </span>
        ))}
      </div>
    </article>
  );
}

const AgentCard = memo(AgentCardComponent);

export default AgentCard;
