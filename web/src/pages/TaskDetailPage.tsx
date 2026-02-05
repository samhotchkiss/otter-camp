import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import LoadingSpinner from "../components/LoadingSpinner";

type TaskStatus = "backlog" | "todo" | "in-progress" | "blocked" | "review" | "done";
type TaskPriority = "low" | "medium" | "high" | "urgent";

type TaskAssignee = {
  id: string;
  name: string;
  avatar?: string;
};

type TaskDetail = {
  id: string;
  title: string;
  status: TaskStatus;
  priority: TaskPriority;
  assignee?: TaskAssignee;
  description: string;
  createdAt: string;
  updatedAt: string;
};

type Subtask = {
  id: string;
  title: string;
  completed: boolean;
  createdAt: string;
};

type Comment = {
  id: string;
  author: string;
  content: string;
  createdAt: string;
};

type ActivityEvent = {
  id: string;
  type:
    | "created"
    | "edited"
    | "status_changed"
    | "priority_changed"
    | "assigned"
    | "subtask_added"
    | "subtask_completed"
    | "comment_added";
  actor: string;
  timestamp: string;
  description: string;
};

export type TaskDetailPageProps = {
  apiEndpoint?: string;
};

const STATUS_OPTIONS: { value: TaskStatus; label: string; icon: string; className: string }[] = [
  { value: "backlog", label: "Backlog", icon: "🗂️", className: "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200" },
  { value: "todo", label: "To Do", icon: "📋", className: "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200" },
  { value: "in-progress", label: "In Progress", icon: "🚀", className: "bg-sky-100 text-sky-700 dark:bg-sky-900/40 dark:text-sky-300" },
  { value: "blocked", label: "Blocked", icon: "⛔", className: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300" },
  { value: "review", label: "Review", icon: "🔍", className: "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300" },
  { value: "done", label: "Done", icon: "✅", className: "bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300" },
];

const PRIORITY_OPTIONS: { value: TaskPriority; label: string; className: string }[] = [
  { value: "low", label: "Low", className: "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200" },
  { value: "medium", label: "Medium", className: "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300" },
  { value: "high", label: "High", className: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300" },
  { value: "urgent", label: "Urgent", className: "bg-fuchsia-100 text-fuchsia-700 dark:bg-fuchsia-900/30 dark:text-fuchsia-300" },
];

const SAMPLE_ASSIGNEES: TaskAssignee[] = [
  { id: "otter-1", name: "Scout Otter", avatar: "🦦" },
  { id: "otter-2", name: "Builder Otter", avatar: "🦦" },
  { id: "otter-3", name: "Lead Otter", avatar: "🦦" },
];

const SAMPLE_TASKS: Record<string, TaskDetail> = {
  "task-1": {
    id: "task-1",
    title: "Set up camp perimeter",
    status: "todo",
    priority: "high",
    assignee: SAMPLE_ASSIGNEES[0],
    description: [
      "Mark boundaries and secure the area.",
      "",
      "### Checklist",
      "- Confirm trail access points",
      "- Place boundary flags every 25m",
      "- Note hazards (water, fallen trees)",
      "",
      "> Tip: Use **high-visibility** tape for dusk checks.",
    ].join("\n"),
    createdAt: new Date(Date.now() - 1000 * 60 * 60 * 6).toISOString(),
    updatedAt: new Date(Date.now() - 1000 * 60 * 15).toISOString(),
  },
  "task-2": {
    id: "task-2",
    title: "Gather firewood",
    status: "todo",
    priority: "medium",
    assignee: SAMPLE_ASSIGNEES[1],
    description: [
      "Collect dry wood for the campfire.",
      "",
      "- Target: 2 bundles (dry) + 1 bundle (kindling)",
      "- Avoid green wood",
    ].join("\n"),
    createdAt: new Date(Date.now() - 1000 * 60 * 60 * 3).toISOString(),
    updatedAt: new Date(Date.now() - 1000 * 60 * 60 * 2).toISOString(),
  },
  "task-3": {
    id: "task-3",
    title: "Check supplies",
    status: "in-progress",
    priority: "medium",
    assignee: SAMPLE_ASSIGNEES[2],
    description: [
      "Inventory all camping gear and note missing items.",
      "",
      "1. Tents & stakes",
      "2. Cooking kit",
      "3. First aid",
      "",
      "Add photos in comments if something is damaged.",
    ].join("\n"),
    createdAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(),
    updatedAt: new Date(Date.now() - 1000 * 60 * 30).toISOString(),
  },
  "task-4": {
    id: "task-4",
    title: "Set up tents",
    status: "done",
    priority: "high",
    assignee: SAMPLE_ASSIGNEES[1],
    description: "Pitch tents and arrange sleeping quarters.",
    createdAt: new Date(Date.now() - 1000 * 60 * 60 * 48).toISOString(),
    updatedAt: new Date(Date.now() - 1000 * 60 * 60 * 30).toISOString(),
  },
};

function formatTimestamp(isoString: string): string {
  const date = new Date(isoString);
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

function formatRelativeTime(isoString: string): string {
  const date = new Date(isoString);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);

  if (diffMins < 1) return "Just now";
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  if (diffDays < 7) return `${diffDays}d ago`;
  return formatTimestamp(isoString);
}

function safeParseJSON<T>(value: string | null): T | null {
  if (!value) return null;
  try {
    return JSON.parse(value) as T;
  } catch {
    return null;
  }
}

function newId(prefix = ""): string {
  const randomUUID = globalThis.crypto?.randomUUID?.bind(globalThis.crypto);
  if (randomUUID) return `${prefix}${randomUUID()}`;
  return `${prefix}${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function SectionCard({
  title,
  description,
  children,
  action,
}: {
  title: string;
  description?: string;
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <section className="rounded-2xl border border-slate-200 bg-white/80 p-5 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/80 sm:p-6">
      <div className="mb-4 flex items-start justify-between gap-4">
        <div>
          <h2 className="text-sm font-semibold uppercase tracking-wide text-slate-600 dark:text-slate-300">
            {title}
          </h2>
          {description ? (
            <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
              {description}
            </p>
          ) : null}
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}

function Pill({ children, className }: { children: ReactNode; className: string }) {
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-medium ${className}`}>
      {children}
    </span>
  );
}

function ActivityIcon({ type }: { type: ActivityEvent["type"] }) {
  const icon = {
    created: "✨",
    edited: "✏️",
    status_changed: "🔄",
    priority_changed: "🎯",
    assigned: "👤",
    subtask_added: "➕",
    subtask_completed: "✅",
    comment_added: "💬",
  }[type];

  return <span className="text-base" aria-hidden="true">{icon}</span>;
}

function getDefaultSubtasks(taskId: string): Subtask[] {
  if (taskId === "task-1") {
    return [
      { id: newId("subtask-"), title: "Walk the perimeter route", completed: true, createdAt: new Date(Date.now() - 1000 * 60 * 80).toISOString() },
      { id: newId("subtask-"), title: "Place boundary flags", completed: false, createdAt: new Date(Date.now() - 1000 * 60 * 60).toISOString() },
      { id: newId("subtask-"), title: "Log hazards in notes", completed: false, createdAt: new Date(Date.now() - 1000 * 60 * 30).toISOString() },
    ];
  }

  return [];
}

function getDefaultComments(taskId: string): Comment[] {
  if (taskId === "task-1") {
    return [
      {
        id: newId("comment-"),
        author: "Lead Otter",
        content: "Remember to flag the creek crossing — slippery after rain.",
        createdAt: new Date(Date.now() - 1000 * 60 * 55).toISOString(),
      },
      {
        id: newId("comment-"),
        author: "Scout Otter",
        content: "Noted two fallen trees on the north edge. Added detour markers.",
        createdAt: new Date(Date.now() - 1000 * 60 * 22).toISOString(),
      },
    ];
  }

  return [];
}

function buildInitialActivity(task: TaskDetail, subtasks: Subtask[], comments: Comment[]): ActivityEvent[] {
  const events: ActivityEvent[] = [
    {
      id: newId("activity-"),
      type: "created",
      actor: task.assignee?.name ?? "System",
      timestamp: task.createdAt,
      description: "created this task",
    },
  ];

  if (task.updatedAt && task.updatedAt !== task.createdAt) {
    events.push({
      id: newId("activity-"),
      type: "edited",
      actor: task.assignee?.name ?? "System",
      timestamp: task.updatedAt,
      description: "updated task details",
    });
  }

  for (const st of subtasks) {
    events.push({
      id: newId("activity-"),
      type: "subtask_added",
      actor: task.assignee?.name ?? "System",
      timestamp: st.createdAt,
      description: `added subtask “${st.title}”`,
    });
    if (st.completed) {
      events.push({
        id: newId("activity-"),
        type: "subtask_completed",
        actor: task.assignee?.name ?? "System",
        timestamp: new Date(new Date(st.createdAt).getTime() + 1000 * 60 * 3).toISOString(),
        description: `completed subtask “${st.title}”`,
      });
    }
  }

  for (const c of comments) {
    events.push({
      id: newId("activity-"),
      type: "comment_added",
      actor: c.author,
      timestamp: c.createdAt,
      description: "added a comment",
    });
  }

  events.sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime());
  return events;
}

export default function TaskDetailPage({ apiEndpoint = "/api/tasks" }: TaskDetailPageProps) {
  const navigate = useNavigate();
  const { taskId } = useParams();

  const [task, setTask] = useState<TaskDetail | null>(null);
  const [subtasks, setSubtasks] = useState<Subtask[]>([]);
  const [comments, setComments] = useState<Comment[]>([]);
  const [activity, setActivity] = useState<ActivityEvent[]>([]);

  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isEditing, setIsEditing] = useState(false);
  const [draftTitle, setDraftTitle] = useState("");
  const [draftDescription, setDraftDescription] = useState("");

  const [newSubtaskTitle, setNewSubtaskTitle] = useState("");
  const [newComment, setNewComment] = useState("");

  const titleInputRef = useRef<HTMLInputElement>(null);

  const storageKeys = useMemo(() => {
    const id = taskId || "unknown";
    return {
      task: `oc.task.${id}`,
      subtasks: `oc.subtasks.${id}`,
      comments: `oc.comments.${id}`,
      activity: `oc.activity.${id}`,
    };
  }, [taskId]);

  const appendActivity = useCallback((event: Omit<ActivityEvent, "id">) => {
    setActivity((prev) => {
      const next: ActivityEvent[] = [{ ...event, id: newId("activity-") }, ...prev];
      try {
        localStorage.setItem(storageKeys.activity, JSON.stringify(next));
      } catch {
        // Ignore storage errors
      }
      return next;
    });
  }, [storageKeys.activity]);

  const persistTask = useCallback((nextTask: TaskDetail) => {
    setTask(nextTask);
    try {
      localStorage.setItem(storageKeys.task, JSON.stringify(nextTask));
    } catch {
      // Ignore storage errors
    }
  }, [storageKeys.task]);

  const persistSubtasks = useCallback((next: Subtask[]) => {
    setSubtasks(next);
    try {
      localStorage.setItem(storageKeys.subtasks, JSON.stringify(next));
    } catch {
      // Ignore storage errors
    }
  }, [storageKeys.subtasks]);

  const persistComments = useCallback((next: Comment[]) => {
    setComments(next);
    try {
      localStorage.setItem(storageKeys.comments, JSON.stringify(next));
    } catch {
      // Ignore storage errors
    }
  }, [storageKeys.comments]);

  // Load task (API → localStorage → sample)
  useEffect(() => {
    let isMounted = true;
    const load = async () => {
      if (!taskId) {
        setError("Missing task id");
        setIsLoading(false);
        return;
      }

      setIsLoading(true);
      setError(null);

      const storedTask = safeParseJSON<TaskDetail>(localStorage.getItem(storageKeys.task));
      const storedSubtasks = safeParseJSON<Subtask[]>(localStorage.getItem(storageKeys.subtasks));
      const storedComments = safeParseJSON<Comment[]>(localStorage.getItem(storageKeys.comments));
      const storedActivity = safeParseJSON<ActivityEvent[]>(localStorage.getItem(storageKeys.activity));

      // Load task details from API when available, but never block the UI on failure.
      let apiTask: TaskDetail | null = null;
      try {
        const response = await fetch(`${apiEndpoint}/${taskId}`);
        if (response.ok) {
          const data = await response.json();
          const raw = (data?.task ?? data) as Partial<Record<string, unknown>>;
          if (raw && typeof raw === "object") {
            const title = typeof raw.title === "string" ? raw.title : "";
            const description = typeof raw.description === "string" ? raw.description : "";
            const status = typeof raw.status === "string" ? raw.status : "todo";
            const priority = typeof raw.priority === "string" ? raw.priority : "medium";
            const createdAt = (typeof raw.createdAt === "string" ? raw.createdAt : typeof raw.created_at === "string" ? raw.created_at : new Date().toISOString());
            const updatedAt = (typeof raw.updatedAt === "string" ? raw.updatedAt : typeof raw.updated_at === "string" ? raw.updated_at : createdAt);

            const normalizedStatus = (STATUS_OPTIONS.some((s) => s.value === status) ? status : "todo") as TaskStatus;
            const normalizedPriority = (PRIORITY_OPTIONS.some((p) => p.value === priority) ? priority : "medium") as TaskPriority;

            apiTask = {
              id: typeof raw.id === "string" ? raw.id : taskId,
              title: title || `Task ${taskId}`,
              description: description || "",
              status: normalizedStatus,
              priority: normalizedPriority,
              createdAt,
              updatedAt,
            };
          }
        }
      } catch {
        // Ignore API errors; fall back to local/sample
      }

      const baseTask = apiTask ?? storedTask ?? SAMPLE_TASKS[taskId] ?? null;
      const baseSubtasks = storedSubtasks ?? getDefaultSubtasks(taskId);
      const baseComments = storedComments ?? getDefaultComments(taskId);
      const baseActivity = storedActivity ?? (baseTask ? buildInitialActivity(baseTask, baseSubtasks, baseComments) : []);

      if (!isMounted) return;

      setTask(baseTask);
      setSubtasks(baseSubtasks);
      setComments(baseComments);
      setActivity(baseActivity);

      if (baseTask) {
        setDraftTitle(baseTask.title);
        setDraftDescription(baseTask.description);
      }

      setIsLoading(false);
    };

    load();
    return () => {
      isMounted = false;
    };
  }, [apiEndpoint, storageKeys, taskId]);

  // Keep activity persisted when it changes (in case it was loaded from sample)
  useEffect(() => {
    try {
      localStorage.setItem(storageKeys.activity, JSON.stringify(activity));
    } catch {
      // Ignore storage errors
    }
  }, [activity, storageKeys.activity]);

  useEffect(() => {
    if (isEditing) {
      titleInputRef.current?.focus();
    }
  }, [isEditing]);

  const statusPill = useMemo(() => {
    if (!task) return null;
    const option = STATUS_OPTIONS.find((o) => o.value === task.status);
    if (!option) return null;
    return (
      <Pill className={option.className}>
        <span aria-hidden="true">{option.icon}</span>
        {option.label}
      </Pill>
    );
  }, [task]);

  const priorityPill = useMemo(() => {
    if (!task) return null;
    const option = PRIORITY_OPTIONS.find((o) => o.value === task.priority);
    if (!option) return null;
    return <Pill className={option.className}>{option.label}</Pill>;
  }, [task]);

  const completedCount = useMemo(() => subtasks.filter((s) => s.completed).length, [subtasks]);

  const handleStartEdit = () => {
    if (!task) return;
    setDraftTitle(task.title);
    setDraftDescription(task.description);
    setIsEditing(true);
  };

  const handleCancelEdit = () => {
    if (!task) return;
    setDraftTitle(task.title);
    setDraftDescription(task.description);
    setIsEditing(false);
  };

  const handleSaveEdit = () => {
    if (!task) return;
    const next: TaskDetail = {
      ...task,
      title: draftTitle.trim() || task.title,
      description: draftDescription,
      updatedAt: new Date().toISOString(),
    };
    persistTask(next);
    appendActivity({
      type: "edited",
      actor: "You",
      timestamp: next.updatedAt,
      description: "edited task details",
    });
    setIsEditing(false);
  };

  const updateStatus = (status: TaskStatus) => {
    if (!task) return;
    if (task.status === status) return;
    const next: TaskDetail = { ...task, status, updatedAt: new Date().toISOString() };
    persistTask(next);
    appendActivity({
      type: "status_changed",
      actor: "You",
      timestamp: next.updatedAt,
      description: `changed status to ${STATUS_OPTIONS.find((o) => o.value === status)?.label ?? status}`,
    });
  };

  const updatePriority = (priority: TaskPriority) => {
    if (!task) return;
    if (task.priority === priority) return;
    const next: TaskDetail = { ...task, priority, updatedAt: new Date().toISOString() };
    persistTask(next);
    appendActivity({
      type: "priority_changed",
      actor: "You",
      timestamp: next.updatedAt,
      description: `changed priority to ${PRIORITY_OPTIONS.find((o) => o.value === priority)?.label ?? priority}`,
    });
  };

  const updateAssignee = (assigneeId: string) => {
    if (!task) return;
    const nextAssignee = SAMPLE_ASSIGNEES.find((a) => a.id === assigneeId);
    const next: TaskDetail = { ...task, assignee: nextAssignee, updatedAt: new Date().toISOString() };
    persistTask(next);
    appendActivity({
      type: "assigned",
      actor: "You",
      timestamp: next.updatedAt,
      description: nextAssignee ? `assigned to ${nextAssignee.name}` : "unassigned",
    });
  };

  const handleAddSubtask = (e: FormEvent) => {
    e.preventDefault();
    if (!task) return;

    const title = newSubtaskTitle.trim();
    if (!title) return;

    const now = new Date().toISOString();
    const next: Subtask[] = [
      {
        id: newId("subtask-"),
        title,
        completed: false,
        createdAt: now,
      },
      ...subtasks,
    ];

    persistSubtasks(next);
    setNewSubtaskTitle("");
    appendActivity({
      type: "subtask_added",
      actor: "You",
      timestamp: now,
      description: `added subtask “${title}”`,
    });
  };

  const toggleSubtask = (subtaskId: string) => {
    const now = new Date().toISOString();
    let changed: Subtask | null = null;

    const next = subtasks.map((s) => {
      if (s.id !== subtaskId) return s;
      changed = { ...s, completed: !s.completed };
      return changed;
    });

    persistSubtasks(next);
    if (changed) {
      appendActivity({
        type: "subtask_completed",
        actor: "You",
        timestamp: now,
        description: `${changed.completed ? "completed" : "reopened"} subtask “${changed.title}”`,
      });
    }
  };

  const deleteSubtask = (subtaskId: string) => {
    persistSubtasks(subtasks.filter((s) => s.id !== subtaskId));
  };

  const handleAddComment = (e: FormEvent) => {
    e.preventDefault();
    if (!task) return;

    const content = newComment.trim();
    if (!content) return;

    const now = new Date().toISOString();
    const next: Comment[] = [
      { id: newId("comment-"), author: "You", content, createdAt: now },
      ...comments,
    ];

    persistComments(next);
    setNewComment("");
    appendActivity({
      type: "comment_added",
      actor: "You",
      timestamp: now,
      description: "added a comment",
    });
  };

  if (isLoading) {
    return (
      <div className="mx-auto max-w-6xl">
        <LoadingSpinner message="Loading task..." size="lg" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="mx-auto max-w-6xl">
        <div className="rounded-2xl border border-red-200 bg-red-50 p-5 text-red-800 dark:border-red-900/40 dark:bg-red-900/20 dark:text-red-200">
          <h1 className="text-lg font-semibold">Couldn’t load task</h1>
          <p className="mt-1 text-sm">{error}</p>
          <div className="mt-4 flex items-center gap-3">
            <button
              type="button"
              onClick={() => navigate(-1)}
              className="rounded-lg bg-white px-3 py-2 text-sm font-medium text-red-700 shadow-sm transition hover:bg-red-50 dark:bg-slate-900 dark:text-red-200 dark:hover:bg-red-900/30"
            >
              Go back
            </button>
            <Link to="/" className="text-sm font-medium text-red-700 underline underline-offset-4 dark:text-red-200">
              Dashboard
            </Link>
          </div>
        </div>
      </div>
    );
  }

  if (!task) {
    return (
      <div className="mx-auto max-w-6xl">
        <div className="rounded-2xl border border-slate-200 bg-white/80 p-6 dark:border-slate-800 dark:bg-slate-900/80">
          <h1 className="text-lg font-semibold text-slate-900 dark:text-white">Task not found</h1>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
            The requested task doesn’t exist (or isn’t available in this demo).
          </p>
          <div className="mt-4">
            <Link to="/" className="text-sm font-medium text-sky-700 underline underline-offset-4 dark:text-sky-300">
              Back to Dashboard
            </Link>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      {/* Header */}
      <header className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={() => navigate(-1)}
              className="inline-flex items-center gap-2 rounded-xl border border-slate-200 bg-white/70 px-3 py-2 text-sm font-medium text-slate-700 shadow-sm transition hover:bg-white dark:border-slate-800 dark:bg-slate-900/50 dark:text-slate-200 dark:hover:bg-slate-900"
              aria-label="Go back"
            >
              <span aria-hidden="true">←</span>
              Back
            </button>
            <span className="text-xs text-slate-500 dark:text-slate-400">
              Task <span className="font-mono">{task.id}</span>
            </span>
          </div>

          <div className="mt-4">
            {isEditing ? (
              <input
                ref={titleInputRef}
                type="text"
                value={draftTitle}
                onChange={(e) => setDraftTitle(e.target.value)}
                className="w-full rounded-xl border border-slate-200 bg-white px-4 py-3 text-2xl font-semibold text-slate-900 shadow-sm focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/30 dark:border-slate-800 dark:bg-slate-900 dark:text-white"
                aria-label="Task title"
              />
            ) : (
              <h1 className="truncate text-2xl font-semibold text-slate-900 dark:text-white sm:text-3xl">
                {task.title}
              </h1>
            )}
          </div>

          <div className="mt-3 flex flex-wrap items-center gap-2">
            {statusPill}
            {priorityPill}
            {task.assignee ? (
              <Pill className="bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200">
                <span aria-hidden="true">{task.assignee.avatar ?? "👤"}</span>
                {task.assignee.name}
              </Pill>
            ) : (
              <Pill className="bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300">Unassigned</Pill>
            )}
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <div className="flex items-center gap-2">
            <label className="sr-only" htmlFor="task-status">Status</label>
            <select
              id="task-status"
              value={task.status}
              onChange={(e) => updateStatus(e.target.value as TaskStatus)}
              className="rounded-xl border border-slate-200 bg-white px-3 py-2 text-sm font-medium text-slate-700 shadow-sm transition hover:bg-slate-50 focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/30 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-200 dark:hover:bg-slate-800"
            >
              {STATUS_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.icon} {opt.label}
                </option>
              ))}
            </select>

            <label className="sr-only" htmlFor="task-priority">Priority</label>
            <select
              id="task-priority"
              value={task.priority}
              onChange={(e) => updatePriority(e.target.value as TaskPriority)}
              className="rounded-xl border border-slate-200 bg-white px-3 py-2 text-sm font-medium text-slate-700 shadow-sm transition hover:bg-slate-50 focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/30 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-200 dark:hover:bg-slate-800"
            >
              {PRIORITY_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>

            <label className="sr-only" htmlFor="task-assignee">Assignee</label>
            <select
              id="task-assignee"
              value={task.assignee?.id ?? ""}
              onChange={(e) => updateAssignee(e.target.value)}
              className="rounded-xl border border-slate-200 bg-white px-3 py-2 text-sm font-medium text-slate-700 shadow-sm transition hover:bg-slate-50 focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/30 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-200 dark:hover:bg-slate-800"
            >
              <option value="">Unassigned</option>
              {SAMPLE_ASSIGNEES.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.avatar ?? "👤"} {a.name}
                </option>
              ))}
            </select>
          </div>

          {isEditing ? (
            <>
              <button
                type="button"
                onClick={handleSaveEdit}
                className="rounded-xl bg-sky-600 px-4 py-2 text-sm font-semibold text-white shadow-sm transition hover:bg-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/40"
              >
                Save
              </button>
              <button
                type="button"
                onClick={handleCancelEdit}
                className="rounded-xl border border-slate-200 bg-white/70 px-4 py-2 text-sm font-semibold text-slate-700 shadow-sm transition hover:bg-white dark:border-slate-800 dark:bg-slate-900/50 dark:text-slate-200 dark:hover:bg-slate-900"
              >
                Cancel
              </button>
            </>
          ) : (
            <button
              type="button"
              onClick={handleStartEdit}
              className="rounded-xl border border-slate-200 bg-white/70 px-4 py-2 text-sm font-semibold text-slate-700 shadow-sm transition hover:bg-white dark:border-slate-800 dark:bg-slate-900/50 dark:text-slate-200 dark:hover:bg-slate-900"
            >
              ✏️ Edit
            </button>
          )}
        </div>
      </header>

      {/* Main grid */}
      <div className="grid gap-6 lg:grid-cols-3">
        <div className="space-y-6 lg:col-span-2">
          {/* Description */}
          <SectionCard
            title="Description"
            description="Markdown supported."
          >
            {isEditing ? (
              <textarea
                value={draftDescription}
                onChange={(e) => setDraftDescription(e.target.value)}
                rows={10}
                className="w-full rounded-xl border border-slate-200 bg-white px-4 py-3 text-sm text-slate-800 shadow-sm focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/30 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-100"
                placeholder="Write a task description…"
              />
            ) : task.description.trim() ? (
              <div className="rounded-xl border border-slate-200 bg-white/70 p-4 dark:border-slate-800 dark:bg-slate-950/20">
                <ReactMarkdown
                  remarkPlugins={[remarkGfm]}
                  components={{
                    h1: ({ children }) => <h3 className="mb-2 text-lg font-semibold text-slate-900 dark:text-white">{children}</h3>,
                    h2: ({ children }) => <h4 className="mb-2 text-base font-semibold text-slate-900 dark:text-white">{children}</h4>,
                    h3: ({ children }) => <h5 className="mb-2 text-sm font-semibold text-slate-900 dark:text-white">{children}</h5>,
                    p: ({ children }) => <p className="text-sm leading-6 text-slate-700 dark:text-slate-200">{children}</p>,
                    a: ({ children, href }) => (
                      <a
                        href={href}
                        target="_blank"
                        rel="noreferrer"
                        className="text-sky-700 underline underline-offset-4 hover:text-sky-600 dark:text-sky-300"
                      >
                        {children}
                      </a>
                    ),
                    ul: ({ children }) => <ul className="list-disc space-y-1 pl-5 text-sm text-slate-700 dark:text-slate-200">{children}</ul>,
                    ol: ({ children }) => <ol className="list-decimal space-y-1 pl-5 text-sm text-slate-700 dark:text-slate-200">{children}</ol>,
                    li: ({ children }) => <li className="leading-6">{children}</li>,
                    code: ({ children }) => <code className="rounded bg-slate-100 px-1.5 py-0.5 text-xs font-medium text-slate-800 dark:bg-slate-800 dark:text-slate-100">{children}</code>,
                    blockquote: ({ children }) => (
                      <blockquote className="border-l-4 border-sky-300 bg-sky-50 px-4 py-2 text-sm text-slate-700 dark:border-sky-700 dark:bg-sky-900/20 dark:text-slate-200">
                        {children}
                      </blockquote>
                    ),
                  }}
                >
                  {task.description}
                </ReactMarkdown>
              </div>
            ) : (
              <p className="text-sm italic text-slate-500 dark:text-slate-400">No description yet.</p>
            )}
          </SectionCard>

          {/* Subtasks */}
          <SectionCard
            title="Subtasks"
            description={`${completedCount}/${subtasks.length} complete`}
          >
            <form onSubmit={handleAddSubtask} className="flex flex-col gap-3 sm:flex-row">
              <input
                type="text"
                value={newSubtaskTitle}
                onChange={(e) => setNewSubtaskTitle(e.target.value)}
                className="flex-1 rounded-xl border border-slate-200 bg-white px-4 py-2 text-sm text-slate-800 shadow-sm focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/30 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-100"
                placeholder="Add a subtask…"
                aria-label="New subtask title"
              />
              <button
                type="submit"
                className="rounded-xl bg-emerald-600 px-4 py-2 text-sm font-semibold text-white shadow-sm transition hover:bg-emerald-500"
              >
                Add
              </button>
            </form>

            <ul className="mt-4 space-y-2">
              {subtasks.length === 0 ? (
                <li className="text-sm text-slate-500 dark:text-slate-400">No subtasks yet.</li>
              ) : (
                subtasks.map((s) => (
                  <li
                    key={s.id}
                    className="flex items-start gap-3 rounded-xl border border-slate-200 bg-white/70 p-3 dark:border-slate-800 dark:bg-slate-950/20"
                  >
                    <input
                      type="checkbox"
                      checked={s.completed}
                      onChange={() => toggleSubtask(s.id)}
                      className="mt-1 h-4 w-4 rounded border-slate-300 text-emerald-600 focus:ring-emerald-500"
                      aria-label={`Mark subtask ${s.title} as ${s.completed ? "incomplete" : "complete"}`}
                    />
                    <div className="min-w-0 flex-1">
                      <p className={`text-sm ${s.completed ? "text-slate-500 line-through dark:text-slate-400" : "text-slate-800 dark:text-slate-100"}`}>
                        {s.title}
                      </p>
                      <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">
                        Added {formatRelativeTime(s.createdAt)}
                      </p>
                    </div>
                    <button
                      type="button"
                      onClick={() => deleteSubtask(s.id)}
                      className="rounded-lg p-2 text-slate-400 transition hover:bg-slate-100 hover:text-red-600 dark:hover:bg-slate-800 dark:hover:text-red-400"
                      aria-label={`Delete subtask ${s.title}`}
                    >
                      <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  </li>
                ))
              )}
            </ul>
          </SectionCard>

          {/* Comments */}
          <SectionCard title="Comments" description="Discuss and keep context close to the work.">
            <form onSubmit={handleAddComment} className="space-y-3">
              <textarea
                value={newComment}
                onChange={(e) => setNewComment(e.target.value)}
                rows={4}
                className="w-full rounded-xl border border-slate-200 bg-white px-4 py-3 text-sm text-slate-800 shadow-sm focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/30 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-100"
                placeholder="Write a comment…"
                aria-label="New comment"
              />
              <div className="flex items-center justify-end">
                <button
                  type="submit"
                  className="rounded-xl bg-sky-600 px-4 py-2 text-sm font-semibold text-white shadow-sm transition hover:bg-sky-500"
                >
                  Post comment
                </button>
              </div>
            </form>

            <ul className="mt-5 space-y-3">
              {comments.length === 0 ? (
                <li className="text-sm text-slate-500 dark:text-slate-400">No comments yet.</li>
              ) : (
                comments.map((c) => (
                  <li
                    key={c.id}
                    className="rounded-2xl border border-slate-200 bg-white/70 p-4 dark:border-slate-800 dark:bg-slate-950/20"
                  >
                    <div className="flex items-center justify-between gap-3">
                      <div className="flex items-center gap-2">
                        <div className="flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-sky-400 to-emerald-400 text-xs font-semibold text-white">
                          {c.author.split(" ").map((p) => p[0]).slice(0, 2).join("").toUpperCase()}
                        </div>
                        <div className="min-w-0">
                          <p className="truncate text-sm font-semibold text-slate-900 dark:text-white">
                            {c.author}
                          </p>
                          <p className="text-xs text-slate-500 dark:text-slate-400">
                            {formatRelativeTime(c.createdAt)}
                          </p>
                        </div>
                      </div>
                    </div>
                    <div className="mt-3 rounded-xl bg-white/70 p-3 text-sm text-slate-800 dark:bg-slate-900/40 dark:text-slate-100">
                      <ReactMarkdown remarkPlugins={[remarkGfm]}>
                        {c.content}
                      </ReactMarkdown>
                    </div>
                  </li>
                ))
              )}
            </ul>
          </SectionCard>
        </div>

        <aside className="space-y-6">
          {/* Metadata */}
          <SectionCard title="Details">
            <dl className="space-y-4 text-sm">
              <div className="flex items-start justify-between gap-4">
                <dt className="text-slate-500 dark:text-slate-400">Created</dt>
                <dd className="text-right font-medium text-slate-800 dark:text-slate-100">{formatTimestamp(task.createdAt)}</dd>
              </div>
              <div className="flex items-start justify-between gap-4">
                <dt className="text-slate-500 dark:text-slate-400">Updated</dt>
                <dd className="text-right font-medium text-slate-800 dark:text-slate-100">{formatTimestamp(task.updatedAt)}</dd>
              </div>
            </dl>
          </SectionCard>

          {/* Activity */}
          <SectionCard title="Activity timeline" description="Recent changes and conversation at a glance.">
            {activity.length === 0 ? (
              <p className="text-sm text-slate-500 dark:text-slate-400">No activity yet.</p>
            ) : (
              <ol className="space-y-3">
                {activity.map((evt) => (
                  <li key={evt.id} className="flex items-start gap-3">
                    <div className="mt-1 flex h-8 w-8 items-center justify-center rounded-xl bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200">
                      <ActivityIcon type={evt.type} />
                    </div>
                    <div className="min-w-0 flex-1">
                      <p className="text-sm text-slate-800 dark:text-slate-100">
                        <span className="font-semibold">{evt.actor}</span>{" "}
                        {evt.description}
                      </p>
                      <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">
                        {formatRelativeTime(evt.timestamp)}
                      </p>
                    </div>
                  </li>
                ))}
              </ol>
            )}
          </SectionCard>
        </aside>
      </div>
    </div>
  );
}
