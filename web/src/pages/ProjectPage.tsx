import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";

const STATUS_STYLES = {
  active: {
    dot: "bg-emerald-400",
    label: "text-emerald-200",
    pill: "border-emerald-500/40 bg-emerald-500/10",
  },
  blocked: {
    dot: "bg-amber-400",
    label: "text-amber-200",
    pill: "border-amber-500/40 bg-amber-500/10",
  },
  idle: {
    dot: "bg-slate-400",
    label: "text-slate-200",
    pill: "border-slate-500/40 bg-slate-500/10",
  },
} as const;

type ProjectStatus = keyof typeof STATUS_STYLES;

type Project = {
  id: string;
  name: string;
  summary: string;
  status: ProjectStatus;
  owner: string;
  lastUpdate: string;
  dueDate: string;
  progress: number;
  taskCount: number;
  completedCount: number;
};

type TimelineStage = {
  title: string;
  description: string;
  status: "complete" | "current" | "upcoming";
  eta: string;
};

type ActivityItem = {
  id: string;
  actor: string;
  action: string;
  detail: string;
  time: string;
  icon: string;
};

type TaskItem = {
  id: string;
  title: string;
  owner: string;
  due: string;
  done: boolean;
};

type Agent = {
  id: string;
  name: string;
  role: string;
  status: ProjectStatus;
  initials: string;
};

const PROJECTS: Project[] = [
  {
    id: "1",
    name: "Otter Camp",
    summary: "Orchestrating AI-assisted product delivery with gentle guardrails.",
    status: "active",
    owner: "Product Ops",
    lastUpdate: "14 minutes ago",
    dueDate: "April 12",
    progress: 72,
    taskCount: 24,
    completedCount: 18,
  },
  {
    id: "2",
    name: "Pearl Proxy",
    summary: "Routing memory, traffic, and intelligence across the raft.",
    status: "blocked",
    owner: "Infra",
    lastUpdate: "2 hours ago",
    dueDate: "March 30",
    progress: 41,
    taskCount: 12,
    completedCount: 5,
  },
  {
    id: "3",
    name: "ItsAlive",
    summary: "Launch-ready deployment workflows for static sites.",
    status: "idle",
    owner: "Growth",
    lastUpdate: "Yesterday",
    dueDate: "May 08",
    progress: 100,
    taskCount: 8,
    completedCount: 8,
  },
];

const TIMELINE_STAGES: TimelineStage[] = [
  {
    title: "Discovery",
    description: "Research, interviews, problem framing",
    status: "complete",
    eta: "Jan 24",
  },
  {
    title: "Design Sprint",
    description: "Prototype flows, UI directions, team review",
    status: "complete",
    eta: "Feb 02",
  },
  {
    title: "Build",
    description: "Implementation, QA, and iterative polish",
    status: "current",
    eta: "Feb 16",
  },
  {
    title: "Launch",
    description: "Release prep, docs, enablement",
    status: "upcoming",
    eta: "Mar 04",
  },
];

const ACTIVITY_STREAM: ActivityItem[] = [
  {
    id: "a1",
    actor: "Mina",
    action: "updated the launch checklist",
    detail: "Added comms handoff for partner channels.",
    time: "8m ago",
    icon: "📝",
  },
  {
    id: "a2",
    actor: "Derek",
    action: "merged the sprint backlog",
    detail: "Re-prioritized instrumentation tasks for analytics.",
    time: "42m ago",
    icon: "✅",
  },
  {
    id: "a3",
    actor: "Priya",
    action: "shared a new design spec",
    detail: "Warm brown palette update and status chips.",
    time: "2h ago",
    icon: "🎨",
  },
  {
    id: "a4",
    actor: "Frank",
    action: "scheduled a stakeholder review",
    detail: "Invites out for Friday 10am PT.",
    time: "Yesterday",
    icon: "📆",
  },
];

const TASKS: TaskItem[] = [
  {
    id: "t1",
    title: "Finalize Project Detail layout",
    owner: "Priya",
    due: "Today",
    done: true,
  },
  {
    id: "t2",
    title: "Hook up activity feed service",
    owner: "Derek",
    due: "Tomorrow",
    done: false,
  },
  {
    id: "t3",
    title: "Validate dark mode contrast",
    owner: "Mina",
    due: "Feb 06",
    done: false,
  },
  {
    id: "t4",
    title: "Draft launch brief for partners",
    owner: "Frank",
    due: "Feb 09",
    done: false,
  },
];

const AGENTS: Agent[] = [
  { id: "u1", name: "Priya", role: "Design Systems", status: "active", initials: "PR" },
  { id: "u2", name: "Derek", role: "Engineering Lead", status: "active", initials: "DK" },
  { id: "u3", name: "Mina", role: "Product Strategist", status: "blocked", initials: "MN" },
  { id: "u4", name: "Frank", role: "Chief of Staff", status: "idle", initials: "FK" },
];

function ProjectHeader({ project }: { project: Project }) {
  const statusStyle = STATUS_STYLES[project.status];

  return (
    <section className="rounded-3xl border border-otter-dark-border bg-gradient-to-br from-otter-dark-surface via-otter-dark-bg to-otter-dark-surface p-6 shadow-lg shadow-black/10 sm:p-8">
      <nav className="text-xs font-semibold uppercase tracking-[0.2em] text-otter-dark-text-muted">
        <Link className="transition hover:text-[#C9A86C]" to="/">
          otter.camp
        </Link>
        <span className="mx-2 text-otter-dark-text-muted/60">&gt;</span>
        <Link className="transition hover:text-[#C9A86C]" to="/projects">
          Projects
        </Link>
        <span className="mx-2 text-otter-dark-text-muted/60">&gt;</span>
        <span className="text-otter-dark-text">{project.name}</span>
      </nav>

      <div className="mt-5 flex flex-wrap items-center justify-between gap-6">
        <div>
          <div className="flex items-center gap-3">
            <span className="text-3xl" aria-hidden="true">
              🦦
            </span>
            <h1 className="text-3xl font-semibold text-otter-dark-text">{project.name}</h1>
          </div>
          <p className="mt-2 max-w-2xl text-sm text-otter-dark-text-muted">
            {project.summary}
          </p>
        </div>
        <div className="flex items-center gap-3">
          <div
            className={`inline-flex items-center gap-2 rounded-full border px-3 py-1 text-xs font-semibold uppercase tracking-wide ${statusStyle.label} ${statusStyle.pill}`}
          >
            <span className={`h-2.5 w-2.5 rounded-full ${statusStyle.dot}`} />
            {project.status}
          </div>
          <button
            type="button"
            className="rounded-full border border-otter-dark-border bg-otter-dark-bg/70 px-4 py-1.5 text-xs font-semibold text-otter-dark-text transition hover:border-[#C9A86C]/60 hover:text-[#C9A86C]"
          >
            View Roadmap
          </button>
        </div>
      </div>

      <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded-2xl border border-otter-dark-border bg-otter-dark-bg/70 p-4">
          <p className="text-xs uppercase tracking-[0.2em] text-otter-dark-text-muted">Owner</p>
          <p className="mt-2 text-sm font-semibold text-otter-dark-text">{project.owner}</p>
        </div>
        <div className="rounded-2xl border border-otter-dark-border bg-otter-dark-bg/70 p-4">
          <p className="text-xs uppercase tracking-[0.2em] text-otter-dark-text-muted">Last Update</p>
          <p className="mt-2 text-sm font-semibold text-otter-dark-text">{project.lastUpdate}</p>
        </div>
        <div className="rounded-2xl border border-otter-dark-border bg-otter-dark-bg/70 p-4">
          <p className="text-xs uppercase tracking-[0.2em] text-otter-dark-text-muted">Due Date</p>
          <p className="mt-2 text-sm font-semibold text-otter-dark-text">{project.dueDate}</p>
        </div>
        <div className="rounded-2xl border border-otter-dark-border bg-otter-dark-bg/70 p-4">
          <p className="text-xs uppercase tracking-[0.2em] text-otter-dark-text-muted">Progress</p>
          <p className="mt-2 text-sm font-semibold text-otter-dark-text">
            {project.completedCount}/{project.taskCount} tasks
          </p>
        </div>
      </div>
    </section>
  );
}

function Timeline({ stages }: { stages: TimelineStage[] }) {
  const completedCount = stages.filter((stage) => stage.status === "complete").length;
  const progress = Math.round((completedCount / stages.length) * 100);

  return (
    <section className="rounded-3xl border border-otter-dark-border bg-otter-dark-surface p-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-otter-dark-text">Timeline</h2>
          <p className="mt-1 text-sm text-otter-dark-text-muted">Stage progress across delivery.</p>
        </div>
        <div className="rounded-full border border-otter-dark-border bg-otter-dark-bg/70 px-3 py-1 text-xs font-semibold text-otter-dark-text-muted">
          {progress}% complete
        </div>
      </div>
      <div className="mt-4 h-2 overflow-hidden rounded-full bg-otter-dark-surface-alt">
        <div
          className="h-full rounded-full bg-[#C9A86C]"
          style={{ width: `${progress}%` }}
        />
      </div>
      <div className="mt-6 space-y-4">
        {stages.map((stage, index) => {
          const isComplete = stage.status === "complete";
          const isCurrent = stage.status === "current";
          return (
            <div key={stage.title} className="flex gap-4">
              <div className="flex flex-col items-center">
                <div
                  className={`flex h-10 w-10 items-center justify-center rounded-xl border text-sm font-semibold ${
                    isComplete
                      ? "border-[#C9A86C]/70 bg-[#C9A86C]/20 text-[#C9A86C]"
                      : isCurrent
                        ? "border-otter-dark-border bg-otter-dark-bg text-otter-dark-text"
                        : "border-otter-dark-border bg-otter-dark-surface-alt text-otter-dark-text-muted"
                  }`}
                >
                  {index + 1}
                </div>
                {index < stages.length - 1 ? (
                  <div className="mt-2 h-10 w-px bg-otter-dark-border" />
                ) : null}
              </div>
              <div className="flex-1 rounded-2xl border border-otter-dark-border bg-otter-dark-bg/60 px-4 py-3">
                <div className="flex items-center justify-between">
                  <h3 className="text-sm font-semibold text-otter-dark-text">{stage.title}</h3>
                  <span className="text-xs text-otter-dark-text-muted">ETA {stage.eta}</span>
                </div>
                <p className="mt-1 text-sm text-otter-dark-text-muted">{stage.description}</p>
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}

function ActivityStream({ items }: { items: ActivityItem[] }) {
  return (
    <section className="rounded-3xl border border-otter-dark-border bg-otter-dark-surface p-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-otter-dark-text">Activity Stream</h2>
          <p className="mt-1 text-sm text-otter-dark-text-muted">Recent updates from the team.</p>
        </div>
        <button
          type="button"
          className="rounded-full border border-otter-dark-border bg-otter-dark-bg/60 px-3 py-1 text-xs font-semibold text-otter-dark-text-muted transition hover:border-[#C9A86C]/60 hover:text-[#C9A86C]"
        >
          View All
        </button>
      </div>
      <div className="mt-5 space-y-4">
        {items.map((item) => (
          <div
            key={item.id}
            className="flex gap-4 rounded-2xl border border-otter-dark-border/60 bg-otter-dark-bg/70 px-4 py-3"
          >
            <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-otter-dark-surface-alt text-lg">
              {item.icon}
            </div>
            <div className="flex-1">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="text-sm text-otter-dark-text">
                  <span className="font-semibold">{item.actor}</span> {item.action}
                </p>
                <span className="text-xs text-otter-dark-text-muted">{item.time}</span>
              </div>
              <p className="mt-1 text-sm text-otter-dark-text-muted">{item.detail}</p>
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}

function TaskList({ tasks }: { tasks: TaskItem[] }) {
  const [items, setItems] = useState(tasks);

  const toggleTask = (id: string) => {
    setItems((prev) =>
      prev.map((task) => (task.id === id ? { ...task, done: !task.done } : task)),
    );
  };

  const completedCount = items.filter((task) => task.done).length;

  return (
    <section className="rounded-3xl border border-otter-dark-border bg-otter-dark-surface p-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-otter-dark-text">Task List</h2>
          <p className="mt-1 text-sm text-otter-dark-text-muted">
            {completedCount}/{items.length} tasks completed.
          </p>
        </div>
        <div className="rounded-full border border-otter-dark-border bg-otter-dark-bg/70 px-3 py-1 text-xs font-semibold text-otter-dark-text-muted">
          Sprint 04
        </div>
      </div>
      <div className="mt-5 space-y-3">
        {items.map((task) => (
          <label
            key={task.id}
            className="flex cursor-pointer items-start gap-3 rounded-2xl border border-otter-dark-border/60 bg-otter-dark-bg/70 px-4 py-3 text-sm text-otter-dark-text transition hover:border-[#C9A86C]/50"
          >
            <input
              type="checkbox"
              checked={task.done}
              onChange={() => toggleTask(task.id)}
              className="mt-1 h-4 w-4 rounded border-otter-dark-border bg-otter-dark-surface accent-[#C9A86C]"
            />
            <div className="flex-1">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span
                  className={`font-semibold ${
                    task.done ? "text-otter-dark-text-muted line-through" : "text-otter-dark-text"
                  }`}
                >
                  {task.title}
                </span>
                <span className="text-xs text-otter-dark-text-muted">Due {task.due}</span>
              </div>
              <p className="mt-1 text-xs text-otter-dark-text-muted">Owner: {task.owner}</p>
            </div>
          </label>
        ))}
      </div>
    </section>
  );
}

function AgentsPanel({ agents }: { agents: Agent[] }) {
  return (
    <section className="rounded-3xl border border-otter-dark-border bg-otter-dark-surface p-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-otter-dark-text">Agents</h2>
          <p className="mt-1 text-sm text-otter-dark-text-muted">Active collaborators on this project.</p>
        </div>
        <span className="rounded-full border border-otter-dark-border bg-otter-dark-bg/70 px-3 py-1 text-xs font-semibold text-otter-dark-text-muted">
          {agents.length} agents
        </span>
      </div>
      <div className="mt-5 space-y-3">
        {agents.map((agent) => {
          const statusStyle = STATUS_STYLES[agent.status];
          return (
            <div
              key={agent.id}
              className="flex items-center gap-3 rounded-2xl border border-otter-dark-border/60 bg-otter-dark-bg/70 px-4 py-3"
            >
              <div className="relative">
                <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-otter-dark-surface-alt text-sm font-semibold text-otter-dark-text">
                  {agent.initials}
                </div>
                <span
                  className={`absolute -bottom-1 -right-1 h-3 w-3 rounded-full border border-otter-dark-bg ${statusStyle.dot}`}
                />
              </div>
              <div className="flex-1">
                <p className="text-sm font-semibold text-otter-dark-text">{agent.name}</p>
                <p className="text-xs text-otter-dark-text-muted">{agent.role}</p>
              </div>
              <span
                className={`rounded-full border px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide ${statusStyle.label} ${statusStyle.pill}`}
              >
                {agent.status}
              </span>
            </div>
          );
        })}
      </div>
    </section>
  );
}

export default function ProjectPage() {
  const { id } = useParams();
  const project = useMemo(
    () => PROJECTS.find((item) => item.id === id) ?? PROJECTS[0],
    [id],
  );

  return (
    <div className="w-full">
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-8 px-4 pb-16 sm:px-6 lg:px-8">
        <ProjectHeader project={project} />
        <div className="grid gap-8 lg:grid-cols-[minmax(0,1.1fr)_minmax(0,0.9fr)]">
          <div className="flex flex-col gap-8">
            <Timeline stages={TIMELINE_STAGES} />
            <TaskList tasks={TASKS} />
            <ActivityStream items={ACTIVITY_STREAM} />
          </div>
          <div className="flex flex-col gap-8">
            <AgentsPanel agents={AGENTS} />
            <section className="rounded-3xl border border-otter-dark-border bg-otter-dark-surface p-6">
              <h2 className="text-lg font-semibold text-otter-dark-text">Highlights</h2>
              <div className="mt-4 space-y-3">
                {[
                  { label: "Current Focus", value: "Shipping Project Detail page" },
                  { label: "Risk", value: "Blocked dependency on analytics" },
                  { label: "Next Review", value: "Friday, 10am PT" },
                ].map((item) => (
                  <div
                    key={item.label}
                    className="rounded-2xl border border-otter-dark-border/60 bg-otter-dark-bg/70 px-4 py-3"
                  >
                    <p className="text-xs uppercase tracking-[0.2em] text-otter-dark-text-muted">
                      {item.label}
                    </p>
                    <p className="mt-2 text-sm font-semibold text-otter-dark-text">{item.value}</p>
                  </div>
                ))}
              </div>
            </section>
          </div>
        </div>
      </div>
    </div>
  );
}
