// Cache bust: 2026-02-05-11:15
import { useState, useEffect, useMemo } from "react";
import { useParams, useNavigate, Link } from "react-router-dom";
import LoadingSpinner from "../components/LoadingSpinner";

const API_URL = import.meta.env.VITE_API_URL || 'https://api.otter.camp';

// Agent color mappings
const agentColors: Record<string, string> = {
  "Derek": "var(--blue, #4A6D7C)",
  "Ivy": "var(--green, #5A7A5C)",
  "Jeff G": "var(--orange, #C87941)",
  "Stone": "#ec4899",
  "Josh S": "var(--blue, #4A6D7C)",
  "Frank": "var(--accent, #C9A86C)",
  "Nova": "#a855f7",
  "Max": "#06b6d4",
  "Penny": "#f59e0b",
  "Beau H": "#10b981",
  "Jeremy H": "#6366f1",
  "Claudette": "#ec4899",
};

// Project emoji mappings  
const projectEmojis: Record<string, string> = {
  "Pearl Proxy": "🔮",
  "Otter Camp": "🦦",
  "ItsAlive": "⚡",
  "Three Stones": "🪨",
  "OpenClaw": "🦀",
};

type Project = {
  id: string;
  name: string;
  description?: string;
  status?: string;
  lead?: string;
  repo_url?: string;
};

type ApiTask = {
  id: string;
  title: string;
  description?: string;
  status: string;
  priority: string;
  assigned_agent_id?: string;
  number?: number;
};

type Task = {
  id: string;
  title: string;
  status: "queued" | "in_progress" | "review" | "done" | "blocked" | "dispatched" | "cancelled";
  priority: "P0" | "P1" | "P2" | "P3";
  assignee: string;
  avatarColor: string;
  blocked?: boolean;
};

type Activity = {
  id: string;
  agent: string;
  avatarColor: string;
  text: string;
  highlight: string;
  timeAgo: string;
};

// Agent ID to name mapping (from database)
const agentIdToName: Record<string, string> = {};

type TaskColumn = {
  key: string;
  title: string;
  statuses: Task["status"][];
};

const COLUMNS: TaskColumn[] = [
  { key: "queued", title: "📋 Queued", statuses: ["queued", "dispatched"] },
  { key: "in_progress", title: "🔨 In Progress", statuses: ["in_progress"] },
  { key: "review", title: "👀 Review", statuses: ["review", "blocked"] },
  { key: "done", title: "✅ Done", statuses: ["done", "cancelled"] },
];

function formatTimeAgo(dateStr: string): string {
  const date = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);
  
  if (diffMins < 1) return "just now";
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  return `${diffDays}d ago`;
}

function TaskCard({ task, onClick }: { task: Task; onClick?: () => void }) {
  const priorityClasses: Record<string, string> = {
    P0: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400",
    P1: "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400",
    P2: "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400",
    P3: "bg-gray-100 text-gray-700 dark:bg-gray-900/30 dark:text-gray-400",
  };

  return (
    <div
      onClick={onClick}
      className={`cursor-pointer rounded-xl border bg-[var(--surface)] p-4 transition hover:-translate-y-0.5 hover:shadow-md ${
        task.blocked
          ? "border-l-4 border-l-[#C9A86C] border-t-[var(--border)] border-r-[var(--border)] border-b-[var(--border)]"
          : "border-[var(--border)] hover:border-[#C9A86C]/50"
      } ${task.status === "done" ? "opacity-70" : ""}`}
    >
      <h4 className="mb-3 text-sm font-semibold text-[var(--text)]">
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
          <span className="text-[var(--text-muted)]">{task.assignee}</span>
        </div>
        {task.status !== "done" && (
          <span className={`rounded px-2 py-0.5 text-[10px] font-semibold ${priorityClasses[task.priority] || priorityClasses.P2}`}>
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
    <div className="flex w-80 flex-shrink-0 flex-col rounded-xl bg-[var(--surface-alt)]">
      <div className="flex items-center gap-3 border-b border-[var(--border)] px-5 py-4">
        <span className="text-sm font-bold text-[var(--text)]">{title}</span>
        <span className="rounded-full bg-[var(--surface)] px-2.5 py-0.5 text-xs font-semibold text-[var(--text-muted)]">
          {tasks.length}
        </span>
      </div>
      <div className="flex flex-1 flex-col gap-3 overflow-y-auto p-3" style={{ maxHeight: "calc(100vh - 340px)" }}>
        {tasks.map((task) => (
          <TaskCard key={task.id} task={task} onClick={() => onTaskClick?.(task)} />
        ))}
        {tasks.length === 0 && (
          <div className="py-8 text-center text-sm text-[var(--text-muted)]">
            No tasks
          </div>
        )}
      </div>
      <button
        type="button"
        className="mx-3 mb-3 rounded-lg border-2 border-dashed border-[var(--border)] bg-transparent py-3 text-sm text-[var(--text-muted)] transition hover:border-[#C9A86C] hover:text-[#C9A86C]"
      >
        + Add Task
      </button>
    </div>
  );
}

function ActivityItem({ activity }: { activity: Activity }) {
  return (
    <div className="rounded-lg p-3 transition hover:bg-[var(--surface-alt)]">
      <div className="mb-1.5 flex items-center gap-2.5">
        <div
          className="flex h-7 w-7 items-center justify-center rounded-full text-xs font-semibold text-white"
          style={{ backgroundColor: activity.avatarColor }}
        >
          {activity.agent[0]}
        </div>
        <span className="text-sm font-semibold text-[var(--text)]">
          {activity.agent}
        </span>
        <span className="ml-auto text-xs text-[var(--text-muted)]">
          {activity.timeAgo}
        </span>
      </div>
      <p className="text-sm text-[var(--text-muted)]">
        {activity.text}
        <strong className="text-[var(--text)]">{activity.highlight}</strong>
      </p>
    </div>
  );
}

type TabKey = "board" | "list" | "activity" | "settings";

export default function ProjectDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [activeTab, setActiveTab] = useState<TabKey>("board");
  const [project, setProject] = useState<Project | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [activity, setActivity] = useState<Activity[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Fetch project and tasks
  useEffect(() => {
    async function fetchData() {
      if (!id) return;
      
      setIsLoading(true);
      setError(null);
      
      try {
        const orgId = localStorage.getItem('otter-camp-org-id');
        
        // Fetch project
        const projectRes = await fetch(`${API_URL}/api/projects/${id}`);
        if (!projectRes.ok) {
          throw new Error('Project not found');
        }
        const projectData = await projectRes.json();
        setProject(projectData);
        
        // Fetch agents to map IDs to names
        const agentsRes = await fetch(`${API_URL}/api/sync/agents`);
        if (agentsRes.ok) {
          const agentsData = await agentsRes.json();
          for (const agent of (agentsData.agents || [])) {
            agentIdToName[agent.id] = agent.name;
          }
        }
        
        // Fetch tasks for this project
        const tasksUrl = orgId 
          ? `${API_URL}/api/tasks?org_id=${orgId}&project_id=${id}`
          : `${API_URL}/api/tasks?project_id=${id}`;
        const tasksRes = await fetch(tasksUrl);
        if (tasksRes.ok) {
          const tasksData = await tasksRes.json();
          const apiTasks: ApiTask[] = tasksData.tasks || tasksData || [];
          
          // Transform API tasks to UI tasks
          const transformedTasks: Task[] = apiTasks.map((t) => {
            const agentName = t.assigned_agent_id ? 
              (agentIdToName[t.assigned_agent_id] || "Unassigned") : 
              "Unassigned";
            return {
              id: t.id,
              title: t.title,
              status: t.status as Task["status"],
              priority: (t.priority || "P2") as Task["priority"],
              assignee: agentName,
              avatarColor: agentColors[agentName] || "var(--accent, #C9A86C)",
              blocked: t.status === "blocked",
            };
          });
          setTasks(transformedTasks);
        }
        
        // Fetch activity for this project
        const activityUrl = orgId
          ? `${API_URL}/api/feed?org_id=${orgId}&limit=10`
          : `${API_URL}/api/feed?limit=10`;
        const activityRes = await fetch(activityUrl);
        if (activityRes.ok) {
          const activityData = await activityRes.json();
          const items = activityData.items || [];
          const transformedActivity: Activity[] = items.slice(0, 5).map((item: {
            id: string;
            agent_name?: string;
            type?: string;
            metadata?: { message?: string; comment?: string; title?: string };
            created_at?: string;
          }) => {
            const agentName = item.agent_name || "Unknown";
            const metadata = item.metadata || {};
            const highlight = metadata.message || metadata.comment || metadata.title || item.type || "";
            return {
              id: item.id,
              agent: agentName,
              avatarColor: agentColors[agentName] || "var(--accent, #C9A86C)",
              text: `${item.type?.replace(/_/g, " ")}: `,
              highlight: highlight,
              timeAgo: item.created_at ? formatTimeAgo(item.created_at) : "",
            };
          });
          setActivity(transformedActivity);
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load project');
      } finally {
        setIsLoading(false);
      }
    }
    
    fetchData();
  }, [id]);

  const tasksByColumn = useMemo(() => {
    const grouped: Record<string, Task[]> = {};
    for (const col of COLUMNS) {
      grouped[col.key] = tasks.filter((t) => col.statuses.includes(t.status));
    }
    return grouped;
  }, [tasks]);

  const waitingCount = useMemo(() => {
    return tasks.filter((t) => t.blocked).length;
  }, [tasks]);

  const activeTaskCount = useMemo(() => {
    return tasks.filter((t) => t.status !== "done" && t.status !== "cancelled").length;
  }, [tasks]);

  if (isLoading) {
    return (
      <div className="flex flex-col items-center justify-center py-20">
        <LoadingSpinner size="lg" />
        <p className="mt-4 text-[var(--text-muted)]">Loading project...</p>
      </div>
    );
  }

  if (error || !project) {
    return (
      <div className="flex flex-col items-center justify-center py-20">
        <div className="text-6xl">🦦</div>
        <h1 className="mt-4 text-2xl font-bold text-[var(--text)]">
          Project Not Found
        </h1>
        <p className="mt-2 text-[var(--text-muted)]">
          {error || "This project doesn't exist or may have been deleted."}
        </p>
        <button
          type="button"
          onClick={() => navigate("/projects")}
          className="mt-6 rounded-xl bg-amber-600 px-5 py-2.5 text-sm font-medium text-white transition hover:bg-amber-700"
        >
          ← Back to Projects
        </button>
      </div>
    );
  }

  const emoji = projectEmojis[project.name] || "📁";
  const status = project.status || "active";
  const statusColors: Record<string, { dot: string; text: string }> = {
    active: { dot: "bg-[var(--green)]", text: "Active" },
    completed: { dot: "bg-[var(--text-muted)]", text: "Completed" },
    archived: { dot: "bg-[var(--text-muted)]", text: "Archived" },
    blocked: { dot: "bg-amber-500", text: "Blocked" },
  };
  const statusDisplay = statusColors[status] || statusColors.active;

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
      <nav className="mb-4 flex items-center gap-2 text-sm text-[var(--text-muted)]">
        <Link to="/projects" className="hover:text-[var(--text)]">
          Projects
        </Link>
        <span>›</span>
        <span className="font-medium text-[var(--text)]">{project.name}</span>
      </nav>

      {/* Project Header */}
      <header className="mb-6 rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6">
        <div className="flex items-center gap-5">
          <div className="flex h-16 w-16 items-center justify-center rounded-2xl bg-[var(--surface-alt)] text-4xl">
            {emoji}
          </div>
          <div className="flex-1">
            <h1 className="text-2xl font-bold text-[var(--text)]">
              {project.name}
            </h1>
            <div className="mt-1 flex flex-wrap items-center gap-4 text-sm text-[var(--text-muted)]">
              <div className="flex items-center gap-1.5">
                <span className={`h-2.5 w-2.5 rounded-full ${statusDisplay.dot}`} />
                {waitingCount > 0 ? (
                  <span>{waitingCount} item{waitingCount !== 1 ? "s" : ""} waiting on you</span>
                ) : (
                  <span>{statusDisplay.text}</span>
                )}
              </div>
              <span>•</span>
              <span>{activeTaskCount} active task{activeTaskCount !== 1 ? "s" : ""}</span>
              {project.lead && (
                <>
                  <span>•</span>
                  <span>Lead: {project.lead}</span>
                </>
              )}
            </div>
            {project.description && (
              <p className="mt-2 text-sm text-[var(--text-muted)]">{project.description}</p>
            )}
          </div>
          <div className="flex gap-3">
            <button
              type="button"
              className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-4 py-2 text-sm font-medium text-[var(--text)] transition hover:bg-[var(--surface-alt)]"
            >
              Settings
            </button>
            <button
              type="button"
              className="rounded-lg bg-[#C9A86C] px-4 py-2 text-sm font-medium text-white transition hover:bg-[#B8975B]"
            >
              + New Task
            </button>
          </div>
        </div>
      </header>

      {/* Tabs */}
      <div className="mb-6 flex gap-1 border-b border-[var(--border)]">
        {tabs.map((tab) => (
          <button
            key={tab.key}
            type="button"
            onClick={() => setActiveTab(tab.key)}
            className={`relative px-5 py-3 text-sm font-medium transition ${
              activeTab === tab.key
                ? "text-amber-600 dark:text-amber-400"
                : "text-[var(--text-muted)] hover:text-[var(--text)]"
            }`}
          >
            {tab.label}
            {tab.badge && (
              <span className="ml-1.5 rounded-full bg-red-500 px-2 py-0.5 text-[10px] font-bold text-white">
                {tab.badge}
              </span>
            )}
            {activeTab === tab.key && (
              <span className="absolute bottom-0 left-0 right-0 h-0.5 bg-amber-600 dark:bg-amber-400" />
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
          <aside className="hidden w-80 flex-shrink-0 rounded-2xl border border-[var(--border)] bg-[var(--surface)] xl:flex xl:flex-col">
            <div className="flex items-center gap-2 border-b border-[var(--border)] px-5 py-4">
              <span className="text-sm">📡</span>
              <span className="text-sm font-bold text-[var(--text)]">
                Recent Activity
              </span>
            </div>
            <div className="flex-1 overflow-y-auto p-3">
              {activity.length > 0 ? (
                activity.map((a) => (
                  <ActivityItem key={a.id} activity={a} />
                ))
              ) : (
                <div className="py-8 text-center text-sm text-[var(--text-muted)]">
                  No recent activity
                </div>
              )}
            </div>
          </aside>
        </div>
      )}

      {activeTab === "list" && (
        <div className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6">
          <div className="space-y-3">
            {tasks.filter(t => t.status !== "done" && t.status !== "cancelled").length > 0 ? (
              tasks.filter(t => t.status !== "done" && t.status !== "cancelled").map((task) => (
                <div
                  key={task.id}
                  onClick={() => handleTaskClick(task)}
                  className="flex cursor-pointer items-center gap-4 rounded-xl border border-[var(--border)] p-4 transition hover:border-[#C9A86C]/50 hover:bg-[var(--surface-alt)]"
                >
                  <input type="checkbox" className="h-5 w-5 rounded border-[var(--border)]" readOnly />
                  <span className="flex-1 text-sm font-medium text-[var(--text)]">
                    {task.title}
                  </span>
                  <span className="text-xs text-[var(--text-muted)]">
                    {task.assignee}
                  </span>
                  <span className={`rounded px-2 py-0.5 text-[10px] font-semibold ${
                    task.priority === "P0" ? "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400" :
                    task.priority === "P1" ? "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400" :
                    task.priority === "P2" ? "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400" :
                    "bg-gray-100 text-gray-700 dark:bg-gray-900/30 dark:text-gray-400"
                  }`}>
                    {task.priority}
                  </span>
                </div>
              ))
            ) : (
              <div className="py-8 text-center text-sm text-[var(--text-muted)]">
                No active tasks
              </div>
            )}
          </div>
        </div>
      )}

      {activeTab === "activity" && (
        <div className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6">
          <div className="space-y-2">
            {activity.length > 0 ? (
              activity.map((a) => (
                <ActivityItem key={a.id} activity={a} />
              ))
            ) : (
              <div className="py-8 text-center text-sm text-[var(--text-muted)]">
                No recent activity
              </div>
            )}
          </div>
        </div>
      )}

      {activeTab === "settings" && (
        <div className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6">
          <h2 className="mb-4 text-lg font-semibold text-[var(--text)]">
            Project Settings
          </h2>
          <p className="text-sm text-[var(--text-muted)]">
            Project settings coming soon...
          </p>
        </div>
      )}
    </div>
  );
}
