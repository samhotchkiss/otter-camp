import { useState, useMemo } from "react";
import { useParams, useNavigate, Link } from "react-router-dom";

// Demo project data - matches structure from ProjectsPage
const DEMO_PROJECTS: Record<string, {
  id: string;
  name: string;
  description: string;
  emoji: string;
  color: string;
  status: string;
  lead: string;
  taskCount: number;
  completedCount: number;
}> = {
  "1": {
    id: "1",
    name: "Otter Camp",
    description: "Task management for AI-assisted workflows",
    emoji: "🦦",
    color: "sky",
    status: "active",
    lead: "Scout Otter",
    taskCount: 24,
    completedCount: 18,
  },
  "2": {
    id: "2",
    name: "Pearl Proxy",
    description: "Memory and routing infrastructure",
    emoji: "🔮",
    color: "emerald",
    status: "active",
    lead: "Builder Otter",
    taskCount: 12,
    completedCount: 5,
  },
  "3": {
    id: "3",
    name: "ItsAlive",
    description: "Static site deployment platform",
    emoji: "⚡",
    color: "amber",
    status: "active",
    lead: "Ivy",
    taskCount: 8,
    completedCount: 8,
  },
  "4": {
    id: "4",
    name: "Three Stones",
    description: "Educational content and presentations",
    emoji: "🪨",
    color: "violet",
    status: "archived",
    lead: "Stone",
    taskCount: 15,
    completedCount: 10,
  },
};

type Task = {
  id: string;
  title: string;
  status: "queued" | "in_progress" | "review" | "done";
  priority: "P0" | "P1" | "P2";
  assignee: string;
  avatarColor: string;
  blocked?: boolean;
};

const DEMO_TASKS: Task[] = [
  { id: "t1", title: "Add email notification system", status: "queued", priority: "P2", assignee: "Ivy", avatarColor: "var(--green, #5A7A5C)" },
  { id: "t2", title: "Improve error handling in deploy flow", status: "queued", priority: "P2", assignee: "Derek", avatarColor: "var(--blue, #4A6D7C)" },
  { id: "t3", title: "Write user documentation for API", status: "queued", priority: "P2", assignee: "Stone", avatarColor: "#ec4899" },
  { id: "t4", title: "Deploy v2.1.0 to production", status: "in_progress", priority: "P0", assignee: "Ivy", avatarColor: "var(--green, #5A7A5C)", blocked: true },
  { id: "t5", title: "Implement onboarding flow redesign", status: "in_progress", priority: "P1", assignee: "Derek", avatarColor: "var(--blue, #4A6D7C)" },
  { id: "t6", title: "Design new dashboard components", status: "in_progress", priority: "P1", assignee: "Jeff G", avatarColor: "var(--orange, #C87941)" },
  { id: "t7", title: "Landing page copy update", status: "review", priority: "P2", assignee: "Stone", avatarColor: "#ec4899" },
  { id: "t8", title: "API endpoint refactor", status: "review", priority: "P2", assignee: "Derek", avatarColor: "var(--blue, #4A6D7C)" },
  { id: "t9", title: "Set up staging environment", status: "done", priority: "P2", assignee: "Ivy", avatarColor: "var(--green, #5A7A5C)" },
  { id: "t10", title: "Write test suite for auth", status: "done", priority: "P1", assignee: "Josh S", avatarColor: "var(--blue, #4A6D7C)" },
  { id: "t11", title: "Fix mobile responsive issues", status: "done", priority: "P2", assignee: "Jeff G", avatarColor: "var(--orange, #C87941)" },
];

type Activity = {
  id: string;
  agent: string;
  avatarColor: string;
  text: string;
  highlight: string;
  timeAgo: string;
};

const DEMO_ACTIVITY: Activity[] = [
  { id: "a1", agent: "Ivy", avatarColor: "var(--green, #5A7A5C)", text: "Waiting on ", highlight: "your approval", timeAgo: "5m ago" },
  { id: "a2", agent: "Derek", avatarColor: "var(--blue, #4A6D7C)", text: "Pushed ", highlight: "3 commits", timeAgo: "23m ago" },
  { id: "a3", agent: "Jeff G", avatarColor: "var(--orange, #C87941)", text: "Completed ", highlight: "dashboard components", timeAgo: "1h ago" },
  { id: "a4", agent: "Stone", avatarColor: "#ec4899", text: "Moved ", highlight: "Landing page copy", timeAgo: "2h ago" },
  { id: "a5", agent: "Josh S", avatarColor: "var(--blue, #4A6D7C)", text: "Completed ", highlight: "auth test suite", timeAgo: "3h ago" },
];

const COLUMNS = [
  { key: "queued", title: "📋 Queued", statuses: ["queued"] },
  { key: "in_progress", title: "🔨 In Progress", statuses: ["in_progress"] },
  { key: "review", title: "👀 Review", statuses: ["review"] },
  { key: "done", title: "✅ Done", statuses: ["done"] },
] as const;

function TaskCard({ task, onClick }: { task: Task; onClick?: () => void }) {
  const priorityClasses: Record<string, string> = {
    P0: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400",
    P1: "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400",
    P2: "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400",
  };

  return (
    <div
      onClick={onClick}
      className={`cursor-pointer rounded-xl border bg-white p-4 transition hover:-translate-y-0.5 hover:shadow-md dark:bg-slate-900 ${
        task.blocked
          ? "border-l-4 border-l-amber-500 border-t-slate-200 border-r-slate-200 border-b-slate-200 dark:border-t-slate-700 dark:border-r-slate-700 dark:border-b-slate-700"
          : "border-slate-200 hover:border-slate-300 dark:border-slate-700 dark:hover:border-slate-600"
      } ${task.status === "done" ? "opacity-70" : ""}`}
    >
      <h4 className="mb-3 text-sm font-semibold text-slate-900 dark:text-white">
        {task.title}
      </h4>
      <div className="flex items-center justify-between text-xs">
        <div className="flex items-center gap-2">
          <div
            className="flex h-6 w-6 items-center justify-center rounded-full text-[10px] font-semibold text-white"
            style={{ backgroundColor: task.avatarColor }}
          >
            {task.assignee[0]}
          </div>
          <span className="text-slate-600 dark:text-slate-400">{task.assignee}</span>
        </div>
        {task.status !== "done" && (
          <span className={`rounded px-2 py-0.5 text-[10px] font-semibold ${priorityClasses[task.priority]}`}>
            {task.priority}{task.blocked ? " • Blocked" : ""}
          </span>
        )}
      </div>
    </div>
  );
}

function BoardColumn({
  title,
  tasks,
  onTaskClick,
}: {
  title: string;
  tasks: Task[];
  onTaskClick?: (task: Task) => void;
}) {
  return (
    <div className="flex w-80 flex-shrink-0 flex-col rounded-xl bg-slate-100 dark:bg-slate-800/50">
      <div className="flex items-center gap-3 border-b border-slate-200 px-5 py-4 dark:border-slate-700">
        <span className="text-sm font-bold text-slate-900 dark:text-white">{title}</span>
        <span className="rounded-full bg-white px-2.5 py-0.5 text-xs font-semibold text-slate-500 dark:bg-slate-700 dark:text-slate-300">
          {tasks.length}
        </span>
      </div>
      <div className="flex flex-1 flex-col gap-3 overflow-y-auto p-3" style={{ maxHeight: "calc(100vh - 340px)" }}>
        {tasks.map((task) => (
          <TaskCard key={task.id} task={task} onClick={() => onTaskClick?.(task)} />
        ))}
      </div>
      <button
        type="button"
        className="mx-3 mb-3 rounded-lg border-2 border-dashed border-slate-300 bg-transparent py-3 text-sm text-slate-500 transition hover:border-emerald-500 hover:text-slate-700 dark:border-slate-600 dark:hover:border-emerald-400 dark:hover:text-slate-300"
      >
        + Add Task
      </button>
    </div>
  );
}

function ActivityItem({ activity }: { activity: Activity }) {
  return (
    <div className="rounded-lg p-3 transition hover:bg-slate-100 dark:hover:bg-slate-800">
      <div className="mb-1.5 flex items-center gap-2.5">
        <div
          className="flex h-7 w-7 items-center justify-center rounded-full text-xs font-semibold text-white"
          style={{ backgroundColor: activity.avatarColor }}
        >
          {activity.agent[0]}
        </div>
        <span className="text-sm font-semibold text-slate-900 dark:text-white">
          {activity.agent}
        </span>
        <span className="ml-auto text-xs text-slate-500 dark:text-slate-400">
          {activity.timeAgo}
        </span>
      </div>
      <p className="text-sm text-slate-600 dark:text-slate-400">
        {activity.text}
        <strong className="text-slate-900 dark:text-white">{activity.highlight}</strong>
      </p>
    </div>
  );
}

type TabKey = "board" | "list" | "activity" | "settings";

export default function ProjectDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [activeTab, setActiveTab] = useState<TabKey>("board");

  const project = id ? DEMO_PROJECTS[id] : null;

  const tasksByColumn = useMemo(() => {
    const grouped: Record<string, Task[]> = {};
    for (const col of COLUMNS) {
      grouped[col.key] = DEMO_TASKS.filter((t) => col.statuses.includes(t.status));
    }
    return grouped;
  }, []);

  const waitingCount = useMemo(() => {
    return DEMO_TASKS.filter((t) => t.blocked).length;
  }, []);

  if (!project) {
    return (
      <div className="flex flex-col items-center justify-center py-20">
        <div className="text-6xl">🦦</div>
        <h1 className="mt-4 text-2xl font-bold text-slate-900 dark:text-white">
          Project Not Found
        </h1>
        <p className="mt-2 text-slate-600 dark:text-slate-400">
          This project doesn't exist or may have been deleted.
        </p>
        <button
          type="button"
          onClick={() => navigate("/projects")}
          className="mt-6 rounded-xl bg-emerald-600 px-5 py-2.5 text-sm font-medium text-white transition hover:bg-emerald-700"
        >
          ← Back to Projects
        </button>
      </div>
    );
  }

  const statusColors: Record<string, { dot: string; text: string }> = {
    active: { dot: "bg-emerald-500", text: "Active" },
    completed: { dot: "bg-slate-400", text: "Completed" },
    archived: { dot: "bg-slate-400", text: "Archived" },
    blocked: { dot: "bg-amber-500", text: "Blocked" },
  };

  const status = statusColors[project.status] || statusColors.active;

  const tabs: { key: TabKey; label: string; badge?: number }[] = [
    { key: "board", label: "Board" },
    { key: "list", label: "List" },
    { key: "activity", label: "Activity" },
    { key: "settings", label: "Settings" },
  ];

  const handleTaskClick = (task: Task) => {
    navigate(`/tasks/${task.id}`);
  };

  return (
    <div className="flex min-h-full flex-col">
      {/* Breadcrumb */}
      <nav className="mb-4 flex items-center gap-2 text-sm text-slate-500 dark:text-slate-400">
        <Link to="/projects" className="hover:text-slate-700 dark:hover:text-slate-200">
          Projects
        </Link>
        <span>›</span>
        <span className="font-medium text-slate-900 dark:text-white">{project.name}</span>
      </nav>

      {/* Project Header */}
      <header className="mb-6 rounded-2xl border border-slate-200 bg-white p-6 dark:border-slate-800 dark:bg-slate-900">
        <div className="flex items-center gap-5">
          <div className="flex h-16 w-16 items-center justify-center rounded-2xl bg-slate-100 text-4xl dark:bg-slate-800">
            {project.emoji}
          </div>
          <div className="flex-1">
            <h1 className="text-2xl font-bold text-slate-900 dark:text-white">
              {project.name}
            </h1>
            <div className="mt-1 flex flex-wrap items-center gap-4 text-sm text-slate-600 dark:text-slate-400">
              <div className="flex items-center gap-1.5">
                <span className={`h-2.5 w-2.5 rounded-full ${status.dot}`} />
                {waitingCount > 0 ? (
                  <span>{waitingCount} item{waitingCount !== 1 ? "s" : ""} waiting on you</span>
                ) : (
                  <span>{status.text}</span>
                )}
              </div>
              <span>•</span>
              <span>{project.taskCount - project.completedCount} active tasks</span>
              <span>•</span>
              <span>Lead: {project.lead}</span>
            </div>
          </div>
          <div className="flex gap-3">
            <button
              type="button"
              className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700"
            >
              Settings
            </button>
            <button
              type="button"
              className="rounded-lg bg-emerald-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-emerald-700"
            >
              + New Task
            </button>
          </div>
        </div>
      </header>

      {/* Tabs */}
      <div className="mb-6 flex gap-1 border-b border-slate-200 dark:border-slate-700">
        {tabs.map((tab) => (
          <button
            key={tab.key}
            type="button"
            onClick={() => setActiveTab(tab.key)}
            className={`relative px-5 py-3 text-sm font-medium transition ${
              activeTab === tab.key
                ? "text-emerald-600 dark:text-emerald-400"
                : "text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200"
            }`}
          >
            {tab.label}
            {tab.badge && (
              <span className="ml-1.5 rounded-full bg-red-500 px-2 py-0.5 text-[10px] font-bold text-white">
                {tab.badge}
              </span>
            )}
            {activeTab === tab.key && (
              <span className="absolute bottom-0 left-0 right-0 h-0.5 bg-emerald-600 dark:bg-emerald-400" />
            )}
          </button>
        ))}
      </div>

      {/* Tab Content */}
      {activeTab === "board" && (
        <div className="flex flex-1 gap-6 overflow-x-auto pb-4">
          {/* Kanban Board */}
          <div className="flex flex-1 gap-5 overflow-x-auto">
            {COLUMNS.map((col) => (
              <BoardColumn
                key={col.key}
                title={col.title}
                tasks={tasksByColumn[col.key] || []}
                onTaskClick={handleTaskClick}
              />
            ))}
          </div>

          {/* Activity Sidebar */}
          <aside className="hidden w-80 flex-shrink-0 rounded-2xl border border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900 xl:flex xl:flex-col">
            <div className="flex items-center gap-2 border-b border-slate-200 px-5 py-4 dark:border-slate-700">
              <span className="text-sm">📡</span>
              <span className="text-sm font-bold text-slate-900 dark:text-white">
                Recent Activity
              </span>
            </div>
            <div className="flex-1 overflow-y-auto p-3">
              {DEMO_ACTIVITY.map((activity) => (
                <ActivityItem key={activity.id} activity={activity} />
              ))}
            </div>
          </aside>
        </div>
      )}

      {activeTab === "list" && (
        <div className="rounded-2xl border border-slate-200 bg-white p-6 dark:border-slate-800 dark:bg-slate-900">
          <div className="space-y-3">
            {DEMO_TASKS.filter(t => t.status !== "done").map((task) => (
              <div
                key={task.id}
                onClick={() => handleTaskClick(task)}
                className="flex cursor-pointer items-center gap-4 rounded-xl border border-slate-200 p-4 transition hover:border-slate-300 hover:bg-slate-50 dark:border-slate-700 dark:hover:border-slate-600 dark:hover:bg-slate-800"
              >
                <input type="checkbox" className="h-5 w-5 rounded border-slate-300 dark:border-slate-600" readOnly />
                <span className="flex-1 text-sm font-medium text-slate-900 dark:text-white">
                  {task.title}
                </span>
                <span className="text-xs text-slate-500 dark:text-slate-400">
                  {task.assignee}
                </span>
                <span className={`rounded px-2 py-0.5 text-[10px] font-semibold ${
                  task.priority === "P0" ? "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400" :
                  task.priority === "P1" ? "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400" :
                  "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400"
                }`}>
                  {task.priority}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {activeTab === "activity" && (
        <div className="rounded-2xl border border-slate-200 bg-white p-6 dark:border-slate-800 dark:bg-slate-900">
          <div className="space-y-2">
            {DEMO_ACTIVITY.map((activity) => (
              <ActivityItem key={activity.id} activity={activity} />
            ))}
          </div>
        </div>
      )}

      {activeTab === "settings" && (
        <div className="rounded-2xl border border-slate-200 bg-white p-6 dark:border-slate-800 dark:bg-slate-900">
          <h2 className="mb-4 text-lg font-semibold text-slate-900 dark:text-white">
            Project Settings
          </h2>
          <p className="text-sm text-slate-600 dark:text-slate-400">
            Project settings coming soon...
          </p>
        </div>
      )}
    </div>
  );
}
